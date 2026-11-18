package main

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
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
	programMetadataPattern = `GET_DDL\(DECODE`
	triggerMetadataPattern = `GET_DDL\('TRIGGER'`
	dependenciesPattern    = `FROM all_dependencies`
	mviewPattern           = `FROM all_mviews`
	schemaObjectsPattern   = `object_name IN \(`
)

// dumpSource is the session as the dump orchestration reads it, with the listing,
// the definitions the host would hand over and the rows faked; the row
// statements are the session's own (Oracle's literals over the mocked columns).
type dumpSource struct {
	*sqldb.Session
	tables  []string
	objects dbdriver.DatabaseObjects
	rows    map[string]*sliceStream
}

func (s dumpSource) ListTables(context.Context, string) ([]string, error) { return s.tables, nil }

func (s dumpSource) ListObjects(context.Context, string) (*dbdriver.DatabaseObjects, error) {
	return &s.objects, nil
}

// ObjectSource is what the host reads as a stored unit's definition: the
// session's own, with the schema the dump must not write.
func (s dumpSource) ObjectSource(_ context.Context, database, _, name, _ string) (string, error) {
	return `CREATE OR REPLACE PROCEDURE "` + database + `"."` + name + `" AS BEGIN NULL; END;` + "\n/", nil
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
// rows and right after them back to ALWAYS with its next value past them, rows
// without the virtual column and the foreign keys after all data.
func TestDumpSchemaGolden(t *testing.T) {
	dumpGolden(t, "dump_schema.sql", schemaDump(t))
}

// schemaDump is the whole schema HR with its data (TestDumpSchemaGolden).
func schemaDump(t *testing.T) []byte {
	t.Helper()
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
		"DEPT": {columns: []string{"ID", "NAME"}, rows: [][]any{{"1", "Sales"}, {"2", "O'Brien & Co"}, {"3", "AT&T"}}},
		"EMP": {columns: []string{"ID", "DEPT_ID", "REGION_ID", "EMAIL", "SALARY", "BONUS"},
			rows: [][]any{{"40", "2", nil, "a@example.com", "1000.5", "100.05"}}},
	}}
	got := runDump(t, session, source, dbdriver.DumpRequest{
		Database: "HR", Whole: true, WithData: true,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Drops: true},
	})
	require.NoError(t, mock.ExpectationsWereMet())
	return got
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

var (
	dependencyColumns = []string{"REFERENCED_TYPE", "REFERENCED_NAME"}
	triggerColumns    = []string{"DDL"}
)

// DBMS_METADATA's DDL of the programs under the dump's transforms: no schema on
// the unit's name, the rest as the user wrote it — names typed with the schema
// (a package called, a view's table, a trigger's ON clause) keep it, and the
// trigger's state follows as an ALTER.
const (
	itemsDDL      = "\n  CREATE TABLE \"ITEMS\" (\"ID\" NUMBER PRIMARY KEY, \"PRICE\" NUMBER) \n  "
	categoriesDDL = "\n  CREATE TABLE \"CATEGORIES\" (\"ID\" NUMBER GENERATED ALWAYS AS IDENTITY MINVALUE 1 START WITH 1 NOT NULL ENABLE, " +
		"\"NAME\" VARCHAR2(20), PRIMARY KEY (\"ID\") USING INDEX ENABLE) \n  "
	fTaxDDL    = "\n  CREATE OR REPLACE EDITIONABLE FUNCTION \"F_TAX\" (p NUMBER) RETURN NUMBER IS\nBEGIN\n  RETURN p * 0.1;\nEND;\n"
	pkgSpecDDL = "\n  CREATE OR REPLACE EDITIONABLE PACKAGE \"UTIL_PKG\" AS\n  PROCEDURE log(msg VARCHAR2);\nEND util_pkg;\n"
	pkgBodyDDL = "\n  CREATE OR REPLACE EDITIONABLE PACKAGE BODY \"UTIL_PKG\" AS\n  PROCEDURE log(msg VARCHAR2) IS BEGIN NULL; END;\nEND util_pkg;\n"
	pRunDDL    = "\n  CREATE OR REPLACE EDITIONABLE PROCEDURE \"P_RUN\" AS\nBEGIN\n  hr . util_pkg.log('run'); -- HR.UTIL_PKG\nEND;\n"
	vItemsDDL  = "\n  CREATE OR REPLACE FORCE EDITIONABLE VIEW \"V_ITEMS\" (\"ID\", \"PRICE\", \"TAX\") AS \n  SELECT id, price, \"HR\".f_tax(price) tax FROM HR.items\n"
	vExpDDL    = "\n  CREATE OR REPLACE FORCE EDITIONABLE VIEW \"V_EXP\" (\"ID\", \"TAX\") AS \n  SELECT hr.id, tax FROM v_items hr WHERE price > 10\n"
	trgBiDDL   = "\n  CREATE OR REPLACE EDITIONABLE TRIGGER \"TRG_BI\" BEFORE INSERT ON HR.ITEMS FOR EACH ROW\nBEGIN\n" +
		"  IF :new.id IS NULL THEN :new.id := item_seq.nextval; END IF;\n  INSERT INTO ref.audit_log VALUES ('ON HR.ITEMS');\nEND;\n" +
		"ALTER TRIGGER \"TRG_BI\" ENABLE"
	trgStmtDDL = "\n  CREATE OR REPLACE EDITIONABLE TRIGGER \"TRG_STMT\" after update on \"HR\".items\nbegin\n  null;\nend;\n" +
		"ALTER TRIGGER \"TRG_STMT\" DISABLE"
)

