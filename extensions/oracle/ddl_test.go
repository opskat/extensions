package main

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mviewQuery = `SELECT owner, mview_name, query FROM all_mviews ` +
	`WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND mview_name = :2`

const objectDDLQuery = `SELECT DBMS_METADATA\.GET_DDL\(object_type, object_name, owner\) AS DDL FROM all_objects ` +
	`WHERE owner = NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AND object_name = :2 ` +
	`AND object_type IN \('TABLE', 'VIEW'\) FETCH FIRST 1 ROW ONLY`

func noMaterializedView(mock sqlmock.Sqlmock, schema, name string) {
	mock.ExpectQuery(mviewQuery).WithArgs(schema, name).
		WillReturnRows(sqlmock.NewRows([]string{"OWNER", "MVIEW_NAME", "QUERY"}))
}

// The DDL is DBMS_METADATA's for the table or view the name denotes.
func TestTableDDLIsTheDatabasesOwn(t *testing.T) {
	session, mock := newMockSession(t)
	noMaterializedView(mock, "HR", "EMP")
	mock.ExpectQuery(objectDDLQuery).WithArgs("HR", "EMP").
		WillReturnRows(names("DDL", "\n  CREATE TABLE \"HR\".\"EMP\" \n   (\t\"ID\" NUMBER(10,0)\n   ) SEGMENT CREATION DEFERRED\n  TABLESPACE \"USERS\" \n"))

	ddl, err := session.TableDDL(context.Background(), "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE \"HR\".\"EMP\" \n   (\t\"ID\" NUMBER(10,0)\n   ) SEGMENT CREATION DEFERRED\n  TABLESPACE \"USERS\"", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A materialized view's definition is built from ALL_MVIEWS.QUERY: on 23ai
// DBMS_METADATA takes longer than the host's browse timeout for it.
func TestMaterializedViewDDLComesFromAllMviews(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(mviewQuery).WithArgs("HR", "EMP_MV").
		WillReturnRows(sqlmock.NewRows([]string{"OWNER", "MVIEW_NAME", "QUERY"}).
			AddRow("HR", "EMP_MV", "SELECT ID FROM EMP\n"))

	ddl, err := session.TableDDL(context.Background(), "HR", "EMP_MV")
	require.NoError(t, err)
	assert.Equal(t, "CREATE MATERIALIZED VIEW \"HR\".\"EMP_MV\" AS\nSELECT ID FROM EMP", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// An object the user cannot see (or that is not there) is not in ALL_OBJECTS;
// DBMS_METADATA is then asked for it by name, so Oracle's own error (ORA-31603)
// is what comes back instead of an empty DDL.
func TestTableDDLOfAnInvisibleTableIsOraclesError(t *testing.T) {
	session, mock := newMockSession(t)
	noMaterializedView(mock, "SYS", "OBJ$")
	mock.ExpectQuery(objectDDLQuery).WithArgs("SYS", "OBJ$").WillReturnRows(names("DDL"))
	mock.ExpectQuery(`SELECT DBMS_METADATA\.GET_DDL\('TABLE', n, o\) AS DDL FROM \(SELECT NVL\(:1, SYS_CONTEXT\('USERENV', 'CURRENT_SCHEMA'\)\) AS o, :2 AS n FROM dual\)`).
		WithArgs("SYS", "OBJ$").
		WillReturnError(errors.New(`ORA-31603: object "OBJ$" of type TABLE not found in schema "SYS"`))

	ddl, err := session.TableDDL(context.Background(), "SYS", "OBJ$")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ORA-31603")
	assert.Empty(t, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}
