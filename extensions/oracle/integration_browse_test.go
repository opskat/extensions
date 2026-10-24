package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browseFixture is a set of objects of every browsed kind in the login user's
// schema, named OPSKAT_BR_*; the test drops them again.
var browseFixture = []string{
	`CREATE TABLE OPSKAT_BR_DEPT (ID NUMBER(10) CONSTRAINT OPSKAT_BR_DEPT_PK PRIMARY KEY)`,
	`CREATE TABLE OPSKAT_BR_EMP (
		ID NUMBER(10) CONSTRAINT OPSKAT_BR_EMP_PK PRIMARY KEY,
		DEPT_ID NUMBER(10) CONSTRAINT OPSKAT_BR_EMP_FK REFERENCES OPSKAT_BR_DEPT (ID),
		NAME VARCHAR2(20 CHAR) DEFAULT 'anon' NOT NULL,
		PAY NUMBER(8,2),
		CODE RAW(16),
		NOTE CLOB)`,
	`CREATE INDEX OPSKAT_BR_EMP_IX ON OPSKAT_BR_EMP (NAME, DEPT_ID)`,
	`COMMENT ON COLUMN OPSKAT_BR_EMP.NAME IS 'full name'`,
	`INSERT INTO OPSKAT_BR_DEPT (ID) VALUES (1)`,
	`INSERT INTO OPSKAT_BR_EMP (ID, DEPT_ID, NAME) VALUES (1, 1, 'ann')`,
	`INSERT INTO OPSKAT_BR_EMP (ID, DEPT_ID, NAME) VALUES (2, 1, 'bob')`,
	`CREATE TABLE OPSKAT_BR_GONE (ID NUMBER)`,
	`DROP TABLE OPSKAT_BR_GONE`,
	`CREATE VIEW OPSKAT_BR_V AS SELECT ID, NAME FROM OPSKAT_BR_EMP`,
	`CREATE PROCEDURE OPSKAT_BR_P AS
BEGIN
  NULL;
END;`,
	`CREATE PACKAGE OPSKAT_BR_PKG AS
  PROCEDURE hire;
END OPSKAT_BR_PKG;`,
	`CREATE PACKAGE BODY OPSKAT_BR_PKG AS
  PROCEDURE hire IS BEGIN NULL; END;
END OPSKAT_BR_PKG;`,
	`CREATE FUNCTION OPSKAT_BR_F RETURN NUMBER AS
BEGIN
  RETURN 1;
END;`,
	`CREATE TRIGGER OPSKAT_BR_T BEFORE INSERT ON OPSKAT_BR_EMP FOR EACH ROW
BEGIN
  NULL;
END;`,
}

var browseCleanup = []string{
	`DROP MATERIALIZED VIEW OPSKAT_BR_MV`,
	`DROP VIEW OPSKAT_BR_V`,
	`DROP PROCEDURE OPSKAT_BR_P`,
	`DROP PACKAGE OPSKAT_BR_PKG`,
	`DROP FUNCTION OPSKAT_BR_F`,
	`DROP TABLE OPSKAT_BR_EMP CASCADE CONSTRAINTS PURGE`,
	`DROP TABLE OPSKAT_BR_DEPT CASCADE CONSTRAINTS PURGE`,
	`DROP TABLE OPSKAT_BR_GONE PURGE`,
	`PURGE TABLE OPSKAT_BR_GONE`,
}

// dropBrowseFixture drops what a previous or this run left; an object that is not
// there fails its DROP, which is the point.
func dropBrowseFixture(db *sql.DB) {
	for _, statement := range browseCleanup {
		_, _ = db.ExecContext(context.Background(), statement)
	}
}

