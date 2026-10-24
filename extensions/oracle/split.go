package main

import (
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
	masked, err := maskLiterals(sql)
	if err != nil {
		return nil, err
	}
	var out []dbdriver.Statement
	for pos := 0; pos < len(masked); {
		start := skipBlank(masked, pos)
		if start >= len(masked) {
			break
		}
		if end, ok := loneSlashAt(masked, start); ok {
			pos = end
			continue
		}
		var end, next int
		if plsqlStart.MatchString(masked[start:]) || declaresPLSQL(masked[start:]) {
			end, next = blockEnd(masked, start)
		} else {
			end, next = plainEnd(masked, start)
		}
		text := strings.TrimRight(masked[start:end], " \t\r\n")
		if declaresPLSQL(text) {
			text = strings.TrimRight(strings.TrimSuffix(text, ";"), " \t\r\n")
		}
		end = start + len(text)
		if end > start {
			out = append(out, classify(sql, masked, start, end))
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
func declaresPLSQL(masked string) bool {
	m := plsqlQueryStart.FindStringSubmatch(masked)
	return m != nil && !strings.EqualFold(m[1], "AS")
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func skipBlank(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

// loneSlashAt reports whether the line holding byte i, from i on, is a '/' alone
// on its line (masked has comments blanked), and where that line ends. i may be
// the end of the text (a last line of blanks).
func loneSlashAt(masked string, i int) (end int, ok bool) {
	if i >= len(masked) || masked[i] != '/' {
		return 0, false
	}
	if ls := strings.LastIndexByte(masked[:i], '\n') + 1; strings.TrimSpace(masked[ls:i]) != "" {
		return 0, false
	}
	end = len(masked)
	if nl := strings.IndexByte(masked[i:], '\n'); nl >= 0 {
		end = i + nl
	}
	if strings.TrimSpace(masked[i:end]) != "/" {
		return 0, false
	}
	return end, true
}

// plainEnd finds the end of the plain statement starting at start: its top-level
// ';' or a lone-slash line, whichever comes first, else the end of the text.
func plainEnd(masked string, start int) (end, next int) {
	semi := strings.IndexByte(masked[start:], ';')
	limit := len(masked)
	if semi >= 0 {
		limit = start + semi
	}
	for ls := start; ls < limit; {
		nl := strings.IndexByte(masked[ls:limit], '\n')
		if nl < 0 {
			break
		}
		ls += nl + 1
		if i := skipInLine(masked, ls); i < limit {
			if e, ok := loneSlashAt(masked, i); ok {
				return ls, e
			}
		}
	}
	if semi >= 0 {
		return limit, limit + 1
	}
	return len(masked), len(masked)
}

func skipInLine(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	return i
}

// blockEnd finds the end of the PL/SQL block starting at start: the first line
// after its first that is a lone '/', else the end of the text.
func blockEnd(masked string, start int) (end, next int) {
	for ls := start; ; {
		nl := strings.IndexByte(masked[ls:], '\n')
		if nl < 0 {
			return len(masked), len(masked)
		}
		ls += nl + 1
		if e, ok := loneSlashAt(masked, skipInLine(masked, ls)); ok {
			return ls, e
		}
	}
}

// maskLiterals returns a string of the same length as sql in which comments are
// blanked and the content of string literals and quoted identifiers (quotes
// included) is replaced by 'q', so the scanners need not know about either.
// Newlines are kept. It fails on an unclosed quote or comment, naming where it opened.
func maskLiterals(sql string) (string, error) {
	b := []byte(sql)
	fill := func(from, to int, ch byte) {
		for k := from; k < to; k++ {
			if b[k] != '\n' {
				b[k] = ch
			}
		}
	}
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && strings.HasPrefix(sql[i:], "--"):
			e := strings.IndexByte(sql[i:], '\n')
			if e < 0 {
				e = len(sql) - i
			}
			fill(i, i+e, ' ')
			i += e
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			e := strings.Index(sql[i+2:], "*/")
			if e < 0 {
				return "", cannotSplit(sql, i, "comment")
			}
			fill(i, i+e+4, ' ')
			i += e + 4
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
				return "", cannotSplit(sql, i, "q-quoted string")
			}
			stop := d + size + e + len(closing) + 1
			fill(i, stop, 'q')
			i = stop
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
				what := "string"
				if c == '"' {
					what = "quoted identifier"
				}
				return "", cannotSplit(sql, i, what)
			}
			fill(i, j+1, 'q')
			i = j + 1
		default:
			i++
		}
	}
	return string(b), nil
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
func classify(sql, masked string, start, end int) dbdriver.Statement {
	st := dbdriver.Statement{Start: start, End: end, Text: sql[start:end]}
	toks := tokenize(masked[start:end])
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
