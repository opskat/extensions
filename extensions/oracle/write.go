package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Row statements: copy / export as SQL, dumps and the grid's edits. The table and
// columns are quoted as the dictionary spells them (Tables). The grid runs its
// statements one at a time, so the dialect's own statements end without ';'
// (the INSERTs of copy / export / dumps are the shared session's and end with
// one, ready to run as a script; a row with a large LOB is a PL/SQL block ending
// with a line holding only /, RowInsertStatement).
var (
	_ sqldb.RowWriter = dialect{}
	_ sqldb.RowEditor = dialect{}

	_ sqldb.EmptyStringNull   = dialect{}
	_ sqldb.TypedFilterValues = dialect{}
	_ sqldb.EqualityComparer  = dialect{}
	_ sqldb.InsertRowWriter   = dialect{}
)

// EmptyStringIsNull: Oracle stores an empty string as NULL, so a filter for
// empty (or not empty) values is IS NULL (IS NOT NULL).
func (dialect) EmptyStringIsNull() bool { return true }

// TypedFilterValues: a LOCAL TIME ZONE column converts a plain string with the
// session's timestamp format, which cannot read the zone the table view shows
// (ORA-01830), so filter values are written like grid-edit values, per column type.
func (dialect) TypedFilterValues() bool { return true }

// EqualitySQL locates a row by a column's original value. Oracle cannot compare
// LOB columns with = (ORA-00932), so those use DBMS_LOB.COMPARE, whose second
// argument must be of the column's type: the literal (a string, or HEXTORAW for
// a BLOB) is converted with TO_CLOB / TO_NCLOB / TO_BLOB. XMLType, JSON and
// VECTOR cannot be compared with = either: the table view shows them serialized
// (serializers, values.go), so that serialization is compared with the shown
// value. Every other column, and one the table does not list, compares with =.
func (dialect) EqualitySQL(columnType, column, literal string) string {
	name := typeName(columnType)
	if format, ok := serializers[name]; ok {
		return "DBMS_LOB.COMPARE(" + fmt.Sprintf(format, column) + ", TO_CLOB(" + literal + ")) = 0"
	}
	switch name {
	case "CLOB":
		return "DBMS_LOB.COMPARE(" + column + ", TO_CLOB(" + literal + ")) = 0"
	case "NCLOB":
		return "DBMS_LOB.COMPARE(" + column + ", TO_NCLOB(" + literal + ")) = 0"
	case "BLOB":
		return "DBMS_LOB.COMPARE(" + column + ", TO_BLOB(" + literal + ")) = 0"
	}
	return column + " = " + literal
}

// literals writes the value the table view shows (values.go) so that the
// statement does not depend on the session's NLS settings, the same in copy /
// export as SQL, dumps and the grid's edits:
//
//   - NUMBER / FLOAT: the number as written, unquoted.
//   - BINARY_FLOAT / BINARY_DOUBLE: the number with its f / d suffix (a NUMBER
//     cannot hold their range), Nan / Inf / -Inf as BINARY_FLOAT_NAN,
//     BINARY_DOUBLE_INFINITY, -BINARY_DOUBLE_INFINITY and so on.
//   - DATE, TIMESTAMP, TIMESTAMP WITH (LOCAL) TIME ZONE: TO_DATE / TO_TIMESTAMP /
//     TO_TIMESTAMP_TZ with the format of the shown text ("2006-01-02 15:04:05",
//     the fraction, " +08:00"), so the fraction and the zone are kept.
//   - INTERVAL DAY TO SECOND / YEAR TO MONTH: TO_DSINTERVAL / TO_YMINTERVAL of
//     go-ora's text ("+01 02:03:04.500000", "+01-02").
//   - NCHAR / NVARCHAR2 / NCLOB: N'...' when the text is ASCII; otherwise
//     UNISTR of its escapes (nationalLiteral), which Oracle does not convert
//     through the database character set as it does an N'...' literal (a GBK
//     database loses an emoji, ï, ø). Other text is a string literal.
//   - RAW / LONG RAW / BLOB: HEXTORAW of the value's bytes (the shown text is them).
//
// A text that is not in the shown layout (typed into the grid or a filter, read
// from an imported file) stays a string literal, which Oracle converts by the
// session's formats (connect.go). An empty string is NULL to Oracle. A LOB too
// large for one literal is the business of RowInsertStatement.
var literals = sqldb.Literals{
	UnicodeText: func(columnType string) bool {
		switch typeName(columnType) {
		case "NCHAR", "NVARCHAR2", "NCLOB":
			return true
		}
		return false
	},
	Binary: func(columnType string) (string, string, bool) {
		switch typeName(columnType) {
		case "RAW", "LONG RAW", "BLOB":
			return "HEXTORAW('", "')", true
		}
		return "", "", false
	},
	Formatted: formattedLiteral,
}

