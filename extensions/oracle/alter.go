package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Altering a table. Each change is one statement, none ending in ';'. Oracle's
// RENAME TO takes the new name unqualified (the table stays in its schema), and
// MODIFY NULL / NOT NULL fails when the column already has that nullability
// (ORA-01451 / ORA-01442), so the session's comparison decides what is emitted:
// only what differs reaches here.
var _ sqldb.TableAlterer = dialect{}

var alterAttributes = []dbdriver.AlterAttribute{
	dbdriver.AlterTableComment, dbdriver.AlterColumnType, dbdriver.AlterColumnNullable,
	dbdriver.AlterColumnDefault, dbdriver.AlterColumnComment,
}

func (dialect) AlterAttributes() []dbdriver.AlterAttribute { return alterAttributes }

const tableCommentQuery = "SELECT comments AS COMMENTS FROM all_tab_comments WHERE owner = " + ownerIs + " AND table_name = :2"

// AlterableTable reads the columns the way the table view does (types as
// declared, default trimmed) and the table's comment, which is NULL when unset.
func (d dialect) AlterableTable(ctx context.Context, conn *sql.Conn, database, table string) (*sqldb.AlterableTable, error) {
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, err
	}
	out := &sqldb.AlterableTable{Name: table, Columns: make([]sqldb.AlterableColumn, 0, len(columns))}
	for _, column := range columns {
		definition := dbdriver.ColumnDefinition{Name: column.Name, Type: column.Type, Nullable: column.Nullable, Comment: column.Comment}
		if column.Default != nil {
			definition.DefaultValue = *column.Default
		}
		out.Columns = append(out.Columns, sqldb.AlterableColumn{ColumnDefinition: definition})
	}
	var comment sql.NullString
	err = conn.QueryRowContext(ctx, tableCommentQuery, database, table).Scan(&comment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out.Comment = comment.String
	return out, nil
}

// AlterTableSQL orders the statements so each names objects as they are by then:
// rename the table, add columns, rename columns, modify (type, nullability,
// default), comments, drop columns, the table comment.
func (dialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change sqldb.TableChange) ([]string, error) {
	target := change.TargetRef
	var statements, additions, renames, modifications, comments []string
	if change.Rename != "" {
		statements = append(statements, "ALTER TABLE "+change.TableRef+" RENAME TO "+quoteIdent(change.Rename))
	}
	for _, column := range change.Columns {
		c := column.Column
		name := quoteIdent(c.Name)
		modify := "ALTER TABLE " + target + " MODIFY " + name
		if column.IsNew() {
			definition, err := sqldb.ColumnDefinitionSQL(sqldb.DefinitionLiterals, quoteIdent, c)
			if err != nil {
				return nil, err
			}
			additions = append(additions, "ALTER TABLE "+target+" ADD "+definition)
			if strings.TrimSpace(c.Comment) != "" {
				statement, err := commentOn("COLUMN "+target+"."+name, c.Comment)
				if err != nil {
					return nil, err
				}
				comments = append(comments, statement)
			}
			continue
		}
		if column.Renamed {
			renames = append(renames, "ALTER TABLE "+target+" RENAME COLUMN "+quoteIdent(column.Current.Name)+" TO "+name)
		}
		if column.TypeChanged {
			modifications = append(modifications, modify+" "+strings.TrimSpace(c.Type))
		}
		if column.NullableChanged {
			if c.Nullable {
				modifications = append(modifications, modify+" NULL")
			} else {
				modifications = append(modifications, modify+" NOT NULL")
			}
		}
		if column.DefaultChanged {
			if strings.TrimSpace(c.DefaultValue) == "" {
				modifications = append(modifications, modify+" DEFAULT NULL")
			} else {
				literal, err := sqldb.DefinitionLiterals.Default(c.DefaultValue, c.Type)
				if err != nil {
					return nil, err
				}
				modifications = append(modifications, modify+" DEFAULT "+literal)
			}
		}
		if column.CommentChanged {
			statement, err := commentOn("COLUMN "+target+"."+name, c.Comment)
			if err != nil {
				return nil, err
			}
			comments = append(comments, statement)
		}
	}

	statements = append(statements, additions...)
	statements = append(statements, renames...)
	statements = append(statements, modifications...)
	statements = append(statements, comments...)
	for _, drop := range change.Drops {
		statements = append(statements, "ALTER TABLE "+target+" DROP COLUMN "+quoteIdent(drop))
	}
	if change.Comment != nil {
		statement, err := commentOn("TABLE "+target, *change.Comment)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

// commentOn sets a comment; an empty one is written as ”, which Oracle stores as no comment.
func commentOn(object, comment string) (string, error) {
	literal, err := sqldb.DefinitionLiterals.Literal(strings.TrimSpace(comment), "")
	if err != nil {
		return "", err
	}
	return "COMMENT ON " + object + " IS " + literal, nil
}
