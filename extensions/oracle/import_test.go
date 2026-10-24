package main

import (
	"context"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func text(s string) dbdriver.ImportValue {
	return dbdriver.ImportValue{Kind: dbdriver.ImportText, Text: s}
}

var null = dbdriver.ImportValue{Kind: dbdriver.ImportNull}

func importStatements(t *testing.T, req dbdriver.ImportRequest) []string {
	t.Helper()
	session, mock := newMockSession(t)
	expectEmpColumns(mock)
	statements, err := session.ImportStatements(context.Background(), "HR", "EMP", req)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	return statements
}

// The column types decide how the import converts the source file's values.
func TestColumnCategory(t *testing.T) {
	for columnType, want := range map[string]dbdriver.ColumnCategory{
		"NUMBER(10)":                   dbdriver.CategoryNumber,
		"NUMBER":                       dbdriver.CategoryNumber,
		"FLOAT(126)":                   dbdriver.CategoryNumber,
		"BINARY_DOUBLE":                dbdriver.CategoryNumber,
		"DATE":                         dbdriver.CategoryDateTime,
		"TIMESTAMP(6)":                 dbdriver.CategoryDateTime,
		"TIMESTAMP(6) WITH TIME ZONE":  dbdriver.CategoryDateTime,
		"RAW(16)":                      dbdriver.CategoryBinary,
		"BLOB":                         dbdriver.CategoryBinary,
		"VARCHAR2(20 CHAR)":            dbdriver.CategoryText,
		"CLOB":                         dbdriver.CategoryText,
		"INTERVAL DAY(2) TO SECOND(6)": dbdriver.CategoryText,
	} {
		assert.Equal(t, want, dialect{}.ColumnCategory(columnType), columnType)
	}
}

// Appending writes one INSERT per row (Oracle 19c has no multi-row VALUES), each
// without ';', values by the column's type; a copy first empties the table.
func TestImportAppendAndCopy(t *testing.T) {
	req := dbdriver.ImportRequest{
		Mode:    dbdriver.ImportAppend,
		Columns: []string{"ID", "NAME", "CODE", "BORN"},
		Rows: [][]dbdriver.ImportValue{
			{text("1"), text("O'Reilly"), {Kind: dbdriver.ImportBinary, Hex: "00ff"}, text("2024-01-02 03:04:05")},
			{text("2"), null, null, null},
		},
		ExtendedInsert: true, MaxStatementSizeKB: 1,
	}
	insert := []string{
		`INSERT INTO "HR"."EMP" ("ID", "NAME", "CODE", "BORN") VALUES ('1', 'O''Reilly', HEXTORAW('00ff'), '2024-01-02 03:04:05')`,
		`INSERT INTO "HR"."EMP" ("ID", "NAME", "CODE", "BORN") VALUES ('2', NULL, NULL, NULL)`,
	}
	assert.Equal(t, insert, importStatements(t, req))

	req.Mode = dbdriver.ImportCopy
	copied := importStatements(t, req)
	require.Len(t, copied, 3)
	// The shared session writes the clearing DELETE with a ";" the connector drops.
	assert.Equal(t, `DELETE FROM "HR"."EMP";`, copied[0])
	assert.Equal(t, insert, copied[1:])
}

// Oracle has no ON CONFLICT: upsert is a MERGE per row that updates only the
// non-key columns, skip is a MERGE with only the insert branch.
func TestImportMerge(t *testing.T) {
	req := dbdriver.ImportRequest{
		Mode:    dbdriver.ImportAppendUpdate,
		Columns: []string{"ID", "NAME"},
		Keys:    []string{"ID"},
		Rows:    [][]dbdriver.ImportValue{{text("1"), text("a")}, {text("2"), null}},
	}
	const insert = `WHEN NOT MATCHED THEN INSERT ("ID", "NAME") VALUES (s."ID", s."NAME")`
	assert.Equal(t, []string{
		`MERGE INTO "HR"."EMP" t USING (SELECT '1' AS "ID", 'a' AS "NAME" FROM DUAL) s ON (t."ID" = s."ID") ` +
			`WHEN MATCHED THEN UPDATE SET t."NAME" = s."NAME" ` + insert,
		`MERGE INTO "HR"."EMP" t USING (SELECT '2' AS "ID", NULL AS "NAME" FROM DUAL) s ON (t."ID" = s."ID") ` +
			`WHEN MATCHED THEN UPDATE SET t."NAME" = s."NAME" ` + insert,
	}, importStatements(t, req))

	req.Mode = dbdriver.ImportAppendSkip
	assert.Equal(t, []string{
		`MERGE INTO "HR"."EMP" t USING (SELECT '1' AS "ID", 'a' AS "NAME" FROM DUAL) s ON (t."ID" = s."ID") ` + insert,
		`MERGE INTO "HR"."EMP" t USING (SELECT '2' AS "ID", NULL AS "NAME" FROM DUAL) s ON (t."ID" = s."ID") ` + insert,
	}, importStatements(t, req))
}

// With only key columns there is nothing to update: the upsert degrades to a skip.
func TestImportMergeWithoutUpdatableColumnsSkips(t *testing.T) {
	statements := importStatements(t, dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppendUpdate, Columns: []string{"ID"}, Keys: []string{"ID"},
		Rows: [][]dbdriver.ImportValue{{text("1")}},
	})
	assert.Equal(t, []string{
		`MERGE INTO "HR"."EMP" t USING (SELECT '1' AS "ID" FROM DUAL) s ON (t."ID" = s."ID") ` +
			`WHEN NOT MATCHED THEN INSERT ("ID") VALUES (s."ID")`,
	}, statements)
}