var (
	numberText   = regexp.MustCompile(`^[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?$`)
	dateTimeText = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d{1,9})?( [-+]\d{2}:\d{2})?$`)
	daySecond    = regexp.MustCompile(`^[-+]?\d+ \d{2}:\d{2}:\d{2}(\.\d+)?$`)
	yearMonth    = regexp.MustCompile(`^[-+]?\d+-\d{2}$`)
)

// formattedLiteral is the literal of text written into a column of columnType
// when the type has a format of its own (see literals).
func formattedLiteral(columnType, text string) (string, bool) {
	name := typeName(columnType)
	switch name {
	case "NUMBER", "FLOAT":
		return text, numberText.MatchString(text)
	case "BINARY_FLOAT", "BINARY_DOUBLE":
		suffix, constant := "d", "BINARY_DOUBLE"
		if name == "BINARY_FLOAT" {
			suffix, constant = "f", "BINARY_FLOAT"
		}
		switch text {
		case "Nan":
			return constant + "_NAN", true
		case "Inf":
			return constant + "_INFINITY", true
		case "-Inf":
			return "-" + constant + "_INFINITY", true
		}
		return text + suffix, numberText.MatchString(text)
	case "DATE", "TIMESTAMP", "TIMESTAMP WITH TIME ZONE", "TIMESTAMP WITH LOCAL TIME ZONE":
		return dateTimeLiteral(name, text)
	case "INTERVAL DAY TO SECOND":
		return "TO_DSINTERVAL(" + sqlString(text) + ")", daySecond.MatchString(text)
	case "INTERVAL YEAR TO MONTH":
		return "TO_YMINTERVAL(" + sqlString(text) + ")", yearMonth.MatchString(text)
	case "NCHAR", "NVARCHAR2", "NCLOB":
		return nationalLiteral(name, text)
	}
	return "", false
}

// nationalLiteral is the literal of national text with a character outside
// ASCII: UNISTR of each piece's escapes (nationalPieces) joined with ||, an
// NCLOB's pieces each converted with TO_NCLOB, since a concatenation of NVARCHAR2
// holds 4000 bytes (ORA-01489) and one of NCLOBs any size. ASCII text is not
// converted on its way to the national character set, so it stays N'...' (ok false).
// An NCHAR's literal is cast to NCHAR of the text's length: UNISTR gives an NVARCHAR2,
// which an NCHAR column compares with unpadded (a filter on '中文' would not find
// the NCHAR(10) value), while N'...' and a plain string compare blank-padded. A
// text longer than an NCHAR can be (maxNcharUnits) fits no NCHAR column and is
// left uncast, which CAST would refuse (ORA-00910).
func nationalLiteral(name, text string) (string, bool) {
	if isASCII(text) {
		return "", false
	}
	pieces, _ := nationalPieces(text)
	for i, piece := range pieces {
		pieces[i] = "UNISTR('" + unicodeEscapes(piece) + "')"
		if name == "NCLOB" && len(pieces) > 1 {
			pieces[i] = "TO_NCLOB(" + pieces[i] + ")"
		}
	}
	literal := strings.Join(pieces, " || ")
	if units := len(utf16.Encode([]rune(text))); name == "NCHAR" && units <= maxNcharUnits {
		if len(pieces) > 1 {
			literal = "(" + literal + ")"
		}
		literal = "CAST(" + literal + " AS NCHAR(" + strconv.Itoa(units) + "))"
	}
	return literal, true
}

// maxNcharUnits is the longest NCHAR in UTF-16 units: 2000 bytes in AL16UTF16.
const maxNcharUnits = 1000

func isASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// unicodeEscapes is text as UNISTR's argument: ASCII as it is with quotes
// doubled and a backslash as \005C, every other character as \XXXX per UTF-16
// unit (a supplementary character as its surrogate pair).
func unicodeEscapes(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\\':
			b.WriteString(`\005C`)
		case r == '\'':
			b.WriteString("''")
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r > 0xFFFF:
			high, low := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\%04X\%04X`, high, low)
		default:
			fmt.Fprintf(&b, `\%04X`, r)
		}
	}
	return b.String()
}

