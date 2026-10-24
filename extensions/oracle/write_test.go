package main

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectEmpColumns answers the column-definition query for HR.EMP: a number key,
// text, a RAW, a BLOB, a DATE and a TIMESTAMP WITH TIME ZONE.
func expectEmpColumns(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0").
			AddRow("NAME", "VARCHAR2(20 CHAR)", "YES", nil, nil, "0", "0", "0").
			AddRow("CODE", "RAW(16)", "YES", nil, nil, "0", "0", "0").
			AddRow("DATA", "BLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("BORN", "DATE", "YES", nil, nil, "0", "0", "0").
			AddRow("SEEN", "TIMESTAMP(6) WITH TIME ZONE", "YES", nil, nil, "0", "0", "0"))
}

// Copy / export as SQL: "SCHEMA"."TABLE" and "COLUMN" as the dictionary spells
// them; a binary column's displayed text (its bytes) is written back as HEXTORAW of
// those bytes; a time zone timestamp goes through TO_TIMESTAMP_TZ; dates and the rest
// are string literals in their displayed form.
func TestRowStatementsWriteOracleLiterals(t *testing.T) {
	session, mock := newMockSession(t)
	ctx := context.Background()

	expectEmpColumns(mock)
	writer, err := session.PrepareRowStatements(ctx, "HR", "EMP")
	require.NoError(t, err)

	inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"ID", "NAME", "CODE", "DATA", "BORN", "SEEN"},
		Rows: []map[string]any{{
			"ID": float64(1), "NAME": "O'Reilly", "CODE": "\x00\xffA", "DATA": []byte{0xde, 0xad},
			"BORN": "2024-01-02 03:04:05", "SEEN": "2024-01-02 03:04:05.123456 +08:00",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`INSERT INTO "HR"."EMP" ("ID", "NAME", "CODE", "DATA", "BORN", "SEEN") VALUES ` +
		`('1', 'O''Reilly', HEXTORAW('00ff41'), HEXTORAW('dead'), '2024-01-02 03:04:05', TO_TIMESTAMP_TZ('2024-01-02 03:04:05.123456 +08:00', 'YYYY-MM-DD HH24:MI:SSXFF TZH:TZM'));`,
	}, inserts)

	updates, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"ID", "NAME"},
		Rows:        []map[string]any{{"ID": float64(2), "NAME": nil}},
		PrimaryKeys: []string{"ID"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "HR"."EMP" SET "ID" = '2', "NAME" = NULL WHERE "ID" = '2'`}, updates)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The grid's statements run one at a time and end without ';'. With a primary key
// a row is found by it.
func TestGridEditStatementsByPrimaryKey(t *testing.T) {
	session, mock := newMockSession(t)
	expectEmpColumns(mock)

	statements, err := session.GridEditStatements(context.Background(), "HR", "EMP", dbdriver.GridEditRequest{
		Columns:     []string{"ID", "NAME", "CODE"},
		PrimaryKeys: []string{"ID"},
		Inserts:     []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{{Column: "ID", Value: float64(3)}, {Column: "CODE", Value: "AB"}}}},
		Updates: []dbdriver.GridUpdate{{
			Row: map[string]any{"ID": "1", "NAME": "ann", "CODE": nil},
			Set: []dbdriver.ColumnValue{{Column: "NAME", Value: "it's"}},
		}},
		Deletes: []map[string]any{{"ID": "2", "NAME": "bob", "CODE": nil}},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.GridEditStatements{
		Inserts: []string{`INSERT INTO "HR"."EMP" ("ID", "CODE") VALUES ('3', HEXTORAW('4142'))`},
		Updates: []string{`UPDATE "HR"."EMP" SET "NAME" = 'it''s' WHERE "ID" = '1'`},
		Deletes: []string{`DELETE FROM "HR"."EMP" WHERE "ID" = '2'`},
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Without a primary key the original values find the rows and ROWID narrows them
// to one, so of two identical rows only one is changed or deleted.
func TestGridEditStatementsWithoutPrimaryKeyUseRowid(t *testing.T) {
	session, mock := newMockSession(t)
	expectEmpColumns(mock)

	row := map[string]any{"ID": "1", "NAME": nil}
	statements, err := session.GridEditStatements(context.Background(), "HR", "EMP", dbdriver.GridEditRequest{
		Columns: []string{"ID", "NAME"},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "NAME", Value: "x"}}}},
		Deletes: []map[string]any{row},
	})
	require.NoError(t, err)
	const exactRow = `ROWID = (SELECT ROWID FROM "HR"."EMP" WHERE "ID" = '1' AND "NAME" IS NULL FETCH FIRST 1 ROW ONLY)`
	assert.Equal(t, []string{`UPDATE "HR"."EMP" SET "NAME" = 'x' WHERE ` + exactRow}, statements.Updates)
	assert.Equal(t, []string{`DELETE FROM "HR"."EMP" WHERE ` + exactRow}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Oracle has no INSERT ... DEFAULT VALUES: a new row with nothing filled in names
// the table's first column that takes a value (an identity column last) and gives it DEFAULT, which leaves
// every other column at its default too.
func TestGridEditInsertsADefaultRow(t *testing.T) {
	session, mock := newMockSession(t)
	expectEmpColumns(mock)

	statements, err := session.GridEditStatements(context.Background(), "HR", "EMP", dbdriver.GridEditRequest{
		Columns: []string{"ID"},
		Inserts: []dbdriver.GridInsert{{}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`DECLARE c VARCHAR2(300); BEGIN ` +
		`SELECT '"' || REPLACE(column_name, '"', '""') || '"' INTO c FROM all_tab_cols ` +
		`WHERE owner = 'HR' AND table_name = 'EMP' AND hidden_column = 'NO' AND virtual_column = 'NO' ` +
		`ORDER BY DECODE(identity_column, 'YES', 1, 0), column_id FETCH FIRST 1 ROW ONLY; ` +
		`EXECUTE IMMEDIATE 'INSERT INTO "HR"."EMP" (' || c || ') VALUES (DEFAULT)'; END;`,
	}, statements.Inserts)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A table opened without a schema is the session's current schema's; quotes in
// the names stay quoted inside the block's string literals.
func TestDefaultRowInsertWithoutSchema(t *testing.T) {
	d := dialect{}
	assert.Equal(t, `DECLARE c VARCHAR2(300); BEGIN `+
		`SELECT '"' || REPLACE(column_name, '"', '""') || '"' INTO c FROM all_tab_cols `+
		`WHERE owner = SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') AND table_name = 'it''s "q"' AND hidden_column = 'NO' AND virtual_column = 'NO' `+
		`ORDER BY DECODE(identity_column, 'YES', 1, 0), column_id FETCH FIRST 1 ROW ONLY; `+
		`EXECUTE IMMEDIATE 'INSERT INTO "it''s ""q""" (' || c || ') VALUES (DEFAULT)'; END;`,
		d.InsertRowSQL(d.TableRef("", `it's "q"`), "", "", false))
}

