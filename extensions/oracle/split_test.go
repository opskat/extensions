package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// split runs splitOracle and checks the part of the host's Statement contract
// that does not depend on the case: text is the sql slice, statements are ordered
// and disjoint, types are uppercase and a reason comes with danger only.
func split(t *testing.T, sql string) []dbdriver.Statement {
	t.Helper()
	got, err := splitOracle(context.Background(), sql)
	require.NoError(t, err)
	prev := 0
	for i, s := range got {
		require.Less(t, s.Start, s.End, "statement %d", i)
		require.LessOrEqual(t, s.End, len(sql), "statement %d", i)
		require.GreaterOrEqual(t, s.Start, prev, "statement %d overlaps", i)
		prev = s.End
		assert.Equal(t, sql[s.Start:s.End], s.Text, "statement %d", i)
		assert.Equal(t, strings.ToUpper(s.Type), s.Type)
		assert.Equal(t, s.Dangerous, s.Reason != "", "statement %d reason", i)
		if s.Dangerous {
			assert.Contains(t, dbdriver.DangerReasons(), s.Reason)
		}
	}
	return got
}

func texts(sts []dbdriver.Statement) []string {
	out := make([]string, len(sts))
	for i, s := range sts {
		out[i] = s.Text
	}
	return out
}

func TestSplitPlainStatements(t *testing.T) {
	got := split(t, "SELECT 1 FROM dual;\n  select 2 from dual ;\nSELECT 3 FROM dual")
	assert.Equal(t, []string{"SELECT 1 FROM dual", "select 2 from dual", "SELECT 3 FROM dual"}, texts(got))
	assert.Empty(t, split(t, "  \n ; ;\n"))
	assert.Empty(t, split(t, "-- only a comment\n/* and another */"))
}

func TestSplitIgnoresSemicolonsAndSlashesInsideLiterals(t *testing.T) {
	for name, sql := range map[string]string{
		"string":        "SELECT 'a;b' FROM dual",
		"escaped quote": "SELECT 'it''s; ok' FROM dual",
		"q bracket":     "SELECT q'[a;b']c]' FROM dual",
		"q bang":        "SELECT Q'!it's; ok!' FROM dual",
		"national q":    "SELECT nq'{a;b}' FROM dual",
		"identifier":    `SELECT "a;b" FROM dual`,
		"line comment":  "SELECT 1 -- a; b\nFROM dual",
		"block comment": "SELECT 1 /* a; b\n/\n*/ FROM dual",
		"slash string":  "SELECT 'a\n/\nb' FROM dual",
		"q multibyte":   "SELECT q'中a;b中' FROM dual",
	} {
		got := split(t, sql+";SELECT 2 FROM dual")
		require.Len(t, got, 2, name)
		assert.Equal(t, sql, got[0].Text, name)
	}
}

func TestSplitStatementExcludesTrailingComment(t *testing.T) {
	got := split(t, "SELECT 1 FROM dual -- tail\n;-- next\nSELECT 2 FROM dual")
	assert.Equal(t, []string{"SELECT 1 FROM dual", "SELECT 2 FROM dual"}, texts(got))
}

func TestSplitMultibyteBoundaries(t *testing.T) {
	sql := "SELECT '中;文' FROM dual;\nSELECT 2 FROM 表"
	got := split(t, sql)
	assert.Equal(t, []string{"SELECT '中;文' FROM dual", "SELECT 2 FROM 表"}, texts(got))
}

