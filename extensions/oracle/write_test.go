package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectRowKey answers the query for the columns that find a row of schema.table
// (InsertRowKey) with key; none when key is empty.
func expectRowKey(mock sqlmock.Sqlmock, schema, table string, key ...string) {
	rows := sqlmock.NewRows([]string{"COLUMN_NAME"})
	for _, column := range key {
		rows.AddRow(column)
	}
	mock.ExpectQuery(`constraint_type IN \('P', 'U'\)`).WithArgs(schema, table).WillReturnRows(rows)
}

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
	expectRowKey(mock, "HR", "EMP", "ID")
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
	expectRowKey(mock, "HR", "VALS")
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
// counted in UTF-8, national text in its escapes and in UTF-16; 2000 bytes of
// HEXTORAW) is a PL/SQL block: the row is inserted with an empty LOB in each such
// column, whose locators the INSERT returns, and each value is appended piece by
// piece, a piece never splitting a character: a CLOB in string literals, an NCLOB
// in base64 of its UTF-16 (1500 units a piece), a BLOB in base64 of its bytes
// (3000 a piece); the block ends with a line holding only /. LOBs that fit stay
// literals of the INSERT.
func TestRowStatementsWriteLargeLobsAsBlocks(t *testing.T) {
	session, mock := newMockSession(t)
	expectDocsColumns(mock)
	expectRowKey(mock, "HR", "DOCS", "ID")
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
		"  DBMS_LOB.APPEND(l2, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(" + base64UTF16(n[:1500]) + ", 'AL16UTF16')));\n" +
		"  DBMS_LOB.APPEND(l2, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(" + base64UTF16(n[1500:]+"中'z") + ", 'AL16UTF16')));\n" +
		"  DBMS_LOB.WRITEAPPEND(l3, 2050, " + base64Bytes(img) + ");\n" +
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

// National text is written so that Oracle does not convert it through the
// database character set, which loses what that set lacks (an emoji, ï and ø in
// ZHS16GBK): a value with any character outside ASCII is UNISTR of its escapes,
// \XXXX per UTF-16 unit (a supplementary character a surrogate pair), a backslash
// \005C and quotes doubled; an NCHAR's is cast to NCHAR of its length in UTF-16
// units, so that it compares blank-padded with an NCHAR column as N'…' does. An
// ASCII value stays N'…'; other text is unchanged.
func TestNationalTextIsWrittenAsUnicodeEscapes(t *testing.T) {
	for _, tc := range []struct{ name, columnType, value, want string }{
		{"emoji and characters outside GBK", "NVARCHAR2(100)", `张三😀 ï\ø's`, `UNISTR('\5F20\4E09\D83D\DE00 \00EF\005C\00F8''s')`},
		{"NCHAR", "NCHAR(4)", "ï", `CAST(UNISTR('\00EF') AS NCHAR(1))`},
		{"NCHAR with a supplementary character", "NCHAR(4)", "😀a", `CAST(UNISTR('\D83D\DE00a') AS NCHAR(3))`},
		{"NCLOB", "NCLOB", "😀", `UNISTR('\D83D\DE00')`},
		{"ASCII national text", "NVARCHAR2(20)", `it's \n`, `N'it''s \n'`},
		{"VARCHAR2", "VARCHAR2(20 CHAR)", "中文😀", `'中文😀'`},
		{"CLOB", "CLOB", "ø", `'ø'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := literals.Literal(tc.value, tc.columnType)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// One string literal holds 4000 bytes (ORA-01704) and UNISTR returns at most 4000
// bytes of UTF-16, cutting the rest off silently, so national text whose escapes
// do not fit is several UNISTR pieces joined with ||, each within both limits
// and never splitting an escape or a surrogate pair. The concatenation stays
// NVARCHAR2; an NCLOB's pieces are converted with TO_NCLOB so that their
// concatenation is not limited to 4000 bytes.
func TestLongNationalTextIsSplitIntoUnistrPieces(t *testing.T) {
	unistr := func(escapes string, n int) string { return "UNISTR('" + strings.Repeat(escapes, n) + "')" }

	got, err := literals.Literal(strings.Repeat("ø", 2000), "NVARCHAR2(2000)")
	require.NoError(t, err)
	assert.Equal(t, unistr(`\00F8`, 800)+" || "+unistr(`\00F8`, 800)+" || "+unistr(`\00F8`, 400), got)

	got, err = literals.Literal("a"+strings.Repeat("😀", 400), "NVARCHAR2(2000)")
	require.NoError(t, err)
	assert.Equal(t, "UNISTR('a"+strings.Repeat(`\D83D\DE00`, 399)+"') || "+unistr(`\D83D\DE00`, 1), got)

	got, err = literals.Literal(strings.Repeat("a", 2000)+"ø", "NVARCHAR2(4000)")
	require.NoError(t, err)
	assert.Equal(t, unistr("a", 2000)+" || "+unistr(`\00F8`, 1), got)

	// An NCHAR of pieces is cast as a whole; one longer than any NCHAR (1000
	// UTF-16 units) is not cast, which Oracle would refuse (ORA-00910).
	got, err = literals.Literal(strings.Repeat("ø", 1000), "NCHAR(1000)")
	require.NoError(t, err)
	assert.Equal(t, "CAST(("+unistr(`\00F8`, 800)+" || "+unistr(`\00F8`, 200)+") AS NCHAR(1000))", got)
	got, err = literals.Literal(strings.Repeat("ø", 1001), "NCHAR(1000)")
	require.NoError(t, err)
	assert.Equal(t, unistr(`\00F8`, 800)+" || "+unistr(`\00F8`, 201), got)

	got, err = literals.Literal(strings.Repeat("ø", 801), "NCLOB")
	require.NoError(t, err)
	assert.Equal(t, "TO_NCLOB("+unistr(`\00F8`, 800)+") || TO_NCLOB("+unistr(`\00F8`, 1)+")", got)
}

// The grid sets and locates national values with the same escapes.
func TestGridEditStatementsWriteNationalTextAsUnicodeEscapes(t *testing.T) {
	session, mock := newMockSession(t)
	expectValsColumns(mock)

	statements, err := session.GridEditStatements(context.Background(), "HR", "VALS", dbdriver.GridEditRequest{
		Columns: []string{"ID", "NCH"},
		Updates: []dbdriver.GridUpdate{{Row: map[string]any{"ID": "1", "NCH": "ï"},
			Set: []dbdriver.ColumnValue{{Column: "NVC", Value: "张三😀"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "HR"."VALS" SET "NVC" = UNISTR('\5F20\4E09\D83D\DE00') ` +
		`WHERE ROWID = (SELECT ROWID FROM "HR"."VALS" WHERE "ID" = 1 AND "NCH" = CAST(UNISTR('\00EF') AS NCHAR(1)) FETCH FIRST 1 ROW ONLY)`},
		statements.Updates)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An NCLOB too large for one literal (its UNISTR escapes past 4000 bytes) is
// appended in pieces of 1500 UTF-16 units, an emoji that would end a piece
// starting the next one whole rather than splitting its surrogate pair.
func TestLargeNclobBlockAppendsUTF16Pieces(t *testing.T) {
	a := strings.Repeat("a", 1499)
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "NBODY", Type: "NCLOB", Value: a + "😀ï" + strings.Repeat("😀", 400)}},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "DECLARE\n  l1 NCLOB;\nBEGIN\n"+
		`  INSERT INTO "DOCS" ("NBODY") VALUES (EMPTY_CLOB()) RETURNING "NBODY" INTO l1;`+"\n"+
		"  DBMS_LOB.APPEND(l1, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR("+base64UTF16(a)+", 'AL16UTF16')));\n"+
		"  DBMS_LOB.APPEND(l1, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR("+base64UTF16("😀ï"+strings.Repeat("😀", 400))+", 'AL16UTF16')));\n"+
		"END;\n/", statement)

	// National text whose escapes fit one literal stays the INSERT's literal.
	_, ok, err = dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "NBODY", Type: "NCLOB", Value: strings.Repeat("😀", 400)}},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

