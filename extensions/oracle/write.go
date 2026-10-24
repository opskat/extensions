package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Row statements: copy / export as SQL and the grid's edits. The table and
// columns are quoted as the dictionary spells them (Tables). The grid runs its
// statements one at a time, so the dialect's own statements end without ';'
// (the INSERTs of copy / export are the shared session's and end with one, ready
// to run as a script).
var (
	_ sqldb.RowWriter = dialect{}
	_ sqldb.RowEditor = dialect{}

	_ sqldb.EmptyStringNull   = dialect{}
	_ sqldb.TypedFilterValues = dialect{}
	_ sqldb.EqualityComparer  = dialect{}
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

// literals writes a binary column's value, whose displayed text is its bytes
// (values.go), as HEXTORAW of those bytes. Everything else is a string literal of
// the displayed text, which Oracle converts to the column's type: a number as
// written, and a DATE / TIMESTAMP / TIMESTAMP WITH TIME ZONE ("2006-01-02
// 15:04:05", the fraction, " +08:00") by the session's NLS_DATE_FORMAT /
// NLS_TIMESTAMP_FORMAT / NLS_TIMESTAMP_TZ_FORMAT, which must spell that layout.
// A TIMESTAMP WITH (LOCAL) TIME ZONE is the exception: a local one takes no zone in
// a plain string (ORA-01830), so their text goes through TO_TIMESTAMP_TZ in the
// shown layout ("SSXFF" reads a fraction or none before the offset), which also
// keeps a copied INSERT / UPDATE independent of the running session's format.
var literals = sqldb.Literals{
	Binary: func(columnType string) (string, string, bool) {
		switch typeName(columnType) {
		case "RAW", "LONG RAW", "BLOB":
			return "HEXTORAW('", "')", true
		}
		return "", "", false
	},
	Converted: func(columnType string) (string, string, bool) {
		upper := strings.ToUpper(strings.TrimSpace(columnType))
		if strings.HasSuffix(upper, "WITH TIME ZONE") || strings.HasSuffix(upper, "WITH LOCAL TIME ZONE") {
			return "TO_TIMESTAMP_TZ(", ", 'YYYY-MM-DD HH24:MI:SSXFF TZH:TZM')", true
		}
		return "", "", false
	},
}

// typeName is the name of a column type as TableColumns spells it, uppercased and
// without its precision: "RAW(16)" is RAW, "TIMESTAMP(6) WITH TIME ZONE" TIMESTAMP.
func typeName(columnType string) string {
	name, _, _ := strings.Cut(columnType, "(")
	return strings.ToUpper(strings.TrimSpace(name))
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) { return literals, nil }

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