func TestSplitPLSQLBlocks(t *testing.T) {
	block := "BEGIN\n  x := 1;\n  y := 2;\nEND;"
	got := split(t, block+"\n/\nSELECT 1 FROM dual;")
	assert.Equal(t, []string{block, "SELECT 1 FROM dual"}, texts(got))
	assert.Equal(t, "PLSQL BLOCK", got[0].Type)

	// without a slash the block runs to the end of the text, ';' included
	got = split(t, "DECLARE n NUMBER; BEGIN n := 1; END;\n")
	assert.Equal(t, []string{"DECLARE n NUMBER; BEGIN n := 1; END;"}, texts(got))

	// a slash that is not alone on its line does not end the block
	got = split(t, "BEGIN\n  x := 4 / 2;\n  y := 'a\n/\n';\nEND;\n/")
	require.Len(t, got, 1)
	assert.Equal(t, "BEGIN\n  x := 4 / 2;\n  y := 'a\n/\n';\nEND;", got[0].Text)

	// a lone slash inside a block comment does not end it either
	got = split(t, "BEGIN\n/* x\n/\n*/ NULL;\nEND;")
	require.Len(t, got, 1)

	// blanks on the last line (an editor's indentation) end the text, not the block
	got = split(t, "BEGIN\n  NULL;\nEND;\n  \t")
	assert.Equal(t, []string{"BEGIN\n  NULL;\nEND;"}, texts(got))

	// a block may open with labels: its ';' still do not end it
	for _, block := range []string{"<<outer>>\nBEGIN\n  NULL;\nEND;", "<<a>> <<b$1>> DECLARE n NUMBER; BEGIN n := 1; END;"} {
		got = split(t, block+"\n/\nSELECT 1 FROM dual")
		assert.Equal(t, []string{block, "SELECT 1 FROM dual"}, texts(got), block)
		assert.Equal(t, "PLSQL BLOCK", got[0].Type, block)
	}
}

// A query may declare PL/SQL functions and procedures in its WITH clause: their
// ';' do not end it. Like a block it runs until a lone '/' or the end of the text;
// like a plain statement its own closing ';' is not part of it.
func TestSplitQueryWithPLSQLDeclarations(t *testing.T) {
	query := "WITH\n  FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual"
	got := split(t, query+";\n/\nSELECT 2 FROM dual")
	assert.Equal(t, []string{query, "SELECT 2 FROM dual"}, texts(got))
	// it runs PL/SQL, so it is typed as a block: a policy that allows SELECT does
	// not let a function that writes through
	assert.Equal(t, "PLSQL BLOCK", got[0].Type)

	query = "with procedure p IS BEGIN NULL; END; FUNCTION f RETURN NUMBER IS BEGIN p; RETURN 1; END; SELECT f FROM dual"
	assert.Equal(t, []string{query}, texts(split(t, query)))

	// a WITH without declarations is a plain statement, also when its query is
	// named FUNCTION or PROCEDURE
	for _, query := range []string{
		"WITH x AS (SELECT 1 n FROM dual) SELECT n FROM x",
		"WITH function AS (SELECT 1 x FROM dual) SELECT x FROM function",
		"WITH procedure (x) AS (SELECT 1 FROM dual) SELECT x FROM procedure",
	} {
		got = split(t, query+"; SELECT 2 FROM dual")
		assert.Equal(t, []string{query, "SELECT 2 FROM dual"}, texts(got), query)
		assert.Equal(t, "SELECT", got[0].Type, query)
	}
}

func TestSplitStoredProgramsAreBlocks(t *testing.T) {
	for _, head := range []string{
		"CREATE PROCEDURE p AS", "create or replace procedure p is", "CREATE OR REPLACE FUNCTION f RETURN NUMBER IS",
		"CREATE OR REPLACE EDITIONABLE PACKAGE pk AS", "CREATE OR REPLACE NONEDITIONABLE PACKAGE BODY pk AS",
		"CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW", "CREATE OR REPLACE TYPE BODY ty AS",
	} {
		sql := head + "\nBEGIN\n  NULL;\nEND;\n/\nSELECT 1 FROM dual"
		got := split(t, sql)
		require.Len(t, got, 2, head)
		assert.Equal(t, head+"\nBEGIN\n  NULL;\nEND;", got[0].Text, head)
		assert.Equal(t, "SELECT 1 FROM dual", got[1].Text)
	}
}

