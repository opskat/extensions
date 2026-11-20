package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

var _ sqldb.TableStatementsWriter = dialect{}

// TableStatements writes a dump's table data in the extension (spec decision 14):
// it reads the rows itself and gives their statements one at a time, so that no
// value crosses to the host and back, and a CLOB, NCLOB or BLOB value larger than
// inlineLobBytes is never whole in memory: the read leaves it out (readDump,
// values.go) and its pieces, about 1 MB at a time, are read by the row's ROWID as
// its blocks are written (rowBlocks). Every row is written as copy as INSERT
// writes it — the INSERT, or the blocks of a row with a large value (spec
// decision 10), each block one statement.
//
// The rows and every piece of them are read in one read-only transaction on a
// connection of the dump's own (readOnlyTransaction), so a row is the row as it
// was when the table's read began, also when another session changes it while its
// pieces are read. Closing the cursor ends the transaction and gives the
// connection back to the pool, or closes it when the transaction cannot be ended.
func (dialect) TableStatements(ctx context.Context, table sqldb.TableStatementsTable) (dbdriver.StatementCursor, error) {
	// The writer is prepared on a connection of its own, before the read holds one.
	writer, err := table.PrepareRowStatements(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := table.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	c := &dumpCursor{ctx: ctx, table: table, conn: conn, writer: writer, pieceQueries: map[string]string{}}
	if err := c.open(); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

// readOnlyTransaction makes every query of the transaction see the data as it was
// when it began. It takes no privilege and holds no lock.
const readOnlyTransaction = "SET TRANSACTION READ ONLY"

// How much of a large value a dump reads at a time: about 1 MB, half a million
// UTF-16 units of a CLOB or NCLOB (DBMS_LOB.GETLENGTH counts them), a whole
// number of BLOB pieces (maxBase64Piece).
const (
	textReadUnits = 1 << 19
	blobReadBytes = 350 * maxBase64Piece
)

// dumpCursor is a dump's statements of one table (TableStatements).
type dumpCursor struct {
	ctx    context.Context
	table  sqldb.TableStatementsTable
	conn   *sql.Conn
	tx     *sql.Tx
	rows   *sql.Rows
	writer dbdriver.RowStatementWriter
	// req is the row writer's request, without its rows.
	req dbdriver.RowStatementsRequest

	// columns are the table's columns the read gives, lengths the index of each
	// one's length column (dumpLengthColumn) and rowid the ROWID's index; values
	// the row read.
	columns []string
	lengths map[string]int
	rowid   int
	values  []any

	// row is how the row writer writes a row with a large value (InsertRow), set
	// at the first one; pieceQueries the query that reads a piece of each column.
	row          *sqldb.InsertRow
	pieceQueries map[string]string

	// The row being written: the statements left of a row read whole, or the
	// blocks of a row read in pieces.
	statements []string
	blocks     *rowBlocks

	current string
	rowEnd  bool
	err     error
	closed  bool
}

func (c *dumpCursor) open() (err error) {
	if c.tx, err = c.conn.BeginTx(c.ctx, nil); err != nil {
		return err
	}
	if _, err = c.tx.ExecContext(c.ctx, readOnlyTransaction); err != nil {
		return err
	}
	ref := dialect{}.TableRef(c.table.Database, c.table.Table)
	if c.rows, err = c.tx.QueryContext(c.ctx, dumpMark+selectStarFrom+ref); err != nil { //nolint:rowserrcheck // dumpCursor.Err reports rows.Err
		return err
	}
	names, err := c.rows.Columns()
	if err != nil {
		return err
	}
	c.lengths, c.rowid = map[string]int{}, -1
	for i, name := range names {
		switch column, ok := strings.CutPrefix(name, dumpLengthColumn("")); {
		case ok:
			c.lengths[column] = i
		case name == dumpRowidColumn:
			c.rowid = i
		default:
			c.columns = append(c.columns, name)
		}
	}
	c.values = make([]any, len(names))
	columns := c.table.Request.Columns
	if columns == nil {
		columns = c.columns
	}
	c.req = dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: columns,
		OmitDatabase: c.table.Request.OmitDatabase, InsertModifier: c.table.Request.InsertModifier}
	return nil
}

func (c *dumpCursor) Next() bool {
	for c.err == nil {
		if c.blocks != nil {
			block, last, err := c.blocks.next()
			if err != nil {
				c.err = err
				return false
			}
			if last {
				c.blocks = nil
			}
			c.current, c.rowEnd = block, last
			return true
		}
		if len(c.statements) > 0 {
			c.current, c.statements = c.statements[0], c.statements[1:]
			c.rowEnd = len(c.statements) == 0
			return true
		}
		if !c.rows.Next() {
			return false
		}
		c.err = c.startRow()
	}
	return false
}

// startRow reads the row the read is on: a row whose values were read whole is
// the row writer's statement; one with a value left for pieces is its blocks.
func (c *dumpCursor) startRow() error {
	ptrs := make([]any, len(c.values))
	for i := range c.values {
		ptrs[i] = &c.values[i]
	}
	if err := c.rows.Scan(ptrs...); err != nil {
		return err
	}
	// As the shared session's cursor gives a row (sqldb's scanRow): bytes as a string.
	row := make(map[string]any, len(c.columns))
	for i, column := range c.columns {
		if b, ok := c.values[i].([]byte); ok {
			row[column] = string(b)
		} else {
			row[column] = c.values[i]
		}
	}
	// The written columns whose values are left for pieces, and their lengths.
	pieces := map[string]int64{}
	for _, column := range c.req.Columns {
		if i, ok := c.lengths[column]; ok && c.values[i] != nil {
			n, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(c.values[i])), 10, 64)
			if err != nil {
				return fmt.Errorf("oracle: length of column %s: %w", quoteIdent(column), err)
			}
			pieces[column] = n
		}
	}
	if len(pieces) == 0 {
		var err error
		c.statements, err = dbdriver.InsertStatements(c.writer, c.req, []map[string]any{row})
		return err
	}
	return c.startBlocks(row, pieces)
}

