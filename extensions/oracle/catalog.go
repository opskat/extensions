package main

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Browsing reads the ALL_* dictionary views, so an object out of the user's reach
// is not listed. The host's "database" is the schema: every method below gets the
// schema as database (empty for the session's current one, see ownerIs) and bare
// object names.
var _ sqldb.Catalog = dialect{}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor() }

// packageMarker ends a package's name in the procedure list, where packages sit
// beside standalone procedures; ObjectSource reads it back, with or without the
// blank before it (the host hands the name back as the desktop shows it).
const packageMarker = "(package)"

// ListDatabases lists the schemas owning a visible table, view or code object,
// without the ones Oracle maintains; the login user's own comes first (even
// before it owns anything), then the rest by name.
func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	return sqldb.QueryNames(ctx, conn,
		"SELECT username FROM all_users WHERE username = USER OR (oracle_maintained = 'N' AND username IN "+
			"(SELECT owner FROM all_objects WHERE object_type IN ('TABLE', 'VIEW', 'MATERIALIZED VIEW', 'PROCEDURE', 'FUNCTION', 'PACKAGE', 'TRIGGER') "+
			"AND object_name NOT LIKE 'BIN$%')) "+
			"ORDER BY DECODE(username, USER, 0, 1), username")
}

// ListTables lists the schema's tables, partitioned ones included. Tables in the
// recycle bin are left out, and so is a materialized view's container table: the
// materialized view is listed with the views.
func (dialect) ListTables(ctx context.Context, conn *sql.Conn, database string) ([]string, error) {
	return sqldb.QueryNames(ctx, conn,
		"SELECT t.table_name FROM all_tables t WHERE t.owner = "+ownerIs+" AND t.dropped = 'NO' "+
			"AND NOT EXISTS (SELECT 1 FROM all_mviews m WHERE m.owner = t.owner AND m.mview_name = t.table_name) "+
			"ORDER BY t.table_name", database)
}

func (d dialect) TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error) {
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("primary keys", err)
	}
	indexes, err := indexes(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("indexes", err)
	}
	// A foreign key into another schema names that schema.
	foreignKeys, err := sqldb.QueryForeignKeys(ctx, conn,
		"SELECT c.constraint_name, cc.column_name, DECODE(rc.owner, c.owner, rc.table_name, rc.owner || '.' || rc.table_name), rcc.column_name "+
			"FROM all_constraints c "+
			"JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name "+
			"JOIN all_constraints rc ON rc.owner = c.r_owner AND rc.constraint_name = c.r_constraint_name "+
			"JOIN all_cons_columns rcc ON rcc.owner = rc.owner AND rcc.constraint_name = rc.constraint_name AND rcc.position = cc.position "+
			"WHERE c.constraint_type = 'R' AND c.owner = "+ownerIs+" AND c.table_name = :2 "+
			"ORDER BY c.constraint_name, cc.position", database, table)
	if err != nil {
		return nil, sqldb.Wrap("foreign keys", err)
	}
	return sqldb.AssembleMetadata(sqldb.ColumnMetas(columns), primaryKeys, indexes, foreignKeys), nil
}