// Oracle cannot compare LOB columns with = (ORA-00932): a row is located by
// DBMS_LOB.COMPARE, whose second argument must be of the column's LOB type, so a
// BLOB's HEXTORAW (a RAW) is converted with TO_BLOB. Other types, and a column
// the table does not list, compare with =.
func TestGridEditStatementsLocateRowsByLobComparison(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "DOCS").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "YES", nil, nil, "0", "0", "0").
			AddRow("BODY", "CLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("NBODY", "NCLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("IMG", "BLOB", "YES", nil, nil, "0", "0", "0"))

	statements, err := session.GridEditStatements(context.Background(), "HR", "DOCS", dbdriver.GridEditRequest{
		Columns: []string{"ID", "BODY", "NBODY", "IMG"},
		Deletes: []map[string]any{{"ID": "1", "BODY": "it's", "NBODY": "n", "IMG": []byte{0xde, 0xad}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`DELETE FROM "HR"."DOCS" WHERE ROWID = (SELECT ROWID FROM "HR"."DOCS" WHERE "ID" = '1'` +
		` AND DBMS_LOB.COMPARE("BODY", TO_CLOB('it''s')) = 0` +
		` AND DBMS_LOB.COMPARE("NBODY", TO_NCLOB('n')) = 0` +
		` AND DBMS_LOB.COMPARE("IMG", TO_BLOB(HEXTORAW('dead'))) = 0 FETCH FIRST 1 ROW ONLY)`}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// XMLType, JSON and VECTOR cannot be compared with = either (ORA-22950 / ORA-40796):
// the table view shows each as its serialized text (values.go), so a row is
// located by comparing that same serialization with the shown value.
func TestGridEditStatementsLocateRowsBySerializedText(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "DOCS").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "YES", nil, nil, "0", "0", "0").
			AddRow("DOC", "XMLTYPE", "YES", nil, nil, "0", "0", "0").
			AddRow("CFG", "JSON", "YES", nil, nil, "0", "0", "0").
			AddRow("EMB", "VECTOR", "YES", nil, nil, "0", "0", "0"))

	statements, err := session.GridEditStatements(context.Background(), "HR", "DOCS", dbdriver.GridEditRequest{
		Columns: []string{"ID", "DOC", "CFG", "EMB"},
		Deletes: []map[string]any{{"ID": "1", "DOC": "<a/>", "CFG": `{"k":1}`, "EMB": "[1,2]"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`DELETE FROM "HR"."DOCS" WHERE ROWID = (SELECT ROWID FROM "HR"."DOCS" WHERE "ID" = '1'` +
		` AND DBMS_LOB.COMPARE(XMLSERIALIZE(CONTENT "DOC" AS CLOB), TO_CLOB('<a/>')) = 0` +
		` AND DBMS_LOB.COMPARE(JSON_SERIALIZE("CFG" RETURNING CLOB), TO_CLOB('{"k":1}')) = 0` +
		` AND DBMS_LOB.COMPARE(FROM_VECTOR("EMB" RETURNING CLOB), TO_CLOB('[1,2]')) = 0 FETCH FIRST 1 ROW ONLY)`}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEqualitySQLByColumnType(t *testing.T) {
	d := dialect{}
	assert.Equal(t, `"A" = '1'`, d.EqualitySQL("NUMBER(10)", `"A"`, "'1'"))
	assert.Equal(t, `"A" = '1'`, d.EqualitySQL("", `"A"`, "'1'"))
	assert.Equal(t, `DBMS_LOB.COMPARE("A", TO_CLOB('x')) = 0`, d.EqualitySQL("clob", `"A"`, "'x'"))
}

// A TIMESTAMP WITH (LOCAL) TIME ZONE is shown with its offset, which the session's
// timestamp format does not read for a local time zone (ORA-01830), so both are
// converted with TO_TIMESTAMP_TZ in the shown layout ("SSXFF" reads a fraction or
// none); NULL stays NULL and a plain TIMESTAMP stays a string literal.
func TestRowStatementsConvertTimeZoneTimestamps(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0").
			AddRow("LTZ", "TIMESTAMP(6) WITH LOCAL TIME ZONE", "YES", nil, nil, "0", "0", "0").
			AddRow("LTZ0", "TIMESTAMP(0) WITH LOCAL TIME ZONE", "YES", nil, nil, "0", "0", "0").
			AddRow("SEEN", "TIMESTAMP(0) WITH TIME ZONE", "YES", nil, nil, "0", "0", "0").
			AddRow("TS", "TIMESTAMP(6)", "YES", nil, nil, "0", "0", "0"))
	writer, err := session.PrepareRowStatements(context.Background(), "HR", "EMP")
	require.NoError(t, err)

	inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"ID", "LTZ", "LTZ0", "SEEN", "TS"},
		Rows: []map[string]any{{
			"ID": float64(1), "LTZ": "2026-10-24 10:11:12 -05:30", "LTZ0": nil,
			"SEEN": "2026-10-24 10:11:12 -05:30", "TS": "2026-10-24 10:11:12.5",
		}},
	})
	require.NoError(t, err)
	const tz = `, 'YYYY-MM-DD HH24:MI:SSXFF TZH:TZM')`
	assert.Equal(t, []string{`INSERT INTO "HR"."EMP" ("ID", "LTZ", "LTZ0", "SEEN", "TS") VALUES ` +
		`('1', TO_TIMESTAMP_TZ('2026-10-24 10:11:12 -05:30'` + tz + `, NULL, ` +
		`TO_TIMESTAMP_TZ('2026-10-24 10:11:12 -05:30'` + tz + `, '2026-10-24 10:11:12.5');`}, inserts)
	require.NoError(t, mock.ExpectationsWereMet())
}
