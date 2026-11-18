package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
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
//   - NCHAR / NVARCHAR2 / NCLOB: N'...'; other text a string literal.
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
	}
	return "", false
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
// and returns its locator, and the value is appended piece by piece, each piece
// one literal. The block ends with a line holding only /. Every other row is the
// shared INSERT (ok false).
func (dialect) RowInsertStatement(row sqldb.InsertRow) (string, bool, error) {
	large := make(map[int]largeLob)
	for i, column := range row.Columns {
		if lob, ok := splitLob(column); ok {
			large[i] = lob
		}
	}
	if len(large) == 0 {
		return "", false, nil
	}
	var declare, appends strings.Builder
	names := make([]string, len(row.Columns))
	values := make([]string, len(row.Columns))
	var returning, into []string
	for i, column := range row.Columns {
		names[i] = row.Quote(column.Name)
		lob, ok := large[i]
		if !ok {
			value, err := row.Literals.Literal(column.Value, column.Type)
			if err != nil {
				return "", false, err
			}
			values[i] = value
			continue
		}
		variable := fmt.Sprintf("l%d", len(into)+1)
		fmt.Fprintf(&declare, "  %s %s;\n", variable, lob.kind)
		values[i] = lob.empty
		returning, into = append(returning, names[i]), append(into, variable)
		for _, arguments := range lob.pieces {
			appends.WriteString("  " + lob.call + "(" + variable + ", " + arguments + ");\n")
		}
	}
	modifier := ""
	if row.Modifier != "" {
		modifier = " " + row.Modifier
	}
	return "DECLARE\n" + declare.String() + "BEGIN\n" +
		fmt.Sprintf("  INSERT INTO %s (%s)%s VALUES (%s) RETURNING %s INTO %s;\n", row.TableRef,
			strings.Join(names, ", "), modifier, strings.Join(values, ", "), strings.Join(returning, ", "), strings.Join(into, ", ")) +
		appends.String() + "END;\n/", true, nil
}

// largeLob is a LOB value that does not fit one literal: the PL/SQL type of its
// locator, the empty LOB the INSERT writes, and the DBMS_LOB procedure that
// appends a piece with the arguments after the locator for each piece.
type largeLob struct {
	kind, empty, call string
	pieces            []string
}

// splitLob reports whether column holds a CLOB, NCLOB or BLOB value too large for
// one literal, and splits it into the calls that append it.
func splitLob(column sqldb.InsertColumn) (largeLob, bool) {
	var text string
	switch v := column.Value.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return largeLob{}, false
	}
	kind := typeName(column.Type)
	var pieces []string
	switch kind {
	case "CLOB", "NCLOB":
		national := kind == "NCLOB"
		texts := textPieces(text, national)
		if len(texts) < 2 {
			return largeLob{}, false
		}
		for _, piece := range texts {
			if national {
				pieces = append(pieces, "TO_NCLOB(N"+sqlString(piece)+")")
			} else {
				pieces = append(pieces, "TO_CLOB("+sqlString(piece)+")")
			}
		}
		return largeLob{kind: kind, empty: "EMPTY_CLOB()", call: "DBMS_LOB.APPEND", pieces: pieces}, true
	case "BLOB":
		if len(text) <= maxRawLiteral {
			return largeLob{}, false
		}
		for start := 0; start < len(text); start += maxRawLiteral {
			piece := text[start:min(start+maxRawLiteral, len(text))]
			pieces = append(pieces, fmt.Sprintf("%d, HEXTORAW('%s')", len(piece), hex.EncodeToString([]byte(piece))))
		}
		return largeLob{kind: kind, empty: "EMPTY_BLOB()", call: "DBMS_LOB.WRITEAPPEND", pieces: pieces}, true
	}
	return largeLob{}, false
}

// textPieces splits text into pieces whose string literals fit maxTextLiteral,
// never inside a character. The size is the literal's, quotes doubled, in UTF-8:
// the size in an AL32UTF8 database, and more than in a GBK one (two bytes for a
// Chinese character). National text is also counted in UTF-16, the AL16UTF16
// national character set.
func textPieces(text string, national bool) []string {
	var pieces []string
	start, size8, size16 := 0, 0, 0
	for i, r := range text {
		n8, n16 := utf8.RuneLen(r), 2
		if r > 0xFFFF {
			n16 = 4
		}
		if r == '\'' {
			n8, n16 = 2*n8, 2*n16
		}
		if size8+n8 > maxTextLiteral || (national && size16+n16 > maxTextLiteral) {
			pieces = append(pieces, text[start:i])
			start, size8, size16 = i, 0, 0
		}
		size8, size16 = size8+n8, size16+n16
	}
	return append(pieces, text[start:])
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