func indexes(ctx context.Context, conn *sql.Conn, database, table string) ([]dbdriver.IndexMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT i.index_name, i.uniqueness, ic.column_name "+
			"FROM all_indexes i JOIN all_ind_columns ic ON ic.index_owner = i.owner AND ic.index_name = i.index_name "+
			"WHERE i.table_owner = "+ownerIs+" AND i.table_name = :2 ORDER BY i.index_name, ic.column_position",
		database, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var b sqldb.IndexBuilder
	for rows.Next() {
		var name, uniqueness, column string
		if err := rows.Scan(&name, &uniqueness, &column); err != nil {
			return nil, err
		}
		b.Add(name, uniqueness == "UNIQUE", column)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return b.Indexes(), nil
}

// ListObjects lists views with materialized views, standalone procedures with
// packages (marked), standalone functions, and the triggers on tables.
func (dialect) ListObjects(ctx context.Context, conn *sql.Conn, database string) (*dbdriver.DatabaseObjects, error) {
	out := &dbdriver.DatabaseObjects{}
	var err error
	if out.Views, err = namedObjects(ctx, conn, dbdriver.ObjectView,
		"SELECT object_name FROM all_objects WHERE owner = "+ownerIs+" "+
			"AND object_type IN ('VIEW', 'MATERIALIZED VIEW') ORDER BY object_name", database); err != nil {
		return nil, sqldb.Wrap("views", err)
	}
	if out.Procedures, err = procedures(ctx, conn, database); err != nil {
		return nil, sqldb.Wrap("procedures", err)
	}
	if out.Functions, err = namedObjects(ctx, conn, dbdriver.ObjectFunction,
		"SELECT object_name FROM all_objects WHERE owner = "+ownerIs+" "+
			"AND object_type = 'FUNCTION' ORDER BY object_name", database); err != nil {
		return nil, sqldb.Wrap("functions", err)
	}
	// A dropped table's triggers go to the recycle bin with it, renamed BIN$….
	if out.Triggers, err = namedObjects(ctx, conn, dbdriver.ObjectTrigger,
		"SELECT trigger_name FROM all_triggers WHERE owner = "+ownerIs+" "+
			"AND base_object_type = 'TABLE' AND trigger_name NOT LIKE 'BIN$%' ORDER BY trigger_name", database); err != nil {
		return nil, sqldb.Wrap("triggers", err)
	}
	return out, nil
}

func namedObjects(ctx context.Context, conn *sql.Conn, objType, query, database string) ([]dbdriver.ObjectMeta, error) {
	names, err := sqldb.QueryNames(ctx, conn, query, database)
	if err != nil {
		return nil, err
	}
	out := make([]dbdriver.ObjectMeta, 0, len(names))
	for _, name := range names {
		out = append(out, dbdriver.ObjectMeta{Name: name, Type: objType})
	}
	return out, nil
}

func procedures(ctx context.Context, conn *sql.Conn, database string) ([]dbdriver.ObjectMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT object_name, object_type FROM all_objects WHERE owner = "+ownerIs+" "+
			"AND object_type IN ('PROCEDURE', 'PACKAGE') ORDER BY object_name, object_type", database)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []dbdriver.ObjectMeta
	for rows.Next() {
		var name, objectType string
		if err := rows.Scan(&name, &objectType); err != nil {
			return nil, err
		}
		if objectType == "PACKAGE" {
			name += " " + packageMarker
		}
		out = append(out, dbdriver.ObjectMeta{Name: name, Type: dbdriver.ObjectProcedure})
	}
	return out, rows.Err()
}

// sourceTypes are the ALL_SOURCE types of each browsed kind of stored unit. A
// package sits among the procedures, and procedures and packages share one name
// space in a schema, so a procedure name finds whichever it is.
var sourceTypes = map[string][]string{
	dbdriver.ObjectProcedure: {"PROCEDURE", "PACKAGE", "PACKAGE BODY"},
	dbdriver.ObjectFunction:  {"FUNCTION"},
	dbdriver.ObjectTrigger:   {"TRIGGER"},
}

// metadataTypes are the DBMS_METADATA object types a unit of each browsed kind is
// asked for when ALL_SOURCE has none of it; a name with the package marker is a
// PACKAGE.
var metadataTypes = map[string]string{
	dbdriver.ObjectProcedure: "PROCEDURE",
	dbdriver.ObjectFunction:  "FUNCTION",
	dbdriver.ObjectTrigger:   "TRIGGER",
}

// ObjectSource gives a view's (or materialized view's) DDL, and a stored unit's
// source as the script that creates it: CREATE OR REPLACE before the stored
// text, a "/" line after each unit; a package has its spec and, when it has one,
// its body. The name is the one ListObjects gave, package marker included.
func (dialect) ObjectSource(ctx context.Context, conn *sql.Conn, database, objType, name, _ string) (string, error) {
	if objType == dbdriver.ObjectView {
		return objectDDL(ctx, conn, database, name, []string{"VIEW"}, "VIEW")
	}
	unit, isPackage := strings.CutSuffix(name, packageMarker)
	metadataType := metadataTypes[objType]
	if isPackage {
		metadataType = "PACKAGE"
	}
	return unitSource(ctx, conn, database, strings.TrimSpace(unit), sourceTypes[objType], metadataType)
}

// unitSource reads the unit's source from ALL_SOURCE. A unit the user cannot see,
// and one that is not there, have no rows: DBMS_METADATA is then asked for it as
// a metadataType, so the error is Oracle's own (ORA-31603).
func unitSource(ctx context.Context, conn *sql.Conn, database, name string, types []string, metadataType string) (string, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT type, text FROM all_source WHERE owner = "+ownerIs+" AND name = :2 "+
			"AND type IN ("+quotedList(types)+") ORDER BY type, line", database, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var units []string
	var unit strings.Builder
	current := ""
	flush := func() {
		if unit.Len() > 0 {
			units = append(units, "CREATE OR REPLACE "+strings.TrimRight(unit.String(), " \t\r\n")+"\n/")
			unit.Reset()
		}
	}
	for rows.Next() {
		var typ string
		var text sql.NullString
		if err := rows.Scan(&typ, &text); err != nil {
			return "", err
		}
		if typ != current {
			flush()
			current = typ
		}
		unit.WriteString(text.String)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	flush()
	if len(units) == 0 {
		return metadataDDL(ctx, conn, database, name, metadataType)
	}
	return strings.Join(units, "\n\n"), nil
}

// quotedList writes words as a list of string literals; they are this file's
// constants, not input.
func quotedList(words []string) string {
	return "'" + strings.Join(words, "', '") + "'"
}