// dateTimeLiteral converts a date-time text of the shown layout with the format
// it is written in: a DATE has no fraction and no zone, a TIMESTAMP a fraction
// or none and no zone, a TIMESTAMP WITH (LOCAL) TIME ZONE a fraction or none and
// a zone.
func dateTimeLiteral(name, text string) (string, bool) {
	m := dateTimeText.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	fraction, zone := m[1] != "", m[2] != ""
	format, function := "YYYY-MM-DD HH24:MI:SS", "TO_TIMESTAMP"
	switch name {
	case "DATE":
		if fraction || zone {
			return "", false
		}
		function = "TO_DATE"
	case "TIMESTAMP":
		if zone {
			return "", false
		}
	default:
		if !zone {
			return "", false
		}
		function = "TO_TIMESTAMP_TZ"
	}
	if fraction {
		format += ".FF"
	}
	if zone {
		format += " TZH:TZM"
	}
	return function + "(" + sqlString(text) + ", " + sqlString(format) + ")", true
}

// typeName is the name of a column type as TableColumns spells it, uppercased and
// without its precisions: "RAW(16)" is RAW, "TIMESTAMP(6) WITH TIME ZONE"
// TIMESTAMP WITH TIME ZONE, "INTERVAL DAY(2) TO SECOND(6)" INTERVAL DAY TO SECOND.
func typeName(columnType string) string {
	var b strings.Builder
	depth := 0
	for _, r := range columnType {
		switch {
		case r == '(':
			depth++
		case r == ')':
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(strings.ToUpper(b.String())), " ")
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

// Oracle's literal limits: a string literal holds 4000 bytes in SQL (ORA-01704),
// counted in the database character set (national text in the national one), so
// a HEXTORAW literal holds 2000 bytes.
const (
	maxTextLiteral = 4000
	maxRawLiteral  = maxTextLiteral / 2
)

// RowInsertStatement writes a row whose CLOB, NCLOB or BLOB value does not fit
// one literal as a PL/SQL block: the INSERT gives each such column an empty LOB
// and returns its locator, and the value is appended piece by piece. The block
// ends with a line holding only /. Every other row is the shared INSERT (ok false).
//
// A CLOB's piece is its string literal (TO_CLOB('…')). An NCLOB's and a BLOB's
// piece is base64 (UTL_ENCODE.BASE64_DECODE), of the bytes or of the text in
// UTF-16 (UTL_I18N.RAW_TO_NCHAR from AL16UTF16): like UNISTR it does not pass
// through the database character set, and it takes 4 bytes per 3 where HEXTORAW
// takes 2 per byte and UNISTR's escapes 5 per character outside ASCII. Each
// piece's literal stays within 4000 bytes, so a line of the file stays within
// SQL*Plus's 4999.
//
// A value may take 16 MB (maxLobBytes) and its appends more, all in the
// extension's memory together with the request and the reply, so they are
// written into one buffer sized for them up front, each piece encoded as it is
// written: no copy of the value and no list of its literals. However large, the
// row is this one block, which depends on nothing another statement left in the
// session.
func (dialect) RowInsertStatement(row sqldb.InsertRow) (string, bool, error) {
	large := make([]*largeLob, len(row.Columns))
	size := 0
	for i, column := range row.Columns {
		if lob, ok := splitLob(column); ok {
			large[i] = &lob
			size += lob.size
		}
	}
	if size == 0 {
		return "", false, nil
	}
	var declare strings.Builder
	names := make([]string, len(row.Columns))
	values := make([]string, len(row.Columns))
	variables := make([]string, len(row.Columns))
	var returning, into []string
	for i, column := range row.Columns {
		names[i] = row.Quote(column.Name)
		lob := large[i]
		if lob == nil {
			value, err := row.Literals.Literal(column.Value, column.Type)
			if err != nil {
				return "", false, err
			}
			values[i] = value
			size += len(value)
			continue
		}
		variables[i] = fmt.Sprintf("l%d", len(into)+1)
		fmt.Fprintf(&declare, "  %s %s;\n", variables[i], lob.kind)
		values[i] = lob.empty
		returning, into = append(returning, names[i]), append(into, variables[i])
	}
	modifier := ""
	if row.Modifier != "" {
		modifier = " " + row.Modifier
	}
	insert := fmt.Sprintf("  INSERT INTO %s (%s)%s VALUES (%s) RETURNING %s INTO %s;\n", row.TableRef,
		strings.Join(names, ", "), modifier, strings.Join(values, ", "), strings.Join(returning, ", "), strings.Join(into, ", "))
	const end = "END;\n/"
	var b strings.Builder
	b.Grow(len("DECLARE\n") + declare.Len() + len("BEGIN\n") + len(insert) + size + len(end))
	b.WriteString("DECLARE\n" + declare.String() + "BEGIN\n" + insert)
	for i, lob := range large {
		if lob == nil {
			continue
		}
		for _, piece := range lob.pieces {
			if err := lob.write(&b, variables[i], piece, row.Literals); err != nil {
				return "", false, err
			}
		}
	}
	b.WriteString(end)
	return b.String(), true, nil
}

// largeLob is a LOB value that does not fit one literal: the PL/SQL type of its
// locator, the empty LOB the INSERT writes, its pieces (parts of the value, not
// copies: strings of a text, []byte or strings of bytes) and about how many bytes
// their appends take.
type largeLob struct {
	kind, empty string
	pieces      []any
	size        int
}

// pieceCall is the most an append takes besides its piece's literal or base64.
const pieceCall = 128

// maxBase64Piece is the bytes one base64 literal of 4000 characters holds.
const maxBase64Piece = maxTextLiteral / 4 * 3

// write writes the append of one piece to the LOB in variable.
func (lob *largeLob) write(b *strings.Builder, variable string, piece any, literals sqldb.Literals) error {
	switch lob.kind {
	case "CLOB":
		literal, err := literals.Literal(piece, "CLOB")
		if err != nil {
			return err
		}
		b.WriteString("  DBMS_LOB.APPEND(" + variable + ", TO_CLOB(" + literal + "));\n")
	case "NCLOB":
		text := piece.(string)
		units := utf16.Encode([]rune(text))
		raw := make([]byte, 2*len(units))
		for i, u := range units {
			binary.BigEndian.PutUint16(raw[2*i:], u)
		}
		b.WriteString("  DBMS_LOB.APPEND(" + variable + ", TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(" + base64Raw(raw) + ", 'AL16UTF16')));\n")
	default:
		var raw []byte
		switch v := piece.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		}
		b.WriteString("  DBMS_LOB.WRITEAPPEND(" + variable + ", " + strconv.Itoa(len(raw)) + ", " + base64Raw(raw) + ");\n")
	}
	return nil
}

// base64Raw is the PL/SQL expression of raw: its base64 decoded.
func base64Raw(raw []byte) string {
	return "UTL_ENCODE.BASE64_DECODE(UTL_RAW.CAST_TO_RAW('" + base64.StdEncoding.EncodeToString(raw) + "'))"
}

// splitLob reports whether column holds a CLOB, NCLOB or BLOB value too large for
// one literal, and splits it into pieces that each fit one: a CLOB's string
// literal (textPieces), an NCLOB's UTF-16 or a BLOB's bytes in base64
// (maxBase64Piece bytes, a surrogate pair never split).
func splitLob(column sqldb.InsertColumn) (largeLob, bool) {
	kind := typeName(column.Type)
	switch kind {
	case "CLOB", "NCLOB":
		var text string
		switch v := column.Value.(type) {
		case string:
			text = v
		case []byte:
			text = string(v)
		default:
			return largeLob{}, false
		}
		lob := largeLob{kind: kind, empty: "EMPTY_CLOB()"}
		if kind == "CLOB" {
			pieces, size := textPieces(text)
			if len(pieces) < 2 {
				return largeLob{}, false
			}
			for _, piece := range pieces {
				lob.pieces = append(lob.pieces, piece)
			}
			lob.size = size + len(pieces)*pieceCall
			return lob, true
		}
		if fitsNationalLiteral(text) {
			return largeLob{}, false
		}
		start, units := 0, 0
		for i, r := range text {
			n := utf16.RuneLen(r)
			if units+n > maxBase64Piece/2 {
				lob.pieces = append(lob.pieces, text[start:i])
				start, units = i, 0
			}
			units += n
			lob.size += 2 * n
		}
		lob.pieces = append(lob.pieces, text[start:])
		lob.size = lob.size*4/3 + len(lob.pieces)*pieceCall
		return lob, true
	case "BLOB":
		// A dump's value is bytes, the grid's the string of them; a piece is a
		// part of either, never a copy.
		var n int
		var piece func(start, end int) any
		switch v := column.Value.(type) {
		case []byte:
			n, piece = len(v), func(start, end int) any { return v[start:end] }
		case string:
			n, piece = len(v), func(start, end int) any { return v[start:end] }
		default:
			return largeLob{}, false
		}
		if n <= maxRawLiteral {
			return largeLob{}, false
		}
		lob := largeLob{kind: kind, empty: "EMPTY_BLOB()"}
		for start := 0; start < n; start += maxBase64Piece {
			end := min(start+maxBase64Piece, n)
			lob.pieces = append(lob.pieces, piece(start, end))
		}
		lob.size = n*4/3 + len(lob.pieces)*pieceCall
		return lob, true
	}
	return largeLob{}, false
}

// fitsNationalLiteral reports whether national text is one literal of its own:
// N'…' or UNISTR of its escapes within maxTextLiteral (nationalPieces).
func fitsNationalLiteral(text string) bool {
	pieces, _ := nationalPieces(text)
	return len(pieces) < 2
}

// textPieces splits text into pieces whose string literals fit maxTextLiteral,
// never inside a character. The size is the literal's, quotes doubled, in UTF-8:
// the size in an AL32UTF8 database, and more than in a GBK one (two bytes for a
// Chinese character).
func textPieces(text string) ([]string, int) {
	return literalPieces(text, func(r rune) (int, int) {
		if r == '\'' {
			return 2, 0
		}
		return utf8.RuneLen(r), 0
	})
}

// nationalPieces splits national text into pieces whose literals fit
// maxTextLiteral, never inside a character, so never inside an escape or a
// surrogate pair: UNISTR's argument (unicodeEscapes, ASCII) holds 4000 bytes, and
// so does its result and an N'...' literal in AL16UTF16, the national character
// set (UNISTR cuts a longer result off without an error). Quotes count doubled.
func nationalPieces(text string) ([]string, int) {
	return literalPieces(text, func(r rune) (int, int) {
		switch {
		case r == '\'':
			return 2, 4
		case r == '\\':
			return 5, 2
		case r < utf8.RuneSelf:
			return 1, 2
		case r > 0xFFFF:
			return 10, 4
		}
		return 5, 2
	})
}

// literalPieces splits text before each character that would take a piece past
// maxTextLiteral in either of the two sizes size gives a character, and returns
// the pieces' total in the first size.
func literalPieces(text string, size func(r rune) (int, int)) ([]string, int) {
	var pieces []string
	start, total1, total2, total := 0, 0, 0, 0
	for i, r := range text {
		n1, n2 := size(r)
		if total1+n1 > maxTextLiteral || total2+n2 > maxTextLiteral {
			pieces = append(pieces, text[start:i])
			start, total1, total2 = i, 0, 0
		}
		total1, total2, total = total1+n1, total2+n2, total+n1
	}
	return append(pieces, text[start:]), total
}

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return fmt.Sprintf("UPDATE %s SET %s WHERE %s", tableRef, set, where)
}

