package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The DDL is DBMS_METADATA's for the object the name denotes: a table, a view, or
// a materialized view (rather than its container table of the same name), picked
// in one statement so GET_DDL runs for that object alone.
func TestTableDDLIsTheDatabasesOwn(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(exact("SELECT DBMS_METADATA.GET_DDL(DECODE(object_type, 'MATERIALIZED VIEW', 'MATERIALIZED_VIEW', object_type), object_name, owner) AS DDL "+
		"FROM (SELECT object_type, object_name, owner FROM all_objects "+
		"WHERE owner = NVL(:1, SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')) AND object_name = :2 "+
		"AND object_type IN ('TABLE', 'VIEW', 'MATERIALIZED VIEW') "+
		"ORDER BY DECODE(object_type, 'TABLE', 1, 0) FETCH FIRST 1 ROW ONLY)")).
		WithArgs("HR", "EMP").
		WillReturnRows(names("DDL", "\n  CREATE TABLE \"HR\".\"EMP\" \n   (\t\"ID\" NUMBER(10,0)\n   ) SEGMENT CREATION DEFERRED\n  TABLESPACE \"USERS\" \n"))

	ddl, err := session.TableDDL(context.Background(), "HR", "EMP")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE \"HR\".\"EMP\" \n   (\t\"ID\" NUMBER(10,0)\n   ) SEGMENT CREATION DEFERRED\n  TABLESPACE \"USERS\"", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A table that does not exist (or is out of the user's reach) has no DDL.
func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery(`DBMS_METADATA\.GET_DDL`).WithArgs("HR", "GONE").WillReturnRows(names("DDL"))

	ddl, err := session.TableDDL(context.Background(), "HR", "GONE")
	require.NoError(t, err)
	assert.Empty(t, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}
