package main

import (
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Import: Oracle 19c has no multi-row VALUES, INSERT IGNORE or ON CONFLICT, so
// every row is a statement of its own (a failed row then names its Oracle error
// alone) and append/update and append/skip are one MERGE per row. The statements
// end without ';' (the connector hands them to Oracle as they are) and values are
// the session's literals (write.go). There is no identity override switch and no
// foreign-key toggle (Descriptor.CanDisableForeignKeyChecks stays false).
var _ sqldb.Importer = dialect{}

// ColumnCategory sorts a column type, as TableColumns spells it ("NUMBER(10)",
// "TIMESTAMP(6) WITH TIME ZONE"), by how the import converts a source value. An
// Oracle DATE holds a time of day, so it is a date-time.
func (dialect) ColumnCategory(columnType string) dbdriver.ColumnCategory {
	name := typeName(columnType)
	switch name {
	case "NUMBER", "FLOAT", "BINARY_FLOAT", "BINARY_DOUBLE", "INTEGER", "INT", "SMALLINT", "DECIMAL", "NUMERIC", "REAL":
		return dbdriver.CategoryNumber
	case "DATE":
		return dbdriver.CategoryDateTime
	}
	if strings.HasPrefix(name, "TIMESTAMP") {
		return dbdriver.CategoryDateTime
	}
	if _, _, ok := literals.Binary(columnType); ok {
		return dbdriver.CategoryBinary
	}
	return dbdriver.CategoryText
}

func (dialect) ImportInsertSQL(ins sqldb.ImportInsert) []string {
	statements := make([]string, len(ins.Rows))
	for i, row := range ins.Rows {
		if ins.Conflict == sqldb.ConflictNone {
			statements[i] = ins.Head("INSERT INTO") + " VALUES (" + strings.Join(row, ", ") + ")"
			continue
		}
		statements[i] = mergeSQL(ins, row)
	}
	return statements
}

// mergeSQL inserts one row, or when its key exists updates the Updates columns
// (ConflictUpdate) or leaves it alone (ConflictSkip).
func mergeSQL(ins sqldb.ImportInsert, row []string) string {
	source := make([]string, len(ins.Columns))
	values := make([]string, len(ins.Columns))
	for i, column := range ins.Columns {
		source[i] = row[i] + " AS " + column
		values[i] = "s." + column
	}
	on := make([]string, len(ins.Keys))
	for i, key := range ins.Keys {
		on[i] = "t." + key + " = s." + key
	}
	matched := ""
	if ins.Conflict == sqldb.ConflictUpdate {
		set := make([]string, len(ins.Updates))
		for i, column := range ins.Updates {
			set[i] = "t." + column + " = s." + column
		}
		matched = "WHEN MATCHED THEN UPDATE SET " + strings.Join(set, ", ") + " "
	}
	return "MERGE INTO " + ins.TableRef + " t USING (SELECT " + strings.Join(source, ", ") + " FROM DUAL) s ON (" +
		strings.Join(on, " AND ") + ") " + matched +
		"WHEN NOT MATCHED THEN INSERT (" + strings.Join(ins.Columns, ", ") + ") VALUES (" + strings.Join(values, ", ") + ")"
}