func setUpBrowseFixture(t *testing.T, db *sql.DB) (withMaterializedView bool) {
	t.Helper()
	dropBrowseFixture(db)
	t.Cleanup(func() { dropBrowseFixture(db) })
	for _, statement := range browseFixture {
		_, err := db.ExecContext(integrationContext(t), statement)
		require.NoError(t, err, statement)
	}
	_, err := db.ExecContext(integrationContext(t), `CREATE MATERIALIZED VIEW OPSKAT_BR_MV AS SELECT ID FROM OPSKAT_BR_DEPT`)
	if err != nil {
		require.Contains(t, err.Error(), "ORA-01031", "only a missing privilege may stop the materialized view")
		t.Log("no CREATE MATERIALIZED VIEW privilege: materialized views are not checked")
		return false
	}
	return true
}

func objectNames(objects []dbdriver.ObjectMeta) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.Name)
	}
	return out
}

func TestIntegrationBrowse(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		withMV := setUpBrowseFixture(t, db)
		session := sqldb.NewSession(db, dialect{})
		var user string
		require.NoError(t, db.QueryRowContext(integrationContext(t), `SELECT USER FROM DUAL`).Scan(&user))

		schemas, err := session.ListDatabases(integrationContext(t))
		require.NoError(t, err)
		require.NotEmpty(t, schemas)
		assert.Equal(t, user, schemas[0], "the login user's schema comes first")
		if user != "SYS" {
			assert.NotContains(t, schemas, "SYS", "Oracle's own schemas are not listed")
		}

		// The login user's schema, named and by default.
		for _, schema := range []string{user, ""} {
			tables, err := session.ListTables(integrationContext(t), schema)
			require.NoError(t, err)
			assert.Contains(t, tables, "OPSKAT_BR_EMP")
			assert.Contains(t, tables, "OPSKAT_BR_DEPT")
			assert.NotContains(t, tables, "OPSKAT_BR_MV", "a materialized view is a view")
			for _, table := range tables {
				assert.False(t, strings.HasPrefix(table, "BIN$"), "recycle bin table %s", table)
			}
		}

		meta, err := session.TableMetadata(integrationContext(t), user, "OPSKAT_BR_EMP")
		require.NoError(t, err)
		assert.Equal(t, []dbdriver.ColumnMeta{
			{Name: "ID", Type: "NUMBER(10)", PrimaryKey: true},
			{Name: "DEPT_ID", Type: "NUMBER(10)", Nullable: true},
			{Name: "NAME", Type: "VARCHAR2(20 CHAR)", Comment: "full name", Default: new("'anon'")},
			{Name: "PAY", Type: "NUMBER(8,2)", Nullable: true},
			{Name: "CODE", Type: "RAW(16)", Nullable: true},
			{Name: "NOTE", Type: "CLOB", Nullable: true},
		}, meta.Columns)
		assert.Contains(t, meta.Indexes, dbdriver.IndexMeta{Name: "OPSKAT_BR_EMP_IX", Columns: []string{"NAME", "DEPT_ID"}})
		assert.Contains(t, meta.Indexes, dbdriver.IndexMeta{Name: "OPSKAT_BR_EMP_PK", Columns: []string{"ID"}, Unique: true})
		assert.Equal(t, []dbdriver.ForeignKeyMeta{
			{Name: "OPSKAT_BR_EMP_FK", Column: "DEPT_ID", ReferencedTable: "OPSKAT_BR_DEPT", ReferencedColumn: "ID"},
		}, meta.ForeignKeys)

		objects, err := session.ListObjects(integrationContext(t), user)
		require.NoError(t, err)
		assert.Contains(t, objectNames(objects.Views), "OPSKAT_BR_V")
		if withMV {
			assert.Contains(t, objectNames(objects.Views), "OPSKAT_BR_MV")
		}
		assert.Contains(t, objectNames(objects.Procedures), "OPSKAT_BR_P")
		assert.Contains(t, objectNames(objects.Procedures), "OPSKAT_BR_PKG (package)")
		assert.Contains(t, objectNames(objects.Functions), "OPSKAT_BR_F")
		assert.Contains(t, objectNames(objects.Triggers), "OPSKAT_BR_T")

		source := func(objType, name string) string {
			t.Helper()
			src, err := session.ObjectSource(integrationContext(t), user, objType, name, "")
			require.NoError(t, err, "%s %s", objType, name)
			return src
		}
		assert.Equal(t, "CREATE OR REPLACE PROCEDURE OPSKAT_BR_P AS\nBEGIN\n  NULL;\nEND;\n/", source(dbdriver.ObjectProcedure, "OPSKAT_BR_P"))
		assert.Equal(t, "CREATE OR REPLACE PACKAGE OPSKAT_BR_PKG AS\n  PROCEDURE hire;\nEND OPSKAT_BR_PKG;\n/\n\n"+
			"CREATE OR REPLACE PACKAGE BODY OPSKAT_BR_PKG AS\n  PROCEDURE hire IS BEGIN NULL; END;\nEND OPSKAT_BR_PKG;\n/",
			source(dbdriver.ObjectProcedure, "OPSKAT_BR_PKG (package)"))
		// The desktop passes the listed name back; the blank may be gone by then.
		assert.Equal(t, source(dbdriver.ObjectProcedure, "OPSKAT_BR_PKG (package)"), source(dbdriver.ObjectProcedure, "OPSKAT_BR_PKG(package)"))
		assert.Equal(t, source(dbdriver.ObjectProcedure, "OPSKAT_BR_PKG (package)"), source(dbdriver.ObjectProcedure, "OPSKAT_BR_PKG"))
		assert.Equal(t, "CREATE OR REPLACE FUNCTION OPSKAT_BR_F RETURN NUMBER AS\nBEGIN\n  RETURN 1;\nEND;\n/", source(dbdriver.ObjectFunction, "OPSKAT_BR_F"))
		assert.Equal(t, "CREATE OR REPLACE TRIGGER OPSKAT_BR_T BEFORE INSERT ON OPSKAT_BR_EMP FOR EACH ROW\nBEGIN\n  NULL;\nEND;\n/", source(dbdriver.ObjectTrigger, "OPSKAT_BR_T"))
		view := source(dbdriver.ObjectView, "OPSKAT_BR_V")
		assert.Contains(t, view, `VIEW "`+user+`"."OPSKAT_BR_V"`)
		assert.Contains(t, view, "OPSKAT_BR_EMP")
		if withMV {
			assert.Equal(t, `CREATE MATERIALIZED VIEW "`+user+`"."OPSKAT_BR_MV" AS`+"\n"+`SELECT ID FROM OPSKAT_BR_DEPT`,
				source(dbdriver.ObjectView, "OPSKAT_BR_MV"))
		}
		_, err = session.ObjectSource(integrationContext(t), user, dbdriver.ObjectProcedure, "OPSKAT_BR_NONE", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-31603")

		ddl, err := session.TableDDL(integrationContext(t), user, "OPSKAT_BR_EMP")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(ddl, `CREATE TABLE "`+user+`"."OPSKAT_BR_EMP"`), ddl)
		ddl, err = session.TableDDL(integrationContext(t), user, "OPSKAT_BR_V")
		require.NoError(t, err)
		assert.Contains(t, ddl, `VIEW "`+user+`"."OPSKAT_BR_V"`)
		if withMV {
			ddl, err = session.TableDDL(integrationContext(t), user, "OPSKAT_BR_MV")
			require.NoError(t, err)
			assert.Equal(t, `CREATE MATERIALIZED VIEW "`+user+`"."OPSKAT_BR_MV" AS`+"\n"+`SELECT ID FROM OPSKAT_BR_DEPT`, ddl)
		}
		// A table that is not there is Oracle's ORA-31603, not an empty DDL.
		_, err = session.TableDDL(integrationContext(t), user, "OPSKAT_BR_NONE")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-31603")
	})
}