// startBlocks starts the blocks of a row whose values in pieces have the given
// lengths. The other values are the ones the row writer would be given
// (dbdriver.InsertStatements, captured), and the row is written as the writer
// writes it (InsertRow): its columns' types, the key that finds it, the table
// reference the request asks for.
func (c *dumpCursor) startBlocks(values map[string]any, pieces map[string]int64) error {
	if c.row == nil {
		if err := c.prepareRow(); err != nil {
			return err
		}
	}
	var sent capturedRow
	if _, err := dbdriver.InsertStatements(&sent, c.req, []map[string]any{values}); err != nil {
		return err
	}
	row := *c.row
	row.Columns = make([]sqldb.InsertColumn, len(c.req.Columns))
	streamed := map[string]*largeLob{}
	rowid := fmt.Sprint(c.values[c.rowid]) // text (dumpRowidSelect)
	for i, name := range c.req.Columns {
		row.Columns[i] = sqldb.InsertColumn{Name: name, Type: c.row.Columns[i].Type, Value: sent.row[name]}
		if length, ok := pieces[name]; ok {
			kind := typeName(row.Columns[i].Type)
			streamed[name] = streamedLob(kind, length, c.pieceReader(name, kind, rowid, length))
		}
	}
	var err error
	c.blocks, _, err = newRowBlocks(row, streamed)
	return err
}

// capturedRow takes the row a row writer is given (dbdriver.InsertStatements).
type capturedRow struct{ row map[string]any }

func (r *capturedRow) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	r.row = req.Rows[0]
	return nil, nil
}

// prepareRow asks what the row writer asked when it was prepared
// (sqldb.Session.PrepareRowStatements): the columns' types and the key that finds
// a row, on the dump's connection.
func (c *dumpCursor) prepareRow() error {
	d := dialect{}
	columns, err := d.TableColumns(c.ctx, c.conn, c.table.Database, c.table.Table)
	if err != nil {
		return fmt.Errorf("query columns: %w", err)
	}
	key, err := d.InsertRowKey(c.ctx, c.conn, c.table.Database, c.table.Table)
	if err != nil {
		return fmt.Errorf("query row key: %w", err)
	}
	types := make(map[string]string, len(columns))
	for _, column := range columns {
		types[column.Name] = column.Type
	}
	ref := d.TableRef(c.table.Database, c.table.Table)
	if c.req.OmitDatabase {
		ref = d.TableRef("", c.table.Table)
	}
	row := sqldb.InsertRow{TableRef: ref, Modifier: c.req.InsertModifier, Quote: quoteIdent, Literals: literals, Key: key}
	for _, name := range c.req.Columns {
		row.Columns = append(row.Columns, sqldb.InsertColumn{Name: name, Type: types[name]})
	}
	c.row = &row
	return nil
}