func TestSplitLoneSlashSeparatesPlainStatements(t *testing.T) {
	got := split(t, "SELECT 1 FROM dual\n/\nSELECT 2 FROM dual\n/\n")
	assert.Equal(t, []string{"SELECT 1 FROM dual", "SELECT 2 FROM dual"}, texts(got))
	got = split(t, "SELECT 1 FROM dual;\n/\n")
	assert.Equal(t, []string{"SELECT 1 FROM dual"}, texts(got))
}

func TestClassifyTypes(t *testing.T) {
	for sql, want := range map[string]string{
		"select 1 from dual": "SELECT",
		"WITH a AS (SELECT 1 x FROM dual) SELECT * FROM a": "SELECT",
		"(SELECT 1 FROM dual)":                             "SELECT",
		"INSERT INTO t VALUES (1)":                         "INSERT",
		"UPDATE t SET a = 1 WHERE b = 2":                   "UPDATE",
		"DELETE FROM t WHERE a = 1":                        "DELETE",
		"MERGE INTO t USING s ON (t.a = s.a) WHEN MATCHED THEN UPDATE SET t.b = s.b": "MERGE",
		"TRUNCATE TABLE t":                                      "TRUNCATE",
		"CREATE TABLE t (a NUMBER)":                             "CREATE TABLE",
		"CREATE GLOBAL TEMPORARY TABLE t (a NUMBER)":            "CREATE TABLE",
		"CREATE OR REPLACE VIEW v AS SELECT 1 x FROM dual":      "CREATE VIEW",
		"CREATE UNIQUE INDEX i ON t (a)":                        "CREATE INDEX",
		"CREATE MATERIALIZED VIEW mv AS SELECT 1 x FROM dual":   "CREATE MATERIALIZED VIEW",
		"CREATE OR REPLACE PUBLIC SYNONYM s FOR t":              "CREATE SYNONYM",
		"CREATE OR REPLACE EDITIONABLE PACKAGE BODY p AS\nEND;": "CREATE PACKAGE BODY",
		"ALTER TABLE t ADD (b NUMBER)":                          "ALTER TABLE",
		"ALTER SESSION SET CURRENT_SCHEMA = x":                  "ALTER SESSION",
		"DROP TABLE t PURGE":                                    "DROP TABLE",
		"DROP PUBLIC SYNONYM s":                                 "DROP SYNONYM",
		"GRANT SELECT ON t TO u":                                "GRANT",
		"REVOKE SELECT ON t FROM u":                             "REVOKE",
		"COMMIT":                                                "COMMIT",
		"rollback":                                              "ROLLBACK",
		"BEGIN NULL; END;":                                      "PLSQL BLOCK",
		"DECLARE x NUMBER; BEGIN NULL; END;":                    "PLSQL BLOCK",
		"CALL p(1)":                                             "CALL",
		"EXEC p(1)":                                             "CALL",
		"execute p(1)":                                          "CALL",
		"EXPLAIN PLAN FOR DELETE FROM t":                        "EXPLAIN",
		"/* hint */ SELECT 1 FROM dual":                         "SELECT",
	} {
		got := split(t, sql)
		require.Len(t, got, 1, sql)
		assert.Equal(t, want, got[0].Type, sql)
	}
}