// The text of an XMLType, JSON or VECTOR value too large for one literal (a
// 1536-dimension vector is) is built in a temporary CLOB, piece by piece, before
// the INSERT, which gives it to the column (XMLType through its constructor,
// the others converting it as they do a literal) and then frees it; a large LOB
// of the same row is still appended after the INSERT. Text that fits stays the
// INSERT's literal.
func TestLargeSerializedTextIsBuiltBeforeTheInsert(t *testing.T) {
	x, j, body := "<a>"+strings.Repeat("x", 3997), strings.Repeat("j", 4001), strings.Repeat("b", 4001)
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{
			{Name: "ID", Type: "NUMBER", Value: "1"},
			{Name: "X", Type: "XMLTYPE", Value: x + "</a>"},
			{Name: "J", Type: "JSON", Value: j},
			{Name: "BODY", Type: "CLOB", Value: body},
			{Name: "V", Type: "VECTOR(3, FLOAT32, DENSE)", Value: "[1,2,3]"},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "DECLARE\n  l1 CLOB;\n  l2 CLOB;\n  l3 CLOB;\nBEGIN\n"+
		"  DBMS_LOB.CREATETEMPORARY(l1, TRUE);\n"+
		"  DBMS_LOB.APPEND(l1, TO_CLOB('"+x+"'));\n"+
		"  DBMS_LOB.APPEND(l1, TO_CLOB('</a>'));\n"+
		"  DBMS_LOB.CREATETEMPORARY(l2, TRUE);\n"+
		"  DBMS_LOB.APPEND(l2, TO_CLOB('"+j[:4000]+"'));\n"+
		"  DBMS_LOB.APPEND(l2, TO_CLOB('j'));\n"+
		`  INSERT INTO "DOCS" ("ID", "X", "J", "BODY", "V") VALUES (1, XMLTYPE(l1), l2, EMPTY_CLOB(), '[1,2,3]') RETURNING "BODY" INTO l3;`+"\n"+
		"  DBMS_LOB.FREETEMPORARY(l1);\n"+
		"  DBMS_LOB.FREETEMPORARY(l2);\n"+
		"  DBMS_LOB.APPEND(l3, TO_CLOB('"+body[:4000]+"'));\n"+
		"  DBMS_LOB.APPEND(l3, TO_CLOB('b'));\n"+
		"END;\n/", statement)

	// Only built text: the INSERT returns nothing.
	statement, ok, err = dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "V", Type: "VECTOR", Value: strings.Repeat("1,", 2001)}},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, statement, `  INSERT INTO "DOCS" ("V") VALUES (l1);`+"\n  DBMS_LOB.FREETEMPORARY(l1);\nEND;\n/")

	_, ok, err = dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "X", Type: "XMLTYPE", Value: "<a>" + strings.Repeat("x", 3993) + "</a>"}},
	})
	require.NoError(t, err)
	assert.False(t, ok)
}

