package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptRecorder runs a SQL file through the host's own orchestration
// (dbdriver.RunScript) with the driver's splitter, recording the statements
// handed to the executor and the client commands the splitter skipped.
type scriptRecorder struct {
	executed []string
	skipped  []skippedCommand
}

func (r *scriptRecorder) SplitScript(_ context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	return splitScriptChunk(chunk, func(s skippedCommand) { r.skipped = append(r.skipped, s) })
}

func (r *scriptRecorder) OpenScriptExecutor(context.Context, string) (dbdriver.ScriptExecutor, error) {
	return recordingExecutor{r}, nil
}

type recordingExecutor struct{ r *scriptRecorder }

func (e recordingExecutor) ExecuteScriptBatch(_ context.Context, batch dbdriver.ScriptBatch) (*dbdriver.BatchResult, error) {
	res := &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{}}
	for i, s := range batch.Statements {
		e.r.executed = append(e.r.executed, s)
		res.Executed = append(res.Executed, dbdriver.BatchStatement{Index: i})
	}
	return res, nil
}

func (recordingExecutor) Close() error { return nil }

// runScriptText runs text as a SQL file read readSize bytes at a time.
func runScriptText(text string, readSize int) (*scriptRecorder, error) {
	r := &scriptRecorder{}
	_, err := dbdriver.RunScript(context.Background(), dbdriver.ScriptRequest{
		Script: strings.NewReader(text), Size: int64(len(text)), ReadSize: readSize,
	}, r)
	return r, err
}

// runAtEveryReadSize runs text at every read size from one byte to the whole
// file, requires each run to execute, skip and stop exactly as reading the file
// at once does, and returns that run.
func runAtEveryReadSize(t *testing.T, text string) (*scriptRecorder, error) {
	t.Helper()
	want, wantErr := runScriptText(text, len(text)+1)
	for size := 1; size <= len(text); size++ {
		got, err := runScriptText(text, size)
		require.Equal(t, want.executed, got.executed, "read size %d", size)
		require.Equal(t, want.skipped, got.skipped, "read size %d", size)
		require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err), "read size %d", size)
	}
	return want, wantErr
}

// unsupportedAt is the stop a run ended with.
func unsupportedAt(t *testing.T, err error) *dbdriver.ScriptUnsupportedError {
	t.Helper()
	var unsupported *dbdriver.ScriptUnsupportedError
	require.True(t, errors.As(err, &unsupported), "want an unsupported construct, got %v", err)
	return unsupported
}

func TestScriptTerminators(t *testing.T) {
	text := "SELECT 1 FROM dual;\n" +
		"INSERT INTO t VALUES (1)  ;\n" +
		"BEGIN\n  x := 1;\nEND;\n/\n" +
		"CREATE OR REPLACE PROCEDURE p AS\nBEGIN\n  NULL;\nEND;\n/\n" +
		"<<l>> DECLARE n NUMBER; BEGIN n := 1; END;\n  /  \n" +
		"WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual;\n/\n" +
		"SELECT 2 FROM dual\n/\n" +
		"SELECT 3 FROM dual"
	got, err := runAtEveryReadSize(t, text)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"SELECT 1 FROM dual",
		"INSERT INTO t VALUES (1)",
		"BEGIN\n  x := 1;\nEND;",
		"CREATE OR REPLACE PROCEDURE p AS\nBEGIN\n  NULL;\nEND;",
		"<<l>> DECLARE n NUMBER; BEGIN n := 1; END;",
		"WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual",
		"SELECT 2 FROM dual",
		"SELECT 3 FROM dual",
	}, got.executed)
	assert.Empty(t, got.skipped)

	// a file saved with CRLF line ends
	got, err = runAtEveryReadSize(t, "SET DEFINE OFF;\r\nInsert into T values ('a&b');\r\nBEGIN\r\n  NULL;\r\nEND;\r\n/\r\nSELECT 1 FROM dual\r\n/\r\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"Insert into T values ('a&b')", "BEGIN\r\n  NULL;\r\nEND;", "SELECT 1 FROM dual"}, got.executed)
	assert.Equal(t, []skippedCommand{{1, "SET"}}, got.skipped)

	// a block without a closing slash runs to the end of the file, ';' included
	got, err = runAtEveryReadSize(t, "SELECT 1 FROM dual;\nBEGIN\n  NULL;\nEND;\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"SELECT 1 FROM dual", "BEGIN\n  NULL;\nEND;"}, got.executed)
}

func TestScriptQuotesAndComments(t *testing.T) {
	text := "SELECT q'[a;b\n/\n]' FROM dual;\n" +
		"SELECT Q'!it's; ok!', nq'{x;}', '中;文', 'it''s; ok' FROM \"a;b\";\n" +
		"-- a comment; with 'a quote\n" +
		"/* block; \n/\n with 'quote */ SELECT 1 FROM dual -- tail;\n;\n" +
		"BEGIN\n  x := 'a\n/\n';\n  /* \n/\n */ y := q'<;\n/\n>';\nEND;\n/\n" +
		"-- trailing comment without a newline"
	got, err := runAtEveryReadSize(t, text)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"SELECT q'[a;b\n/\n]' FROM dual",
		"SELECT Q'!it's; ok!', nq'{x;}', '中;文', 'it''s; ok' FROM \"a;b\"",
		"SELECT 1 FROM dual",
		"BEGIN\n  x := 'a\n/\n';\n  /* \n/\n */ y := q'<;\n/\n>';\nEND;",
	}, got.executed)
}

