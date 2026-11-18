package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantPrograms lets a throwaway user of createDumpUser create views,
// materialized views, stored units and triggers.
func grantPrograms(t *testing.T, admin *sql.DB, name string) {
	t.Helper()
	ctx := integrationContext(t)
	conn, err := admin.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	var container string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV', 'CON_NAME') FROM dual").Scan(&container))
	if container == "CDB$ROOT" { // see createDumpUser
		_, err := conn.ExecContext(ctx, `ALTER SESSION SET "_ORACLE_SCRIPT" = TRUE`)
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, "GRANT CREATE VIEW, CREATE MATERIALIZED VIEW, CREATE PROCEDURE, CREATE TRIGGER TO "+quoteIdent(name))
	require.NoError(t, err)
}

// programsFixture is a schema with what the programs part of a dump covers: a
// function (its name typed with the schema) used by a view, a view on that view
// (listed before it), a package with its body, a procedure calling the package
// (listed before it), a row trigger using a sequence (typed with the schema, on
// its table typed with the schema), a disabled statement trigger (lower case,
// on its table with the schema) and a materialized view, which the dump leaves
// out. The rows go in before the triggers exist: a
// trigger firing on the restored rows would mark them.
func programsFixture(owner string) []string {
	return []string{
		"CREATE SEQUENCE ITEM_SEQ START WITH 100",
		"CREATE TABLE ITEMS (ID NUMBER PRIMARY KEY, PRICE NUMBER, SOURCE VARCHAR2(10))",
		"CREATE TABLE ITEMS_LOG (ID NUMBER, EVENT VARCHAR2(10))",
		"INSERT INTO ITEMS VALUES (1, 5, 'FIXTURE')",
		"INSERT INTO ITEMS VALUES (2, 50, 'FIXTURE')",
		"CREATE OR REPLACE FUNCTION " + owner + ".F_TAX(p NUMBER) RETURN NUMBER IS\nBEGIN\n  RETURN p * 0.1;\nEND;",
		"CREATE OR REPLACE PACKAGE UTIL_PKG AS\n  PROCEDURE log(p_id NUMBER, p_event VARCHAR2);\nEND util_pkg;",
		"CREATE OR REPLACE PACKAGE BODY UTIL_PKG AS\n  PROCEDURE log(p_id NUMBER, p_event VARCHAR2) IS\n  BEGIN\n" +
			"    INSERT INTO items_log VALUES (p_id, p_event);\n  END;\nEND util_pkg;",
		"CREATE OR REPLACE PROCEDURE P_RUN AS\nBEGIN\n  util_pkg.log(0, 'run');\nEND;",
		"CREATE VIEW V_ITEMS AS SELECT id, price, f_tax(price) tax FROM items",
		"CREATE VIEW V_EXP (ID, TAX) AS SELECT id, tax FROM v_items WHERE price > 10",
		"CREATE MATERIALIZED VIEW ITEMS_MV AS SELECT id, price FROM items",
		"CREATE TRIGGER " + owner + ".TRG_BI BEFORE INSERT ON " + owner + ".ITEMS FOR EACH ROW\nBEGIN\n" +
			"  IF :new.id IS NULL THEN :new.id := item_seq.nextval; END IF;\n  :new.source := 'TRIGGER';\nEND;",
		"create trigger trg_stmt after update on " + strings.ToLower(owner) + ".items\nbegin\n  util_pkg.log(-1, 'update');\nend;",
		"ALTER TRIGGER TRG_STMT DISABLE",
	}
}

// programTypes are the object types of the programs a dump writes.
const programTypes = "'VIEW', 'FUNCTION', 'PROCEDURE', 'PACKAGE', 'PACKAGE BODY', 'TRIGGER'"

// layout is whitespace and quotes, which a unit's first line loses or gains
// with the schema.
var layout = regexp.MustCompile(`[\s"]+`)

// programShape is what a round trip must keep of owner's programs, each line
// sorted, the owner blanked: the objects and their validity, the views' text,
// every unit's source (its first line, the header DBMS_METADATA rewrites, without
// the owner, whitespace and quotes), the triggers' events and state, and their
// dependencies.
func programShape(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	o := sqlString(owner)
	ownerPrefix := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(owner) + `\.`)
	var shape []string
	for _, part := range []struct{ name, query string }{
		{"object", "SELECT object_name, object_type, status FROM all_objects WHERE owner = " + o +
			" AND object_type IN (" + programTypes + ")"},
		{"view", "SELECT view_name, text_vc FROM all_views WHERE owner = " + o},
		{"source", "SELECT name, type, line, text FROM all_source WHERE owner = " + o},
		{"trigger", "SELECT trigger_name, trigger_type, triggering_event, base_object_type, DECODE(table_owner, owner, '-', table_owner), " +
			"table_name, status, when_clause FROM all_triggers WHERE owner = " + o},
		{"dependency", "SELECT name, type, DECODE(referenced_owner, owner, '-', referenced_owner), referenced_name, referenced_type " +
			"FROM all_dependencies WHERE owner = " + o + " AND type IN (" + programTypes + ")"},
	} {
		for _, row := range textRows(t, admin, part.query) {
			if strings.Contains(row, "BIN$") { // the copies a drop leaves in the recycle bin
				continue
			}
			if fields := strings.SplitN(row, "|", 4); part.name == "source" && fields[2] == "1" {
				fields[3] = strings.ToUpper(layout.ReplaceAllString(ownerPrefix.ReplaceAllString(fields[3], ""), ""))
				row = strings.Join(fields, "|")
			}
			shape = append(shape, part.name+": "+row)
		}
	}
	slices.Sort(shape)
	return shape
}

