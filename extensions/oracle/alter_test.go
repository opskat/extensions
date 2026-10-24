package main

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared CREATE / DROP TABLE take the quoting from the dialect; the
// statements are run one at a time, so none ends in ';'.
func TestCreateAndDropTableStatements(t *testing.T) {
	session, _ := newMockSession(t)
	ctx := context.Background()

	created, err := session.CreateTableStatements(ctx, "HR", dbdriver.CreateTableRequest{
		Name: "Staff",
		Columns: []dbdriver.NewColumn{
			{Name: "ID", Type: "NUMBER(10)"},
			{Name: "Full Name", Type: "VARCHAR2(40 CHAR)", Nullable: true, DefaultValue: "anon"},
			{Name: "Status", Type: "VARCHAR2(10)", DefaultValue: "new"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"HR\".\"Staff\" (\n  \"ID\" NUMBER(10) NOT NULL,\n  \"Full Name\" VARCHAR2(40 CHAR) DEFAULT 'anon',\n  \"Status\" VARCHAR2(10) DEFAULT 'new' NOT NULL\n)"}, created)

	dropped, err := session.DropTableStatements(ctx, "HR", "Staff")
	require.NoError(t, err)
	assert.Equal(t, []string{`DROP TABLE "HR"."Staff"`}, dropped)
}

func TestTruncateTableStatements(t *testing.T) {
	session, _ := newMockSession(t)
	statements, err := session.TruncateTableStatements(context.Background(), "HR", "Staff")
	require.NoError(t, err)
	assert.Equal(t, []string{`TRUNCATE TABLE "HR"."Staff"`}, statements)
}

// expectTable queues the two dictionary reads of the table the alter tests change.
func expectTable(mock sqlmock.Sqlmock, comment any) {
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", nil, "key", "0", "0", "0").
			AddRow("NAME", "VARCHAR2(20 CHAR)", "YES", "'anon' \n", nil, "0", "0", "0").
			AddRow("NOTE", "CLOB", "YES", nil, nil, "0", "0", "0"))
	mock.ExpectQuery(`FROM all_tab_comments`).WithArgs("HR", "EMP").
		WillReturnRows(sqlmock.NewRows([]string{"COMMENTS"}).AddRow(comment))
}

func TestAlterTableModel(t *testing.T) {
	session, mock := newMockSession(t)
	expectTable(mock, "staff")

	model, err := session.AlterTableModel(context.Background(), "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, "EMP", model.Name)
	assert.Equal(t, "staff", model.Comment)
	assert.Equal(t, []dbdriver.ColumnDefinition{
		{Name: "ID", Type: "NUMBER(10)", Comment: "key"},
		{Name: "NAME", Type: "VARCHAR2(20 CHAR)", Nullable: true, DefaultValue: "'anon'"},
		{Name: "NOTE", Type: "CLOB", Nullable: true},
	}, model.Columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A table without a comment has a NULL one in the dictionary.
func TestAlterTableModelWithoutComment(t *testing.T) {
	session, mock := newMockSession(t)
	expectTable(mock, nil)
	model, err := session.AlterTableModel(context.Background(), "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, "", model.Comment)
}

func TestAlterTableStatements(t *testing.T) {
	session, mock := newMockSession(t)
	expectTable(mock, "staff")

	req := dbdriver.AlterTableRequest{
		Name:    "STAFF",
		Comment: "all staff",
		Columns: []dbdriver.AlterColumn{
			// Type, nullability, default and comment all change; the column is renamed too.
			{OriginalName: "ID", Name: "EMP_ID", Type: "NUMBER(12)", Nullable: true, DefaultValue: "0", Comment: "it's the key"},
			// Only the default is dropped: no type or nullability clause.
			{OriginalName: "NAME", Name: "NAME", Type: "VARCHAR2(20 CHAR)", Nullable: true, DefaultValue: ""},
			// Unchanged.
			{OriginalName: "NOTE", Name: "NOTE", Type: "CLOB", Nullable: true},
			{Name: "AGE", Type: "NUMBER(3)", DefaultValue: "18", Comment: "years"},
			{Name: "Nick", Type: "VARCHAR2(10)", Nullable: true},
		},
		Drops: nil,
	}
	got, err := session.AlterTableStatements(context.Background(), "HR", "EMP", req)
	require.NoError(t, err)
	assert.Equal(t, "STAFF", got.Table)
	assert.Equal(t, []string{
		`ALTER TABLE "HR"."EMP" RENAME TO "STAFF"`,
		`ALTER TABLE "HR"."STAFF" ADD "AGE" NUMBER(3) DEFAULT 18 NOT NULL`,
		`ALTER TABLE "HR"."STAFF" ADD "Nick" VARCHAR2(10)`,
		`ALTER TABLE "HR"."STAFF" RENAME COLUMN "ID" TO "EMP_ID"`,
		`ALTER TABLE "HR"."STAFF" MODIFY "EMP_ID" NUMBER(12)`,
		`ALTER TABLE "HR"."STAFF" MODIFY "EMP_ID" NULL`,
		`ALTER TABLE "HR"."STAFF" MODIFY "EMP_ID" DEFAULT 0`,
		`ALTER TABLE "HR"."STAFF" MODIFY "NAME" DEFAULT NULL`,
		`COMMENT ON COLUMN "HR"."STAFF"."EMP_ID" IS 'it''s the key'`,
		`COMMENT ON COLUMN "HR"."STAFF"."AGE" IS 'years'`,
		`COMMENT ON TABLE "HR"."STAFF" IS 'all staff'`,
	}, got.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAlterTableDropsColumnsAndClearsComments(t *testing.T) {
	session, mock := newMockSession(t)
	expectTable(mock, "staff")

	got, err := session.AlterTableStatements(context.Background(), "HR", "EMP", dbdriver.AlterTableRequest{
		Name:    "EMP",
		Comment: "",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "ID", Name: "ID", Type: "NUMBER(10)"}, // comment "key" cleared
			{OriginalName: "NAME", Name: "NAME", Type: "VARCHAR2(20 CHAR)", Nullable: true, DefaultValue: "'anon'"},
		},
		Drops: []string{"NOTE"},
	})
	require.NoError(t, err)
	assert.Equal(t, "EMP", got.Table)
	assert.Equal(t, []string{
		`COMMENT ON COLUMN "HR"."EMP"."ID" IS ''`,
		`ALTER TABLE "HR"."EMP" DROP COLUMN "NOTE"`,
		`COMMENT ON TABLE "HR"."EMP" IS ''`,
	}, got.Statements)
}

func TestAlterTableWithoutChangesHasNoStatements(t *testing.T) {
	session, mock := newMockSession(t)
	expectTable(mock, "staff")
	got, err := session.AlterTableStatements(context.Background(), "HR", "EMP", dbdriver.AlterTableRequest{
		Name: "EMP", Comment: "staff",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "ID", Name: "ID", Type: "number(10)", Comment: "key"},
			{OriginalName: "NAME", Name: "NAME", Type: "VARCHAR2(20 CHAR)", Nullable: true, DefaultValue: "'anon'"},
			{OriginalName: "NOTE", Name: "NOTE", Type: "CLOB", Nullable: true},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, got.Statements)
}

func TestAlterTableOfMissingTableFails(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "GONE").WillReturnRows(columnRows())
	mock.ExpectQuery(`FROM all_tab_comments`).WithArgs("HR", "GONE").WillReturnRows(sqlmock.NewRows([]string{"COMMENTS"}))
	_, err := session.AlterTableStatements(context.Background(), "HR", "GONE", dbdriver.AlterTableRequest{Name: "X"})
	require.Error(t, err)
}