// InsertRowSQL writes the filled-in columns. An Oracle identity column that
// rejects explicit values (GENERATED ALWAYS) has no override switch, so identity
// is never set (TableColumns marks it NoIdentityOverride).
func (dialect) InsertRowSQL(tableRef, columns, values string, _ bool) string {
	if columns == "" {
		return defaultRowInsert(tableRef)
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", tableRef, columns, values)
}

// defaultRowInsert inserts a row of defaults. Oracle has no INSERT ... DEFAULT
// VALUES and an INSERT names at least one column, so the block looks up the
// table's first column that takes a value (not hidden or virtual; an identity
// column only when there is no other) and gives it DEFAULT.
func defaultRowInsert(tableRef string) string {
	schema, table, _, err := splitTableRef(tableRef)
	if err != nil {
		panic(fmt.Sprintf("oracle: %q is not a reference TableRef wrote: %v", tableRef, err))
	}
	owner := "SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')"
	if schema != "" {
		owner = sqlString(schema)
	}
	return "DECLARE c VARCHAR2(300); BEGIN " +
		`SELECT '"' || REPLACE(column_name, '"', '""') || '"' INTO c FROM all_tab_cols ` +
		"WHERE owner = " + owner + " AND table_name = " + sqlString(table) +
		" AND hidden_column = 'NO' AND virtual_column = 'NO' " +
		"ORDER BY DECODE(identity_column, 'YES', 1, 0), column_id FETCH FIRST 1 ROW ONLY; " +
		"EXECUTE IMMEDIATE " + sqlString("INSERT INTO "+tableRef+" (") + " || c || ') VALUES (DEFAULT)'; END;"
}

func (dialect) DeleteRowSQL(tableRef, where string) string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s", tableRef, where)
}

// ExactRow narrows the rows the original values match to one by its ROWID, so of
// identical rows in a table without a primary key only one is changed or deleted
// (hence Descriptor.LocatesRowWithoutPrimaryKey).
func (dialect) ExactRow(tableRef, where string) (string, bool) {
	return fmt.Sprintf("ROWID = (SELECT ROWID FROM %s WHERE %s FETCH FIRST 1 ROW ONLY)", tableRef, where), true
}

// sqlString is text as an Oracle string literal.
func sqlString(text string) string {
	return "'" + strings.ReplaceAll(text, "'", "''") + "'"
}
