package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// splitOracle splits sql into statements and classifies each one, without
// touching the database. A plain statement ends at a top-level ';' (not part of
// its text); a PL/SQL block (anonymous BEGIN / DECLARE, or a stored program
// CREATE) ends at a '/' alone on its line or at the end of the text, and its text
// keeps the closing ';'. A query whose WITH clause declares PL/SQL functions or
// procedures ends like a block, but its closing ';' is not part of its text, as
// for any query. A '/' alone on a line also ends a plain statement, as in
// SQL*Plus. Text it cannot split (an unclosed quote or comment) is an error that
// names the position.
func splitOracle(_ context.Context, sql string) ([]dbdriver.Statement, error) {
	m := newMasker(sql, true)
	var out []dbdriver.Statement
	for pos := 0; ; {
		start := m.skipBlank(pos)
		if start >= len(sql) {
			break
		}
		if end, ok := m.loneSlashAt(start); ok {
			pos = end
			continue
		}
		end, next, closed := m.statementAt(start)
		if !closed && m.open >= 0 {
			return nil, cannotSplit(sql, m.open, m.what)
		}
		if end > start {
			out = append(out, classify(sql, m.b, start, end))
		}
		pos = next
	}
	return out, nil
}

// plsqlStart matches the statements that end at a lone '/' rather than at ';'. An
// anonymous block may open with labels (<<outer>>).
var plsqlStart = regexp.MustCompile(`(?i)^(?:(?:<<\s*[^<>\s]+\s*>>\s*)*(?:BEGIN|DECLARE)\b|CREATE\s+(?:OR\s+REPLACE\s+)?(?:(?:EDITIONABLE|NONEDITIONABLE)\s+)?(?:PROCEDURE|FUNCTION|PACKAGE|TRIGGER|TYPE)\b)`)

var plsqlQueryStart = regexp.MustCompile(`(?i)^WITH\s+(?:FUNCTION|PROCEDURE)\s+([^\s(]+)`)