func TestClassifyDanger(t *testing.T) {
	for sql, want := range map[string]string{
		"UPDATE t SET a = 1":                                    dbdriver.ReasonNoWhereUpdate,
		"update t set a = 1 -- where x":                         dbdriver.ReasonNoWhereUpdate,
		"UPDATE t SET a = (SELECT 1 FROM u WHERE u.id = 1)":     dbdriver.ReasonNoWhereUpdate,
		"UPDATE t SET a = 'WHERE'":                              dbdriver.ReasonNoWhereUpdate,
		"DELETE FROM t":                                         dbdriver.ReasonNoWhereDelete,
		"DELETE FROM t WHERE id IN (SELECT 1 FROM dual) OR 1=1": "",
		"UPDATE t SET a = 1 WHERE b = 2":                        "",
		"DELETE t WHERE id = 1":                                 "",
		"CALL p(1)":                                             dbdriver.ReasonCall,
		"EXEC p":                                                dbdriver.ReasonCall,
		"SELECT * FROM t FOR UPDATE":                            "",
		"INSERT INTO t VALUES (1)":                              "",
		"DROP TABLE t":                                          "",
		"MERGE INTO t USING s ON (t.a = s.a) WHEN MATCHED THEN UPDATE SET t.b = s.b":                                      "",
		"BEGIN\n  DELETE FROM t;\nEND;":                                                                                   dbdriver.ReasonNoWhereDelete,
		"<<l>>\nBEGIN\n  DELETE FROM t;\nEND;":                                                                            dbdriver.ReasonNoWhereDelete,
		"BEGIN\n  UPDATE t SET a = 1;\nEND;":                                                                              dbdriver.ReasonNoWhereUpdate,
		"BEGIN\n  UPDATE t SET a = 1 WHERE b = 2;\n  DELETE t WHERE b = 2;\nEND;":                                         "",
		"BEGIN\n  IF x THEN\n    DELETE FROM t;\n  END IF;\nEND;":                                                         dbdriver.ReasonNoWhereDelete,
		"BEGIN\n  EXECUTE IMMEDIATE 'DELETE FROM t';\nEND;":                                                               "",
		"BEGIN\n  NULL; -- DELETE FROM t;\nEND;":                                                                          "",
		"BEGIN\n  FOR r IN (SELECT * FROM t FOR UPDATE) LOOP NULL; END LOOP;\nEND;":                                       "",
		"BEGIN\n  MERGE INTO t USING s ON (t.a = s.a) WHEN MATCHED THEN UPDATE SET t.b = s.b DELETE WHERE t.b = 0;\nEND;": "",
		"BEGIN\n  FORALL i IN 1 .. 10\n    DELETE FROM t;\nEND;":                                                          dbdriver.ReasonNoWhereDelete,
		"BEGIN\n  x := 1;\n  FORALL i IN INDICES OF ids SAVE EXCEPTIONS\n    UPDATE t SET a = 1;\nEND;":                   dbdriver.ReasonNoWhereUpdate,
		"BEGIN\n  FORALL i IN 1 .. ids.COUNT\n    DELETE FROM t WHERE id = ids(i);\n  ids.DELETE;\nEND;":                  "",
		"WITH FUNCTION f RETURN NUMBER IS BEGIN DELETE FROM t; RETURN 1; END;\nSELECT f FROM dual":                        dbdriver.ReasonNoWhereDelete,
		"WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM t WHERE a = 1":                              "",
		"CREATE OR REPLACE PROCEDURE p AS\nBEGIN\n  DELETE FROM t;\nEND;":                                                 "",
		"CREATE TRIGGER trg BEFORE DELETE OR UPDATE ON t\nBEGIN\n  NULL;\nEND;":                                           "",
	} {
		got := split(t, sql)
		require.Len(t, got, 1, sql)
		assert.Equal(t, want, got[0].Reason, sql)
		assert.Equal(t, want != "", got[0].Dangerous, sql)
	}
}

func TestSplitCannotSplit(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT 'abc FROM dual":             "byte 7",
		"SELECT 1;\nSELECT \"abc FROM dual": "line 2, column 8",
		"SELECT 1 /* open FROM dual":        "byte 9",
		"SELECT q'[abc' FROM dual":          "byte 7",
		"BEGIN\n  x := 'oops;\nEND;":        "line 2",
	} {
		got, err := splitOracle(context.Background(), sql)
		require.Error(t, err, sql)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), want, sql)
	}
}
