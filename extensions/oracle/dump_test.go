package main

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden rewrites the golden dump files from the current output:
// go test -run TestDump -update-golden.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/dump_*.sql")

// The dictionary queries the dump hooks run are told apart by a distinctive part
// of each (the mock matches them out of order).
const (
	setTransformsPattern   = `'SEGMENT_ATTRIBUTES', FALSE`
	resetTransformsPattern = `SESSION_TRANSFORM, 'DEFAULT'`
	tableMetadataPattern   = `GET_DDL\('TABLE'`
	indexMetadataPattern   = `GET_DDL\('INDEX'`
	sequencesPattern       = `FROM all_sequences`
	sequenceUsersPattern   = `default_length > 0`
	commentsPattern        = `all_tab_comments`
	foreignKeysPattern     = `constraint_type = 'R'`
	dumpColumnsPattern     = `virtual_column`
	rowColumnsPattern      = `IS_NO_IDENTITY_OVERRIDE`
)

// dumpSource is the session as the dump orchestration reads it, with the listing,
// the definitions the host would hand over and the rows faked; the row
// statements are the session's own (Oracle's literals over the mocked columns).
type dumpSource struct {
	*sqldb.Session
	tables []string
	rows   map[string]*sliceStream
}

func (s dumpSource) ListTables(context.Context, string) ([]string, error) { return s.tables, nil }

func (s dumpSource) ListObjects(context.Context, string) (*dbdriver.DatabaseObjects, error) {
	return &dbdriver.DatabaseObjects{}, nil
}

// TableDDL is what the host reads as a table's definition: DBMS_METADATA's, with
// the schema and the storage the dump must not write.
func (s dumpSource) TableDDL(_ context.Context, database, table string) (string, error) {
	return `CREATE TABLE "` + database + `"."` + table + `" ("ID" NUMBER) TABLESPACE "USERS"`, nil
}

func (s dumpSource) StreamTableRows(_ context.Context, _, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	if !query.OwnRows {
		return nil, errors.New("the dump reads a table's own rows")
	}
	return s.rows[table], nil
}

// sliceStream is a table's rows as a cursor gives them.
type sliceStream struct {
	columns []string
	rows    [][]any
	at      int
}

func (s *sliceStream) Columns() []string { return s.columns }
func (s *sliceStream) Next() bool        { s.at++; return s.at <= len(s.rows) }
func (s *sliceStream) Row() []any        { return s.rows[s.at-1] }
func (s *sliceStream) Err() error        { return nil }
func (s *sliceStream) Close() error      { return nil }

// newDumpMock is a session over a mock whose expectations match in any order:
// the orchestration asks per object, the order is its own.
func newDumpMock(t *testing.T) (*sqldb.Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	return sqldb.NewSession(db, dialect{}), mock
}

func rowsOf(columns []string, values ...[]any) *sqlmock.Rows {
	rows := sqlmock.NewRows(columns)
	for _, v := range values {
		driverValues := make([]driver.Value, len(v))
		for i, value := range v {
			driverValues[i] = value
		}
		rows.AddRow(driverValues...)
	}
	return rows
}

var (
	sequenceColumns   = []string{"SEQUENCE_NAME", "MIN_VALUE", "MAX_VALUE", "INCREMENT_BY", "CYCLE_FLAG", "ORDER_FLAG", "CACHE_SIZE", "LAST_NUMBER"}
	commentColumns    = []string{"COLUMN_NAME", "COMMENTS"}
	foreignKeyColumns = []string{"CONSTRAINT_NAME", "COLUMN_NAME", "OWNER", "R_OWNER", "R_TABLE_NAME", "R_COLUMN_NAME", "DELETE_RULE", "DEFERRABLE", "DEFERRED", "STATUS", "VALIDATED"}
	defaultColumns    = []string{"TABLE_NAME", "OWNER", "DATA_DEFAULT"}
	dumpColumnColumns = []string{"COLUMN_NAME", "VIRTUAL_COLUMN", "GENERATION_TYPE", "DEFAULT_ON_NULL"}
)

