package main

import "github.com/opskat/opskat/pkg/dbdriver/sqldb"

// CREATE TABLE and DROP TABLE are the session's own and take the quoting from
// Tables (tables.go); TRUNCATE TABLE is the dialect's.
var _ sqldb.TableTruncator = dialect{}

// TruncateTableSQL empties the table. Oracle commits it and cannot roll it back.
func (dialect) TruncateTableSQL(tableRef string) string { return "TRUNCATE TABLE " + tableRef }