// declaresPLSQL reports whether the masked statement is a query whose WITH clause
// declares a PL/SQL function or procedure ("WITH FUNCTION f RETURN …"), not one
// whose subquery is named FUNCTION or PROCEDURE ("WITH function AS (…)").
func declaresPLSQL(masked []byte) bool {
	m := plsqlQueryStart.FindSubmatch(masked)
	return m != nil && !bytes.EqualFold(m[1], []byte("AS"))
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// masker masks SQL text as far as a scan has read it: comments are blanked and
// the content of string literals and quoted identifiers (quotes included) is
// replaced by 'q', so the scanners need not know about either. Newlines are
// kept, so a line of the masked text is the same line of the text. Text from an
// unterminated quote or comment on stays unmasked: the scanners stop at limit().
//
// The whole-text classifier (splitOracle) and the SQL file splitter
// (splitScriptChunk) both scan with it. final says the text runs to the end of
// its input; when it does not, a '/' line that reaches the end of the text may be
// the start of a longer line and is no lone slash yet.
type masker struct {
	src   string
	final bool
	b     []byte // b[:done] is masked
	done  int
	open  int    // where an unterminated quote or comment opens, or -1
	what  string // what opens there
}

func newMasker(src string, final bool) *masker {
	return &masker{src: src, final: final, b: []byte(src), open: -1}
}

// maskLiterals returns sql masked as a whole. It fails on an unclosed quote or
// comment, naming where it opened.
func maskLiterals(sql string) (string, error) {
	m := newMasker(sql, true)
	m.upTo(len(sql))
	if m.open >= 0 {
		return "", cannotSplit(sql, m.open, m.what)
	}
	return string(m.b), nil
}

// upTo masks the text at least up to byte j, or up to an unterminated quote or
// comment.
func (m *masker) upTo(j int) {
	j = min(j, len(m.src))
	for m.done < j && m.open < 0 {
		m.done = m.maskUnit(m.done)
	}
}

// limit is where the masked text ends: the end of the text, or the unterminated
// quote or comment.
func (m *masker) limit() int {
	if m.open >= 0 {
		return m.open
	}
	return len(m.src)
}

// restart masks again from at on, as text outside any quote or comment: the
// text before it (a client command line, whose quotes are no quotes) is used up.
func (m *masker) restart(at int) {
	if at < m.done {
		copy(m.b[at:m.done], m.src[at:m.done])
	}
	m.done, m.open, m.what = at, -1, ""
}

// maskUnit masks the comment, quote or single byte at i and returns where the
// next one starts; an unterminated quote or comment is recorded in open.
func (m *masker) maskUnit(i int) int {
	sql, b := m.src, m.b
	fill := func(from, to int, ch byte) {
		for k := from; k < to; k++ {
			if b[k] != '\n' {
				b[k] = ch
			}
		}
	}
	unterminated := func(what string) int {
		m.open, m.what = i, what
		return len(sql)
	}
	c := sql[i]
	switch {
	case c == '-' && strings.HasPrefix(sql[i:], "--"):
		e := strings.IndexByte(sql[i:], '\n')
		if e < 0 {
			e = len(sql) - i
		}
		fill(i, i+e, ' ')
		return i + e
	case c == '/' && strings.HasPrefix(sql[i:], "/*"):
		e := strings.Index(sql[i+2:], "*/")
		if e < 0 {
			return unterminated("comment")
		}
		fill(i, i+e+4, ' ')
		return i + e + 4
	case qQuoteAt(sql, i):
		// q'<d>…<d>' with the closing delimiter of ([{< being )]}>; the
		// delimiter may be a multibyte character.
		d := i + 2 // index of the delimiter; one further for nq'
		if sql[i] != 'q' && sql[i] != 'Q' {
			d++
		}
		_, size := utf8.DecodeRuneInString(sql[d:])
		closing := sql[d : d+size]
		if k := strings.Index("([{<", closing); k >= 0 {
			closing = ")]}>"[k : k+1]
		}
		e := strings.Index(sql[d+size:], closing+"'")
		if e < 0 {
			return unterminated("q-quoted string")
		}
		stop := d + size + e + len(closing) + 1
		fill(i, stop, 'q')
		return stop
	case c == '\'' || c == '"':
		// '' inside a string is an escaped quote; an identifier cannot hold a quote.
		j := i + 1
		for ; j < len(sql); j++ {
			if sql[j] != c {
				continue
			}
			if c == '\'' && j+1 < len(sql) && sql[j+1] == '\'' {
				j++
				continue
			}
			break
		}
		if j >= len(sql) {
			if c == '"' {
				return unterminated("quoted identifier")
			}
			return unterminated("string")
		}
		fill(i, j+1, 'q')
		return j + 1
	default:
		return i + 1
	}
}

// skipBlank returns the first byte from i on that is not blank once masked (a
// comment is blank), or the limit.
func (m *masker) skipBlank(i int) int {
	for ; ; i++ {
		m.upTo(i + 1)
		if i >= m.limit() || !isBlank(m.b[i]) {
			return i
		}
	}
}

// lineEnd is the index of the newline that ends the line holding byte i, or the
// end of the text.
func lineEnd(s string, i int) int {
	if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
		return i + nl
	}
	return len(s)
}

// atLineStart reports whether only blanks (or comments) precede byte i, which
// is masked, on its line.
func (m *masker) atLineStart(i int) bool {
	for k := i - 1; k >= 0 && m.b[k] != '\n'; k-- {
		if !isBlank(m.b[k]) {
			return false
		}
	}
	return true
}

// loneSlashAt reports whether the line holding byte i, from i on, is a '/' alone
// on its line, and where that line ends. i may be the end of the text (a last
// line of blanks).
func (m *masker) loneSlashAt(i int) (end int, ok bool) {
	if i >= m.limit() {
		return 0, false
	}
	m.upTo(i + 1)
	if m.b[i] != '/' || !m.atLineStart(i) {
		return 0, false
	}
	end = lineEnd(m.src, i)
	if end == len(m.src) && !m.final {
		return 0, false
	}
	m.upTo(end)
	if end > m.limit() || string(bytes.TrimSpace(m.b[i:end])) != "/" {
		return 0, false
	}
	return end, true
}

// statementAt finds the statement that starts at start (neither blank nor a lone
// slash): end is where its text ends (trailing blanks and comments, and the
// closing ';' of a query that declares PL/SQL, excluded), next where the text
// after its terminator begins, and closed whether a terminator ended it rather
// than the end of the masked text (limit).
func (m *masker) statementAt(start int) (end, next int, closed bool) {
	end, next, closed = m.plainEnd(start)
	// The head that makes it a block precedes the first ';' or lone slash.
	if head := m.b[start:end]; plsqlStart.Match(head) || declaresPLSQL(head) {
		end, next, closed = m.blockEnd(start)
	}
	text := bytes.TrimRight(m.b[start:end], " \t\r\n")
	if declaresPLSQL(text) {
		text = bytes.TrimRight(bytes.TrimSuffix(text, []byte(";")), " \t\r\n")
	}
	return start + len(text), next, closed
}