// expectSequences answers the schema's own sequences (identity columns' are not
// in the dictionary query's answer).
func expectSequences(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(sequencesPattern).WithArgs("HR").WillReturnRows(rowsOf(sequenceColumns,
		[]any{"AUDIT_SEQ", "-1000", "-1", "-1", "Y", "Y", "0", "-5"},
		[]any{"EMP_SEQ", "1", "9999999999999999999999999999", "1", "N", "N", "20", "41"},
	))
}

// expectTableMetadata answers DBMS_METADATA for a table and its indexes on a
// connection with the dump's transforms set, and resets them after.
func expectTableMetadata(mock sqlmock.Sqlmock, table, ddl string, indexes ...string) {
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(tableMetadataPattern).WithArgs("HR", table).WillReturnRows(names("DDL", ddl))
	mock.ExpectQuery(indexMetadataPattern).WithArgs("HR", table).WillReturnRows(names("DDL", indexes...))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
}

// DBMS_METADATA's DDL under the dump's transforms: no schema on the table, no
// storage; a sequence default keeps the schema it was typed or resolved with.
const (
	deptDDL = "\n  CREATE TABLE \"DEPT\" \n   (\t\"ID\" NUMBER GENERATED ALWAYS AS IDENTITY MINVALUE 1 MAXVALUE 9999999999999999999999999999 " +
		"INCREMENT BY 1 START WITH 1 CACHE 20 NOORDER  NOCYCLE  NOKEEP  NOSCALE  NOT NULL ENABLE, \n\t\"NAME\" VARCHAR2(30), \n\t" +
		" CONSTRAINT \"DEPT_PK\" PRIMARY KEY (\"ID\")\n  USING INDEX  ENABLE\n   ) \n  "
	empDDL = "\n  CREATE TABLE \"EMP\" \n   (\t\"ID\" NUMBER DEFAULT \"HR\".\"EMP_SEQ\".\"NEXTVAL\" NOT NULL ENABLE, \n\t\"DEPT_ID\" NUMBER, \n\t" +
		"\"REGION_ID\" NUMBER, \n\t\"EMAIL\" VARCHAR2(100), \n\t\"SALARY\" NUMBER(10,2), \n\t" +
		"\"BONUS\" NUMBER GENERATED ALWAYS AS (\"SALARY\"*0.1) VIRTUAL , \n\t" +
		" CONSTRAINT \"EMP_PK\" PRIMARY KEY (\"ID\")\n  USING INDEX  ENABLE, \n\t" +
		" CONSTRAINT \"EMP_SALARY_CK\" CHECK (salary > 0) ENABLE\n   ) \n  "
	empEmailIndex = "\n  CREATE UNIQUE INDEX \"EMP_EMAIL_UK\" ON \"EMP\" (\"EMAIL\") \n  "
	empDeptIndex  = "\n  CREATE INDEX \"EMP_DEPT_IX\" ON \"EMP\" (\"DEPT_ID\") \n  "
)

// expectEmpAttachments answers EMP's sequence use, comments and foreign keys: one
// in the schema, one into another schema, deferred and not validated.
func expectEmpAttachments(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(sequenceUsersPattern).WithArgs("HR", "EMP").
		WillReturnRows(rowsOf(defaultColumns, []any{"EMP", "HR", `"HR"."EMP_SEQ"."NEXTVAL"`}))
	mock.ExpectQuery(commentsPattern).WithArgs("HR", "EMP").WillReturnRows(rowsOf(commentColumns))
	mock.ExpectQuery(foreignKeysPattern).WithArgs("HR", "EMP").WillReturnRows(rowsOf(foreignKeyColumns,
		[]any{"EMP_DEPT_FK", "DEPT_ID", "HR", "HR", "DEPT", "ID", "CASCADE", "NOT DEFERRABLE", "IMMEDIATE", "ENABLED", "VALIDATED"},
		[]any{"EMP_REGION_FK", "REGION_ID", "HR", "REF", "REGIONS", "ID", "SET NULL", "DEFERRABLE", "DEFERRED", "ENABLED", "NOT VALIDATED"},
	))
}

func dumpGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, got, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func runDump(t *testing.T, session *sqldb.Session, source dumpSource, req dbdriver.DumpRequest) []byte {
	t.Helper()
	dialect, err := session.DumpDialect()
	require.NoError(t, err)
	var out bytes.Buffer
	req.Writer = &out
	req.Header = dbdriver.DumpHeader{Version: "1.15.1", Asset: "hr-db", Time: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	_, err = dbdriver.Dump(context.Background(), req, source, dialect)
	require.NoError(t, err)
	return out.Bytes()
}

// A whole schema with its data: the drop section first (foreign keys, then the
// tables, then the sequences, each a PL/SQL block ignoring "does not exist" and
// ending in /), the schema's own sequences starting at their current value,
// schema-less tables with their comments and separate indexes, a sequence
// default without the schema, an identity column switched to BY DEFAULT for its
// rows, rows without the virtual column, the foreign keys after all data and the
// identity back to ALWAYS with its next value past the rows.
func TestDumpSchemaGolden(t *testing.T) {
	session, mock := newDumpMock(t)
	expectSequences(mock)

	expectTableMetadata(mock, "DEPT", deptDDL)
	mock.ExpectQuery(sequenceUsersPattern).WithArgs("HR", "DEPT").WillReturnRows(rowsOf(defaultColumns))
	mock.ExpectQuery(commentsPattern).WithArgs("HR", "DEPT").WillReturnRows(rowsOf(commentColumns,
		[]any{nil, "Departments"}, []any{"NAME", "Department's name"}))
	mock.ExpectQuery(foreignKeysPattern).WithArgs("HR", "DEPT").WillReturnRows(rowsOf(foreignKeyColumns))
	mock.ExpectQuery(dumpColumnsPattern).WithArgs("HR", "DEPT").WillReturnRows(rowsOf(dumpColumnColumns,
		[]any{"ID", "NO", "ALWAYS", "NO"}, []any{"NAME", "NO", nil, "NO"}))

	expectTableMetadata(mock, "EMP", empDDL, empDeptIndex, empEmailIndex)
	expectEmpAttachments(mock)
	mock.ExpectQuery(dumpColumnsPattern).WithArgs("HR", "EMP").WillReturnRows(rowsOf(dumpColumnColumns,
		[]any{"ID", "NO", nil, "NO"}, []any{"DEPT_ID", "NO", nil, "NO"}, []any{"REGION_ID", "NO", nil, "NO"},
		[]any{"EMAIL", "NO", nil, "NO"}, []any{"SALARY", "NO", nil, "NO"}, []any{"BONUS", "YES", nil, "NO"}))

	mock.ExpectQuery(rowColumnsPattern).WithArgs("HR", "DEPT").WillReturnRows(columnRows().
		AddRow("ID", "NUMBER", "NO", nil, nil, 1, 1, 1).
		AddRow("NAME", "VARCHAR2(30)", "YES", nil, "Department's name", 0, 0, 0))
	mock.ExpectQuery(rowColumnsPattern).WithArgs("HR", "EMP").WillReturnRows(columnRows().
		AddRow("ID", "NUMBER", "NO", `"HR"."EMP_SEQ"."NEXTVAL"`, nil, 0, 0, 0).
		AddRow("DEPT_ID", "NUMBER", "YES", nil, nil, 0, 0, 0).
		AddRow("REGION_ID", "NUMBER", "YES", nil, nil, 0, 0, 0).
		AddRow("EMAIL", "VARCHAR2(100)", "YES", nil, nil, 0, 0, 0).
		AddRow("SALARY", "NUMBER(10,2)", "YES", nil, nil, 0, 0, 0).
		AddRow("BONUS", "NUMBER", "YES", `"SALARY"*0.1`, nil, 0, 0, 0))

	source := dumpSource{Session: session, tables: []string{"DEPT", "EMP"}, rows: map[string]*sliceStream{
		"DEPT": {columns: []string{"ID", "NAME"}, rows: [][]any{{"1", "Sales"}, {"2", "O'Brien & Co"}}},
		"EMP": {columns: []string{"ID", "DEPT_ID", "REGION_ID", "EMAIL", "SALARY", "BONUS"},
			rows: [][]any{{"40", "2", nil, "a@example.com", "1000.5", "100.05"}}},
	}}
	got := runDump(t, session, source, dbdriver.DumpRequest{
		Database: "HR", Whole: true, WithData: true,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Drops: true},
	})
	dumpGolden(t, "dump_schema.sql", got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// One selected table, structure only and without its non-unique indexes: the
// sequence its default uses comes along (the table would not be created without
// it), the schema's other sequences do not; there are no data switches.
func TestDumpSelectedTableGolden(t *testing.T) {
	session, mock := newDumpMock(t)
	expectSequences(mock)
	// Defaults are matched by the sequence they name: AUDIT_LOG's (not dumped)
	// uses AUDIT_SEQ, a default in another schema's sequence is not this schema's.
	mock.ExpectQuery(sequenceUsersPattern).WithArgs("HR").WillReturnRows(rowsOf(defaultColumns,
		[]any{"EMP", "HR", `"HR"."EMP_SEQ"."NEXTVAL"`}, []any{"EMP", "HR", `'"HR"."X"' || "REF"."AUDIT_SEQ"."NEXTVAL"`},
		[]any{"AUDIT_LOG", "HR", `"HR"."AUDIT_SEQ"."NEXTVAL" + 1`}))

	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(tableMetadataPattern).WithArgs("HR", "EMP").WillReturnRows(names("DDL", empDDL))
	mock.ExpectQuery(indexMetadataPattern+`.*uniqueness = 'UNIQUE'`).WithArgs("HR", "EMP").WillReturnRows(names("DDL", empEmailIndex))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	expectEmpAttachments(mock)

	got := runDump(t, session, dumpSource{Session: session}, dbdriver.DumpRequest{
		Database: "HR", Tables: []string{"EMP"},
		Options: dbdriver.DumpOptions{ForeignKeys: true, Drops: true},
	})
	dumpGolden(t, "dump_selected_table.sql", got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The dump's DBMS_METADATA transforms are session state: they are reset on the
// connection also when reading the DDL fails, so a later View DDL on it is the
// database's own.
func TestDumpTableResetsTransformsAfterAFailure(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(tableMetadataPattern).WithArgs("HR", "GONE").
		WillReturnError(errors.New(`ORA-31603: object "GONE" of type TABLE not found in schema "HR"`))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))

	dialect, err := session.(*sqldb.Session).DumpDialect()
	require.NoError(t, err)
	_, err = dialect.DumpObject(context.Background(), dbdriver.DumpObject{
		Database: "HR", Kind: dbdriver.DumpTable, Name: "GONE", Definition: `CREATE TABLE "HR"."GONE" ("ID" NUMBER)`,
	})
	require.ErrorContains(t, err, "ORA-31603")
	require.NoError(t, mock.ExpectationsWereMet())
}

// A connection whose transforms could not be reset is closed rather than handed
// back to the pool, where it would give a View DDL without schema and storage.
func TestDumpTableDiscardsAConnectionItCannotReset(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(tableMetadataPattern).WithArgs("HR", "EMP").WillReturnRows(names("DDL", empDDL))
	mock.ExpectQuery(indexMetadataPattern).WithArgs("HR", "EMP").WillReturnRows(names("DDL"))
	mock.ExpectExec(resetTransformsPattern).WillReturnError(errors.New("ORA-03113: end-of-file on communication channel"))
	mock.ExpectClose()

	dialect, err := session.(*sqldb.Session).DumpDialect()
	require.NoError(t, err)
	_, err = dialect.DumpObject(context.Background(), dbdriver.DumpObject{
		Database: "HR", Kind: dbdriver.DumpTable, Name: "EMP", Definition: `CREATE TABLE "HR"."EMP" ("ID" NUMBER)`,
	})
	require.ErrorContains(t, err, "ORA-03113")
	require.NoError(t, mock.ExpectationsWereMet())
}