// expectProgram answers DBMS_METADATA for a view or stored unit (a package's spec
// and body) on a connection with the dump's transforms set and reset after, and
// the dictionary's dependencies of it.
func expectProgram(mock sqlmock.Sqlmock, name string, dependencies [][]any, ddl ...string) {
	if strings.HasPrefix(name, "V_") {
		mock.ExpectQuery(mviewPattern).WithArgs("HR", name).WillReturnRows(names("N", "0"))
	}
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(programMetadataPattern).WithArgs("HR", name).WillReturnRows(names("DDL", ddl...))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(dependenciesPattern).WithArgs("HR", name).WillReturnRows(rowsOf(dependencyColumns, dependencies...))
}

func expectTrigger(mock sqlmock.Sqlmock, name, ddl string) {
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(triggerMetadataPattern).WithArgs("HR", name).WillReturnRows(rowsOf(triggerColumns, []any{ddl}))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
}

// A whole schema with its programs: views, functions, procedures and packages
// without the schema, ordered by the dictionary's dependencies (a package before
// the procedure calling it, a function before the views using it, a view before
// the view on it), a package's spec before its body, each PL/SQL unit ending in
// /; the triggers after all data, without the schema of their table, keeping
// their state; the drop section drops them all in reverse, ignoring "does not
// exist". The views are the ones ListObjects lists (Oracle's ListTables lists
// tables only); a materialized view among them is left out.
func TestDumpProgramsGolden(t *testing.T) {
	dumpGolden(t, "programs/dump_schema.sql", programsDump(t))
}

// An identity column is back to GENERATED ALWAYS and continues past the rows
// right after its table's rows, before any view, unit or trigger exists: the
// ALTER invalidates what depends on the table, and a disabled trigger would stay
// invalid until compiled.
func TestDumpSwitchesIdentityBackBeforeThePrograms(t *testing.T) {
	file := string(programsDump(t))
	switchBack := strings.Index(file, `'ALTER TABLE "CATEGORIES" MODIFY ("ID" GENERATED ALWAYS AS IDENTITY (START WITH LIMIT VALUE))'`)
	require.Positive(t, switchBack)
	assert.Less(t, strings.Index(file, "Records of CATEGORIES"), switchBack)
	assert.Less(t, switchBack, strings.Index(file, "Table structure for ITEMS"))
	assert.Less(t, switchBack, strings.Index(file, "Function structure for F_TAX"))
}