// SQL Developer and SQL*Plus scripts carry client commands that only change what
// the client shows: they are skipped, each logged with its line.
func TestScriptSkipsDisplayCommands(t *testing.T) {
	text := "--------------------------------------------------------\n" +
		"--  File created - Wednesday-November-18-2026\n" +
		"--------------------------------------------------------\n" +
		"SET DEFINE OFF;\n" + // 4
		"REM INSERTING into T\n" +
		"  PROMPT it's loading\n" +
		"set echo on feedback off\n" +
		"col name format a30\n" +
		"SPOOL out.log\n" + // 9
		"CLEAR SCREEN\n" +
		"TTITLE 'it''s'\n" +
		"BTITLE OFF\n" +
		"SHOW ERRORS\n" +
		"Insert into T (ID,NAME) values ('1','AT&T');\n" + // 14
		"/\n" +
		"remark done\n" +
		"SPOOL OFF"
	got, err := runAtEveryReadSize(t, text)
	require.NoError(t, err)
	assert.Equal(t, []string{"Insert into T (ID,NAME) values ('1','AT&T')"}, got.executed)
	assert.Equal(t, []skippedCommand{
		{4, "SET"}, {5, "REMARK"}, {6, "PROMPT"}, {7, "SET"}, {8, "COLUMN"}, {9, "SPOOL"}, {10, "CLEAR"},
		{11, "TTITLE"}, {12, "BTITLE"}, {13, "SHOW"}, {15, "/"}, {16, "REMARK"}, {17, "SPOOL"},
	}, got.skipped)
}

// SET TRANSACTION / ROLE / CONSTRAINTS are SQL, and a SET line inside a statement
// is the statement's own.
func TestScriptSetStatementsAreSQL(t *testing.T) {
	text := "SET TRANSACTION READ ONLY;\nSET ROLE NONE;\nset constraints all deferred;\nUPDATE t\nSET a = 1;\n"
	got, err := runAtEveryReadSize(t, text)
	require.NoError(t, err)
	assert.Equal(t, []string{"SET TRANSACTION READ ONLY", "SET ROLE NONE", "set constraints all deferred", "UPDATE t\nSET a = 1"}, got.executed)
	assert.Empty(t, got.skipped)
}

// Client commands that change what runs stop the run before them, as an
// unsupported construct to run with SQL*Plus.
func TestScriptStopsOnSQLPlusCommands(t *testing.T) {
	for line, construct := range map[string]string{
		"@other.sql":                      "@",
		"  @@sub.sql":                     "@@",
		"START other.sql":                 "START",
		"sta other":                       "START",
		"WHENEVER SQLERROR EXIT FAILURE;": "WHENEVER",
		"CONNECT scott/tiger@db":          "CONNECT",
		"conn scott/tiger":                "CONNECT",
		"EXIT":                            "EXIT",
		"quit;":                           "QUIT",
		"HOST ls":                         "HOST",
		"!ls":                             "!",
		"DEFINE x = 'it''s'":              "DEFINE",
		"def x":                           "DEFINE",
		"UNDEFINE x":                      "UNDEFINE",
		"ACCEPT x PROMPT 'it's'":          "ACCEPT",
		"VARIABLE v NUMBER":               "VARIABLE",
		"var v number":                    "VARIABLE",
	} {
		text := "SELECT 1 FROM dual;\n" + line + "\nSELECT 2 FROM dual;\n"
		got, err := runAtEveryReadSize(t, text)
		assert.Equal(t, []string{"SELECT 1 FROM dual"}, got.executed, line)
		assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: construct, Tool: "SQL*Plus", Line: 2}, unsupportedAt(t, err), line)
	}
}

