package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// How the connection runs what it is given.
//
// A statement with bind arguments is one the driver generated itself; it reaches
// go-ora exactly as written. Any other text is what a user, the AI or opsctl
// wrote — the host hands a whole text to one Session.Execute — so it is split
// with splitOracle and run one statement at a time: a plain statement without
// its trailing ';' (Oracle rejects it), a PL/SQL block with its closing ';'
// (Oracle needs it). The shared session (sqldb) chooses between a query and an
// execution by the text's first word; every statement but the last is executed
// and the last is run the way the session asked, so a query's result is the last
// statement's.
//
// A table's own reads (the table view's pages, an export) are resolved with the
// table's columns known (values.go).
//
// The query panel's paging is done here too: the dialect only marks the text
// (markCount / markPage), and the connection counts it, or runs it as written and
// stops reading at the end of the requested page. Wrapping it in a subquery
// would break on duplicate column names, FOR UPDATE and a trailing comment.

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return c.queryRows(ctx, query, args)
	}
	if text, ok := strings.CutPrefix(query, tableMark); ok {
		return c.queryTable(ctx, text)
	}
	if text, ok := strings.CutPrefix(query, countMark); ok {
		return c.queryCount(ctx, text)
	}
	if offset, limit, text, ok := parsePageMark(query); ok {
		return c.queryPage(ctx, text, offset, limit)
	}
	stmts, err := splitScript(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 1 && isTableRead(stmts[0].Text) {
		return c.queryTable(ctx, stmts[0].Text)
	}
	last := len(stmts) - 1
	for i := range last {
		if _, err := c.execStatement(ctx, stmts, i); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := c.queryRows(ctx, stmts[last].Text, nil)
	return r, statementError(err, last, len(stmts))
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		return c.exec(ctx, query, args)
	}
	stmts, err := splitScript(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 1 {
		return c.exec(ctx, stmts[0].Text, nil)
	}
	var affected int64
	for i := range stmts {
		n, err := c.execStatement(ctx, stmts, i)
		if err != nil {
			return nil, err
		}
		affected += n
	}
	return driver.RowsAffected(affected), nil
}

// execStatement executes statement i of a script and returns the rows it
// affected. It does not start once ctx has ended (the query timeout), and its
// failure names the statement.
func (c *conn) execStatement(ctx context.Context, stmts []dbdriver.Statement, i int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := c.exec(ctx, stmts[i].Text, nil)
	if err != nil {
		return 0, statementError(err, i, len(stmts))
	}
	n, err := res.RowsAffected()
	return n, statementError(err, i, len(stmts))
}

// statementError names statement i of a script of n in err; the error of a text
// of one statement stays Oracle's own. A broken connection (driver.ErrBadConn)
// after statement 0 is no longer one: database/sql would run the whole text again
// on another connection, and the statements before i have run and, outside a
// transaction, committed.
func statementError(err error, i, n int) error {
	if err == nil || n == 1 {
		return err
	}
	if i > 0 && errors.Is(err, driver.ErrBadConn) {
		return fmt.Errorf("oracle: statement %d of %d: the connection broke after the statements before it ran: %v", i+1, n, err)
	}
	return fmt.Errorf("oracle: statement %d of %d: %w", i+1, n, err)
}

// splitScript splits a text into the statements to run.
func splitScript(ctx context.Context, text string) ([]dbdriver.Statement, error) {
	stmts, err := splitOracle(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, errors.New("oracle: the SQL text holds no statement")
	}
	return stmts, nil
}

// queryRows is query for database/sql: a failed query returns no Rows at all.
func (c *conn) queryRows(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r, err := c.query(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (c *conn) query(ctx context.Context, query string, args []driver.NamedValue) (*rows, error) {
	q, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, errors.New("oracle: connection cannot run queries")
	}
	inner, err := q.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	r := &rows{inner: inner, left: -1}
	if typed, ok := inner.(driver.RowsColumnTypeDatabaseTypeName); ok {
		r.types = make([]string, len(inner.Columns()))
		for i := range r.types {
			r.types[i] = typed.ColumnTypeDatabaseTypeName(i)
		}
	}
	return r, nil
}

func (c *conn) exec(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return execContext(ctx, c.inner, query, args)
}

// ---- paging ----

// The marks the dialect puts in front of the query panel's paged query. Both are
// comments, so a marked text is still the query itself.
const (
	countMark     = "/*opskat:count*/"
	pageMarkStart = "/*opskat:page "
)

func markCount(query string) string { return countMark + query }

func markPage(query string, offset, limit int) string {
	return fmt.Sprintf("%s%d %d*/%s", pageMarkStart, offset, limit, query)
}

// parsePageMark reads what markPage wrote.
func parsePageMark(query string) (offset, limit int, text string, ok bool) {
	rest, ok := strings.CutPrefix(query, pageMarkStart)
	if !ok {
		return 0, 0, "", false
	}
	head, text, ok := strings.Cut(rest, "*/")
	if !ok {
		return 0, 0, "", false
	}
	if _, err := fmt.Sscanf(head, "%d %d", &offset, &limit); err != nil {
		return 0, 0, "", false
	}
	return offset, limit, text, true
}

// pagedStatement is the one statement of a paged query. The panel runs the text
// again for every page (and once more to count it), so a text of several
// statements would run them again each time: it is refused.
func pagedStatement(ctx context.Context, text string) (dbdriver.Statement, error) {
	stmts, err := splitScript(ctx, text)
	if err != nil {
		return dbdriver.Statement{}, err
	}
	if len(stmts) > 1 {
		return dbdriver.Statement{}, fmt.Errorf("oracle: paging a result needs one statement, the text has %d; run them one at a time", len(stmts))
	}
	return stmts[0], nil
}

// queryCount counts the rows of the query: its statement, without a trailing
// comment or ';' and without its locking clause (FOR UPDATE is not allowed in a
// subquery, and a count needs no lock), inside SELECT COUNT(*).
func (c *conn) queryCount(ctx context.Context, text string) (driver.Rows, error) {
	st, err := pagedStatement(ctx, text)
	if err != nil {
		return nil, err
	}
	query, err := withoutLockingClause(st.Text)
	if err != nil {
		return nil, err
	}
	return c.queryRows(ctx, "SELECT COUNT(*) FROM ("+query+")", nil)
}

// queryPage runs the query as written and returns its rows from offset on, at
// most limit of them; closing the rows closes the cursor on the rest.
func (c *conn) queryPage(ctx context.Context, text string, offset, limit int) (driver.Rows, error) {
	st, err := pagedStatement(ctx, text)
	if err != nil {
		return nil, err
	}
	r, err := c.query(ctx, st.Text, nil)
	if err != nil {
		return nil, err
	}
	r.left = max(limit, 0)
	dest := make([]driver.Value, len(r.inner.Columns()))
	for range offset {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, r.Close())
		}
		err := r.inner.Next(dest)
		if errors.Is(err, io.EOF) {
			r.left = 0
			break
		}
		if err != nil {
			return nil, errors.Join(err, r.Close())
		}
	}
	return r, nil
}

