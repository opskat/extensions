package main

// dialect is Oracle's SQL for the shared session (pkg/dbdriver/sqldb).
//
// Oracle cannot page the query panel's query by wrapping it in a subquery
// (duplicate column names, FOR UPDATE and a trailing comment all break it), so
// CountSQL and PagedSQL only mark the query; the driver's connection counts it,
// or runs it as written and stops reading at the end of the page (exec.go).
type dialect struct{}

// CountSQL marks query for the connection to count.
func (dialect) CountSQL(query string) string { return markCount(query) }

// PagedSQL marks query for the connection to return limit rows from offset on.
func (dialect) PagedSQL(query string, offset, limit int) string {
	return markPage(query, offset, limit)
}