// base64UTF16 is the PL/SQL raw of text's UTF-16 (big-endian) in base64.
func base64UTF16(text string) string {
	var raw []byte
	for _, u := range utf16.Encode([]rune(text)) {
		raw = append(raw, byte(u>>8), byte(u))
	}
	return base64Bytes(raw)
}

// base64Bytes is the PL/SQL raw of raw in base64.
func base64Bytes(raw []byte) string {
	return "UTL_ENCODE.BASE64_DECODE(UTL_RAW.CAST_TO_RAW('" + base64.StdEncoding.EncodeToString(raw) + "'))"
}

// In a table with neither a primary key nor a unique key of NOT NULL columns (no
// InsertRow.Key), a row is one PL/SQL block however many pieces it appends (up to
// maxBlockBytes, TestRowWithoutAKeyIsOneBlockOfAtMost24MB): it inserts the row
// with empty LOBs and appends every piece to the locators the INSERT returns
// (spec decision 10), depending on nothing another statement left in the
// session. The pieces are the value.
func TestLargeRowIsOneBlock(t *testing.T) {
	var body strings.Builder
	for i := 0; body.Len() < 2_500_000; i++ {
		fmt.Fprintf(&body, "line %d 'q' 中文\n", i)
	}
	img := make([]byte, 1_200_000)
	for i := range img {
		img[i] = byte(i * 7)
	}
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"HR"."DOCS"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{
			{Name: "ID", Type: "NUMBER", Value: "7"},
			{Name: "BODY", Type: "CLOB", Value: body.String()},
			{Name: "IMG", Type: "BLOB", Value: img},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	statements, err := splitOracle(context.Background(), statement)
	require.NoError(t, err)
	require.Len(t, statements, 1)
	st := statements[0].Text
	assert.True(t, strings.HasPrefix(st, "DECLARE\n  l1 CLOB;\n  l2 BLOB;\nBEGIN\n"+
		`  INSERT INTO "HR"."DOCS" ("ID", "BODY", "IMG") VALUES (7, EMPTY_CLOB(), EMPTY_BLOB()) RETURNING "BODY", "IMG" INTO l1, l2;`+"\n"), st[:300])
	assert.True(t, strings.HasSuffix(st, "\nEND;"), st[len(st)-100:])

	clobPiece := regexp.MustCompile(`DBMS_LOB\.APPEND\(l1, TO_CLOB\('((?:[^']|'')*)'\)\)`)
	blobPiece := regexp.MustCompile(`WRITEAPPEND\(l2, (\d+), UTL_ENCODE\.BASE64_DECODE\(UTL_RAW\.CAST_TO_RAW\('([^']*)'\)\)\)`)
	var text strings.Builder
	var data []byte
	for _, m := range clobPiece.FindAllStringSubmatch(st, -1) {
		text.WriteString(strings.ReplaceAll(m[1], "''", "'"))
	}
	for _, m := range blobPiece.FindAllStringSubmatch(st, -1) {
		raw, err := base64.StdEncoding.DecodeString(m[2])
		require.NoError(t, err)
		assert.Equal(t, m[1], strconv.Itoa(len(raw)))
		data = append(data, raw...)
	}
	assert.Equal(t, body.String(), text.String())
	assert.Equal(t, img, data)
}

// largeRowValues are a row's values too large for one block of about 1 MB: a
// CLOB of 2.5 MB with quotes and Chinese, a BLOB of 1.2 MB.
func largeRowValues() (string, []byte) {
	var body strings.Builder
	for i := 0; body.Len() < 2_500_000; i++ {
		fmt.Fprintf(&body, "line %d 'q' 中文\n", i)
	}
	img := make([]byte, 1_200_000)
	for i := range img {
		img[i] = byte(i * 7)
	}
	return body.String(), img
}

var (
	clobAppend  = regexp.MustCompile(`DBMS_LOB\.APPEND\((l\d+), TO_CLOB\('((?:[^']|'')*)'\)\)`)
	nclobAppend = regexp.MustCompile(`DBMS_LOB\.APPEND\((l\d+), TO_NCLOB\(UTL_I18N\.RAW_TO_NCHAR\(UTL_ENCODE\.BASE64_DECODE\(UTL_RAW\.CAST_TO_RAW\('([^']*)'\)\), 'AL16UTF16'\)\)\)`)
	blobAppend  = regexp.MustCompile(`WRITEAPPEND\((l\d+), (\d+), UTL_ENCODE\.BASE64_DECODE\(UTL_RAW\.CAST_TO_RAW\('([^']*)'\)\)\)`)
	appendLine  = regexp.MustCompile(`^  DBMS_LOB\.(APPEND|WRITEAPPEND)\((l\d+), `)
)

// appended is what statements append to each variable, in order: a CLOB's text,
// an NCLOB's text (from its UTF-16), a BLOB's bytes.
func appended(t *testing.T, statements ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, st := range statements {
		for _, m := range clobAppend.FindAllStringSubmatch(st, -1) {
			out[m[1]] += strings.ReplaceAll(m[2], "''", "'")
		}
		for _, m := range nclobAppend.FindAllStringSubmatch(st, -1) {
			raw, err := base64.StdEncoding.DecodeString(m[2])
			require.NoError(t, err)
			units := make([]uint16, len(raw)/2)
			for i := range units {
				units[i] = uint16(raw[2*i])<<8 | uint16(raw[2*i+1])
			}
			out[m[1]] += string(utf16.Decode(units))
		}
		for _, m := range blobAppend.FindAllStringSubmatch(st, -1) {
			raw, err := base64.StdEncoding.DecodeString(m[3])
			require.NoError(t, err)
			assert.Equal(t, m[2], strconv.Itoa(len(raw)))
			out[m[1]] += string(raw)
		}
	}
	return out
}

// outline is statement with each append's piece left out and each run of appends
// to one variable one line "  DBMS_LOB.APPEND(l1, …) × n", for a golden file a
// reader can follow.
func outline(statement string) string {
	for _, call := range []*regexp.Regexp{clobAppend, nclobAppend} {
		statement = call.ReplaceAllString(statement, "DBMS_LOB.APPEND($1, …)")
	}
	statement = blobAppend.ReplaceAllString(statement, "WRITEAPPEND($1, …)")
	var b strings.Builder
	lines := strings.Split(statement, "\n")
	for i := 0; i < len(lines); i++ {
		m := appendLine.FindStringSubmatch(lines[i])
		if m == nil {
			b.WriteString(lines[i] + "\n")
			continue
		}
		n := 1
		for i+1 < len(lines) && lines[i+1] == lines[i] {
			i, n = i+1, n+1
		}
		fmt.Fprintf(&b, "  DBMS_LOB.%s(%s, …) × %d\n", m[1], m[2], n)
	}
	return b.String()
}

// splitBlocks splits a row's statement as the run does and checks that each
// block stays within about 1 MB of appends.
func splitBlocks(t *testing.T, statement string) []string {
	t.Helper()
	statements, err := splitOracle(context.Background(), statement)
	require.NoError(t, err)
	blocks := make([]string, len(statements))
	for i, st := range statements {
		blocks[i] = st.Text
		assert.LessOrEqual(t, len(st.Text), 1_100_000, "block %d", i)
	}
	return blocks
}

// A row whose appends take more than about 1 MB, in a table with a primary key
// (InsertRowKey, looked up when the writer is prepared), is several blocks, each
// ending with a line holding only /: the first inserts the row with empty LOBs
// and appends its first pieces; each further one finds the row by its key with
// SELECT … FOR UPDATE, which locks it so its LOBs can be written, and appends the
// next pieces of about 1 MB to the LOBs it names. No block depends on what
// another left in the session. The pieces are the values (spec decision 10).
func TestLargeRowIsSeveralBlocksFoundByThePrimaryKey(t *testing.T) {
	session, mock := newMockSession(t)
	expectDocsColumns(mock)
	expectRowKey(mock, "HR", "DOCS", "ID")
	writer, err := session.PrepareRowStatements(context.Background(), "HR", "DOCS")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	body, img := largeRowValues()
	statements, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"ID", "BODY", "IMG", "NOTE"},
		Rows: []map[string]any{{"ID": "7", "BODY": body, "IMG": img, "NOTE": "it's"}},
	})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	assert.True(t, strings.HasSuffix(statements[0], "\nEND;\n/"))
	valuesGolden(t, "large_row_primary_key.txt", outline(statements[0]))

	blocks := splitBlocks(t, statements[0])
	assert.Len(t, blocks, 5)
	got := appended(t, blocks...)
	assert.Equal(t, body, got["l1"])
	assert.Equal(t, string(img), got["l2"])
}