// programsDump is the whole schema HR with its programs and data
// (TestDumpProgramsGolden): a GENERATED ALWAYS identity table CATEGORIES, a
// table ITEMS and the programs on ITEMS.
func programsDump(t *testing.T) []byte {
	t.Helper()
	session, mock := newDumpMock(t)
	mock.ExpectQuery(sequencesPattern).WithArgs("HR").WillReturnRows(rowsOf(sequenceColumns,
		[]any{"ITEM_SEQ", "1", "9999999999999999999999999999", "1", "N", "N", "20", "1"}))
	expectTableMetadata(mock, "CATEGORIES", categoriesDDL)
	mock.ExpectQuery(sequenceUsersPattern).WithArgs("HR", "CATEGORIES").WillReturnRows(rowsOf(defaultColumns))
	mock.ExpectQuery(commentsPattern).WithArgs("HR", "CATEGORIES").WillReturnRows(rowsOf(commentColumns))
	mock.ExpectQuery(foreignKeysPattern).WithArgs("HR", "CATEGORIES").WillReturnRows(rowsOf(foreignKeyColumns))
	mock.ExpectQuery(dumpColumnsPattern).WithArgs("HR", "CATEGORIES").WillReturnRows(rowsOf(dumpColumnColumns,
		[]any{"ID", "NO", "ALWAYS", "NO"}, []any{"NAME", "NO", nil, "NO"}))
	mock.ExpectQuery(rowColumnsPattern).WithArgs("HR", "CATEGORIES").WillReturnRows(columnRows().
		AddRow("ID", "NUMBER", "NO", nil, nil, 1, 1, 1).
		AddRow("NAME", "VARCHAR2(20)", "YES", nil, nil, 0, 0, 0))
	expectTableMetadata(mock, "ITEMS", itemsDDL)
	mock.ExpectQuery(sequenceUsersPattern).WithArgs("HR", "ITEMS").WillReturnRows(rowsOf(defaultColumns))
	mock.ExpectQuery(commentsPattern).WithArgs("HR", "ITEMS").WillReturnRows(rowsOf(commentColumns))
	mock.ExpectQuery(foreignKeysPattern).WithArgs("HR", "ITEMS").WillReturnRows(rowsOf(foreignKeyColumns))
	mock.ExpectQuery(dumpColumnsPattern).WithArgs("HR", "ITEMS").WillReturnRows(rowsOf(dumpColumnColumns,
		[]any{"ID", "NO", nil, "NO"}, []any{"PRICE", "NO", nil, "NO"}))
	mock.ExpectQuery(rowColumnsPattern).WithArgs("HR", "ITEMS").WillReturnRows(columnRows().
		AddRow("ID", "NUMBER", "NO", nil, nil, 0, 0, 0).
		AddRow("PRICE", "NUMBER", "YES", nil, nil, 0, 0, 0))

	expectProgram(mock, "F_TAX", nil, fTaxDDL)
	expectProgram(mock, "P_RUN", [][]any{{"PACKAGE", "UTIL_PKG"}}, pRunDDL)
	// The body depends on its own spec.
	expectProgram(mock, "UTIL_PKG", [][]any{{"PACKAGE", "UTIL_PKG"}}, pkgSpecDDL, pkgBodyDDL)
	expectProgram(mock, "V_EXP", [][]any{{"FUNCTION", "F_TAX"}, {"VIEW", "V_ITEMS"}}, vExpDDL)
	expectProgram(mock, "V_ITEMS", [][]any{{"FUNCTION", "F_TAX"}}, vItemsDDL)
	mock.ExpectQuery(mviewPattern).WithArgs("HR", "ITEMS_MV").WillReturnRows(names("N", "1"))
	// Which names typed with the schema are its objects: hr.id in V_EXP is a
	// column of the view's alias hr.
	mock.ExpectQuery(schemaObjectsPattern).WithArgs("HR", "UTIL_PKG").WillReturnRows(names("OBJECT_NAME", "UTIL_PKG"))
	mock.ExpectQuery(schemaObjectsPattern).WithArgs("HR", "F_TAX", "ITEMS").WillReturnRows(names("OBJECT_NAME", "F_TAX", "ITEMS"))
	mock.ExpectQuery(schemaObjectsPattern).WithArgs("HR", "ID").WillReturnRows(names("OBJECT_NAME"))
	mock.ExpectQuery(schemaObjectsPattern).WithArgs("HR", "ITEMS").WillReturnRows(names("OBJECT_NAME", "ITEMS"))
	mock.ExpectQuery(schemaObjectsPattern).WithArgs("HR", "ITEMS").WillReturnRows(names("OBJECT_NAME", "ITEMS"))
	expectTrigger(mock, "TRG_BI", trgBiDDL)
	expectTrigger(mock, "TRG_STMT", trgStmtDDL)

	source := dumpSource{
		Session: session, tables: []string{"CATEGORIES", "ITEMS"},
		objects: dbdriver.DatabaseObjects{
			Views: []dbdriver.ObjectMeta{
				{Name: "ITEMS_MV", Type: dbdriver.ObjectView}, {Name: "V_EXP", Type: dbdriver.ObjectView}, {Name: "V_ITEMS", Type: dbdriver.ObjectView},
			},
			Procedures: []dbdriver.ObjectMeta{
				{Name: "P_RUN", Type: dbdriver.ObjectProcedure}, {Name: "UTIL_PKG (package)", Type: dbdriver.ObjectProcedure},
			},
			Functions: []dbdriver.ObjectMeta{{Name: "F_TAX", Type: dbdriver.ObjectFunction}},
			Triggers: []dbdriver.ObjectMeta{
				{Name: "TRG_BI", Type: dbdriver.ObjectTrigger}, {Name: "TRG_STMT", Type: dbdriver.ObjectTrigger},
			},
		},
		rows: map[string]*sliceStream{
			"CATEGORIES": {columns: []string{"ID", "NAME"}, rows: [][]any{{"1", "Books"}}},
			"ITEMS":      {columns: []string{"ID", "PRICE"}, rows: [][]any{{"1", "12.5"}}},
		},
	}
	got := runDump(t, session, source, dbdriver.DumpRequest{
		Database: "HR", Whole: true, WithData: true,
		Options: dbdriver.DumpOptions{Views: true, Routines: true, Triggers: true, Drops: true},
	})
	require.NoError(t, mock.ExpectationsWereMet())
	return got
}