// pieceReader reads the value of column (of kind, length DBMS_LOB.GETLENGTH units)
// of the row with rowid part by part, in the read's transaction.
func (c *dumpCursor) pieceReader(column, kind, rowid string, length int64) func() (any, bool, error) {
	query, ok := c.pieceQueries[column]
	if !ok {
		query = pieceQuery(dialect{}.TableRef(c.table.Database, c.table.Table), quoteIdent(column), kind)
		c.pieceQueries[column] = query
	}
	offset := int64(1)
	return func() (any, bool, error) {
		if offset > length {
			return nil, false, nil
		}
		if kind == "BLOB" {
			n := min(blobReadBytes, length-offset+1)
			var part []byte
			if err := c.tx.QueryRowContext(c.ctx, query, offset, n, rowid).Scan(&part); err != nil {
				return nil, false, fmt.Errorf("oracle: read column %s: %w", quoteIdent(column), err)
			}
			if int64(len(part)) != n {
				return nil, false, fmt.Errorf("oracle: read %d bytes of column %s from byte %d, not %d", len(part), quoteIdent(column), offset, n)
			}
			offset += n
			return part, true, nil
		}
		// Oracle reads one unit fewer when the last would be the first half of a
		// surrogate pair (pieceQuery), so the next piece starts on a character.
		n := min(textReadUnits, length-offset+1)
		var part string
		var read int64
		if err := c.tx.QueryRowContext(c.ctx, query, offset, n, rowid).Scan(&part, &read); err != nil {
			return nil, false, fmt.Errorf("oracle: read column %s: %w", quoteIdent(column), err)
		}
		if read < 1 || read < n-1 || read > n {
			return nil, false, fmt.Errorf("oracle: read %d units of column %s from unit %d, not %d", read, quoteIdent(column), offset, n)
		}
		offset += read
		return part, true, nil
	}
}

// pieceQuery reads a piece of column of the row with a ROWID of table. A CLOB's
// or NCLOB's is SUBSTR, which gives a CLOB of any size, of :2 units from :1 —
// one fewer when the last would split a surrogate pair: Oracle leaves out its
// first half — and how many units it read. A BLOB's is copied into a temporary
// BLOB by a function of the query: SQL's SUBSTR takes no BLOB (ORA-22835) and its
// DBMS_LOB.SUBSTR gives at most 2000 bytes.
func pieceQuery(table, column, kind string) string {
	if kind == "BLOB" {
		return "WITH FUNCTION opskat_piece(b BLOB, o NUMBER, n NUMBER) RETURN BLOB IS r BLOB; BEGIN " +
			"DBMS_LOB.CREATETEMPORARY(r, TRUE, DBMS_LOB.CALL); DBMS_LOB.COPY(r, b, n, 1, o); RETURN r; END; " +
			"SELECT opskat_piece(" + column + ", :1, :2) FROM " + table + " WHERE ROWID = :3"
	}
	return "SELECT v.s, DBMS_LOB.GETLENGTH(v.s) FROM (SELECT SUBSTR(" + column + ", :1, :2) s FROM " + table + " WHERE ROWID = :3) v"
}

func (c *dumpCursor) Statement() string { return c.current }
func (c *dumpCursor) RowEnd() bool      { return c.rowEnd }

func (c *dumpCursor) Err() error {
	if c.err != nil {
		return c.err
	}
	if c.rows != nil {
		return c.rows.Err()
	}
	return nil
}

// Close ends the read and its transaction and gives the connection back; one
// whose transaction cannot be ended is closed instead, so that no other use of
// the pool runs in it.
func (c *dumpCursor) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	var errs []error
	if c.rows != nil {
		errs = append(errs, c.rows.Close())
	}
	if c.tx != nil {
		if err := c.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			_ = c.conn.Raw(func(any) error { return driver.ErrBadConn })
			errs = append(errs, fmt.Errorf("end the dump's read-only transaction: %w", err))
		}
	}
	errs = append(errs, c.conn.Close())
	return errors.Join(errs...)
}