// A unique key of NOT NULL columns finds the row as a primary key does (which
// key, InsertRowKey decides); its values, a RAW, national text outside ASCII and
// GBK and a DATE among them, are written with the INSERT's literals, so the
// condition compares each column with what the INSERT stored.
func TestLargeRowIsFoundByACompositeKeyOfAnyType(t *testing.T) {
	var text strings.Builder
	for text.Len() < 1_800_000 {
		text.WriteString("中文😀'x\\ ")
	}
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals, Key: []string{"CODE", "NAME", "BORN"},
		Columns: []sqldb.InsertColumn{
			{Name: "CODE", Type: "RAW(16)", Value: []byte{0x00, 0xff, 0x10}},
			{Name: "NAME", Type: "NVARCHAR2(20)", Value: "张三😀"},
			{Name: "BORN", Type: "DATE", Value: "2024-02-29 13:14:15"},
			{Name: "NBODY", Type: "NCLOB", Value: text.String()},
		},
	})
	require.NoError(t, err)
	require.True(t, ok)
	valuesGolden(t, "large_row_composite_key.txt", outline(statement))
	blocks := splitBlocks(t, statement)
	assert.Len(t, blocks, 3)
	assert.Equal(t, text.String(), appended(t, blocks...)["l1"])
}

// A table with neither key keeps the row one block (TestLargeRowIsOneBlock), and so
// does a statement that does not write every key column with a value: a later
// block could not find the row.
func TestLargeRowWithoutItsKeyIsOneBlock(t *testing.T) {
	body, img := largeRowValues()
	for name, columns := range map[string][]sqldb.InsertColumn{
		"key not written": {{Name: "BODY", Type: "CLOB", Value: body}, {Name: "IMG", Type: "BLOB", Value: img}},
		"key without a value": {{Name: "ID", Type: "NUMBER"}, {Name: "BODY", Type: "CLOB", Value: body},
			{Name: "IMG", Type: "BLOB", Value: img}},
	} {
		t.Run(name, func(t *testing.T) {
			statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
				TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals, Key: []string{"ID"}, Columns: columns,
			})
			require.NoError(t, err)
			require.True(t, ok)
			statements, err := splitOracle(context.Background(), statement)
			require.NoError(t, err)
			assert.Len(t, statements, 1)
		})
	}
}

