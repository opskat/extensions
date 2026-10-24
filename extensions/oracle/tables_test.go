package main

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMockSession is the session the SDK builds over the driver's database, on a
// mock that matches each expected query as a regular expression.
func newMockSession(t *testing.T) (dbdriver.Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return sqldb.NewSession(db, dialect{}), mock
}

// exact matches the whole statement, character for character.
func exact(statement string) string {
	return "^" + regexp.QuoteMeta(statement) + "$"
}

// columnRows is a column-definition result as the dictionary query returns it.
func columnRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT",
		"IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS", "IS_NO_IDENTITY_OVERRIDE",
	})
}

func TestTableRefQuotesSchemaAndTableAsGiven(t *testing.T) {
	d := dialect{}
	assert.Equal(t, `"HR"."Employees"`, d.TableRef("HR", "Employees"))
	// A dot belongs to the name: the schema is the database argument.
	assert.Equal(t, `"HR"."A.B"`, d.TableRef("HR", "A.B"))
	// Without a schema the session's own (the login user's) resolves the name.
	assert.Equal(t, `"EMP"`, d.TableRef("", "EMP"))
	assert.Equal(t, `"mixed Case"`, d.QuoteIdent("mixed Case"))
}

func TestStarterQueryFetchesTheFirstRows(t *testing.T) {
	session, _ := newMockSession(t)
	query, err := session.StarterQuery(context.Background(), "HR", "EMP", 100)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "HR"."EMP" FETCH FIRST 100 ROWS ONLY`, query)
}

func TestOpenTable(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`FROM all_constraints c JOIN all_cons_columns`).WithArgs("HR", "EMP").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("ID"))
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", `"HR"."ISEQ$$_1".nextval `, nil, "1", "1", "1").
			AddRow("NAME", "VARCHAR2(20 CHAR)", "YES", "'anon' \n", "full name", "0", "0", "0").
			AddRow("NOTE", "CLOB", "YES", nil, nil, "0", "0", "0"))
	mock.ExpectQuery(exact(`SELECT COUNT(*) FROM "HR"."EMP"`)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(2))
	mock.ExpectQuery(exact(tableMark + `SELECT * FROM "HR"."EMP" FETCH FIRST 50 ROWS ONLY`)).
		WillReturnRows(sqlmock.NewRows([]string{"ID", "NAME", "NOTE"}).AddRow("1", "a", nil).AddRow("2", "b", "x"))

	opened, err := session.OpenTable(context.Background(), "HR", "EMP", 50)
	require.NoError(t, err)
	assert.Equal(t, []string{"ID"}, opened.PrimaryKeys)
	assert.Equal(t, int64(2), opened.TotalCount)
	assert.Equal(t, []string{"ID", "NAME", "NOTE"}, opened.FirstPage.Columns)
	assert.Len(t, opened.FirstPage.Rows, 2)
	assert.Equal(t, map[string]string{"ID": "NUMBER(10)", "NAME": "VARCHAR2(20 CHAR)", "NOTE": "CLOB"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "ID", HasDefault: true, AutoIncrement: true},
		{Name: "NAME", Nullable: true, HasDefault: true},
		{Name: "NOTE", Nullable: true},
	}, opened.ColumnRules)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The dictionary keeps a default as typed, trailing blanks and newline included.
func TestTableColumnsTrimTheDefault(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`FROM all_tab_columns c`).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().
			AddRow("ID", "NUMBER(10)", "NO", `"HR"."ISEQ$$_1".nextval `, nil, "1", "1", "1").
			AddRow("NAME", "VARCHAR2(20 CHAR)", "YES", "'anon' \n", "full name", "0", "0", "0").
			AddRow("NOTE", "CLOB", "YES", nil, nil, "0", "0", "0"))

	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	columns, err := dialect{}.TableColumns(context.Background(), conn, "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, []sqldb.TableColumn{
		{
			Name: "ID", Type: "NUMBER(10)", HasDefault: true, Default: new(`"HR"."ISEQ$$_1".nextval`),
			AutoIncrement: true, IdentityAlways: true, NoIdentityOverride: true,
		},
		{Name: "NAME", Type: "VARCHAR2(20 CHAR)", Nullable: true, HasDefault: true, Default: new("'anon'"), Comment: "full name"},
		{Name: "NOTE", Type: "CLOB", Nullable: true},
	}, columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTableRowsPagesWithOffsetFetch(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(exact(tableMark + `SELECT * FROM "HR"."EMP" WHERE "NAME" LIKE '%a%' ORDER BY "ID" DESC OFFSET 40 ROWS FETCH NEXT 20 ROWS ONLY`)).
		WillReturnRows(sqlmock.NewRows([]string{"ID", "NAME"}).AddRow("41", "ann"))

	rows, err := session.TableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "NAME", Operator: dbdriver.OpContains, Value: "a"}},
		Sorts:   []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortDescending}},
	}, dbdriver.Page{Index: 2, Size: 20})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"41", "ann"}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTableRowsWithoutFilterOrSort(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(exact(tableMark + `SELECT * FROM "HR"."EMP" OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY`)).
		WillReturnRows(sqlmock.NewRows([]string{"ID"}))

	_, err := session.TableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 20})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// plainFilterValues is the dialect with filter values written as plain strings:
// the shared filter semantics are checked against SQL text only, without the
// column lookup typed filter values (dialect.TypedFilterValues) send first.
type plainFilterValues struct{ dialect }

func (plainFilterValues) TypedFilterValues() bool { return false }

func TestFilterSemantics(t *testing.T) {
	sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
		NewSession: func(db *sql.DB) dbdriver.Session { return sqldb.NewSession(db, plainFilterValues{}) },
		Quote:      func(name string) string { return `"` + name + `"` },
		Database:   "HR",
		Table:      "EMP",
		TableRef:   `"HR"."EMP"`,

		EmptyStringIsNull: true,
	})
}