var forUpdate = regexp.MustCompile(`(?i)\bFOR\s+UPDATE\b`)

// withoutLockingClause cuts a query's top-level FOR UPDATE clause, which Oracle
// allows only at the end of the outermost query, and the blanks and comments
// before it: a line comment left at the end would swallow the subquery's ")".
func withoutLockingClause(query string) (string, error) {
	masked, err := maskLiterals(query)
	if err != nil {
		return "", err
	}
	for _, m := range forUpdate.FindAllStringIndex(masked, -1) {
		if strings.Count(masked[:m[0]], "(") == strings.Count(masked[:m[0]], ")") {
			return query[:len(strings.TrimRight(masked[:m[0]], " \t\r\n"))], nil
		}
	}
	return query, nil
}

// ---- results ----

// rows is every result set the connection returns: the one place a row's values
// pass on their way to database/sql. It stops after left rows (a page); left < 0
// is no limit.
type rows struct {
	inner driver.Rows
	left  int
	// types are go-ora's type names of the columns (empty when the connection
	// reports none) and fractions the fractional seconds of each (nil when unknown):
	// what displayValue needs to show a value.
	types     []string
	fractions []int
	// names replace the column names the database reports (nil keeps them): the
	// table view's unaliased SELECT-list expressions (queryTable).
	names []string
}

func (r *rows) Columns() []string {
	if r.names != nil {
		return r.names
	}
	return r.inner.Columns()
}

func (r *rows) Close() error { return r.inner.Close() }

func (r *rows) Next(dest []driver.Value) error {
	if r.left == 0 {
		return io.EOF
	}
	if err := r.inner.Next(dest); err != nil {
		return err
	}
	if r.left > 0 {
		r.left--
	}
	for i, v := range dest {
		var typeName string
		if i < len(r.types) {
			typeName = r.types[i]
		}
		fraction := -1
		if i < len(r.fractions) {
			fraction = r.fractions[i]
		}
		dest[i] = displayValue(v, typeName, fraction)
	}
	return nil
}