// plainEnd finds the end of the plain statement starting at start: its top-level
// ';' or a lone-slash line, whichever comes first, else the limit.
func (m *masker) plainEnd(start int) (end, next int, closed bool) {
	for ls := start; ; {
		if ls > start {
			if e, ok := m.loneSlashAt(m.skipInLine(ls)); ok {
				return ls, e, true
			}
		}
		i := m.indexEither(ls, ';', '\n')
		if lim := m.limit(); i >= lim {
			return lim, lim, false
		}
		if m.b[i] == ';' {
			return i, i + 1, true
		}
		ls = i + 1
	}
}

// indexEither is the first byte from i on that is c1 or c2 once masked, or the
// limit.
func (m *masker) indexEither(i int, c1, c2 byte) int {
	for i < m.limit() {
		m.upTo(i + 1)
		for end := min(m.done, m.limit()); i < end; i++ {
			if m.b[i] == c1 || m.b[i] == c2 {
				return i
			}
		}
	}
	return m.limit()
}

// skipInLine returns the first byte from i on that is not a blank within the
// line once masked.
func (m *masker) skipInLine(i int) int {
	for ; ; i++ {
		m.upTo(i + 1)
		if i >= m.limit() || (m.b[i] != ' ' && m.b[i] != '\t' && m.b[i] != '\r') {
			return i
		}
	}
}

// blockEnd finds the end of the PL/SQL block starting at start: the first line
// after its first that is a lone '/', else the limit.
func (m *masker) blockEnd(start int) (end, next int, closed bool) {
	for ls := start; ; {
		le := lineEnd(m.src, ls)
		m.upTo(le)
		if lim := m.limit(); le >= lim {
			return lim, lim, false
		}
		ls = le + 1
		if e, ok := m.loneSlashAt(m.skipInLine(ls)); ok {
			return ls, e, true
		}
	}
}

