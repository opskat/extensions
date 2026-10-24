package main

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func names(column string, values ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{column})
	for _, v := range values {
		rows.AddRow(v)
	}
	return rows
}

// Schemas are the users owning a visible table, view or code object, without
// Oracle's own; the login user's comes first even when it owns nothing yet.
func TestListDatabasesListsSchemas(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_users WHERE username = USER OR \(oracle_maintained = 'N' AND username IN ` +
		`\(SELECT owner FROM all_objects WHERE object_type IN \('TABLE', 'VIEW', 'MATERIALIZED VIEW', 'PROCEDURE', 'FUNCTION', 'PACKAGE', 'TRIGGER'\) ` +
		`AND object_name NOT LIKE 'BIN\$%'\)\) ORDER BY DECODE\(username, USER, 0, 1\), username`).
		WillReturnRows(names("USERNAME", "OPSKAT", "APP", "HR"))

	schemas, err := session.ListDatabases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"OPSKAT", "APP", "HR"}, schemas)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Tables of the schema, partitioned ones included (ALL_TABLES lists them), not the
// recycle bin's and not a materialized view's container table (it is a view).
func TestListTablesOfTheSchema(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tables t WHERE t\.owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND t\.dropped = 'NO' ` +
		`AND NOT EXISTS \(SELECT 1 FROM all_mviews m WHERE m\.owner = t\.owner AND m\.mview_name = t\.table_name\) ORDER BY t\.table_name`).
		WithArgs("HR").
		WillReturnRows(names("TABLE_NAME", "DEPT", "EMP", "Mixed"))

	tables, err := session.ListTables(context.Background(), "HR")
	require.NoError(t, err)
	assert.Equal(t, []string{"DEPT", "EMP", "Mixed"}, tables)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Without a schema the session's current one is browsed: ” binds as NULL.
