package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A dump's table data is written by the extension itself (sqldb.TableStatementsWriter,
// spec decision 14): it reads the rows in a read-only transaction of its own
// connection, a CLOB, NCLOB or BLOB value larger than inlineLobBytes in pieces,
// and gives the statements one at a time. These tests drive it through the shared
// session, as the extension SDK does, over the driver's connection (exec.go) with
// sqlmock standing in for go-ora.

const docs = `"HR"."DOCS"`

// expectDocsWriter answers what preparing the row writer of HR.DOCS asks (and
// what the dump asks again for the first row it reads in pieces): its columns
// and the key that finds a row.
func expectDocsWriter(mock sqlmock.Sqlmock, key ...string) {
	mock.ExpectQuery(tableColumnsQuery).WithArgs("HR", "DOCS").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0").
			AddRow("BODY", "CLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("NBODY", "NCLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("IMG", "BLOB", "YES", nil, nil, "0", "0", "0").
			AddRow("NOTE", "VARCHAR2(20)", "YES", nil, nil, "0", "0", "0"))
	rows := sqlmock.NewRows([]string{"COLUMN_NAME"})
	for _, column := range key {
		rows.AddRow(column)
	}
	mock.ExpectQuery(rowKeyQuery).WithArgs("HR", "DOCS").WillReturnRows(rows)
}

// inline is a large LOB column's expression in a dump's read: the value only
// when it is read with the row.
func inline(column string, units int) string {
	return `CASE WHEN DBMS_LOB.GETLENGTH("` + column + `") <= ` + strconv.Itoa(units) + ` THEN "` + column + `" END`
}

// expectDocsRead expects the read-only transaction and the dump's read of HR.DOCS
// (ID, BODY CLOB, NBODY NCLOB, IMG BLOB, NOTE): each LOB value within
// inlineLobBytes read with the row, a larger one's length in a column of its own,
// the ROWID by which its pieces are read, and the guard that stops the read before
// a value over 16 MB (lobGuard). Fetches are sized by the largest row's LOB bytes
// as read with the row.
func expectDocsRead(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectBegin()
	mock.ExpectExec("SET TRANSACTION READ ONLY").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(tableColumnTypesQuery).WithArgs("HR", "DOCS").WillReturnRows(tableColumnTypes().
		AddRow("ID", "NUMBER", nil).AddRow("BODY", "CLOB", nil).AddRow("NBODY", "NCLOB", nil).
		AddRow("IMG", "BLOB", nil).AddRow("NOTE", "VARCHAR2", nil))
	read := func(column string, units, bytes int) string {
		return `CASE WHEN DBMS_LOB.GETLENGTH("` + column + `") <= ` + strconv.Itoa(units) + ` THEN DBMS_LOB.GETLENGTH("` + column + `") ELSE 0 END * ` + strconv.Itoa(bytes)
	}
	mock.ExpectQuery(`SELECT MAX(` + read("BODY", 131072, 3) + ` + ` + read("NBODY", 131072, 3) + ` + ` + read("IMG", 262144, 1) +
		`) FROM (SELECT "BODY", "NBODY", "IMG" FROM ` + docs + `)`).
		WillReturnRows(sqlmock.NewRows([]string{"MAX"}).AddRow("1000"))
	unread := func(column string, units int) string {
		return `CASE WHEN DBMS_LOB.GETLENGTH("` + column + `") > ` + strconv.Itoa(units) + ` THEN DBMS_LOB.GETLENGTH("` + column + `") END`
	}
	_, bodyGuard := guardedSelect(`"BODY"`, 8388608)
	_, nbodyGuard := guardedSelect(`"NBODY"`, 8388608)
	_, imgGuard := guardedSelect(`"IMG"`, 16777216)
	mock.ExpectQuery(`SELECT "ID", ` + inline("BODY", 131072) + `, ` + inline("NBODY", 131072) + `, ` + inline("IMG", 262144) + `, "NOTE", ` +
		unread("BODY", 131072) + `, ` + unread("NBODY", 131072) + `, ` + unread("IMG", 262144) + `, CAST(ROWID AS VARCHAR2(4000)), ` +
		bodyGuard + `, ` + nbodyGuard + `, ` + imgGuard + ` FROM ` + docs).WillReturnRows(rows)
}