// A substitution variable stops the run while DEFINE is on (SQL*Plus would
// prompt for it, also inside a string); SET DEFINE turns it off, on, or to
// another character.
func TestScriptSubstitutionVariables(t *testing.T) {
	got, err := runAtEveryReadSize(t, "SELECT 1 FROM dual;\nINSERT INTO t VALUES (1,\n'&name');\n")
	assert.Equal(t, []string{"SELECT 1 FROM dual"}, got.executed)
	assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: "&name", Tool: "SQL*Plus", Line: 3}, unsupportedAt(t, err))

	got, err = runAtEveryReadSize(t, "BEGIN\n  x := &&count;\nEND;\n/\n")
	assert.Empty(t, got.executed)
	assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: "&&count", Tool: "SQL*Plus", Line: 2}, unsupportedAt(t, err))

	got, err = runAtEveryReadSize(t, "SELECT 'a & b' FROM dual;\nSET DEFINE OFF\nSELECT '&x' FROM dual;\nset def on\nSELECT '&y' FROM dual;\n")
	assert.Equal(t, []string{"SELECT 'a & b' FROM dual", "SELECT '&x' FROM dual"}, got.executed)
	assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: "&y", Tool: "SQL*Plus", Line: 5}, unsupportedAt(t, err))

	got, err = runAtEveryReadSize(t, "SELECT 1 FROM dual;\nEXEC p('&x')\n")
	assert.Equal(t, []string{"SELECT 1 FROM dual"}, got.executed)
	assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: "&x", Tool: "SQL*Plus", Line: 2}, unsupportedAt(t, err))

	got, err = runAtEveryReadSize(t, "SET DEFINE '^'\nSELECT '&x' FROM dual;\nSELECT '^x' FROM dual;\n")
	assert.Equal(t, []string{"SELECT '&x' FROM dual"}, got.executed)
	assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: "^x", Tool: "SQL*Plus", Line: 3}, unsupportedAt(t, err))
}

func TestScriptExecRunsAsBlock(t *testing.T) {
	got, err := runAtEveryReadSize(t, "EXEC p(1);\n  execute pkg.run('a;b')  \nexec DBMS_STATS.GATHER_SCHEMA_STATS(USER)\nSELECT 1 FROM dual;")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"BEGIN p(1); END;", "BEGIN pkg.run('a;b'); END;", "BEGIN DBMS_STATS.GATHER_SCHEMA_STATS(USER); END;", "SELECT 1 FROM dual",
	}, got.executed)
}

// A text the splitter cannot close (an unterminated quote or comment) is handed
// on as the last statement at every read size: the statements before it run,
// and it fails with the position of the quote.
func TestScriptUnterminatedQuote(t *testing.T) {
	got, err := runAtEveryReadSize(t, "SELECT 1 FROM dual;\n\nSELECT 'abc FROM dual;\nSELECT 2 FROM dual;\n")
	require.NoError(t, err)
	assert.Equal(t, []string{"SELECT 1 FROM dual", "SELECT 'abc FROM dual;\nSELECT 2 FROM dual;"}, got.executed)
}

// Every statement the splitter hands on reaches Oracle as written: the
// connection splits a text once more (exec.go) and must find it one statement.
func TestScriptStatementsPassThroughTheConnection(t *testing.T) {
	text := "SET DEFINE OFF;\nInsert into T (ID,NAME) values ('1','a;b');\nBEGIN\n  NULL;\nEND;\n/\nEXEC p('x;y')\n" +
		"WITH FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\nSELECT f FROM dual;\n/\nCREATE OR REPLACE PACKAGE pk AS\n  PROCEDURE p;\nEND;\n/\n"
	got, err := runScriptText(text, len(text))
	require.NoError(t, err)
	require.Len(t, got.executed, 5)
	for _, statement := range got.executed {
		again, err := splitOracle(context.Background(), statement)
		require.NoError(t, err, statement)
		require.Len(t, again, 1, statement)
		assert.Equal(t, statement, again[0].Text)
	}
}

func TestScriptStateStartsWithDefineOn(t *testing.T) {
	split, err := dialect{}.SplitScript(context.Background(), dbdriver.ScriptChunk{Text: "SELECT '&x' FROM dual;", Final: true})
	require.NoError(t, err)
	require.NotNil(t, split.Unsupported)
	assert.Equal(t, dbdriver.ScriptUnsupported{Construct: "&x", Tool: "SQL*Plus", Offset: 8}, *split.Unsupported)
	assert.Empty(t, split.Statements)
}
