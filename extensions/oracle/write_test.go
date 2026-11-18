package main

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
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
// those bytes; numbers unquoted, dates and timestamps through their conversion
// functions (each type: TestRowStatementsWriteEachOracleType).
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
		`(1, 'O''Reilly', HEXTORAW('00ff41'), HEXTORAW('dead'), TO_DATE('2024-01-02 03:04:05', 'YYYY-MM-DD HH24:MI:SS'), ` +
		`TO_TIMESTAMP_TZ('2024-01-02 03:04:05.123456 +08:00', 'YYYY-MM-DD HH24:MI:SS.FF TZH:TZM'));`,
	}, inserts)

	updates, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"ID", "NAME"},
		Rows:        []map[string]any{{"ID": float64(2), "NAME": nil}},
		PrimaryKeys: []string{"ID"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "HR"."EMP" SET "ID" = 2, "NAME" = NULL WHERE "ID" = 2`}, updates)
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
		Inserts: []string{`INSERT INTO "HR"."EMP" ("ID", "CODE") VALUES (3, HEXTORAW('4142'))`},
		Updates: []string{`UPDATE "HR"."EMP" SET "NAME" = 'it''s' WHERE "ID" = 1`},
		Deletes: []string{`DELETE FROM "HR"."EMP" WHERE "ID" = 2`},
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
	const exactRow = `ROWID = (SELECT ROWID FROM "HR"."EMP" WHERE "ID" = 1 AND "NAME" IS NULL FETCH FIRST 1 ROW ONLY)`
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
	assert.Equal(t, []string{`DELETE FROM "HR"."DOCS" WHERE ROWID = (SELECT ROWID FROM "HR"."DOCS" WHERE "ID" = 1` +
		` AND DBMS_LOB.COMPARE("BODY", TO_CLOB('it''s')) = 0` +
		` AND DBMS_LOB.COMPARE("NBODY", TO_NCLOB(N'n')) = 0` +
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
	assert.Equal(t, []string{`DELETE FROM "HR"."DOCS" WHERE ROWID = (SELECT ROWID FROM "HR"."DOCS" WHERE "ID" = 1` +
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

// valuesGolden compares got with testdata/values/name (rewritten by -update-golden).
func valuesGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", "values", name)
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// expectValsColumns answers the column-definition query for HR.VALS, a column of
// every type a value is written for.
func expectValsColumns(mock sqlmock.Sqlmock) {
	rows := columnRows()
	for _, c := range [][2]string{
		{"ID", "NUMBER(10)"}, {"N", "NUMBER"}, {"FL", "FLOAT(126)"}, {"BF", "BINARY_FLOAT"}, {"BD", "BINARY_DOUBLE"},
		{"D", "DATE"}, {"TS", "TIMESTAMP(6)"}, {"TS0", "TIMESTAMP(0)"}, {"TS9", "TIMESTAMP(9)"},
		{"TSTZ", "TIMESTAMP(6) WITH TIME ZONE"}, {"TZ0", "TIMESTAMP(0) WITH TIME ZONE"},
		{"LTZ", "TIMESTAMP(6) WITH LOCAL TIME ZONE"}, {"DS", "INTERVAL DAY(2) TO SECOND(6)"},
		{"YM", "INTERVAL YEAR(2) TO MONTH"}, {"CH", "CHAR(4)"}, {"VC", "VARCHAR2(20 CHAR)"}, {"NCH", "NCHAR(4)"},
		{"NVC", "NVARCHAR2(20)"}, {"C", "CLOB"}, {"NC", "NCLOB"}, {"RW", "RAW(16)"}, {"B", "BLOB"},
	} {
		rows.AddRow(c[0], c[1], "YES", nil, nil, "0", "0", "0")
	}
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "VALS").WillReturnRows(rows)
}

var valsColumns = []string{"ID", "N", "FL", "BF", "BD", "D", "TS", "TS0", "TS9", "TSTZ", "TZ0", "LTZ", "DS", "YM",
	"CH", "VC", "NCH", "NVC", "C", "NC", "RW", "B"}

// Each type's value as the table view shows it is written so that it does not
// depend on the session's NLS settings: numbers as written, binary floats with
// their suffix or their special constant, dates, timestamps and intervals through
// conversion functions with an explicit format that keeps the fraction and the
// zone, national text with N, binary as HEXTORAW. A text that is not in the shown
// layout (typed in the grid) stays a string literal for Oracle to convert.
func TestRowStatementsWriteEachOracleType(t *testing.T) {
	session, mock := newMockSession(t)
	expectValsColumns(mock)
	writer, err := session.PrepareRowStatements(context.Background(), "HR", "VALS")
	require.NoError(t, err)

	inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: valsColumns,
		Rows: []map[string]any{{
			"ID": float64(1), "N": "12345678901234567890.123456789", "FL": "-0.5", "BF": "3.4028235e+38",
			"BD": "1.7976931348623157e+308", "D": "2024-01-02 03:04:05", "TS": "2024-01-02 03:04:05.123456",
			"TS0": "2024-01-02 03:04:05", "TS9": "2024-01-02 03:04:05.123456789",
			"TSTZ": "2024-01-02 03:04:05.123456 +08:00", "TZ0": "2024-01-02 03:04:05 -05:30",
			"LTZ": "2024-01-02 03:04:05.500000 +00:00", "DS": "+01 02:03:04.500000", "YM": "+01-02",
			"CH": "ab  ", "VC": "O'Reilly", "NCH": "中文", "NVC": "名字's", "C": "clob 文本", "NC": "nclob 文本",
			"RW": "\x00\xffA", "B": []byte{0xde, 0xad},
		}, {
			"ID": "2", "N": float64(1e-7), "BF": "Nan", "BD": "-Inf", "DS": "-00 00:00:01.250000", "YM": "-03-11", "VC": "",
		}, {
			"ID": "3", "N": "-42", "FL": "7", "BF": "Inf", "BD": "Nan",
		}, {
			"ID": "4", "BF": "-Inf", "BD": "5e-324",
		}, {
			"ID": "5", "N": "1,5", "BF": "fast", "D": "2024-01-02", "TS": "2024-01-02 03:04", "TSTZ": "2024-01-02 03:04:05",
			"DS": "1 day",
		}},
	})
	require.NoError(t, err)
	valuesGolden(t, "insert_rows.sql", strings.Join(inserts, "\n")+"\n")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The grid writes the same literals: a DATE / TIMESTAMP / INTERVAL it sets, and
// one it locates a row by, go through their conversion functions.
func TestGridEditStatementsWriteExplicitFormats(t *testing.T) {
	session, mock := newMockSession(t)
	expectValsColumns(mock)

	row := map[string]any{"ID": "1", "D": "2024-01-02 03:04:05", "DS": "+01 02:03:04.500000"}
	statements, err := session.GridEditStatements(context.Background(), "HR", "VALS", dbdriver.GridEditRequest{
		Columns: []string{"ID", "D", "DS"},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "TS", Value: "2024-01-02 03:04:05.5"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "HR"."VALS" SET "TS" = TO_TIMESTAMP('2024-01-02 03:04:05.5', 'YYYY-MM-DD HH24:MI:SS.FF') ` +
		`WHERE ROWID = (SELECT ROWID FROM "HR"."VALS" WHERE "ID" = 1 AND "D" = TO_DATE('2024-01-02 03:04:05', 'YYYY-MM-DD HH24:MI:SS') ` +
		`AND "DS" = TO_DSINTERVAL('+01 02:03:04.500000') FETCH FIRST 1 ROW ONLY)`}, statements.Updates)
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectDocsColumns answers the column-definition query for HR.DOCS: a key, a
// CLOB, an NCLOB, a BLOB and a short text.
func expectDocsColumns(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "DOCS").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0").
			AddRow("BODY", "CLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("NBODY", "NCLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("IMG", "BLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("NOTE", "VARCHAR2(20)", "YES", nil, nil, "0", "0", "0"))
}

// A row whose LOB does not fit one literal (4000 bytes of text, quotes doubled,
// counted in UTF-8 and for national text also in UTF-16; 2000 bytes of HEXTORAW)
// is a PL/SQL block: the row is inserted with an empty LOB in each such column,
// whose locators the INSERT returns, and each value is appended piece by piece,
// a piece never splitting a character; the block ends with a line holding only /.
// LOBs that fit stay literals of the INSERT.
func TestRowStatementsWriteLargeLobsAsBlocks(t *testing.T) {
	session, mock := newMockSession(t)
	expectDocsColumns(mock)
	writer, err := session.PrepareRowStatements(context.Background(), "HR", "DOCS")
	require.NoError(t, err)

	x, y := strings.Repeat("x", 3999), "y 100% %s y"
	n := strings.Repeat("n", 1999)
	img := make([]byte, 2050)
	for i := range img {
		img[i] = byte(i)
	}
	inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind:         dbdriver.RowInsert,
		Columns:      []string{"ID", "BODY", "NBODY", "IMG", "NOTE"},
		OmitDatabase: true,
		Rows: []map[string]any{
			{"ID": "1", "BODY": x + "中" + y, "NBODY": n + "中'z", "IMG": img, "NOTE": "it's"},
			{"ID": "2", "BODY": "small", "NBODY": nil, "IMG": string(img[:2000]), "NOTE": nil},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"DECLARE\n" +
		"  l1 CLOB;\n" +
		"  l2 NCLOB;\n" +
		"  l3 BLOB;\n" +
		"BEGIN\n" +
		`  INSERT INTO "DOCS" ("ID", "BODY", "NBODY", "IMG", "NOTE") VALUES (1, EMPTY_CLOB(), EMPTY_CLOB(), EMPTY_BLOB(), 'it''s')` +
		` RETURNING "BODY", "NBODY", "IMG" INTO l1, l2, l3;` + "\n" +
		"  DBMS_LOB.APPEND(l1, TO_CLOB('" + x + "'));\n" +
		"  DBMS_LOB.APPEND(l1, TO_CLOB('中" + y + "'));\n" +
		"  DBMS_LOB.APPEND(l2, TO_NCLOB(N'" + n + "中'));\n" +
		"  DBMS_LOB.APPEND(l2, TO_NCLOB(N'''z'));\n" +
		"  DBMS_LOB.WRITEAPPEND(l3, 2000, HEXTORAW('" + hex.EncodeToString(img[:2000]) + "'));\n" +
		"  DBMS_LOB.WRITEAPPEND(l3, 50, HEXTORAW('" + hex.EncodeToString(img[2000:]) + "'));\n" +
		"END;\n" +
		"/",
		`INSERT INTO "DOCS" ("ID", "BODY", "NBODY", "IMG", "NOTE") VALUES (2, 'small', NULL, HEXTORAW('` +
			hex.EncodeToString(img[:2000]) + `'), NULL);`,
	}, inserts)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The insert modifier of the request stays between the columns and VALUES in a
// block too.
func TestLargeLobBlockKeepsTheInsertModifier(t *testing.T) {
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Modifier: "/*+ APPEND */", Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "BODY", Type: "CLOB", Value: strings.Repeat("a", 4001)}},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, statement, `INSERT INTO "DOCS" ("BODY") /*+ APPEND */ VALUES (EMPTY_CLOB()) RETURNING "BODY" INTO l1;`)
}
