package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
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

// The dump round trip creates its own users, so it also needs an administrator
// of the target's database: ORACLE_TEST_19C_ADMIN and so on, in the form of
// the target's variable (oracle://system:PASSWORD@HOST:PORT/SERVICE). Without it
// the subtest skips.
func adminTarget(t *testing.T, target oracleTarget) oracleTarget {
	t.Helper()
	for _, tc := range integrationTargets {
		if tc.name != target.name {
			continue
		}
		raw := os.Getenv(tc.env + "_ADMIN")
		if raw == "" {
			t.Skipf("%s_ADMIN is not set", tc.env)
		}
		return parseTarget(t, tc.name, tc.env+"_ADMIN", raw)
	}
	t.Fatalf("no integration target %q", target.name)
	return oracleTarget{}
}

// dumpUserPassword is the password of the throwaway users.
const dumpUserPassword = "OpskatDump_1"

// createDumpUser creates a throwaway user named OPSKAT_DUMP_<tag><suffix> with
// what a schema owner has, dropped with its objects when the test ends, and
// returns it logged in.
func createDumpUser(t *testing.T, admin *sql.DB, target oracleTarget, name string) *sql.DB {
	t.Helper()
	ctx := integrationContext(t)
	var container, tablespace string
	require.NoError(t, admin.QueryRowContext(ctx,
		"SELECT SYS_CONTEXT('USERENV', 'CON_NAME'), (SELECT property_value FROM database_properties "+
			"WHERE property_name = 'DEFAULT_PERMANENT_TABLESPACE') FROM dual").Scan(&container, &tablespace))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP USER "+quoteIdent(name)+" CASCADE")
		if err != nil && !strings.Contains(err.Error(), "ORA-01918") { // never created
			assert.NoError(t, err, "drop user %s", name)
		}
	})
	statements := []string{
		"CREATE USER " + quoteIdent(name) + " IDENTIFIED BY " + quoteIdent(dumpUserPassword) +
			" DEFAULT TABLESPACE " + quoteIdent(tablespace) + " QUOTA UNLIMITED ON " + quoteIdent(tablespace),
		"GRANT CREATE SESSION, CREATE TABLE, CREATE SEQUENCE TO " + quoteIdent(name),
	}
	if container == "CDB$ROOT" {
		// The 19c container is a CDB without a PDB the test reaches: a local user
		// in the root takes Oracle's own script mode (as the test's login user was made).
		statements = slices.Insert(statements, 0, `ALTER SESSION SET "_ORACLE_SCRIPT" = TRUE`)
	}
	conn, err := admin.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	for _, statement := range statements {
		_, err := conn.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}
	user := target
	user.username, user.password = name, dumpUserPassword
	return user.open(t, "")
}

// dumpFixture is a schema with what a table dump covers: user sequences (one
// descending, ordered, uncached and cycling), an identity column GENERATED
// ALWAYS with its own options, a column defaulting to a sequence, a virtual
// column, primary key, unique and check constraints, a unique function-based and
// a descending index, a foreign key into the schema with ON DELETE CASCADE and a
// deferred self-reference, comments (one in Chinese, on GBK 19c too) and rows.
var dumpFixture = []string{
	"CREATE SEQUENCE EMP_SEQ START WITH 100 INCREMENT BY 5 MAXVALUE 100000 CACHE 7 CYCLE",
	"CREATE SEQUENCE AUDIT_SEQ START WITH -1 INCREMENT BY -1 MINVALUE -1000 MAXVALUE -1 NOCACHE ORDER",
	"SELECT EMP_SEQ.NEXTVAL FROM dual",
	"CREATE TABLE DEPT (ID NUMBER GENERATED ALWAYS AS IDENTITY (START WITH 10 INCREMENT BY 2 CACHE 5) PRIMARY KEY, " +
		"NAME VARCHAR2(30 CHAR) NOT NULL, CODE CHAR(4) CONSTRAINT DEPT_CODE_UK UNIQUE)",
	"COMMENT ON TABLE DEPT IS '部门'",
	"COMMENT ON COLUMN DEPT.NAME IS 'Department''s name'",
	"CREATE TABLE EMP (ID NUMBER DEFAULT EMP_SEQ.NEXTVAL CONSTRAINT EMP_PK PRIMARY KEY, " +
		"DEPT_ID NUMBER CONSTRAINT EMP_DEPT_FK REFERENCES DEPT ON DELETE CASCADE, EMAIL VARCHAR2(100), " +
		"SALARY NUMBER(10,2) CHECK (SALARY > 0), BONUS NUMBER GENERATED ALWAYS AS (SALARY * 0.1) VIRTUAL, MGR_ID NUMBER, " +
		"CONSTRAINT EMP_MGR_FK FOREIGN KEY (MGR_ID) REFERENCES EMP (ID) DEFERRABLE INITIALLY DEFERRED)",
	"CREATE UNIQUE INDEX EMP_EMAIL_UK ON EMP (LOWER(EMAIL))",
	"CREATE INDEX EMP_DEPT_IX ON EMP (DEPT_ID, SALARY DESC)",
	"INSERT INTO DEPT (NAME, CODE) VALUES ('Sales', 'S01')",
	"INSERT INTO DEPT (NAME, CODE) VALUES ('O''Brien & Co', NULL)",
	"INSERT INTO EMP (DEPT_ID, EMAIL, SALARY, MGR_ID) VALUES (10, 'a@example.com', 1000.5, NULL)",
	"INSERT INTO EMP (DEPT_ID, EMAIL, SALARY, MGR_ID) VALUES (12, 'b@example.com', 20, 105)",
}