// A name of the schema's own objects loses the schema however it was typed
// with it; strings, comments, names after a dot, a database link, a bind, a
// table alias spelled like the schema, another schema and a mixed case schema
// not typed quoted keep theirs.
func TestWithoutOwner(t *testing.T) {
	objects := map[string]bool{"ITEMS": true, "UTIL_PKG": true, "Items": true, "SEQ": true}
	for _, tc := range []struct {
		name, owner, ddl, want string
	}{
		{"unquoted", "HR", `ON HR.ITEMS FOR EACH ROW`, `ON ITEMS FOR EACH ROW`},
		{"any case, quoted, blanks around the dot", "HR", `on "HR" . items; hr.util_pkg.log; Hr."SEQ".nextval`,
			`on items; util_pkg.log; "SEQ".nextval`},
		{"quoted mixed case", "Hr", `ON "Hr"."Items"`, `ON "Items"`},
		{"unquoted is not the mixed case schema", "Hr", `ON HR.ITEMS`, `ON HR.ITEMS`},
		{"another schema", "HR", `INSERT INTO ref.items VALUES (1)`, `INSERT INTO ref.items VALUES (1)`},
		{"strings and comments", "HR", "'HR.ITEMS' || q'[HR.ITEMS]' -- HR.ITEMS\n/* HR.ITEMS */", "'HR.ITEMS' || q'[HR.ITEMS]' -- HR.ITEMS\n/* HR.ITEMS */"},
		{"after a dot, a link or a bind", "HR", `x.hr.items; t@hr.items; :hr.items`, `x.hr.items; t@hr.items; :hr.items`},
		{"an object over a database link", "HR", `FROM hr.items@remote, HR.ITEMS @ remote`, `FROM hr.items@remote, HR.ITEMS @ remote`},
		{"an alias spelled like the schema", "HR", `SELECT hr.id FROM items hr`, `SELECT hr.id FROM items hr`},
		{"a longer name", "HR", `HR_X.ITEMS, X_HR.ITEMS, HR$.ITEMS`, `HR_X.ITEMS, X_HR.ITEMS, HR$.ITEMS`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withoutOwner(tc.ddl, tc.owner, func(names []string) (map[string]bool, error) { return objects, nil })
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Without a name typed with the schema the dictionary is not asked.
func TestWithoutOwnerAsksNothingWithoutAQualifiedName(t *testing.T) {
	got, err := withoutOwner(`SELECT id FROM items`, "HR", func([]string) (map[string]bool, error) {
		return nil, errors.New("asked")
	})
	require.NoError(t, err)
	assert.Equal(t, `SELECT id FROM items`, got)
}

// DBMS_METADATA writes a trigger's state as an ALTER after it; a trigger DDL
// without one is not taken for an enabled trigger.
func TestTriggerDDLWithoutItsStateIsAnError(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectExec(setTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(triggerMetadataPattern).WithArgs("HR", "TRG").
		WillReturnRows(rowsOf(triggerColumns, []any{`CREATE OR REPLACE TRIGGER "TRG" BEFORE INSERT ON ITEMS BEGIN NULL; END;`}))
	mock.ExpectExec(resetTransformsPattern).WillReturnResult(sqlmock.NewResult(0, 0))

	dialect, err := session.(*sqldb.Session).DumpDialect()
	require.NoError(t, err)
	_, err = dialect.DumpObject(context.Background(), dbdriver.DumpObject{
		Database: "HR", Kind: dbdriver.ObjectTrigger, Name: "TRG", Definition: `CREATE OR REPLACE TRIGGER TRG BEFORE INSERT ON ITEMS BEGIN NULL; END;`,
	})
	require.ErrorContains(t, err, "state")
	require.NoError(t, mock.ExpectationsWereMet())
}

// A dump runs back through Run SQL File as written: a value with an & is no
// SQL*Plus substitution variable in it.
func TestDumpRunsBackAsASQLFile(t *testing.T) {
	file := string(schemaDump(t))
	got, err := runScriptText(file, len(file)+1)
	require.NoError(t, err)
	assert.Contains(t, got.executed, `INSERT INTO "DEPT" ("ID", "NAME") VALUES (3, 'AT&T')`)
}