// Without a key a 16 MB BLOB (the largest value, maxLobBytes) is one block of
// about 23 MB, which runs (integration_lob_test.go); a row whose one block would
// take more than 24 MB of SQL text stops the statement before any of it is
// written, naming the table and the size, instead of writing a block that would
// exhaust the extension's memory where it runs. With a key the same row is blocks
// of about 1 MB.
func TestRowWithoutAKeyIsOneBlockOfAtMost24MB(t *testing.T) {
	b16, b2 := make([]byte, maxLobBytes), make([]byte, 2<<20)
	statement, ok, err := dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"BIG"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "ID", Type: "NUMBER", Value: "1"}, {Name: "B", Type: "BLOB", Value: b16}},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, strings.Count(statement, "\n/"))
	assert.Greater(t, len(statement), 22_000_000)
	valuesGolden(t, "large_row_no_key.txt", outline(statement))

	row := sqldb.InsertRow{
		TableRef: `"BIG"`, Quote: quoteIdent, Literals: literals,
		Columns: []sqldb.InsertColumn{{Name: "ID", Type: "NUMBER", Value: "1"}, {Name: "B", Type: "BLOB", Value: b16},
			{Name: "B2", Type: "BLOB", Value: b2}},
	}
	_, _, err = dialect{}.RowInsertStatement(row)
	require.Error(t, err)
	assert.Regexp(t, `^oracle: a row of table "BIG" would be a PL/SQL block of about 2\d\.\d MB \(\d+ bytes\) of SQL text, `+
		`more than the 24 MB \(25165824 bytes\) one block may take: the table has neither a primary key nor a unique key `+
		`of NOT NULL columns by which further blocks could find the row$`, err.Error())

	// The same row whose statement leaves its key out.
	row.Key = []string{"ID", "CODE"}
	_, _, err = dialect{}.RowInsertStatement(row)
	assert.ErrorContains(t, err, `one block may take: the statement does not write the columns that find the row ("ID", "CODE") with values`)

	// The text of an XMLType, JSON or VECTOR value is built within one block,
	// key or not: a value whose literal pieces pass 24 MB (quotes count twice).
	_, _, err = dialect{}.RowInsertStatement(sqldb.InsertRow{
		TableRef: `"BIG"`, Quote: quoteIdent, Literals: literals, Key: []string{"ID"},
		Columns: []sqldb.InsertColumn{{Name: "ID", Type: "NUMBER", Value: "1"},
			{Name: "J", Type: "JSON", Value: strings.Repeat(`'`, 13<<20)}},
	})
	assert.ErrorContains(t, err, `one block may take: the text of its XMLType, JSON or VECTOR values is built within the first block`)

	row.Key = []string{"ID"}
	statement, ok, err = dialect{}.RowInsertStatement(row)
	require.NoError(t, err)
	require.True(t, ok)
	blocks := splitBlocks(t, statement)
	assert.Greater(t, len(blocks), 20)
}