// docsRows is a result of the dump's read of HR.DOCS: the five columns, the three
// lengths, the ROWID and the three guards.
func docsRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"ID", "BODY", "NBODY", "IMG", "NOTE", "LB", "LNB", "LI", "ROWID", "GB", "GNB", "GI"})
}

// clobPieceQuery and blobPieceQuery read a piece of HR.DOCS's column of the row
// with a ROWID: a CLOB's or NCLOB's units from an offset, and how many it read
// (Oracle's SUBSTR leaves out the first half of a surrogate pair its last unit
// would split); a BLOB's bytes from an offset, copied into a temporary BLOB (SQL's
// SUBSTR does not take a BLOB, DBMS_LOB.SUBSTR in SQL returns at most 2000 bytes).
func clobPieceQuery(column string) string {
	return `SELECT v.s, DBMS_LOB.GETLENGTH(v.s) FROM (SELECT SUBSTR("` + column + `", :1, :2) s FROM ` + docs + ` WHERE ROWID = :3) v`
}

func blobPieceQuery(column string) string {
	return `WITH FUNCTION opskat_piece(b BLOB, o NUMBER, n NUMBER) RETURN BLOB IS r BLOB; BEGIN ` +
		`DBMS_LOB.CREATETEMPORARY(r, TRUE, DBMS_LOB.CALL); DBMS_LOB.COPY(r, b, n, 1, o); RETURN r; END; ` +
		`SELECT opskat_piece("` + column + `", :1, :2) FROM ` + docs + ` WHERE ROWID = :3`
}

var docsColumns = []string{"ID", "BODY", "NBODY", "IMG", "NOTE"}

// dumpStatements reads the statements the session writes for HR.DOCS's dump, a
// page at a time as the host does, and the rows they finish.
func dumpStatements(t *testing.T, s *sqldb.Session, req dbdriver.TableStatementsRequest) ([]string, int, error) {
	t.Helper()
	source, err := s.TableStatements()
	require.NoError(t, err)
	pages, err := source.TableStatements(context.Background(), "HR", "DOCS", req)
	if err != nil {
		return nil, 0, err
	}
	var statements []string
	rows := 0
	for {
		page, err := pages.Next()
		if err != nil {
			return statements, rows, errors.Join(err, pages.Close())
		}
		statements, rows = append(statements, page.Statements...), rows+page.Rows
		if page.Done {
			return statements, rows, pages.Close()
		}
	}
}

func dumpRequest() dbdriver.TableStatementsRequest {
	return dbdriver.TableStatementsRequest{Columns: docsColumns, OmitDatabase: true, OwnRows: true, PageBytes: dbdriver.DumpPageBytes, BatchRows: 500}
}

// noConnectionInUse fails when the dump kept a connection of the pool.
func noConnectionInUse(t *testing.T, db *sql.DB) {
	t.Helper()
	assert.Zero(t, db.Stats().InUse, "connections in use")
}