// textRows is every row of query, each as its values joined by "|" ("" for NULL).
func textRows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(integrationContext(t), query)
	require.NoError(t, err, query)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var out []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(values))
		for i := range values {
			targets[i] = &values[i]
		}
		require.NoError(t, rows.Scan(targets...), query)
		texts := make([]string, len(values))
		for i, v := range values {
			texts[i] = strings.TrimSpace(v.String)
		}
		out = append(out, strings.Join(texts, "|"))
	}
	require.NoError(t, rows.Err(), query)
	return out
}

// systemNames are the names Oracle makes up, different in every copy.
var systemNames = regexp.MustCompile(`SYS_C\d+|SYS_NC\d+\$|ISEQ\$\$_\d+`)

var digits = regexp.MustCompile(`\d+`)

// schemaShape is what a round trip must keep of owner's tables and sequences,
// each line sorted, the owner and the names Oracle makes up blanked.
func schemaShape(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	o := sqlString(owner)
	var shape []string
	for _, part := range []struct{ name, query string }{
		{"column", "SELECT c.table_name, c.column_name, c.data_type, c.data_length, c.data_precision, c.data_scale, c.char_used, " +
			"c.nullable, c.virtual_column, c.hidden_column, c.data_default, ic.generation_type, " +
			"s.increment_by, s.min_value, s.max_value, s.cycle_flag, s.cache_size FROM all_tab_cols c " +
			"LEFT JOIN all_tab_identity_cols ic ON ic.owner = c.owner AND ic.table_name = c.table_name AND ic.column_name = c.column_name " +
			"LEFT JOIN all_sequences s ON s.sequence_owner = ic.owner AND s.sequence_name = ic.sequence_name WHERE c.owner = " + o},
		{"constraint", "SELECT c.table_name, c.constraint_name, c.constraint_type, c.search_condition_vc, c.delete_rule, " +
			"c.deferrable, c.deferred, c.status, c.validated, rc.table_name, (SELECT LISTAGG(cc.column_name, ',') " +
			"WITHIN GROUP (ORDER BY cc.position) FROM all_cons_columns cc WHERE cc.owner = c.owner AND cc.constraint_name = c.constraint_name) " +
			"FROM all_constraints c LEFT JOIN all_constraints rc ON rc.owner = c.r_owner AND rc.constraint_name = c.r_constraint_name " +
			"WHERE c.owner = " + o},
		{"index", "SELECT i.table_name, i.index_name, i.uniqueness, i.index_type, (SELECT LISTAGG(ic.column_name || ' ' || ic.descend, ',') " +
			"WITHIN GROUP (ORDER BY ic.column_position) FROM all_ind_columns ic WHERE ic.index_owner = i.owner AND ic.index_name = i.index_name) " +
			"FROM all_indexes i WHERE i.table_owner = " + o + " AND i.index_type <> 'LOB'"},
		{"expression", "SELECT index_name, column_position, column_expression FROM all_ind_expressions WHERE index_owner = " + o},
		{"comment", "SELECT table_name, '-', comments FROM all_tab_comments WHERE owner = " + o + " AND comments IS NOT NULL " +
			"UNION ALL SELECT table_name, column_name, comments FROM all_col_comments WHERE owner = " + o + " AND comments IS NOT NULL"},
		{"sequence", "SELECT sequence_name, min_value, max_value, increment_by, cycle_flag, order_flag, cache_size " +
			"FROM all_sequences WHERE sequence_owner = " + o + " AND sequence_name NOT LIKE 'ISEQ$$%'"},
	} {
		for _, row := range textRows(t, admin, part.query) {
			if strings.Contains(row, "BIN$") { // the copies a drop leaves in the recycle bin
				continue
			}
			row = strings.ReplaceAll(row, quoteIdent(owner)+".", `"OWNER".`)
			shape = append(shape, part.name+": "+systemNames.ReplaceAllStringFunc(row, func(name string) string {
				return digits.ReplaceAllString(name, "")
			}))
		}
	}
	slices.Sort(shape)
	return shape
}

// schemaRows is owner's rows of the fixture's tables.
func schemaRows(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	schema := quoteIdent(owner)
	return slices.Concat(
		textRows(t, admin, "SELECT 'DEPT', TO_CHAR(ID), NAME, CODE FROM "+schema+".DEPT ORDER BY ID"),
		textRows(t, admin, "SELECT 'EMP', TO_CHAR(ID), TO_CHAR(DEPT_ID), EMAIL, TO_CHAR(SALARY), TO_CHAR(BONUS), TO_CHAR(MGR_ID) FROM "+
			schema+".EMP ORDER BY ID"))
}

