package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// The DDL is the database's own, from DBMS_METADATA, except a materialized view's,
// which is written from ALL_MVIEWS (DBMS_METADATA takes ~36 s for it on 23ai,
// longer than the host's browse timeout). Reading another schema's objects through
// DBMS_METADATA takes SELECT_CATALOG_ROLE; without it Oracle's ORA-31603 is the
// error.
var _ sqldb.DDL = dialect{}

// TableDDL gives the DDL of a table, view or materialized view by that name. An
// object the user cannot see, and one that does not exist, are the same to
// Oracle: the error is DBMS_METADATA's ORA-31603, not an empty DDL.
func (dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	return objectDDL(ctx, conn, database, table, []string{"TABLE", "VIEW"}, "TABLE")
}

// objectDDL gives the DDL of the materialized view, else of the object of
// objectTypes, named name in the schema, trimmed. An object not in ALL_OBJECTS is
// asked of DBMS_METADATA as a missingType, whose error is what the caller gets.
func objectDDL(ctx context.Context, conn *sql.Conn, database, name string, objectTypes []string, missingType string) (string, error) {
	var owner, mview string
	var query sql.NullString
	err := conn.QueryRowContext(ctx,
		"SELECT owner, mview_name, query FROM all_mviews WHERE owner = "+ownerIs+" AND mview_name = :2",
		database, name).Scan(&owner, &mview, &query)
	if err == nil {
		return "CREATE MATERIALIZED VIEW " + dialect{}.TableRef(owner, mview) + " AS\n" + strings.TrimSpace(query.String), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var ddl sql.NullString
	err = conn.QueryRowContext(ctx,
		"SELECT DBMS_METADATA.GET_DDL(object_type, object_name, owner) AS DDL FROM all_objects "+
			"WHERE owner = "+ownerIs+" AND object_name = :2 "+
			"AND object_type IN ("+quotedList(objectTypes)+") FETCH FIRST 1 ROW ONLY",
		database, name).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return metadataDDL(ctx, conn, database, name, missingType)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(ddl.String), nil
}

// metadataDDL asks DBMS_METADATA for the objectType object named name in the
// schema, trimmed. For an object the user cannot see, or one that is not there,
// the error is Oracle's ORA-31603.
func metadataDDL(ctx context.Context, conn *sql.Conn, database, name, objectType string) (string, error) {
	var ddl sql.NullString
	if err := conn.QueryRowContext(ctx,
		"SELECT DBMS_METADATA.GET_DDL('"+objectType+"', n, o) AS DDL FROM (SELECT "+ownerIs+" AS o, :2 AS n FROM dual)",
		database, name).Scan(&ddl); err != nil {
		return "", err
	}
	return strings.TrimSpace(ddl.String), nil
}
