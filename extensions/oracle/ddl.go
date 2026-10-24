package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// The DDL is the database's own, from DBMS_METADATA. Reading another schema's
// objects through it takes SELECT_CATALOG_ROLE; without it Oracle's ORA-31603 is
// the error.
var _ sqldb.DDL = dialect{}

// TableDDL gives the DDL of a table, or of a view or materialized view by that
// name; "" when there is none the user can see.
func (dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	return metadataDDL(ctx, conn, database, table, []string{"TABLE", "VIEW", "MATERIALIZED VIEW"})
}

// metadataDDL picks the one object of objectTypes named name in the schema and
// returns its DDL, trimmed; "" when there is no such object. A materialized view
// shares its name with its container table, so it goes first; picking the object
// in an inline view means GET_DDL runs for that object alone.
func metadataDDL(ctx context.Context, conn *sql.Conn, database, name string, objectTypes []string) (string, error) {
	var ddl sql.NullString
	err := conn.QueryRowContext(ctx,
		"SELECT DBMS_METADATA.GET_DDL(DECODE(object_type, 'MATERIALIZED VIEW', 'MATERIALIZED_VIEW', object_type), object_name, owner) AS DDL "+
			"FROM (SELECT object_type, object_name, owner FROM all_objects "+
			"WHERE owner = "+ownerIs+" AND object_name = :2 "+
			"AND object_type IN ("+quotedList(objectTypes)+") "+
			"ORDER BY DECODE(object_type, 'TABLE', 1, 0) FETCH FIRST 1 ROW ONLY)",
		database, name).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(ddl.String), nil
}
