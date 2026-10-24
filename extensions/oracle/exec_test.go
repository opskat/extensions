package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newExecSession is the shared session over the driver's connection, the way the
// SDK assembles it (sqldb.NewSession(db, dialect)), with sqlmock standing in for
// go-ora. Only the statements the driver sends after the login reach the mock.
func newExecSession(t *testing.T) (*sqldb.Session, *sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	expectDateFormats(mock)
	db := sql.OpenDB(&connector{login: login})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(context.Background()))
	return sqldb.NewSession(db, dialect{}), db, mock
}

// ---- execution: one statement at a time, trailing ';' ----

func TestExecuteDropsTrailingSemicolonOfPlainStatement(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec("DELETE FROM T WHERE ID = 1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT 1 FROM DUAL").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow("1"))

	res, err := s.Execute(context.Background(), "DELETE FROM T WHERE ID = 1;")
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.AffectedRows)
	res, err = s.Execute(context.Background(), "SELECT 1 FROM DUAL;  -- one\n")
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"1"}}, res.Rows.Rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecuteKeepsSemicolonOfPLSQLBlock(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec("BEGIN NULL; END;").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE OR REPLACE PROCEDURE P AS BEGIN NULL; END;").WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := s.Execute(context.Background(), "BEGIN NULL; END;\n/\n")
	require.NoError(t, err)
	_, err = s.Execute(context.Background(), "CREATE OR REPLACE PROCEDURE P AS BEGIN NULL; END;")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A text of several statements (the AI and opsctl send whole texts) runs one
// statement at a time; the affected rows add up.
func TestExecuteRunsStatementsOneByOne(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec("INSERT INTO T VALUES (1)").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE T SET A = 'x;y' WHERE ID = 1").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("BEGIN DELETE FROM T WHERE ID = 9; END;").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("COMMIT").WillReturnResult(sqlmock.NewResult(0, 0))

	res, err := s.Execute(context.Background(),
		"INSERT INTO T VALUES (1);\nUPDATE T SET A = 'x;y' WHERE ID = 1; -- two\nBEGIN DELETE FROM T WHERE ID = 9; END;\n/\nCOMMIT")
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.AffectedRows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A text that starts with a query reaches the driver as a query: every statement
// runs in order and the result is the last statement's.
func TestExecuteQueryTextReturnsLastStatementResult(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec("SELECT 1 FROM DUAL").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO T VALUES (1)").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT(*) FROM T").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow("4")).RowsWillBeClosed()

	res, err := s.Execute(context.Background(), "SELECT 1 FROM DUAL; INSERT INTO T VALUES (1); SELECT COUNT(*) FROM T;")
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Equal(t, []string{"COUNT(*)"}, res.Rows.Columns)
	assert.Equal(t, [][]any{{"4"}}, res.Rows.Rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A failing statement stops the run: the ones after it do not run, and the error
// says which statement failed, with Oracle's message as is.
func TestExecuteStopsAtFailingStatement(t *testing.T) {
	s, _, mock := newExecSession(t)
	oraErr := errors.New("ORA-00942: table or view does not exist")
	mock.ExpectExec("INSERT INTO T VALUES (1)").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO NOPE VALUES (1)").WillReturnError(oraErr)

	_, err := s.Execute(context.Background(), "INSERT INTO T VALUES (1); INSERT INTO NOPE VALUES (1); INSERT INTO T VALUES (2)")
	require.ErrorIs(t, err, oraErr)
	assert.Contains(t, err.Error(), "statement 2 of 3")
	assert.NoError(t, mock.ExpectationsWereMet(), "the third statement does not run")
}

func TestExecuteSingleStatementErrorIsOraclesAsIs(t *testing.T) {
	s, _, mock := newExecSession(t)
	oraErr := errors.New("ORA-00942: table or view does not exist")
	mock.ExpectExec("DELETE FROM NOPE").WillReturnError(oraErr)

	_, err := s.Execute(context.Background(), "DELETE FROM NOPE;")
	require.ErrorIs(t, err, oraErr)
	assert.NotContains(t, err.Error(), "statement 1")
}

func TestExecuteRefusesTextItCannotSplit(t *testing.T) {
	s, _, mock := newExecSession(t)
	_, err := s.Execute(context.Background(), "UPDATE T SET A = 'open WHERE ID = 1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot split")
	assert.Contains(t, err.Error(), "line 1, column 18")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing runs")
}

func TestExecuteRefusesTextWithoutStatement(t *testing.T) {
	s, _, mock := newExecSession(t)
	_, err := s.Execute(context.Background(), "-- nothing here\n/* or here */")
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing runs")
}

// Statements the driver generates with binds reach go-ora exactly as written.
func TestBoundStatementsPassThrough(t *testing.T) {
	_, db, mock := newExecSession(t)
	mock.ExpectExec("BEGIN UPDATE T SET A = :1 WHERE ID = :2; END;").WithArgs("x", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT A FROM T WHERE ID = :1").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"A"}).AddRow("x"))

	_, err := db.ExecContext(context.Background(), "BEGIN UPDATE T SET A = :1 WHERE ID = :2; END;", "x", 1)
	require.NoError(t, err)
	var a string
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT A FROM T WHERE ID = :1", 1).Scan(&a))
	assert.Equal(t, "x", a)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- query timeout ----

// stepConn is a go-ora stand-in that runs hook before answering each statement.
type stepConn struct {
	driver.Conn
	ran    []string
	hook   func(query string)
	cursor driver.Rows
}

func (c *stepConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.ran = append(c.ran, query)
	c.hook(query)
	return driver.RowsAffected(1), nil
}

func (c *stepConn) Close() error { return nil }

// The query timeout ending during a run stops it: no further statement starts,
// and the run fails with the timeout.
func TestExecuteStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &stepConn{hook: func(string) { cancel() }}
	c := &conn{inner: inner}

	_, err := c.ExecContext(ctx, "INSERT INTO T VALUES (1); INSERT INTO T VALUES (2)", nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"INSERT INTO T VALUES (1)"}, inner.ran)
}

// endlessRows is a cursor that never ends and records whether it was closed.
type endlessRows struct {
	read   int
	closed bool
	onNext func()
}

func (r *endlessRows) Columns() []string { return []string{"N"} }
func (r *endlessRows) Close() error      { r.closed = true; return nil }
func (r *endlessRows) Next(dest []driver.Value) error {
	r.read++
	dest[0] = int64(r.read)
	r.onNext()
	return nil
}

func (c *stepConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.ran = append(c.ran, query)
	return c.cursor, nil
}

// Skipping to a page stops when the query timeout ends and closes the cursor.
func TestPageStopsSkippingWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cursor := &endlessRows{}
	cursor.onNext = func() {
		if cursor.read == 3 {
			cancel()
		}
	}
	c := &conn{inner: &stepConn{cursor: cursor}}

	_, err := c.QueryContext(ctx, dialect{}.PagedSQL("SELECT N FROM T", 1000, 10), nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 3, cursor.read)
	assert.True(t, cursor.closed)
}

// ---- query panel paging: count, then cut the cursor at the page ----

func countingRows(columns []string, n int) *sqlmock.Rows {
	rows := sqlmock.NewRows(columns)
	for i := 1; i <= n; i++ {
		values := make([]driver.Value, len(columns))
		for j := range values {
			values[j] = int64(i)
		}
		rows.AddRow(values...)
	}
	return rows
}

func TestExecutePagedCutsTheCursor(t *testing.T) {
	cases := []struct {
		name      string
		statement string
		count     string
		page      string
		columns   []string
	}{
		{
			name:      "duplicate column names",
			statement: "SELECT A.ID, B.ID FROM A JOIN B ON A.ID = B.ID ORDER BY 1",
			count:     "SELECT COUNT(*) FROM (SELECT A.ID, B.ID FROM A JOIN B ON A.ID = B.ID ORDER BY 1)",
			page:      "SELECT A.ID, B.ID FROM A JOIN B ON A.ID = B.ID ORDER BY 1",
			columns:   []string{"ID", "ID"},
		},
		{
			name:      "FOR UPDATE",
			statement: "SELECT ID FROM T WHERE NAME <> 'for update' ORDER BY ID for update of id nowait;",
			count:     "SELECT COUNT(*) FROM (SELECT ID FROM T WHERE NAME <> 'for update' ORDER BY ID)",
			page:      "SELECT ID FROM T WHERE NAME <> 'for update' ORDER BY ID for update of id nowait",
			columns:   []string{"ID"},
		},
		{
			name:      "FOR UPDATE after a line comment",
			statement: "SELECT ID FROM T -- the ids\nFOR UPDATE",
			count:     "SELECT COUNT(*) FROM (SELECT ID FROM T)",
			page:      "SELECT ID FROM T -- the ids\nFOR UPDATE",
			columns:   []string{"ID"},
		},
		{
			name:      "trailing line comment",
			statement: "SELECT ID FROM T -- the ids",
			count:     "SELECT COUNT(*) FROM (SELECT ID FROM T)",
			page:      "SELECT ID FROM T",
			columns:   []string{"ID"},
		},
		{
			name:      "own OFFSET / FETCH and ORDER BY",
			statement: "SELECT ID FROM T ORDER BY ID DESC OFFSET 2 ROWS FETCH FIRST 5 ROWS ONLY",
			count:     "SELECT COUNT(*) FROM (SELECT ID FROM T ORDER BY ID DESC OFFSET 2 ROWS FETCH FIRST 5 ROWS ONLY)",
			page:      "SELECT ID FROM T ORDER BY ID DESC OFFSET 2 ROWS FETCH FIRST 5 ROWS ONLY",
			columns:   []string{"ID"},
		},
		{
			name:      "WITH and subqueries",
			statement: "WITH X AS (SELECT ID FROM T) SELECT ID FROM X WHERE ID IN (SELECT ID FROM U) ORDER BY ID",
			count:     "SELECT COUNT(*) FROM (WITH X AS (SELECT ID FROM T) SELECT ID FROM X WHERE ID IN (SELECT ID FROM U) ORDER BY ID)",
			page:      "WITH X AS (SELECT ID FROM T) SELECT ID FROM X WHERE ID IN (SELECT ID FROM U) ORDER BY ID",
			columns:   []string{"ID"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, mock := newExecSession(t)
			mock.ExpectQuery(tc.count).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(7)))
			mock.ExpectQuery(tc.page).WillReturnRows(countingRows(tc.columns, 7)).RowsWillBeClosed()

			res, err := s.ExecutePaged(context.Background(), tc.statement, dbdriver.Page{Index: 1, Size: 3})
			require.NoError(t, err)
			require.NotNil(t, res.Rows)
			require.NotNil(t, res.Rows.Total)
			assert.Equal(t, int64(7), *res.Rows.Total)
			assert.Equal(t, tc.columns, res.Rows.Columns)
			require.Len(t, res.Rows.Rows, 3)
			for i, row := range res.Rows.Rows {
				assert.Equal(t, int64(4+i), row[0], "page 2 of 3 rows starts at the 4th row")
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExecutePagedLastAndPastLastPage(t *testing.T) {
	for _, tc := range []struct {
		page dbdriver.Page
		want []int64
	}{
		{dbdriver.Page{Index: 2, Size: 3}, []int64{7}},
		{dbdriver.Page{Index: 5, Size: 3}, nil},
	} {
		t.Run(fmt.Sprintf("page %d", tc.page.Index), func(t *testing.T) {
			s, _, mock := newExecSession(t)
			mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT ID FROM T)").WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(7)))
			mock.ExpectQuery("SELECT ID FROM T").WillReturnRows(countingRows([]string{"ID"}, 7)).RowsWillBeClosed()

			res, err := s.ExecutePaged(context.Background(), "SELECT ID FROM T", tc.page)
			require.NoError(t, err)
			var got []int64
			for _, row := range res.Rows.Rows {
				got = append(got, row[0].(int64))
			}
			assert.Equal(t, tc.want, got)
			assert.Equal(t, []string{"ID"}, res.Rows.Columns)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Paging re-runs the text for every page, so a text of several statements is
// refused before any of them runs.
func TestExecutePagedRefusesSeveralStatements(t *testing.T) {
	s, _, mock := newExecSession(t)
	_, err := s.ExecutePaged(context.Background(), "SELECT ID FROM T; DELETE FROM T", dbdriver.Page{Index: 0, Size: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one statement")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing runs")
}

func TestExecutePagedOtherStatementsRunUnpaged(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec("UPDATE T SET A = 1 WHERE ID = 2").WillReturnResult(sqlmock.NewResult(0, 1))

	res, err := s.ExecutePaged(context.Background(), "UPDATE T SET A = 1 WHERE ID = 2;", dbdriver.Page{Index: 0, Size: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.AffectedRows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---- broken connections ----

// brokenConn is a go-ora stand-in whose connection breaks at the statement failOn:
// go-ora then answers driver.ErrBadConn. ran records the statements of every
// connection opened.
type brokenConn struct {
	driver.Conn
	ran    *[]string
	failOn string
}

func (c *brokenConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	*c.ran = append(*c.ran, query)
	if query == c.failOn {
		return nil, driver.ErrBadConn
	}
	return driver.RowsAffected(1), nil
}

func (c *brokenConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	*c.ran = append(*c.ran, query)
	if query == c.failOn {
		return nil, driver.ErrBadConn
	}
	return &endlessRows{onNext: func() {}}, nil
}

func (c *brokenConn) Close() error { return nil }

// brokenConnector opens a brokenConn per connection; the first breaks at failOn,
// the later ones do not.
type brokenConnector struct {
	ran    []string
	failOn string
	opened int
}

func (b *brokenConnector) Connect(context.Context) (driver.Conn, error) {
	b.opened++
	failOn := ""
	if b.opened == 1 {
		failOn = b.failOn
	}
	return &conn{inner: &brokenConn{ran: &b.ran, failOn: failOn}}, nil
}

func (b *brokenConnector) Driver() driver.Driver { return nil }

// database/sql runs a text again on another connection when the driver answers
// driver.ErrBadConn. Once a statement of the text has run (and, outside a
// transaction, committed) the text must not run again: the failure is the
// statement's error, not a bad connection.
func TestBrokenConnectionAfterARunStatementDoesNotRunTheTextAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{"execution", func(db *sql.DB) error {
			_, err := db.ExecContext(context.Background(), "INSERT INTO T VALUES (1); INSERT INTO T VALUES (2)")
			return err
		}},
		{"query", func(db *sql.DB) error {
			rows, err := db.QueryContext(context.Background(), "INSERT INTO T VALUES (1); INSERT INTO T VALUES (2)")
			if err == nil {
				_ = rows.Close()
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &brokenConnector{failOn: "INSERT INTO T VALUES (2)"}
			db := sql.OpenDB(c)
			t.Cleanup(func() { _ = db.Close() })

			err := tc.run(db)
			assert.Equal(t, []string{"INSERT INTO T VALUES (1)", "INSERT INTO T VALUES (2)"}, c.ran, "the first INSERT runs once")
			require.Error(t, err)
			assert.NotErrorIs(t, err, driver.ErrBadConn)
			assert.Contains(t, err.Error(), "statement 2 of 2")
		})
	}
}

// A connection that is broken before anything of the text ran is retried on
// another one, as database/sql does for any statement.
func TestBrokenConnectionBeforeTheFirstStatementIsRetried(t *testing.T) {
	c := &brokenConnector{failOn: "INSERT INTO T VALUES (1)"}
	db := sql.OpenDB(c)
	t.Cleanup(func() { _ = db.Close() })

	res, err := db.ExecContext(context.Background(), "INSERT INTO T VALUES (1); INSERT INTO T VALUES (2)")
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.Equal(t, []string{"INSERT INTO T VALUES (1)", "INSERT INTO T VALUES (1)", "INSERT INTO T VALUES (2)"}, c.ran)
}