// programRows is owner's rows of the fixture's tables.
func programRows(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	schema := quoteIdent(owner)
	return slices.Concat(
		textRows(t, admin, "SELECT TO_CHAR(ID), TO_CHAR(PRICE), SOURCE FROM "+schema+".ITEMS ORDER BY ID"),
		textRows(t, admin, "SELECT TO_CHAR(ID), EVENT FROM "+schema+".ITEMS_LOG ORDER BY ID, EVENT"))
}

// A schema's views, functions, procedures, packages and triggers dumped with its
// tables run into another user as the same objects, all valid: nothing in the
// file names the source schema, the units come in dependency order, the
// triggers after the rows (which they would have marked) with their state.
// A materialized view is not in the file. Run over the original (a procedure changed, the triggers' states swapped, a
// view dropped meanwhile) with its drop section, the file restores it.
func TestIntegrationDumpProgramsRoundTrip(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		admin := adminTarget(t, target).open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_PRG_S"+suffix, "OPSKAT_PRG_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		grantPrograms(t, admin, srcName)
		grantPrograms(t, admin, dstName)
		src, dst := sqldb.NewSession(srcDB, dialect{}), sqldb.NewSession(dstDB, dialect{})
		for _, statement := range programsFixture(srcName) {
			_, err := src.Execute(integrationContext(t), statement)
			require.NoError(t, err, statement)
		}
		programs, tables, rows := programShape(t, admin, srcName), schemaShape(t, admin, srcName), programRows(t, admin, srcName)
		require.Len(t, textRows(t, admin, "SELECT object_name FROM all_objects WHERE owner = "+sqlString(srcName)+
			" AND object_type IN ("+programTypes+") AND status = 'VALID'"), 8)

		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := src.Dump(ctx, dbdriver.DumpRequest{
			Database: srcName, Whole: true, WithData: true, Writer: &out,
			Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true, Drops: true},
		})
		require.NoError(t, err)
		file := out.String()
		for line := range strings.Lines(file) {
			if !strings.HasPrefix(line, "--") {
				assert.NotContains(t, strings.ToUpper(line), srcName)
				assert.NotContains(t, line, "ITEMS_MV")
			}
		}
		for _, want := range []string{`CREATE OR REPLACE EDITIONABLE PACKAGE "UTIL_PKG"`, `ALTER TRIGGER "TRG_STMT" DISABLE;`} {
			assert.Contains(t, file, want)
		}
		assert.Less(t, strings.Index(file, `PACKAGE "UTIL_PKG"`), strings.Index(file, `PACKAGE BODY "UTIL_PKG"`))

		// The dump's DBMS_METADATA transforms did not stay on the pool's connections.
		ddl, err := src.TableDDL(integrationContext(t), srcName, "V_ITEMS")
		require.NoError(t, err)
		assert.Contains(t, ddl, quoteIdent(srcName)+`."V_ITEMS"`)

		runOK(t, dst, dstName, file)
		assert.Equal(t, programs, programShape(t, admin, dstName))
		assert.Equal(t, withoutMaterializedView(tables), schemaShape(t, admin, dstName))
		assert.Equal(t, rows, programRows(t, admin, dstName))
		// The restored row trigger fills the id from the copy's own sequence.
		_, err = dst.Execute(integrationContext(t), "INSERT INTO ITEMS (PRICE) VALUES (7)")
		require.NoError(t, err)
		assert.Equal(t, []string{"100|TRIGGER"}, textRows(t, admin,
			"SELECT TO_CHAR(ID), SOURCE FROM "+quoteIdent(dstName)+".ITEMS WHERE PRICE = 7"))

		// Over the original, changed meanwhile, the file restores it.
		for _, change := range []string{
			"CREATE OR REPLACE PROCEDURE P_RUN AS\nBEGIN\n  util_pkg.log(1, 'changed');\nEND;",
			"ALTER TRIGGER TRG_STMT ENABLE", "ALTER TRIGGER TRG_BI DISABLE", "DROP VIEW V_EXP",
		} {
			_, err := src.Execute(integrationContext(t), change)
			require.NoError(t, err, change)
		}
		require.NotEqual(t, programs, programShape(t, admin, srcName))
		runOK(t, src, srcName, file)
		assert.Equal(t, programs, programShape(t, admin, srcName))
		assert.Equal(t, tables, schemaShape(t, admin, srcName))
		assert.Equal(t, rows, programRows(t, admin, srcName))
	})
}

// withoutMaterializedView is a schema shape without the fixture's materialized
// view (its container table's columns), which the dump leaves out.
func withoutMaterializedView(shape []string) []string {
	return slices.DeleteFunc(slices.Clone(shape), func(line string) bool { return strings.Contains(line, "ITEMS_MV") })
}