// lastNumbers is the next value each of owner's sequences would write to disk.
func lastNumbers(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	return textRows(t, admin, "SELECT sequence_name, TO_CHAR(last_number) FROM all_sequences WHERE sequence_owner = "+
		sqlString(owner)+" AND sequence_name NOT LIKE 'ISEQ$$%' ORDER BY sequence_name")
}

func runOK(t *testing.T, s *sqldb.Session, schema, file string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := s.RunScript(ctx, dbdriver.ScriptRequest{Database: schema, Script: strings.NewReader(file), Size: int64(len(file))})
	require.NoError(t, err)
	require.Nil(t, res.Failure, "%+v", res.Failure)
}

// A schema's tables and sequences dumped with their data run into another user
// with the same structure and rows: nothing in the file names the source schema
// or its storage, the sequences start past the values handed out, and the
// identity column is GENERATED ALWAYS again and continues past the rows. Run over
// the original (changed meanwhile) with its drop section, the file restores it.
func TestIntegrationDumpTablesRoundTrip(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		adminInfo := adminTarget(t, target)
		admin := adminInfo.open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_DUMP_S"+suffix, "OPSKAT_DUMP_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		src, dst := sqldb.NewSession(srcDB, dialect{}), sqldb.NewSession(dstDB, dialect{})
		for _, statement := range dumpFixture {
			_, err := src.Execute(integrationContext(t), statement)
			require.NoError(t, err, statement)
		}
		shape, rows, sequences := schemaShape(t, admin, srcName), schemaRows(t, admin, srcName), lastNumbers(t, admin, srcName)

		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := src.Dump(ctx, dbdriver.DumpRequest{
			Database: srcName, Whole: true, WithData: true, Writer: &out,
			Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Drops: true},
		})
		require.NoError(t, err)
		file := out.String()
		// The comment header names the source; the statements do not.
		var statements strings.Builder
		for line := range strings.Lines(file) {
			if !strings.HasPrefix(line, "--") {
				statements.WriteString(line)
			}
		}
		for _, absent := range []string{srcName, "TABLESPACE", "STORAGE", "SEGMENT CREATION", "PCTFREE", "ISEQ$$"} {
			assert.NotContains(t, statements.String(), absent)
		}

		// The dump's DBMS_METADATA transforms did not stay on the pool's connections.
		ddl, err := src.TableDDL(integrationContext(t), srcName, "DEPT")
		require.NoError(t, err)
		assert.Contains(t, ddl, quoteIdent(srcName)+`."DEPT"`)
		assert.Contains(t, ddl, "TABLESPACE")

		runOK(t, dst, dstName, file)
		assert.Equal(t, shape, schemaShape(t, admin, dstName))
		assert.Equal(t, rows, schemaRows(t, admin, dstName))
		for i, restored := range lastNumbers(t, admin, dstName) {
			name, restoredNext, _ := strings.Cut(restored, "|")
			_, sourceNext, _ := strings.Cut(sequences[i], "|")
			assert.Equal(t, strings.Split(sequences[i], "|")[0], name)
			assert.GreaterOrEqual(t, mustInt(t, restoredNext), mustInt(t, sourceNext), name)
		}
		// The identity continues past the rows (10, 12), the sequence default past
		// the values the source handed out (100, 105, 110).
		_, err = dst.Execute(integrationContext(t), "INSERT INTO DEPT (NAME) VALUES ('after')")
		require.NoError(t, err)
		_, err = dst.Execute(integrationContext(t), "INSERT INTO EMP (DEPT_ID, EMAIL, SALARY) VALUES (10, 'c@example.com', 1)")
		require.NoError(t, err)
		after := textRows(t, admin, "SELECT TO_CHAR(ID) FROM "+quoteIdent(dstName)+".DEPT WHERE NAME = 'after'")
		require.Len(t, after, 1)
		assert.Greater(t, mustInt(t, after[0]), int64(12))
		_, err = dst.Execute(integrationContext(t), "INSERT INTO DEPT (ID, NAME) VALUES (99, 'explicit')")
		require.ErrorContains(t, err, "ORA-32795") // GENERATED ALWAYS again

		// Over the original, changed meanwhile, the file restores it.
		for _, change := range []string{
			"INSERT INTO DEPT (NAME) VALUES ('extra')", "DROP INDEX EMP_DEPT_IX", "DROP SEQUENCE AUDIT_SEQ",
		} {
			_, err := src.Execute(integrationContext(t), change)
			require.NoError(t, err, change)
		}
		runOK(t, src, srcName, file)
		assert.Equal(t, shape, schemaShape(t, admin, srcName))
		assert.Equal(t, rows, schemaRows(t, admin, srcName))
	})
}

func mustInt(t *testing.T, text string) int64 {
	t.Helper()
	var n int64
	_, err := fmt.Sscan(text, &n)
	require.NoError(t, err, text)
	return n
}
