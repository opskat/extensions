package main

import (
	"context"
	"database/sql"
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

// rowKeyQuery names, in order, the columns of the key that finds one row: the
// primary key, else the unique key of the fewest columns (then by name) whose
// columns are all NOT NULL and written by a dump (not virtual or hidden, see
// dumpColumnsQuery). Only an enabled constraint holds the values unique. A LOB
// cannot be part of either.
const rowKeyQuery = "SELECT cc.column_name AS COLUMN_NAME FROM (" +
	"SELECT c.owner, c.constraint_name FROM all_constraints c " +
	"WHERE c.owner = " + ownerIs + " AND c.table_name = :2 AND c.constraint_type IN ('P', 'U') AND c.status = 'ENABLED' " +
	"AND NOT EXISTS (SELECT 1 FROM all_cons_columns k JOIN all_tab_cols tc " +
	"ON tc.owner = k.owner AND tc.table_name = k.table_name AND tc.column_name = k.column_name " +
	"WHERE k.owner = c.owner AND k.constraint_name = c.constraint_name " +
	"AND (tc.nullable = 'Y' OR tc.virtual_column = 'YES' OR tc.hidden_column = 'YES')) " +
	"ORDER BY DECODE(c.constraint_type, 'P', 0, 1), " +
	"(SELECT COUNT(*) FROM all_cons_columns k WHERE k.owner = c.owner AND k.constraint_name = c.constraint_name), " +
	"c.constraint_name FETCH FIRST 1 ROW ONLY) u " +
	"JOIN all_cons_columns cc ON cc.owner = u.owner AND cc.constraint_name = u.constraint_name " +
	"ORDER BY cc.position"

// InsertRowKey is the key by which a further block of a large row finds it
// (RowInsertStatement, rowKeyQuery); nil when the table has none.
func (dialect) InsertRowKey(ctx context.Context, conn *sql.Conn, database, table string) ([]string, error) {
	key, err := sqldb.QueryPrimaryKeys(ctx, conn, "COLUMN_NAME", rowKeyQuery, database, table)
	if err != nil || len(key) == 0 {
		return nil, err
	}
	return key, nil
}

// fitsNationalLiteral reports whether national text is one literal of its own:
// N'…' or UNISTR of its escapes within maxTextLiteral (nationalPieces).
func fitsNationalLiteral(text string) bool {
	pieces, _ := nationalPieces(text)
	return len(pieces) < 2
}

// textLiteralSize is a character's size in a string literal, quotes doubled, in
// UTF-8: the size in an AL32UTF8 database, and more than in a GBK one (two bytes
// for a Chinese character).
func textLiteralSize(r rune) (int, int) {
	if r == '\'' {
		return 2, 0
	}
	return utf8.RuneLen(r), 0
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
// maxTextLiteral in either of the two sizes size gives a character
// (pieceSplitter), and returns the pieces' total in the first size.
func literalPieces(text string, size func(r rune) (int, int)) ([]string, int) {
	split := pieceSplitter{size: size, limit: maxTextLiteral}
	pieces := split.end(split.text(text, nil))
	out, total := make([]string, len(pieces)), 0
	for i, piece := range pieces {
		out[i], total = piece.value.(string), total+piece.measure
	}
	return out, total
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
