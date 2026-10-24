package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runStatements runs each generated statement on its own, as the host does.
func runStatements(t *testing.T, s *sqldb.Session, statements []string) {
	t.Helper()
	for _, statement := range statements {
		_, err := s.Execute(integrationContext(t), statement)
		require.NoError(t, err, statement)
	}
}

// Create, alter, truncate and drop a table through the statements the session
// generates, then read the result back from the dictionary.
func TestIntegrationTableDDL(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		db.SetMaxOpenConns(1)
		s := sqldb.NewSession(db, dialect{})
		ctx := integrationContext(t)
		var schema string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT USER FROM DUAL`).Scan(&schema))
		name := fmt.Sprintf("Ddl_%d", time.Now().UnixNano()%1_000_000_000)
		renamed := name + "_R"

		created, err := s.CreateTableStatements(ctx, schema, dbdriver.CreateTableRequest{
			Name: name,
			Columns: []dbdriver.NewColumn{
				{Name: "ID", Type: "NUMBER(10)"},
				{Name: "Label", Type: "VARCHAR2(20 CHAR)", Nullable: true, DefaultValue: "none"},
				// DEFAULT must precede NOT NULL in Oracle's column definition.
				{Name: "Status", Type: "VARCHAR2(10)", DefaultValue: "new"},
			},
		})
		require.NoError(t, err)
		runStatements(t, s, created)
		table := renamed
		t.Cleanup(func() {
			for _, n := range []string{renamed, name} {
				_, _ = s.Execute(integrationContext(t), "DROP TABLE "+dialect{}.TableRef(schema, n)+" PURGE")
			}
			// The session's DROP TABLE below leaves the table in the recycle bin.
			_, _ = s.Execute(integrationContext(t), "PURGE TABLE "+dialect{}.TableRef(schema, renamed))
		})

		_, err = s.Execute(ctx, fmt.Sprintf(`INSERT INTO "%s"."%s" ("ID") VALUES (1)`, schema, name))
		require.NoError(t, err)

		// Everything changes at once; the statements run in the order given.
		altered, err := s.AlterTableStatements(ctx, schema, name, dbdriver.AlterTableRequest{
			Name:    renamed,
			Comment: "ddl test",
			Columns: []dbdriver.AlterColumn{
				{OriginalName: "ID", Name: "EMP_ID", Type: "NUMBER(12)", Nullable: true, Comment: "key"},
				{OriginalName: "Label", Name: "Label", Type: "VARCHAR2(40 CHAR)", Nullable: false, DefaultValue: "other"},
				{Name: "Added", Type: "NUMBER(3)", DefaultValue: "7", Comment: "new"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, renamed, altered.Table)
		runStatements(t, s, altered.Statements)

		model, err := s.AlterTableModel(ctx, schema, table)
		require.NoError(t, err)
		assert.Equal(t, "ddl test", model.Comment)
		require.Len(t, model.Columns, 4)
		assert.Equal(t, dbdriver.ColumnDefinition{Name: "EMP_ID", Type: "NUMBER(12)", Nullable: true, Comment: "key"}, model.Columns[0])
		assert.Equal(t, "VARCHAR2(40 CHAR)", model.Columns[1].Type)
		assert.False(t, model.Columns[1].Nullable)
		assert.Equal(t, "'other'", model.Columns[1].DefaultValue)
		assert.Equal(t, dbdriver.ColumnDefinition{Name: "Status", Type: "VARCHAR2(10)", DefaultValue: "'new'"}, model.Columns[2])
		assert.Equal(t, dbdriver.ColumnDefinition{Name: "Added", Type: "NUMBER(3)", DefaultValue: "7", Comment: "new"}, model.Columns[3])

		// Nothing differs now: no statement, so no ORA-01451 / ORA-01442.
		again, err := s.AlterTableStatements(ctx, schema, table, dbdriver.AlterTableRequest{
			Name: renamed, Comment: model.Comment,
			Columns: []dbdriver.AlterColumn{
				{OriginalName: "EMP_ID", Name: "EMP_ID", Type: "NUMBER(12)", Nullable: true, Comment: "key"},
				{OriginalName: "Label", Name: "Label", Type: "VARCHAR2(40 CHAR)", DefaultValue: "'other'"},
				{OriginalName: "Added", Name: "Added", Type: "NUMBER(3)", DefaultValue: "7", Comment: "new"},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, again.Statements)

		dropped, err := s.AlterTableStatements(ctx, schema, table, dbdriver.AlterTableRequest{
			Name: renamed, Comment: model.Comment, Drops: []string{"Added"},
			Columns: []dbdriver.AlterColumn{
				{OriginalName: "EMP_ID", Name: "EMP_ID", Type: "NUMBER(12)", Nullable: true, Comment: "key"},
				{OriginalName: "Label", Name: "Label", Type: "VARCHAR2(40 CHAR)", DefaultValue: "'other'"},
			},
		})
		require.NoError(t, err)
		runStatements(t, s, dropped.Statements)

		truncated, err := s.TruncateTableStatements(ctx, schema, table)
		require.NoError(t, err)
		runStatements(t, s, truncated)
		var rows int
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s"."%s"`, schema, table)).Scan(&rows))
		assert.Zero(t, rows)

		drop, err := s.DropTableStatements(ctx, schema, table)
		require.NoError(t, err)
		runStatements(t, s, drop)
		_, err = s.Execute(ctx, fmt.Sprintf(`SELECT 1 FROM "%s"."%s"`, schema, table))
		assert.Error(t, err)
	})
}
