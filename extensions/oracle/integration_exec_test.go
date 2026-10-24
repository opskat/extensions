package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execSession opens the shared session over the driver's database, the way the
// SDK does, with one connection (a FOR UPDATE lock stays with the session), and
// a table of 25 numbered rows that is dropped when the test ends.
func execSession(t *testing.T, target oracleTarget) (*sqldb.Session, string) {
	t.Helper()
	db := target.open(t, "")
	db.SetMaxOpenConns(1)
	s := sqldb.NewSession(db, dialect{})
	table := fmt.Sprintf("OPSKAT_EXEC_%d", time.Now().UnixNano()%1_000_000_000)
	_, err := s.Execute(integrationContext(t), "CREATE TABLE "+table+" (N NUMBER(10), NAME VARCHAR2(30))")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := s.Execute(ctx, "DROP TABLE "+table+" PURGE")
		assert.NoError(t, err)
	})
	_, err = s.Execute(integrationContext(t),
		"INSERT INTO "+table+" (N, NAME) SELECT LEVEL, 'row ' || LEVEL FROM DUAL CONNECT BY LEVEL <= 25")
	require.NoError(t, err)
	return s, table
}

func firstColumn(rs *dbdriver.RowSet) []string {
	var out []string
	for _, row := range rs.Rows {
		out = append(out, fmt.Sprint(row[0]))
	}
	return out
}

func numbers(from, to int) []string {
	var out []string
	for n := from; n <= to; n++ {
		out = append(out, fmt.Sprint(n))
	}
	return out
}

func TestIntegrationExecutePaged(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table := execSession(t, target)
		cases := []struct {
			name      string
			statement string
			page      dbdriver.Page
			total     int64
			columns   []string
			want      []string
		}{
			{"duplicate column names", "SELECT N, N FROM " + table + " ORDER BY N", dbdriver.Page{Index: 1, Size: 10}, 25, []string{"N", "N"}, numbers(11, 20)},
			{"FOR UPDATE", "SELECT N FROM " + table + " WHERE N <= 15 ORDER BY N FOR UPDATE", dbdriver.Page{Index: 1, Size: 10}, 15, []string{"N"}, numbers(11, 15)},
			{"trailing comment", "SELECT N FROM " + table + " ORDER BY N -- the last page", dbdriver.Page{Index: 2, Size: 10}, 25, []string{"N"}, numbers(21, 25)},
			{"own OFFSET / FETCH", "SELECT N FROM " + table + " ORDER BY N OFFSET 5 ROWS FETCH FIRST 12 ROWS ONLY", dbdriver.Page{Index: 1, Size: 10}, 12, []string{"N"}, numbers(16, 17)},
			{"WITH and ';'", "WITH X AS (SELECT N FROM " + table + ") SELECT N FROM X WHERE N > 20 ORDER BY N;", dbdriver.Page{Index: 0, Size: 3}, 5, []string{"N"}, numbers(21, 23)},
			{"past the last page", "SELECT N FROM " + table, dbdriver.Page{Index: 9, Size: 10}, 25, []string{"N"}, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				res, err := s.ExecutePaged(integrationContext(t), tc.statement, tc.page)
				require.NoError(t, err)
				require.NotNil(t, res.Rows)
				require.NotNil(t, res.Rows.Total)
				assert.Equal(t, tc.total, *res.Rows.Total)
				assert.Equal(t, tc.columns, res.Rows.Columns)
				assert.Equal(t, tc.want, firstColumn(res.Rows))
			})
		}
	})
}

func TestIntegrationExecuteStatements(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table := execSession(t, target)
		ctx := integrationContext(t)

		res, err := s.Execute(ctx, "INSERT INTO "+table+" (N) VALUES (100);\nINSERT INTO "+table+" (N) VALUES (101);")
		require.NoError(t, err)
		assert.Equal(t, int64(2), res.AffectedRows)

		_, err = s.Execute(ctx, "BEGIN\n  INSERT INTO "+table+" (N) VALUES (102);\nEND;\n/\n")
		require.NoError(t, err, "the block keeps its closing ';'")

		res, err = s.Execute(ctx, "SELECT 1 FROM DUAL; SELECT COUNT(*) FROM "+table+" WHERE N >= 100;")
		require.NoError(t, err)
		require.NotNil(t, res.Rows)
		assert.Equal(t, []string{"3"}, firstColumn(res.Rows), "the result is the last statement's")

		_, err = s.Execute(ctx, "INSERT INTO "+table+" (N) VALUES (103); INSERT INTO NO_SUCH_TABLE_OPSKAT VALUES (1); INSERT INTO "+table+" (N) VALUES (104)")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "statement 2 of 3")
		assert.Contains(t, err.Error(), "ORA-00942")
		res, err = s.Execute(ctx, "SELECT N FROM "+table+" WHERE N >= 103 ORDER BY N")
		require.NoError(t, err)
		assert.Equal(t, []string{"103"}, firstColumn(res.Rows), "statements after the failing one do not run")
	})
}

// The asset's query timeout ending cancels the running statement and reports
// the timeout; the session keeps working.
func TestIntegrationExecuteTimeout(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		s := sqldb.NewSession(db, dialect{})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		start := time.Now()
		_, err := s.Execute(ctx, "BEGIN DBMS_SESSION.SLEEP(30); END;")
		require.Error(t, err)
		assert.Less(t, time.Since(start), 15*time.Second)
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "ORA-01013"), "got %v", err)

		res, err := s.Execute(integrationContext(t), "SELECT 1 FROM DUAL")
		require.NoError(t, err)
		assert.Equal(t, []string{"1"}, firstColumn(res.Rows))
	})
}
