package main

import (
	"context"
	"fmt"
	"net"
	"sync"
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

// A query that declares a PL/SQL function in its WITH clause runs as one
// statement, in the query panel's pages too.
func TestIntegrationQueryWithPLSQLFunction(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table := execSession(t, target)
		query := "WITH FUNCTION twice(n NUMBER) RETURN NUMBER IS BEGIN RETURN n * 2; END;\n" +
			"SELECT twice(N) FROM " + table + " WHERE N <= 3 ORDER BY N;"

		res, err := s.Execute(integrationContext(t), query)
		require.NoError(t, err)
		require.NotNil(t, res.Rows)
		assert.Equal(t, []string{"2", "4", "6"}, firstColumn(res.Rows))

		res, err = s.ExecutePaged(integrationContext(t), query+"\n/", dbdriver.Page{Index: 1, Size: 2})
		require.NoError(t, err)
		require.NotNil(t, res.Rows)
		require.NotNil(t, res.Rows.Total)
		assert.Equal(t, int64(3), *res.Rows.Total)
		assert.Equal(t, []string{"6"}, firstColumn(res.Rows))
	})
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
			// ORDER BY 1: Oracle itself rejects ORDER BY N here as ambiguous (ORA-00960).
			{"duplicate column names", "SELECT N, N FROM " + table + " ORDER BY 1", dbdriver.Page{Index: 1, Size: 10}, 25, []string{"N", "N"}, numbers(11, 20)},
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

// reachPath is the host's reach path for one driver instance: it dials directly
// and keeps the connections, which the host closes when a call's context ends.
type reachPath struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (p *reachPath) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns = append(p.conns, c)
	return c, nil
}

func (p *reachPath) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// The asset's query timeout ending makes a running statement return, the way
// the host does it: the guest runs every call with context.Background()
// (sdkdbdriver serve), and the host, when the call's context ends, closes the
// instance's connections, reports the timeout and discards the instance (opskat
// pkg/extension driver_session.go). A deadline on the guest's own context is not
// what stops it (23ai, unlike 19c, ignores go-ora's in-band break during
// DBMS_SESSION.SLEEP). The driver returns once its connection is gone, and the
// next instance logs in and works.
func TestIntegrationExecuteTimeout(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		reach := &reachPath{}
		req := target.openRequest("")
		req.Transport.Dial = reach.dial
		db, _, err := open(integrationContext(t), req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		s := sqldb.NewSession(db, dialect{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stop := context.AfterFunc(ctx, reach.close)
		defer stop()

		start := time.Now()
		_, err = s.Execute(context.Background(), "BEGIN DBMS_SESSION.SLEEP(30); END;")
		require.Error(t, err)
		assert.Less(t, time.Since(start), 15*time.Second)
		assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "the statement failed before the timeout: %v", err)

		res, err := sqldb.NewSession(target.open(t, ""), dialect{}).Execute(integrationContext(t), "SELECT 1 FROM DUAL")
		require.NoError(t, err)
		assert.Equal(t, []string{"1"}, firstColumn(res.Rows))
	})
}