// Another schema's object the user cannot see is not listed; asking for its
// source reports Oracle's error.
func TestIntegrationBrowseWithoutPrivilege(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		session := sqldb.NewSession(db, dialect{})
		var canSeeAll string
		require.NoError(t, db.QueryRowContext(integrationContext(t),
			`SELECT DECODE(COUNT(*), 0, 'N', 'Y') FROM session_roles WHERE role IN ('SELECT_CATALOG_ROLE', 'DBA')`).Scan(&canSeeAll))
		if canSeeAll == "Y" {
			t.Skip("the user reads every schema's metadata")
		}
		tables, err := session.ListTables(integrationContext(t), "SYS")
		require.NoError(t, err)
		assert.NotContains(t, tables, "USER$", "a table the user may not read")
		// SYS.ALL_USERS is granted to PUBLIC, so it is listed, but its DDL is
		// another schema's metadata.
		objects, err := session.ListObjects(integrationContext(t), "SYS")
		require.NoError(t, err)
		assert.Contains(t, objectNames(objects.Views), "ALL_USERS")
		_, err = session.ObjectSource(integrationContext(t), "SYS", dbdriver.ObjectView, "ALL_USERS", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-31603")
		// SYS.OBJ$ and SYS.DBA_USERS are not visible to the user at all.
		ddl, err := session.TableDDL(integrationContext(t), "SYS", "OBJ$")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-")
		assert.Empty(t, ddl)
		_, err = session.ObjectSource(integrationContext(t), "SYS", dbdriver.ObjectView, "DBA_USERS", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-")
		// SYS.DBMS_SYS_SQL is not granted to PUBLIC: its source is out of reach.
		_, err = session.ObjectSource(integrationContext(t), "SYS", dbdriver.ObjectProcedure, "DBMS_SYS_SQL (package)", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-31603")
	})
}

func TestIntegrationTableData(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		setUpBrowseFixture(t, db)
		session := sqldb.NewSession(db, dialect{})
		var user string
		require.NoError(t, db.QueryRowContext(integrationContext(t), `SELECT USER FROM DUAL`).Scan(&user))

		starter, err := session.StarterQuery(integrationContext(t), user, "OPSKAT_BR_EMP", 5)
		require.NoError(t, err)
		assert.Equal(t, `SELECT * FROM "`+user+`"."OPSKAT_BR_EMP" FETCH FIRST 5 ROWS ONLY`, starter)

		opened, err := session.OpenTable(integrationContext(t), user, "OPSKAT_BR_EMP", 1)
		require.NoError(t, err)
		assert.Equal(t, []string{"ID"}, opened.PrimaryKeys)
		assert.Equal(t, int64(2), opened.TotalCount)
		assert.Len(t, opened.FirstPage.Rows, 1)
		assert.Equal(t, "NUMBER(10)", opened.ColumnTypes["ID"])

		page, err := session.TableRows(integrationContext(t), user, "OPSKAT_BR_EMP", dbdriver.TableQuery{
			Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "DEPT_ID", Operator: dbdriver.OpEqual, Value: "1"}},
			Sorts:   []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortDescending}},
		}, dbdriver.Page{Index: 1, Size: 1})
		require.NoError(t, err)
		require.Len(t, page.Rows, 1)
		assert.Equal(t, "ann", page.Rows[0][2], "the second page of ID descending is ID 1")

		count, err := session.CountTableRows(integrationContext(t), user, "OPSKAT_BR_EMP",
			[]dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "NAME", Operator: dbdriver.OpBeginsWith, Value: "b"}})
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})
}

// An empty string is NULL in Oracle, so the "is empty" / "is not empty" filters
// are IS NULL / IS NOT NULL (a comparison with ” would match nothing).
func TestIntegrationEmptyStringFilters(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_BR_EMPTY", "ID NUMBER(10), NOTE VARCHAR2(20)")
		ctx := integrationContext(t)
		for _, values := range []string{"(1, 'x')", "(2, '')", "(3, NULL)"} {
			_, err := s.Execute(ctx, "INSERT INTO "+table+" (ID, NOTE) VALUES "+values)
			require.NoError(t, err)
		}
		count := func(operator dbdriver.FilterOperator) int64 {
			n, err := s.CountTableRows(ctx, schema, table,
				[]dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "NOTE", Operator: operator}})
			require.NoError(t, err)
			return n
		}
		assert.Equal(t, int64(1), count(dbdriver.OpIsNotEmpty))
		assert.Equal(t, int64(2), count(dbdriver.OpIsEmpty))
	})
}