// Rows whose values fit the read (no LOB larger than inlineLobBytes) are written
// exactly as copy as INSERT writes them — the INSERT, or the PL/SQL block of a
// LOB that does not fit one literal — one statement a row, in the read-only
// transaction, which ends with the read.
func TestDumpWritesRowsAsCopyAsInsert(t *testing.T) {
	s, db, mock := newExecSession(t)
	expectDocsWriter(mock, "ID")
	body := strings.Repeat("x", 4001)
	expectDocsRead(mock, docsRows().
		AddRow("1", "it's", nil, []byte{0xde, 0xad}, "a", nil, nil, nil, "AAA1", nil, nil, nil).
		AddRow("2", body, "中文", nil, nil, nil, nil, nil, "AAA2", nil, nil, nil))
	mock.ExpectRollback()

	statements, rows, err := dumpStatements(t, s, dumpRequest())
	require.NoError(t, err)
	assert.Equal(t, 2, rows)
	copied := dialect{}.mustRowInsertStatement(t, docsRow(map[string]any{"ID": "2", "BODY": body, "NBODY": "中文"}))
	assert.Equal(t, []string{
		`INSERT INTO "DOCS" ("ID", "BODY", "NBODY", "IMG", "NOTE") VALUES (1, 'it''s', NULL, HEXTORAW('dead'), 'a');`,
		copied,
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
	noConnectionInUse(t, db)
}

// docsRow is the row of HR.DOCS a dump writes, as the row writer hands it to
// RowInsertStatement.
func docsRow(values map[string]any) sqldb.InsertRow {
	types := map[string]string{"ID": "NUMBER(10)", "BODY": "CLOB", "NBODY": "NCLOB", "IMG": "BLOB", "NOTE": "VARCHAR2(20)"}
	row := sqldb.InsertRow{TableRef: `"DOCS"`, Quote: quoteIdent, Literals: literals, Key: []string{"ID"}}
	for _, column := range docsColumns {
		row.Columns = append(row.Columns, sqldb.InsertColumn{Name: column, Type: types[column], Value: values[column]})
	}
	return row
}

// mustRowInsertStatement is RowInsertStatement's statement of row, which must be one.
func (d dialect) mustRowInsertStatement(t *testing.T, row sqldb.InsertRow) string {
	t.Helper()
	statement, ok, err := d.RowInsertStatement(row)
	require.NoError(t, err)
	require.True(t, ok)
	return statement
}

// A value larger than inlineLobBytes is not read with the row: its pieces are
// read by the row's ROWID, about 1 MB at a time (half a million UTF-16 units of
// a CLOB or NCLOB, 1,050,000 bytes of a BLOB), as the blocks are written, and the
// row's statements — its blocks, one statement each — are the ones the row writer
// writes for the whole values (copy as INSERT; spec decision 10).
func TestDumpReadsLargeLobsInPieces(t *testing.T) {
	s, db, mock := newExecSession(t)
	expectDocsWriter(mock, "ID")
	var body strings.Builder
	for i := 0; body.Len() < 900_000; i++ {
		body.WriteString("line 'q' 中文\n")
	}
	text := body.String()
	units := len(utf16.Encode([]rune(text)))
	img := make([]byte, 1_200_000)
	for i := range img {
		img[i] = byte(i * 7)
	}
	expectDocsRead(mock, docsRows().AddRow("7", nil, nil, nil, "it's", units, nil, len(img), "AAAR7", nil, nil, nil))
	expectDocsWriter(mock, "ID")
	first := utf16Prefix(text, textReadUnits)
	mock.ExpectQuery(clobPieceQuery("BODY")).WithArgs(1, textReadUnits, "AAAR7").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE", "N"}).AddRow(first, textReadUnits))
	rest := units - textReadUnits
	mock.ExpectQuery(clobPieceQuery("BODY")).WithArgs(textReadUnits+1, rest, "AAAR7").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE", "N"}).AddRow(text[len(first):], rest))
	mock.ExpectQuery(blobPieceQuery("IMG")).WithArgs(1, blobReadBytes, "AAAR7").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE"}).AddRow(img[:blobReadBytes]))
	mock.ExpectQuery(blobPieceQuery("IMG")).WithArgs(blobReadBytes+1, len(img)-blobReadBytes, "AAAR7").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE"}).AddRow(img[blobReadBytes:]))
	mock.ExpectRollback()

	statements, rows, err := dumpStatements(t, s, dumpRequest())
	require.NoError(t, err)
	assert.Equal(t, 1, rows)
	whole := dialect{}.mustRowInsertStatement(t, docsRow(map[string]any{"ID": "7", "BODY": text, "IMG": img, "NOTE": "it's"}))
	assert.Equal(t, splitBlocksAnySize(t, whole), statements)
	require.NoError(t, mock.ExpectationsWereMet())
	noConnectionInUse(t, db)
}

// utf16Prefix is the start of text that holds its first n UTF-16 units.
func utf16Prefix(text string, n int) string {
	for i, r := range text {
		if n -= utf16.RuneLen(r); n < 0 {
			return text[:i]
		}
	}
	return text
}

// A piece read never ends inside a surrogate pair, which would leave half a
// character on each side: Oracle's SUBSTR reads one unit fewer when the last
// unit asked for begins a pair, and says how many it read, and the next piece
// starts there, on the pair. A piece read is never cut in Go.
func TestDumpPieceReadsKeepSurrogatePairsWhole(t *testing.T) {
	s, db, mock := newExecSession(t)
	expectDocsWriter(mock, "ID")
	text := "a" + strings.Repeat("😀", 300_000)
	units := 1 + 600_000
	expectDocsRead(mock, docsRows().AddRow("1", nil, nil, nil, nil, nil, units, nil, "AAAR1", nil, nil, nil))
	expectDocsWriter(mock, "ID")
	first := utf16Prefix(text, textReadUnits-1)
	mock.ExpectQuery(clobPieceQuery("NBODY")).WithArgs(1, textReadUnits, "AAAR1").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE", "N"}).AddRow(first, textReadUnits-1))
	rest := units - (textReadUnits - 1)
	mock.ExpectQuery(clobPieceQuery("NBODY")).WithArgs(textReadUnits, rest, "AAAR1").
		WillReturnRows(sqlmock.NewRows([]string{"PIECE", "N"}).AddRow(text[len(first):], rest))
	mock.ExpectRollback()

	statements, _, err := dumpStatements(t, s, dumpRequest())
	require.NoError(t, err)
	whole := dialect{}.mustRowInsertStatement(t, docsRow(map[string]any{"ID": "1", "NBODY": text}))
	assert.Equal(t, splitBlocksAnySize(t, whole), statements)
	require.NoError(t, mock.ExpectationsWereMet())
	noConnectionInUse(t, db)
}

// A value over 16 MB stops the dump when its row is read, before any of it is
// read (no piece is asked for), naming the table, the column and the size; the
// transaction ends and the connection goes back to the pool.
func TestDumpStopsBeforeReadingAValueOverTheLimit(t *testing.T) {
	s, db, mock := newExecSession(t)
	expectDocsWriter(mock, "ID")
	expectDocsRead(mock, docsRows().AddRow("1", nil, nil, nil, nil, nil, nil, 17825792, "AAAR1", nil, nil, "17825792"))
	mock.ExpectRollback()

	statements, _, err := dumpStatements(t, s, dumpRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `column "IMG" of table "HR"."DOCS" holds a value of 17.0 MB (17825792 bytes)`)
	assert.Empty(t, statements)
	require.NoError(t, mock.ExpectationsWereMet())
	noConnectionInUse(t, db)
}

// The dump's connection goes back to the pool with its transaction ended when
// the statements are read to the end (TestDumpWritesRowsAsCopyAsInsert), when
// reading a piece fails, and when the host closes the stream before the end (a
// cancelled dump).
func TestDumpReleasesItsConnection(t *testing.T) {
	t.Run("a piece fails", func(t *testing.T) {
		s, db, mock := newExecSession(t)
		expectDocsWriter(mock, "ID")
		expectDocsRead(mock, docsRows().AddRow("1", nil, nil, nil, nil, 300_000, nil, nil, "AAAR1", nil, nil, nil))
		expectDocsWriter(mock, "ID")
		mock.ExpectQuery(clobPieceQuery("BODY")).WillReturnError(errors.New("ORA-01013: user requested cancel"))
		mock.ExpectRollback()

		_, _, err := dumpStatements(t, s, dumpRequest())
		require.ErrorContains(t, err, "ORA-01013")
		require.NoError(t, mock.ExpectationsWereMet())
		noConnectionInUse(t, db)
	})
	t.Run("closed before the end", func(t *testing.T) {
		s, db, mock := newExecSession(t)
		expectDocsWriter(mock, "ID")
		expectDocsRead(mock, docsRows().
			AddRow("1", "a", nil, nil, nil, nil, nil, nil, "AAAR1", nil, nil, nil).
			AddRow("2", "b", nil, nil, nil, nil, nil, nil, "AAAR2", nil, nil, nil))
		mock.ExpectRollback()

		source, err := s.TableStatements()
		require.NoError(t, err)
		req := dumpRequest()
		req.BatchRows = 1
		pages, err := source.TableStatements(context.Background(), "HR", "DOCS", req)
		require.NoError(t, err)
		page, err := pages.Next()
		require.NoError(t, err)
		assert.Equal(t, 1, page.Rows)
		require.NoError(t, pages.Close())
		require.NoError(t, mock.ExpectationsWereMet())
		noConnectionInUse(t, db)
	})
}