// qQuoteAt reports whether an alternative-quoting literal q'… / nq'… starts at i.
func qQuoteAt(sql string, i int) bool {
	q := i
	if i < len(sql) && (sql[i] == 'n' || sql[i] == 'N') {
		q = i + 1
	}
	if q+2 >= len(sql) || (sql[q] != 'q' && sql[q] != 'Q') || sql[q+1] != '\'' || isBlank(sql[q+2]) {
		return false
	}
	return i == 0 || !isWordByte(sql[i-1])
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func cannotSplit(sql string, at int, what string) error {
	line := 1 + strings.Count(sql[:at], "\n")
	col := 1 + utf8.RuneCountInString(sql[strings.LastIndexByte(sql[:at], '\n')+1:at])
	return fmt.Errorf("cannot split: unterminated %s starting at byte %d (line %d, column %d)", what, at, line, col)
}

// token is a word or a single punctuation character of masked SQL, uppercased,
// with the parenthesis depth it sits at.
type token struct {
	text  string
	depth int
}

func tokenize(masked string) []token {
	var toks []token
	depth := 0
	for i := 0; i < len(masked); {
		c := masked[i]
		switch {
		case isBlank(c):
			i++
		case isWordByte(c):
			j := i
			for j < len(masked) && isWordByte(masked[j]) {
				j++
			}
			toks = append(toks, token{strings.ToUpper(masked[i:j]), depth})
			i = j
		default:
			if c == ')' {
				depth--
			}
			toks = append(toks, token{string(c), depth})
			if c == '(' {
				depth++
			}
			i++
		}
	}
	return toks
}

// classify builds the Statement for sql[start:end].
func classify(sql string, masked []byte, start, end int) dbdriver.Statement {
	st := dbdriver.Statement{Start: start, End: end, Text: sql[start:end]}
	toks := tokenize(string(masked[start:end]))
	first := ""
	for i := 0; i < len(toks); i++ {
		if toks[i].text == "(" {
			continue
		}
		if n := labelLen(toks[i:]); n > 0 {
			i += n - 1
			continue
		}
		first = toks[i].text
		break
	}
	switch first {
	case "SELECT", "WITH":
		st.Type = "SELECT"
		if declaresPLSQL(masked[start:end]) {
			// A query that declares PL/SQL runs it as a block does.
			st.Type, st.Reason = "PLSQL BLOCK", blockDanger(toks)
		}
	case "BEGIN", "DECLARE":
		st.Type = "PLSQL BLOCK"
		st.Reason = blockDanger(toks)
	case "CALL", "EXEC", "EXECUTE":
		st.Type, st.Reason = "CALL", dbdriver.ReasonCall
	case "UPDATE":
		st.Type = first
		if !hasWhere(toks, 0) {
			st.Reason = dbdriver.ReasonNoWhereUpdate
		}
	case "DELETE":
		st.Type = first
		if !hasWhere(toks, 0) {
			st.Reason = dbdriver.ReasonNoWhereDelete
		}
	case "CREATE", "ALTER", "DROP":
		st.Type = strings.TrimSpace(first + " " + objectType(toks[1:]))
	case "":
		st.Type = "OTHER"
	default:
		st.Type = first
	}
	st.Dangerous = st.Reason != ""
	return st
}

// labelLen is the number of tokens of the <<label>> that starts toks, or 0.
func labelLen(toks []token) int {
	if len(toks) < 5 || toks[0].text != "<" || toks[1].text != "<" || toks[3].text != ">" || toks[4].text != ">" {
		return 0
	}
	return 5
}

// objectModifiers are the words that may sit between CREATE / ALTER / DROP and
// the object type without being part of it.
var objectModifiers = map[string]bool{
	"OR": true, "REPLACE": true, "EDITIONABLE": true, "NONEDITIONABLE": true, "UNIQUE": true, "BITMAP": true,
	"GLOBAL": true, "PRIVATE": true, "TEMPORARY": true, "SHARDED": true, "DUPLICATED": true, "PUBLIC": true,
	"FORCE": true, "NOFORCE": true, "EDITIONING": true, "IMMUTABLE": true, "BLOCKCHAIN": true,
}

// objectType names the object a CREATE / ALTER / DROP works on (TABLE, PACKAGE
// BODY, MATERIALIZED VIEW), given the tokens after the verb.
func objectType(toks []token) string {
	i := 0
	for i < len(toks) && objectModifiers[toks[i].text] {
		i++
	}
	if i >= len(toks) {
		return ""
	}
	obj := toks[i].text
	if i+1 < len(toks) {
		switch next := toks[i+1].text; {
		case obj == "MATERIALIZED" && (next == "VIEW" || next == "ZONEMAP"):
			obj += " " + next
		case (obj == "PACKAGE" || obj == "TYPE") && next == "BODY":
			obj += " " + next
		}
	}
	return obj
}

// hasWhere reports whether the DML statement that starts the tokens has a WHERE
// of its own, not one inside a parenthesised subquery or a literal.
func hasWhere(toks []token, depth int) bool {
	for _, t := range toks {
		if t.text == ";" && t.depth <= depth {
			return false
		}
		if t.text == "WHERE" && t.depth == depth {
			return true
		}
	}
	return false
}

// blockBoundaries are the tokens after which a statement of a block starts (">"
// closes a <<label>>).
var blockBoundaries = map[string]bool{";": true, "BEGIN": true, "THEN": true, "ELSE": true, "LOOP": true, ">": true}

// startsBlockStatement reports whether toks[i] starts a statement of a block: it
// follows a boundary, or it is the DML of a FORALL that does.
func startsBlockStatement(toks []token, i int) bool {
	for k := i - 1; k >= 0; k-- {
		if blockBoundaries[toks[k].text] {
			return k == i-1 || toks[k+1].text == "FORALL"
		}
	}
	return false
}

// blockDanger returns the danger reason of an anonymous block that runs an
// UPDATE or DELETE without WHERE as one of its statements, else "".
func blockDanger(toks []token) string {
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		if (t.text != "UPDATE" && t.text != "DELETE") || i+1 >= len(toks) || !startsBlockStatement(toks, i) {
			continue
		}
		// MERGE's own clauses: "UPDATE SET …" and "DELETE [WHERE …]" name no table.
		if next := toks[i+1].text; next == "SET" || next == ";" || next == "WHERE" {
			continue
		}
		if hasWhere(toks[i:], t.depth) {
			continue
		}
		if t.text == "UPDATE" {
			return dbdriver.ReasonNoWhereUpdate
		}
		return dbdriver.ReasonNoWhereDelete
	}
	return ""
}
