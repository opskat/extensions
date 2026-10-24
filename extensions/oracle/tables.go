package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Table browsing. The host's "database" is the schema, so a table is the pair
// (database, table) and its reference is "SCHEMA"."TABLE", both quoted exactly as
// the dictionary spells them; a dot inside table belongs to the name.
var _ sqldb.Tables = dialect{}

// ownerIs matches a dictionary view's owner column against the schema bound as
// :1. An empty schema is the session's current one: Oracle binds an empty string as NULL.
const ownerIs = "NVL(:1, SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA'))"

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

// TableRef is "SCHEMA"."TABLE"; without a schema the bare quoted name resolves in
// the session's current schema.
func (dialect) TableRef(database, table string) string {
	if database == "" {
		return quoteIdent(table)
	}
	return quoteIdent(database) + "." + quoteIdent(table)
}

const primaryKeysQuery = "SELECT cc.column_name AS COLUMN_NAME FROM all_constraints c " +
	"JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name AND cc.table_name = c.table_name " +
	"WHERE c.constraint_type = 'P' AND c.owner = " + ownerIs + " AND c.table_name = :2 " +
	"ORDER BY cc.position"

func (dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]string, error) {
	return sqldb.QueryPrimaryKeys(ctx, conn, "COLUMN_NAME", primaryKeysQuery, database, table)
}

// tableColumnsQuery spells each column's type as declared: DATA_TYPE alone says
// VARCHAR2 or NUMBER, while TIMESTAMP and INTERVAL already carry their precision.
// An Oracle identity column cannot be written explicitly when it is GENERATED
// ALWAYS and has no override switch, so ALWAYS also means no identity override.
const tableColumnsQuery = "SELECT c.column_name AS COLUMN_NAME, " +
	"CASE " +
	"WHEN c.data_type IN ('VARCHAR2', 'CHAR') THEN c.data_type || '(' || DECODE(c.char_used, 'C', c.char_length || ' CHAR', c.data_length) || ')' " +
	"WHEN c.data_type IN ('NVARCHAR2', 'NCHAR') THEN c.data_type || '(' || c.char_length || ')' " +
	"WHEN c.data_type = 'RAW' THEN 'RAW(' || c.data_length || ')' " +
	"WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL THEN 'NUMBER(' || c.data_precision || DECODE(c.data_scale, 0, '', ',' || c.data_scale) || ')' " +
	"WHEN c.data_type = 'NUMBER' AND c.data_scale = 0 THEN 'NUMBER(*,0)' " +
	"WHEN c.data_type = 'FLOAT' THEN 'FLOAT(' || c.data_precision || ')' " +
	"ELSE c.data_type END AS DATA_TYPE, " +
	"DECODE(c.nullable, 'Y', 'YES', 'NO') AS IS_NULLABLE, " +
	"c.data_default AS COLUMN_DEFAULT, " +
	"cm.comments AS COLUMN_COMMENT, " +
	"DECODE(c.identity_column, 'YES', 1, 0) AS IS_AUTO_INCREMENT, " +
	"DECODE(ic.generation_type, 'ALWAYS', 1, 0) AS IS_IDENTITY_ALWAYS, " +
	"DECODE(ic.generation_type, 'ALWAYS', 1, 0) AS IS_NO_IDENTITY_OVERRIDE " +
	"FROM all_tab_columns c " +
	"LEFT JOIN all_col_comments cm ON cm.owner = c.owner AND cm.table_name = c.table_name AND cm.column_name = c.column_name " +
	"LEFT JOIN all_tab_identity_cols ic ON ic.owner = c.owner AND ic.table_name = c.table_name AND ic.column_name = c.column_name " +
	"WHERE c.owner = " + ownerIs + " AND c.table_name = :2 " +
	"ORDER BY c.column_id"

// TableColumns reads the columns in table order. DATA_DEFAULT is a LONG holding
// the default as typed, trailing blanks and newline included: it is trimmed here
// because a LONG takes no SQL function.
func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, database, table string) ([]sqldb.TableColumn, error) {
	columns, err := sqldb.QueryTableColumns(ctx, conn, tableColumnsQuery, database, table)
	if err != nil {
		return nil, err
	}
	for i, column := range columns {
		if column.Default != nil {
			columns[i].Default = new(strings.TrimSpace(*column.Default))
		}
	}
	return columns, nil
}

// tableMark marks a query of the table view for the connection to resolve (see
// values.go). The starter query for the SQL editor is not marked: it is the
// user's text once they have it.
const tableMark = "/*opskat:table*/"

// FirstPageSQL is the first page the table view opens with: no filter or order.
func (d dialect) FirstPageSQL(tableRef string, limit int) string {
	return tableMark + d.StarterSQL(tableRef, limit)
}

func (dialect) StarterSQL(tableRef string, limit int) string {
	return fmt.Sprintf("SELECT * FROM %s FETCH FIRST %d ROWS ONLY", tableRef, limit)
}

func (dialect) TablePageSQL(sel sqldb.TableSelect) string {
	query := "SELECT * FROM " + sel.TableRef + sqldb.WhereClause(sel.Where)
	if sel.OrderBy != "" {
		query += " ORDER BY " + sel.OrderBy
	}
	return tableMark + fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, sel.Offset, sel.Limit)
}
