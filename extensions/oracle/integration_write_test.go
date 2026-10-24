package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSession opens the shared session over the driver's database with one
// connection, creates table (columns as given) dropped when the test ends, and
// returns the login user's schema the table is in.
func writeSession(t *testing.T, target oracleTarget, prefix, columns string) (s *sqldb.Session, schema, table string) {
	t.Helper()
	db := target.open(t, "")
	db.SetMaxOpenConns(1)
	s = sqldb.NewSession(db, dialect{})
	table = fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()%1_000_000_000)
	ctx := integrationContext(t)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT USER FROM DUAL`).Scan(&schema))
	_, err := s.Execute(ctx, "CREATE TABLE "+table+" ("+columns+")")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := s.Execute(ctx, "DROP TABLE "+table+" PURGE")
		assert.NoError(t, err)
	})
	return s, schema, table
}

// runEach runs each statement on its own, as the grid submits them, and requires
// each to change exactly one row.
func runEach(t *testing.T, s *sqldb.Session, statements []string) {
	t.Helper()
	for _, statement := range statements {
		res, err := s.Execute(integrationContext(t), statement)
		require.NoError(t, err, statement)
		assert.Equal(t, int64(1), res.AffectedRows, statement)
	}
}

// queryStrings is the first row of query, each value as text.
func queryStrings(t *testing.T, s *sqldb.Session, query string) []string {
	t.Helper()
	res, err := s.Execute(integrationContext(t), query)
	require.NoError(t, err, query)
	require.NotNil(t, res.Rows)
	require.NotEmpty(t, res.Rows.Rows, query)
	out := make([]string, len(res.Rows.Rows[0]))
	for i, v := range res.Rows.Rows[0] {
		if v != nil {
			out[i] = fmt.Sprint(v)
		}
	}
	return out
}

// Grid edits on a table with a primary key: the new row's text, quote included,
// and binary values arrive as written; the update and delete hit only their row.
func TestIntegrationGridEditByPrimaryKey(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_WR_PK",
			"ID NUMBER(10) PRIMARY KEY, NAME VARCHAR2(20 CHAR), CODE RAW(8), DATA BLOB")
		ctx := integrationContext(t)
		_, err := s.Execute(ctx, "INSERT INTO "+table+" (ID, NAME) VALUES (1, 'ann')")
		require.NoError(t, err)
		_, err = s.Execute(ctx, "INSERT INTO "+table+" (ID, NAME) VALUES (2, 'bob')")
		require.NoError(t, err)

		statements, err := s.GridEditStatements(ctx, schema, table, dbdriver.GridEditRequest{
			Columns:     []string{"ID", "NAME", "CODE", "DATA"},
			PrimaryKeys: []string{"ID"},
			Inserts: []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{
				{Column: "ID", Value: float64(3)}, {Column: "NAME", Value: "O'Reilly 中文"},
				{Column: "CODE", Value: "\x00\x7fA"}, {Column: "DATA", Value: "\x01\x02"},
			}}},
			Updates: []dbdriver.GridUpdate{{
				Row: map[string]any{"ID": "1", "NAME": "ann", "CODE": nil, "DATA": nil},
				Set: []dbdriver.ColumnValue{{Column: "NAME", Value: "anne"}},
			}},
			Deletes: []map[string]any{{"ID": "2", "NAME": "bob", "CODE": nil, "DATA": nil}},
		})
		require.NoError(t, err)
		runEach(t, s, statements.Inserts)
		runEach(t, s, statements.Updates)
		runEach(t, s, statements.Deletes)

		assert.Equal(t, []string{"O'Reilly 中文", "007F41", "0102"},
			queryStrings(t, s, "SELECT NAME, RAWTOHEX(CODE), RAWTOHEX(DBMS_LOB.SUBSTR(DATA)) FROM "+table+" WHERE ID = 3"))
		assert.Equal(t, []string{"anne"}, queryStrings(t, s, "SELECT NAME FROM "+table+" WHERE ID = 1"))
		assert.Equal(t, []string{"2"}, queryStrings(t, s, "SELECT COUNT(*) FROM "+table))
	})
}

// Without a primary key ROWID picks one of identical rows: the update and the
// delete each change one of them.
func TestIntegrationGridEditWithoutPrimaryKey(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_WR_NOPK", "N NUMBER(10), NAME VARCHAR2(20)")
		ctx := integrationContext(t)
		for range 3 {
			_, err := s.Execute(ctx, "INSERT INTO "+table+" (N, NAME) VALUES (1, 'dup')")
			require.NoError(t, err)
		}

		row := map[string]any{"N": "1", "NAME": "dup"}
		statements, err := s.GridEditStatements(ctx, schema, table, dbdriver.GridEditRequest{
			Columns: []string{"N", "NAME"},
			Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "NAME", Value: "one"}}}},
			Deletes: []map[string]any{row},
		})
		require.NoError(t, err)
		runEach(t, s, statements.Updates)
		runEach(t, s, statements.Deletes)

		assert.Equal(t, []string{"1", "1"}, queryStrings(t, s,
			"SELECT COUNT(CASE NAME WHEN 'one' THEN 1 END), COUNT(CASE NAME WHEN 'dup' THEN 1 END) FROM "+table))
	})
}

// Oracle cannot compare LOBs with =: without a primary key a row carrying a CLOB
// and a BLOB is still found by its original values (DBMS_LOB.COMPARE), and
// ROWID narrows it to one of two identical rows.
func TestIntegrationGridEditWithoutPrimaryKeyLobColumns(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_WR_LOB", "N NUMBER(10), BODY CLOB, IMG BLOB")
		ctx := integrationContext(t)
		for range 2 {
			_, err := s.Execute(ctx, "INSERT INTO "+table+" (N, BODY, IMG) VALUES (1, 'it''s a body', HEXTORAW('DEAD'))")
			require.NoError(t, err)
		}

		row := map[string]any{"N": "1", "BODY": "it's a body", "IMG": "\xde\xad"}
		statements, err := s.GridEditStatements(ctx, schema, table, dbdriver.GridEditRequest{
			Columns: []string{"N", "BODY", "IMG"},
			Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "BODY", Value: "changed"}}}},
			Deletes: []map[string]any{{"N": "1", "BODY": "changed", "IMG": "\xde\xad"}},
		})
		require.NoError(t, err)
		runEach(t, s, statements.Updates)
		assert.Equal(t, []string{"1", "1"}, queryStrings(t, s,
			"SELECT COUNT(CASE WHEN DBMS_LOB.COMPARE(BODY, 'changed') = 0 THEN 1 END), "+
				"COUNT(CASE WHEN DBMS_LOB.COMPARE(BODY, 'it''s a body') = 0 THEN 1 END) FROM "+table))
		runEach(t, s, statements.Deletes)
		assert.Equal(t, []string{"1"}, queryStrings(t, s, "SELECT COUNT(*) FROM "+table))
	})
}

// A new row with nothing filled in takes every default, an identity column's too.
func TestIntegrationGridEditInsertsADefaultRow(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_WR_DEF",
			"ID NUMBER GENERATED ALWAYS AS IDENTITY, NAME VARCHAR2(20) DEFAULT 'anon', NOTE VARCHAR2(20)")
		statements, err := s.GridEditStatements(integrationContext(t), schema, table, dbdriver.GridEditRequest{
			Columns: []string{"ID", "NAME", "NOTE"},
			Inserts: []dbdriver.GridInsert{{}},
		})
		require.NoError(t, err)
		runEach(t, s, statements.Inserts)
		assert.Equal(t, []string{"1", "anon", ""}, queryStrings(t, s, "SELECT ID, NAME, NOTE FROM "+table))
	})
}

// The values the table view shows write back unchanged: copying a row as UPDATE
// (and the grid, which writes the same literals) keeps DATE, TIMESTAMP to
// nanoseconds, the time zone, a local time zone and binary values as they were
// (the session's NLS formats read the shown layout, connect.go), and copying it as
// INSERT recreates it.
func TestIntegrationDisplayedValuesWriteBack(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_WR_VAL",
			"ID NUMBER(10) PRIMARY KEY, D DATE, TS TIMESTAMP(6), TS9 TIMESTAMP(9), TSTZ TIMESTAMP(3) WITH TIME ZONE, "+
				"TZ0 TIMESTAMP(0) WITH TIME ZONE, TSLTZ TIMESTAMP(6) WITH LOCAL TIME ZONE, RW RAW(4), BL BLOB")
		ctx := integrationContext(t)
		_, err := s.Execute(ctx, "INSERT INTO "+table+" VALUES (1, DATE '2026-10-23' + 3723/86400, "+
			"TIMESTAMP '2026-10-23 01:02:03.123456', TIMESTAMP '2026-10-23 01:02:03.123456789', "+
			"TIMESTAMP '2026-10-23 01:02:03.100 +08:00', TIMESTAMP '2026-10-23 01:02:03 -05:30', "+
			"TIMESTAMP '2026-10-23 01:02:03.654321', "+
			"HEXTORAW('00417F10'), HEXTORAW('0A0B'))")
		require.NoError(t, err)
		const readBack = "SELECT TO_CHAR(D, 'YYYY-MM-DD HH24:MI:SS'), TO_CHAR(TS, 'YYYY-MM-DD HH24:MI:SS.FF6'), " +
			"TO_CHAR(TS9, 'YYYY-MM-DD HH24:MI:SS.FF9'), TO_CHAR(TSTZ, 'YYYY-MM-DD HH24:MI:SS.FF3 TZH:TZM'), " +
			"TO_CHAR(TZ0, 'YYYY-MM-DD HH24:MI:SS TZH:TZM'), TO_CHAR(TSLTZ, 'YYYY-MM-DD HH24:MI:SS.FF6'), " +
			"RAWTOHEX(RW), RAWTOHEX(DBMS_LOB.SUBSTR(BL)) FROM "
		want := []string{
			"2026-10-23 01:02:03", "2026-10-23 01:02:03.123456", "2026-10-23 01:02:03.123456789",
			"2026-10-23 01:02:03.100 +08:00", "2026-10-23 01:02:03 -05:30", "2026-10-23 01:02:03.654321",
			"00417F10", "0A0B",
		}

		page, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		require.Len(t, page.Rows, 1)
		row := columnValues(page, 0)
		writer, err := s.PrepareRowStatements(ctx, schema, table)
		require.NoError(t, err)

		updates, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: dbdriver.RowUpdate, Columns: page.Columns, Rows: []map[string]any{row}, PrimaryKeys: []string{"ID"},
		})
		require.NoError(t, err)
		runEach(t, s, updates)
		assert.Equal(t, want, queryStrings(t, s, readBack+table+" WHERE ID = 1"))

		row["ID"] = float64(2)
		inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: dbdriver.RowInsert, Columns: page.Columns, Rows: []map[string]any{row},
		})
		require.NoError(t, err)
		runEach(t, s, inserts)
		assert.Equal(t, want, queryStrings(t, s, readBack+table+" WHERE ID = 2"))
	})
}