func TestListTablesWithoutSchema(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tables t WHERE t\.owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\)`).
		WithArgs("").WillReturnRows(names("TABLE_NAME"))

	tables, err := session.ListTables(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, tables)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTableMetadata(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, "employee id", "0", "0", "0").
			AddRow("DEPT_ID", "NUMBER(10)", "YES", nil, nil, "0", "0", "0").
			AddRow("NAME", "VARCHAR2(20 CHAR)", "NO", "'anon' ", nil, "0", "0", "0"))
	mock.ExpectQuery(`FROM all_constraints c JOIN all_cons_columns cc .* c\.constraint_type = 'P'`).WithArgs("HR", "EMP").
		WillReturnRows(names("COLUMN_NAME", "ID"))
	mock.ExpectQuery(`FROM all_indexes i JOIN all_ind_columns ic ON ic\.index_owner = i\.owner AND ic\.index_name = i\.index_name `+
		`WHERE i\.table_owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND i\.table_name = :2 ORDER BY i\.index_name, ic\.column_position`).
		WithArgs("HR", "EMP").
		WillReturnRows(sqlmock.NewRows([]string{"INDEX_NAME", "UNIQUENESS", "COLUMN_NAME"}).
			AddRow("EMP_NAME_IX", "NONUNIQUE", "NAME").
			AddRow("EMP_NAME_IX", "NONUNIQUE", "DEPT_ID").
			AddRow("EMP_PK", "UNIQUE", "ID"))
	// A foreign key into another schema names that schema, one into the same
	// schema the bare table.
	mock.ExpectQuery(`SELECT c\.constraint_name, cc\.column_name, DECODE\(rc\.owner, c\.owner, rc\.table_name, rc\.owner \|\| '\.' \|\| rc\.table_name\), rcc\.column_name `+
		`FROM all_constraints c `+
		`JOIN all_cons_columns cc ON cc\.owner = c\.owner AND cc\.constraint_name = c\.constraint_name `+
		`JOIN all_constraints rc ON rc\.owner = c\.r_owner AND rc\.constraint_name = c\.r_constraint_name `+
		`JOIN all_cons_columns rcc ON rcc\.owner = rc\.owner AND rcc\.constraint_name = rc\.constraint_name AND rcc\.position = cc\.position `+
		`WHERE c\.constraint_type = 'R' AND c\.owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND c\.table_name = :2 `+
		`ORDER BY c\.constraint_name, cc\.position`).
		WithArgs("HR", "EMP").
		WillReturnRows(sqlmock.NewRows([]string{"CONSTRAINT_NAME", "COLUMN_NAME", "R_TABLE", "R_COLUMN"}).
			AddRow("EMP_DEPT_FK", "DEPT_ID", "DEPT", "ID").
			AddRow("EMP_ORG_FK", "ORG_ID", "APP.ORG", "ID"))

	meta, err := session.TableMetadata(context.Background(), "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ColumnMeta{
		{Name: "ID", Type: "NUMBER(10)", PrimaryKey: true, Comment: "employee id"},
		{Name: "DEPT_ID", Type: "NUMBER(10)", Nullable: true},
		{Name: "NAME", Type: "VARCHAR2(20 CHAR)", Default: new("'anon'")},
	}, meta.Columns)
	assert.Equal(t, []dbdriver.IndexMeta{
		{Name: "EMP_NAME_IX", Columns: []string{"NAME", "DEPT_ID"}},
		{Name: "EMP_PK", Columns: []string{"ID"}, Unique: true},
	}, meta.Indexes)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{
		{Name: "EMP_DEPT_FK", Column: "DEPT_ID", ReferencedTable: "DEPT", ReferencedColumn: "ID"},
		{Name: "EMP_ORG_FK", Column: "ORG_ID", ReferencedTable: "APP.ORG", ReferencedColumn: "ID"},
	}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListObjects(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`SELECT object_name FROM all_objects WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) ` +
		`AND object_type IN \('VIEW', 'MATERIALIZED VIEW'\) ORDER BY object_name`).
		WithArgs("HR").WillReturnRows(names("OBJECT_NAME", "EMP_MV", "EMP_V"))
	mock.ExpectQuery(`SELECT object_name, object_type FROM all_objects WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) ` +
		`AND object_type IN \('PROCEDURE', 'PACKAGE'\) ORDER BY object_name, object_type`).
		WithArgs("HR").
		WillReturnRows(sqlmock.NewRows([]string{"OBJECT_NAME", "OBJECT_TYPE"}).
			AddRow("ADD_EMP", "PROCEDURE").AddRow("EMP_PKG", "PACKAGE"))
	mock.ExpectQuery(`SELECT object_name FROM all_objects WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) ` +
		`AND object_type = 'FUNCTION' ORDER BY object_name`).
		WithArgs("HR").WillReturnRows(names("OBJECT_NAME", "EMP_COUNT"))
	mock.ExpectQuery(`SELECT trigger_name FROM all_triggers WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) ` +
		`AND base_object_type = 'TABLE' AND trigger_name NOT LIKE 'BIN\$%' ORDER BY trigger_name`).
		WithArgs("HR").WillReturnRows(names("TRIGGER_NAME", "EMP_BI"))

	objects, err := session.ListObjects(context.Background(), "HR")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.DatabaseObjects{
		Views: []dbdriver.ObjectMeta{{Name: "EMP_MV", Type: dbdriver.ObjectView}, {Name: "EMP_V", Type: dbdriver.ObjectView}},
		Procedures: []dbdriver.ObjectMeta{
			{Name: "ADD_EMP", Type: dbdriver.ObjectProcedure},
			{Name: "EMP_PKG (package)", Type: dbdriver.ObjectProcedure},
		},
		Functions: []dbdriver.ObjectMeta{{Name: "EMP_COUNT", Type: dbdriver.ObjectFunction}},
		Triggers:  []dbdriver.ObjectMeta{{Name: "EMP_BI", Type: dbdriver.ObjectTrigger}},
	}, objects)
	require.NoError(t, mock.ExpectationsWereMet())
}

// sourceRows is ALL_SOURCE's (TYPE, TEXT): one row per line, newline included.
func sourceRows(lines ...[2]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"TYPE", "TEXT"})
	for _, l := range lines {
		rows.AddRow(l[0], l[1])
	}
	return rows
}

const sourceQueryPrefix = `SELECT type, text FROM all_source WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND name = :2 `

// A stored unit's source reads back as the statement that creates it, ended by
// the "/" line that ends a PL/SQL unit in a script.
func TestObjectSourceOfAProcedure(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('PROCEDURE', 'PACKAGE', 'PACKAGE BODY'\) ORDER BY type, line`).
		WithArgs("HR", "ADD_EMP").
		WillReturnRows(sourceRows(
			[2]string{"PROCEDURE", "PROCEDURE add_emp(p_name VARCHAR2) AS\n"},
			[2]string{"PROCEDURE", "BEGIN\n"},
			[2]string{"PROCEDURE", "  INSERT INTO emp(name) VALUES (p_name);\n"},
			[2]string{"PROCEDURE", "END;"},
		))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectProcedure, "ADD_EMP", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE PROCEDURE add_emp(p_name VARCHAR2) AS\nBEGIN\n  INSERT INTO emp(name) VALUES (p_name);\nEND;\n/", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceOfAPackageHasSpecAndBody(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('PROCEDURE', 'PACKAGE', 'PACKAGE BODY'\) ORDER BY type, line`).
		WithArgs("HR", "EMP_PKG").
		WillReturnRows(sourceRows(
			[2]string{"PACKAGE", "PACKAGE emp_pkg AS\n"},
			[2]string{"PACKAGE", "  PROCEDURE hire;\n"},
			[2]string{"PACKAGE", "END emp_pkg;\n"},
			[2]string{"PACKAGE BODY", "PACKAGE BODY emp_pkg AS\n"},
			[2]string{"PACKAGE BODY", "  PROCEDURE hire IS BEGIN NULL; END;\n"},
			[2]string{"PACKAGE BODY", "END emp_pkg;"},
		))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectProcedure, "EMP_PKG (package)", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE PACKAGE emp_pkg AS\n  PROCEDURE hire;\nEND emp_pkg;\n/\n\n"+
		"CREATE OR REPLACE PACKAGE BODY emp_pkg AS\n  PROCEDURE hire IS BEGIN NULL; END;\nEND emp_pkg;\n/", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceOfAPackageWithoutBody(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('PROCEDURE', 'PACKAGE', 'PACKAGE BODY'\)`).
		WithArgs("HR", "CONSTS").
		WillReturnRows(sourceRows([2]string{"PACKAGE", "PACKAGE consts AS\n  k CONSTANT NUMBER := 1;\nEND;\n"}))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectProcedure, "CONSTS (package)", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE PACKAGE consts AS\n  k CONSTANT NUMBER := 1;\nEND;\n/", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceOfAFunctionAndATrigger(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('FUNCTION'\)`).WithArgs("HR", "EMP_COUNT").
		WillReturnRows(sourceRows([2]string{"FUNCTION", "FUNCTION emp_count RETURN NUMBER AS BEGIN RETURN 0; END;"}))
	mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('TRIGGER'\)`).WithArgs("HR", "EMP_BI").
		WillReturnRows(sourceRows([2]string{"TRIGGER", "TRIGGER emp_bi BEFORE INSERT ON emp FOR EACH ROW\nBEGIN NULL; END;\n"}))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectFunction, "EMP_COUNT", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE FUNCTION emp_count RETURN NUMBER AS BEGIN RETURN 0; END;\n/", src)

	src, err = session.ObjectSource(context.Background(), "HR", dbdriver.ObjectTrigger, "EMP_BI", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE TRIGGER emp_bi BEFORE INSERT ON emp FOR EACH ROW\nBEGIN NULL; END;\n/", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ALL_SOURCE has no row for a unit that is gone or out of the user's reach;
// DBMS_METADATA is then asked for it by name, so the error is Oracle's own
// (ORA-31603), for a package by the package's type.
func TestObjectSourceOfAMissingOrInvisibleUnitIsOraclesError(t *testing.T) {
	for _, c := range []struct{ objType, name, unit, metadataType string }{
		{dbdriver.ObjectProcedure, "GONE", "GONE", "PROCEDURE"},
		{dbdriver.ObjectProcedure, "DBMS_SYS_SQL (package)", "DBMS_SYS_SQL", "PACKAGE"},
		{dbdriver.ObjectFunction, "GONE", "GONE", "FUNCTION"},
		{dbdriver.ObjectTrigger, "GONE", "GONE", "TRIGGER"},
	} {
		session, mock := newMockSession(t)
		mock.ExpectQuery(sourceQueryPrefix).WithArgs("SYS", c.unit).WillReturnRows(sourceRows())
		mock.ExpectQuery(`SELECT DBMS_METADATA\.GET_DDL\('`+c.metadataType+`', n, o\) AS DDL FROM \(SELECT NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AS o, :2 AS n FROM dual\)`).
			WithArgs("SYS", c.unit).
			WillReturnError(errors.New(`ORA-31603: object "` + c.unit + `" of type ` + c.metadataType + ` not found in schema "SYS"`))

		_, err := session.ObjectSource(context.Background(), "SYS", c.objType, c.name, "")
		require.Error(t, err, c.name)
		assert.Contains(t, err.Error(), "ORA-31603", c.name)
		require.NoError(t, mock.ExpectationsWereMet(), c.name)
	}
}

// A view's definition is the database's own DDL for it.
func TestObjectSourceOfAViewIsItsDDL(t *testing.T) {
	session, mock := newMockSession(t)
	noMaterializedView(mock, "HR", "EMP_V")
	mock.ExpectQuery(`SELECT DBMS_METADATA\.GET_DDL\(.* AND object_type IN \('VIEW'\) `).
		WithArgs("HR", "EMP_V").
		WillReturnRows(names("DDL", "\n  CREATE OR REPLACE FORCE EDITIONABLE VIEW \"HR\".\"EMP_V\" (\"ID\") AS \n  SELECT id FROM emp\n"))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectView, "EMP_V", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE OR REPLACE FORCE EDITIONABLE VIEW \"HR\".\"EMP_V\" (\"ID\") AS \n  SELECT id FROM emp", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceOfAMaterializedViewComesFromAllMviews(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(mviewQuery).WithArgs("HR", "EMP_MV").
		WillReturnRows(sqlmock.NewRows([]string{"OWNER", "MVIEW_NAME", "QUERY"}).AddRow("HR", "EMP_MV", "SELECT ID FROM EMP"))

	src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectView, "EMP_MV", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE MATERIALIZED VIEW \"HR\".\"EMP_MV\" AS\nSELECT ID FROM EMP", src)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A view the user cannot see is not in ALL_OBJECTS; Oracle's error for it comes
// from DBMS_METADATA rather than a message of the driver's own.
func TestObjectSourceOfAnInvisibleViewIsOraclesError(t *testing.T) {
	session, mock := newMockSession(t)
	noMaterializedView(mock, "SYS", "DBA_USERS")
	mock.ExpectQuery(`AND object_type IN \('VIEW'\)`).WithArgs("SYS", "DBA_USERS").WillReturnRows(names("DDL"))
	mock.ExpectQuery(`GET_DDL\('VIEW', n, o\)`).WithArgs("SYS", "DBA_USERS").
		WillReturnError(errors.New(`ORA-31603: object "DBA_USERS" of type VIEW not found in schema "SYS"`))

	_, err := session.ObjectSource(context.Background(), "SYS", dbdriver.ObjectView, "DBA_USERS", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ORA-31603")
	require.NoError(t, mock.ExpectationsWereMet())
}

// The desktop asks for a package by the name ListObjects gave it, which reaches the
// driver without the blank before "(package)" as well; a bare name finds it too.
func TestObjectSourceOfAPackageByAnyListedSpelling(t *testing.T) {
	for _, name := range []string{"EMP_PKG (package)", "EMP_PKG(package)", "EMP_PKG"} {
		session, mock := newMockSession(t)
		mock.ExpectQuery(sourceQueryPrefix+`AND type IN \('PROCEDURE', 'PACKAGE', 'PACKAGE BODY'\)`).
			WithArgs("HR", "EMP_PKG").
			WillReturnRows(sourceRows([2]string{"PACKAGE", "PACKAGE emp_pkg AS\nEND;"}))

		src, err := session.ObjectSource(context.Background(), "HR", dbdriver.ObjectProcedure, name, "")
		require.NoError(t, err, name)
		assert.Equal(t, "CREATE OR REPLACE PACKAGE emp_pkg AS\nEND;\n/", src, name)
		require.NoError(t, mock.ExpectationsWereMet(), name)
	}
}
