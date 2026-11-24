package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runScriptFile runs file on the session's schema the way Run SQL File does,
// reading readSize bytes at a time (0 for the host's default).
func runScriptFile(t *testing.T, s *sqldb.Session, schema, file string, readSize int) (*dbdriver.ScriptResult, error) {
	t.Helper()
	return s.RunScript(integrationContext(t), dbdriver.ScriptRequest{
		Database: schema, Script: strings.NewReader(file), Size: int64(len(file)), ReadSize: readSize,
	})
}

// tableRows is every row of query, each value as text ("" for NULL).
func tableRows(t *testing.T, s *sqldb.Session, query string) [][]string {
	t.Helper()
	res, err := s.Execute(integrationContext(t), query)
	require.NoError(t, err, query)
	require.NotNil(t, res.Rows)
	out := make([][]string, len(res.Rows.Rows))
	for i, row := range res.Rows.Rows {
		out[i] = make([]string, len(row))
		for j, v := range row {
			if v != nil {
				out[i][j] = fmt.Sprint(v)
			}
		}
	}
	return out
}

// A SQL Developer "Export → Insert statements" file, with a PL/SQL block and an
// EXEC added, runs as a whole: its client commands are skipped, SET DEFINE OFF
// keeps "AT&T" as written, and the rows arrive as the file has them — also when
// the file is read a few bytes at a time.
func TestIntegrationRunSQLDeveloperExport(t *testing.T) {
	export, err := os.ReadFile("testdata/sqldeveloper_insert_export.sql")
	require.NoError(t, err)
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_SCRIPT",
			"ID NUMBER(10) PRIMARY KEY, NAME VARCHAR2(40 CHAR), NOTE VARCHAR2(200 CHAR), CREATED DATE")
		file := strings.ReplaceAll(string(export), "OPSKAT_SCRIPT_T", table)
		for _, readSize := range []int{0, 7} {
			_, err := s.Execute(integrationContext(t), "DELETE FROM "+table)
			require.NoError(t, err)

			res, err := runScriptFile(t, s, schema, file, readSize)
			require.NoError(t, err, "read size %d", readSize)
			assert.Nil(t, res.Failure, "read size %d", readSize)
			assert.Equal(t, int64(6), res.Succeeded, "read size %d", readSize)
			assert.Equal(t, int64(0), res.Failed, "read size %d", readSize)

			assert.Equal(t, [][]string{
				{"1", "AT&T", "it's; fine (checked)", "2026-11-18 10:11:12"},
				{"2", "中文 名字", "line one\n/\nline three", ""},
				{"3", "q", "a;b -- not a comment", ""},
				{"4", "from a block", "", ""},
				{"5", "from exec", "", ""},
			}, tableRows(t, s, "SELECT TO_CHAR(ID), NAME, NOTE, TO_CHAR(CREATED, 'YYYY-MM-DD HH24:MI:SS') FROM "+table+" ORDER BY ID"),
				"read size %d", readSize)
		}
	})
}

// A client command that changes what runs stops the run: the statements before
// it have run, the ones after it have not.
func TestIntegrationRunScriptStopsOnSQLPlusCommands(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_SCRIPT_STOP", "ID NUMBER(10) PRIMARY KEY, NAME VARCHAR2(20)")
		for command, construct := range map[string]string{
			"@other.sql":                     "@",
			"WHENEVER SQLERROR EXIT FAILURE": "WHENEVER",
		} {
			_, err := s.Execute(integrationContext(t), "DELETE FROM "+table)
			require.NoError(t, err)
			file := "Insert into " + table + " (ID,NAME) values (1,'before');\n" + command + "\n" +
				"Insert into " + table + " (ID,NAME) values (2,'after');\n"

			res, err := runScriptFile(t, s, schema, file, 0)
			var unsupported *dbdriver.ScriptUnsupportedError
			require.True(t, errors.As(err, &unsupported), "%s: %v", command, err)
			assert.Equal(t, &dbdriver.ScriptUnsupportedError{Construct: construct, Tool: "SQL*Plus", Line: 2}, unsupported)
			require.NotNil(t, res)
			assert.Equal(t, int64(1), res.Succeeded, command)
			assert.Equal(t, [][]string{{"1", "before"}}, tableRows(t, s, "SELECT TO_CHAR(ID), NAME FROM "+table+" ORDER BY ID"), command)
		}
	})
}
