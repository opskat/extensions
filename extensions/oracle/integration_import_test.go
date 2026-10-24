package main

import (
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func importBinary(hex string) dbdriver.ImportValue {
	return dbdriver.ImportValue{Kind: dbdriver.ImportBinary, Hex: hex}
}

// Every import mode against a real table: the generated statements run through the
// session's batch, values land by column type (NUMBER, text, DATE, RAW) and a row
// that Oracle rejects reports its Oracle error without stopping the others.
func TestIntegrationImportModes(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_IMP",
			"ID NUMBER(10) PRIMARY KEY, NAME VARCHAR2(20 CHAR), BORN DATE, CODE RAW(8)")
		ctx := integrationContext(t)
		run := func(req dbdriver.ImportRequest) *dbdriver.BatchResult {
			statements, err := s.ImportStatements(ctx, schema, table, req)
			require.NoError(t, err)
			res, err := s.ExecuteBatch(ctx, dbdriver.BatchRequest{Database: schema, Table: table, Statements: statements, ContinueOnError: true})
			require.NoError(t, err)
			return res
		}
		columns := []string{"ID", "NAME", "BORN", "CODE"}

		require.False(t, run(dbdriver.ImportRequest{Mode: dbdriver.ImportAppend, Columns: columns, Rows: [][]dbdriver.ImportValue{
			{text("1"), text("O'Reilly 中文"), text("2024-01-02 03:04:05"), importBinary("00ff")},
			{text("2"), null, null, null},
		}}).Failed())
		assert.Equal(t, []string{"O'Reilly 中文", "2024-01-02 03:04:05", "00FF"},
			queryStrings(t, s, "SELECT NAME, TO_CHAR(BORN, 'YYYY-MM-DD HH24:MI:SS'), RAWTOHEX(CODE) FROM "+table+" WHERE ID = 1"))

		run(dbdriver.ImportRequest{Mode: dbdriver.ImportAppendUpdate, Columns: []string{"ID", "NAME"}, Keys: []string{"ID"},
			Rows: [][]dbdriver.ImportValue{{text("2"), text("bob")}, {text("3"), text("cat")}}})
		assert.Equal(t, []string{"3"}, queryStrings(t, s, "SELECT COUNT(*) FROM "+table))
		assert.Equal(t, []string{"bob"}, queryStrings(t, s, "SELECT NAME FROM "+table+" WHERE ID = 2"))

		run(dbdriver.ImportRequest{Mode: dbdriver.ImportAppendSkip, Columns: []string{"ID", "NAME"}, Keys: []string{"ID"},
			Rows: [][]dbdriver.ImportValue{{text("3"), text("zed")}, {text("4"), text("dan")}}})
		assert.Equal(t, []string{"cat"}, queryStrings(t, s, "SELECT NAME FROM "+table+" WHERE ID = 3"))
		assert.Equal(t, []string{"dan"}, queryStrings(t, s, "SELECT NAME FROM "+table+" WHERE ID = 4"))

		run(dbdriver.ImportRequest{Mode: dbdriver.ImportUpdate, Columns: []string{"ID", "NAME"}, Keys: []string{"ID"},
			Rows: [][]dbdriver.ImportValue{{text("1"), text("ann")}}})
		assert.Equal(t, []string{"ann"}, queryStrings(t, s, "SELECT NAME FROM "+table+" WHERE ID = 1"))

		run(dbdriver.ImportRequest{Mode: dbdriver.ImportDelete, Columns: []string{"ID"}, Keys: []string{"ID"},
			Rows: [][]dbdriver.ImportValue{{text("4")}}})
		assert.Equal(t, []string{"3"}, queryStrings(t, s, "SELECT COUNT(*) FROM "+table))

		// A too long value fails its own row with the Oracle error (ORA-12899).
		res := run(dbdriver.ImportRequest{Mode: dbdriver.ImportAppend, Columns: []string{"ID", "NAME"}, Rows: [][]dbdriver.ImportValue{
			{text("10"), text("this name is far too long")}, {text("11"), text("ok")},
		}})
		failed := 0
		for _, r := range res.Executed {
			if r.Error != "" {
				failed++
				assert.Contains(t, r.Error, "ORA-")
			}
		}
		assert.Equal(t, 1, failed)

		run(dbdriver.ImportRequest{Mode: dbdriver.ImportCopy, Columns: []string{"ID", "NAME"}, Rows: [][]dbdriver.ImportValue{{text("7"), text("only")}}})
		assert.Equal(t, []string{"1"}, queryStrings(t, s, "SELECT COUNT(*) FROM "+table))
	})
}
