package main

import "fmt"

// dialect is Oracle's SQL for the shared session (pkg/dbdriver/sqldb).
//
// For now it is a stub: PagedSQL returns the query unchanged, so a paged query
// returns every row. Oracle cannot page by wrapping the query in a subquery
// (duplicate column names, FOR UPDATE and a trailing comment all break it), so
// paging moves into the driver's connection, which stops reading at the cutoff.
type dialect struct{}

// CountSQL counts the query's rows; an Oracle subquery takes no AS alias.
func (dialect) CountSQL(query string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s)", query)
}

// PagedSQL returns query unchanged (see dialect).
func (dialect) PagedSQL(query string, _, _ int) string {
	return query
}
