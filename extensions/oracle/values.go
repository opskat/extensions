package main

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	goora "github.com/sijms/go-ora/v2"
)

// How a value is shown. go-ora hands database/sql the Go value of each column;
// the shared session (sqldb) turns []byte into a string and leaves everything
// else to the JSON encoding, so the text shown is decided here, in rows.Next
// (exec.go), before sqldb sees the value:
//
//   - NUMBER / FLOAT, CHAR, VARCHAR2, CLOB, INTERVAL, ROWID: go-ora's text, which
//     is exact (a NUMBER is never a float64) and keeps a CHAR's padding.
//   - DATE: "YYYY-MM-DD HH:MM:SS". TIMESTAMP: the same with the column's
//     fractional seconds; WITH (LOCAL) TIME ZONE adds the offset "+08:00".
//   - BINARY_FLOAT / BINARY_DOUBLE: the shortest text that reads back the value
//     (NaN and infinities in Oracle's spelling: JSON cannot carry them).
//   - BOOLEAN (23ai): "true" / "false". VECTOR (23ai): "[1,2.5]".
//   - RAW / LONG RAW / BLOB: the value's bytes, untouched. sqldb shows them as the
//     string of those bytes, and the write path (sqldb Literals.Binary) writes a
//     displayed value back as the hex of the text's bytes, so the bytes are the
//     display, exactly as the MySQL driver shows a BLOB. A byte that is not valid
//     UTF-8 does not survive the JSON between the extension and the grid.
//   - NULL stays nil.
//
// Types go-ora cannot read (XMLType reads back as NULL, a native JSON does not
// decode) are serialized by the table view's SELECT list instead (queryTable).

const (
	dateLayout = "2006-01-02 15:04:05"
	zoneLayout = " -07:00"
)

// displayValue is the shown value of v, a value of a column of go-ora type
// typeName (the name database/sql reports). fraction is the column's fractional
// seconds when the dictionary told (a TIMESTAMP's precision); negative when it is
// unknown, and then the digits that are not zero are shown.
func displayValue(v driver.Value, typeName string, fraction int) driver.Value {
	switch x := v.(type) {
	case time.Time:
		return displayTime(x, typeName, fraction)
	case float32:
		return displayFloat(float64(x), 32)
	case float64:
		return displayFloat(x, 64)
	case bool:
		return strconv.FormatBool(x)
	case []float32:
		return displayVector(x, func(f float32) string { return strconv.FormatFloat(float64(f), 'g', -1, 32) })
	case []float64:
		return displayVector(x, func(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) })
	case []byte:
		// INT8 vectors come as bytes. go-ora names no VECTOR type: the name is its number.
		if typeName == goora.VECTOR.String() {
			return displayVector(x, func(b byte) string { return strconv.Itoa(int(int8(b))) })
		}
	}
	return v
}

func displayTime(t time.Time, typeName string, fraction int) string {
	upper := strings.ToUpper(typeName)
	if upper == "DATE" {
		return t.Format(dateLayout)
	}
	text := t.Format(dateLayout)
	switch {
	case fraction < 0:
		text += strings.TrimRight(fmt.Sprintf(".%09d", t.Nanosecond()), "0.")
	case fraction > 0:
		text += fmt.Sprintf(".%09d", t.Nanosecond())[:fraction+1]
	}
	if strings.HasPrefix(upper, "TIMESTAMP") && strings.Contains(upper, "TZ") {
		text += t.Format(zoneLayout)
	}
	return text
}

func displayFloat(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "Nan"
	case math.IsInf(f, 1):
		return "Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

func displayVector[T any](elements []T, format func(T) string) string {
	parts := make([]string, len(elements))
	for i, e := range elements {
		parts[i] = format(e)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ---- the table view ----

// The table view reads a page with the table's columns known: the dictionary says
// which columns hold a type go-ora cannot read (they are serialized to text in the
// SELECT list), the precision of each TIMESTAMP, and which hold a CLOB, NCLOB or
// BLOB, read, like a serialized column's text, only within maxLobBytes
// (lobGuard). The dialect marks the query (tableMark) and the connection
// resolves it here; the page is still
// "SELECT * FROM <table> ..." when no column needs either. The export's read
// (StreamTableRows) is the one table query the shared session writes without the
// mark, so a text that is just that — SELECT * FROM "SCHEMA"."TABLE" with at most
// a WHERE and an ORDER BY — is read the same way (isTableRead), also when a user
// types it. Any other query a user types is never rewritten, so free SQL keeps
// go-ora's behaviour.
//
// SELECT * leaves out invisible columns, which have no COLUMN_ID.
const tableColumnTypesQuery = "SELECT column_name, data_type, data_scale FROM all_tab_columns " +
	"WHERE owner = " + ownerIs + " AND table_name = :2 AND column_id IS NOT NULL ORDER BY column_id"

const selectStarFrom = "SELECT * FROM "

// serializers is the SELECT-list expression of a column of each type go-ora
// cannot read, by the dictionary's DATA_TYPE.
var serializers = map[string]string{
	"XMLTYPE": "XMLSERIALIZE(CONTENT %s AS CLOB)",
	"JSON":    "JSON_SERIALIZE(%s RETURNING CLOB)",
	"VECTOR":  "FROM_VECTOR(%s RETURNING CLOB)",
}

// The largest LOB value a table read reads. Every value of a read is held whole
// in the extension's memory (512 MiB, the module included) together with what is
// made of it — its JSON, a dump's statement of it (RowInsertStatement) — and
// running out of it cannot be recovered from, so a larger value stops the read before it is read
// (lobGuard). The size is the value's in Oracle: bytes for a BLOB, 2 bytes per
// character (as DBMS_LOB.GETLENGTH counts them: UTF-16 code units) for a CLOB or
// NCLOB, which a Unicode database stores as UTF-16. 16 MB is 16 × 2^20 bytes:
// a BLOB of 16,777,216 bytes, a CLOB or NCLOB of 8,388,608 characters. An
// XMLType, JSON or VECTOR value is read as the CLOB of its text (serializers),
// and measured as that CLOB.
const maxLobBytes = 16 << 20

// lobUnitBytes is the bytes per DBMS_LOB.GETLENGTH unit of each type a table
// read guards, by the dictionary's DATA_TYPE.
var lobUnitBytes = map[string]int{"BLOB": 1, "CLOB": 2, "NCLOB": 2, "XMLTYPE": 2, "JSON": 2, "VECTOR": 2}

// lobGuard is the size check of one LOB column of a table read: the SELECT list
// reads the column only when its value is within maxLobBytes, and a guard column
// at the end of the list (not shown) holds the length of a larger one. expr is
// the column as the read selects it: its quoted name, or its serialization.
type lobGuard struct {
	table, column, dataType, expr string
}

// selects are the guarded column's expression and its guard column's. The guard
// measures the value once: a serialization is not cheap.
func (g lobGuard) selects() (column, guard string) {
	limit := maxLobBytes / lobUnitBytes[g.dataType]
	return fmt.Sprintf("CASE WHEN DBMS_LOB.GETLENGTH(%[1]s) <= %[2]d THEN %[1]s END", g.expr, limit),
		fmt.Sprintf("NULLIF(GREATEST(DBMS_LOB.GETLENGTH(%[1]s), %[2]d), %[2]d)", g.expr, limit)
}

// err is the error of a value the guard column says has length (in
// DBMS_LOB.GETLENGTH units).
func (g lobGuard) err(length driver.Value) error {
	n, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(length)), 10, 64)
	if err != nil {
		return fmt.Errorf("oracle: length of column %s of table %s: %w", quoteIdent(g.column), g.table, err)
	}
	unit := "bytes"
	if g.dataType != "BLOB" {
		unit = "characters"
	}
	return fmt.Errorf("oracle: column %s of table %s holds a value of %.1f MB (%d %s); OpsKat reads a CLOB, NCLOB or BLOB "+
		"value, or the text of an XMLType, JSON or VECTOR value, of at most 16 MB (16777216 bytes, a character counting "+
		"2 bytes), so the rows of this table "+
		"cannot be read, dumped, exported or copied while it holds one", quoteIdent(g.column), g.table,
		float64(n*int64(lobUnitBytes[g.dataType]))/(1<<20), n, unit)
}

// How a whole-table read fetches LOB values. go-ora reads a query with LOB
// columns with their values inline and sizes each fetch from the columns'
// declared lengths alone, counting a LOB as 2 bytes: 128 KiB / row size + 1 rows
// (go-ora command.go, queryLobPrefetch) — thousands of rows for a table of an ID
// and a BLOB, all held at once, whatever their values take. So the read first
// asks for its largest row's LOB bytes (largestLobRow) and adds NULL columns,
// not shown, whose declared lengths bring a fetch down to about lobFetchBytes of
// values (fetchPadding).
const (
	lobFetchBytes = 8 << 20
	// goOraLobFetch is the 128 KiB go-ora divides by the row size.
	goOraLobFetch = 0x20000
	// minLobRowSize is the smallest row size go-ora sees in a guarded read: a LOB
	// (2) and its guard column (a NUMBER, 22).
	minLobRowSize = 24
)

// lobMemoryBytes is the bytes a DBMS_LOB.GETLENGTH unit of each guarded type
// takes in the extension at most: a byte, or a character's UTF-8.
var lobMemoryBytes = map[string]int64{"BLOB": 1, "CLOB": 3, "NCLOB": 3, "XMLTYPE": 3, "JSON": 3, "VECTOR": 3}

// largestLobRow is the most bytes the LOB values of one row of the read take in
// the extension (lobMemoryBytes), measured on the server without reading them.
func (c *conn) largestLobRow(ctx context.Context, guards []lobGuard, rest string) (int64, error) {
	columns := make([]string, len(guards))
	sizes := make([]string, len(guards))
	for i, g := range guards {
		columns[i] = quoteIdent(g.column)
		sizes[i] = fmt.Sprintf("NVL(DBMS_LOB.GETLENGTH(%s), 0) * %d", g.expr, lobMemoryBytes[g.dataType])
	}
	r, err := c.query(ctx, "SELECT MAX("+strings.Join(sizes, " + ")+") FROM (SELECT "+strings.Join(columns, ", ")+" FROM "+rest+")", nil)
	if err != nil {
		return 0, err
	}
	dest := make([]driver.Value, 1)
	if err := r.Next(dest); err != nil {
		return 0, errors.Join(err, r.Close())
	}
	if err := r.Close(); err != nil {
		return 0, err
	}
	if dest[0] == nil { // no rows
		return 0, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(dest[0])), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("oracle: size of the table's LOB values: %w", err)
	}
	return n, nil
}

// fetchPadding is the NULL columns that make go-ora fetch rows of up to rowBytes
// of LOB values about lobFetchBytes at a time, at least a row: declared lengths
// totalling goOraLobFetch / (rows - 1), and past goOraLobFetch for one row. None
// when go-ora fetches no more rows than that anyway.
func fetchPadding(rowBytes int64) []string {
	rows := lobFetchBytes / max(rowBytes, 1)
	if rows > goOraLobFetch/minLobRowSize {
		return nil
	}
	total := goOraLobFetch + 1
	if rows > 1 {
		total = int((goOraLobFetch + rows - 2) / (rows - 1))
	}
	var columns []string
	for ; total > 0; total -= maxTextLiteral {
		columns = append(columns, fmt.Sprintf("CAST(NULL AS VARCHAR2(%d BYTE))", min(total, maxTextLiteral)))
	}
	return columns
}

type dictColumn struct {
	name     string
	dataType string
	fraction int // fractional seconds of a TIMESTAMP, -1 otherwise
}

// booleanSelect is the SELECT-list expression that shows a 23ai BOOLEAN column
// (dictionary DATA_TYPE BOOLEAN) as true / false: go-ora reports it as a NUMBER
// (1 / 0), so only the table view, which knows the column's type, can tell it.
// The write path compares the column itself (EqualitySQL), not this text.
const booleanSelect = "CASE WHEN %[1]s THEN 'true' WHEN NOT %[1]s THEN 'false' END"

// unpaged is a read of the whole table (an export, a dump), whose fetches are
// sized to its LOB values (fetchPadding); a page is at most the page's rows.
func (c *conn) queryTable(ctx context.Context, text string, unpaged bool) (driver.Rows, error) {
	rest, ok := strings.CutPrefix(text, selectStarFrom)
	if !ok {
		return nil, fmt.Errorf("oracle: table query does not start with %q: %.40s", selectStarFrom, text)
	}
	schema, table, _, err := splitTableRef(rest)
	if err != nil {
		return nil, err
	}
	columns, err := c.tableColumns(ctx, schema, table)
	if err != nil {
		return nil, err
	}
	query := text
	list := make([]string, len(columns))
	names := make([]string, len(columns))
	serialized := false
	fractions := make(map[string]int, len(columns))
	tableRef := quoteIdent(table)
	if schema != "" {
		tableRef = quoteIdent(schema) + "." + tableRef
	}
	var guards []lobGuard
	var guardList []string
	for i, col := range columns {
		list[i], names[i] = quoteIdent(col.name), col.name
		format, ok := serializers[col.dataType]
		if col.dataType == "BOOLEAN" {
			format, ok = booleanSelect, true
		}
		if ok {
			list[i] = fmt.Sprintf(format, list[i])
			serialized = true
		}
		if _, ok := lobUnitBytes[col.dataType]; ok {
			g := lobGuard{table: tableRef, column: col.name, dataType: col.dataType, expr: list[i]}
			var guard string
			list[i], guard = g.selects()
			guards, guardList = append(guards, g), append(guardList, guard)
			serialized = true
		}
		fractions[col.name] = col.fraction
	}
	hidden := guardList
	if unpaged && len(guards) > 0 {
		rowBytes, err := c.largestLobRow(ctx, guards, rest)
		if err != nil {
			return nil, err
		}
		hidden = append(hidden, fetchPadding(rowBytes)...)
	}
	if serialized {
		query = "SELECT " + strings.Join(append(list, hidden...), ", ") + " FROM " + rest
	}
	r, err := c.query(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	if serialized {
		// The expressions have no alias, so an ORDER BY "COL" sorts by the column
		// rather than by the expression (a CLOB cannot be sorted, ORA-22848); the
		// rows carry the columns' names instead of the expressions' text.
		r.names = names
		r.guards, r.hidden = guards, len(hidden)
	}
	r.fractions = make([]int, len(r.Columns()))
	for i, name := range r.Columns() {
		f, ok := fractions[name]
		if !ok {
			f = -1
		}
		r.fractions[i] = f
	}
	return r, nil
}

// tableColumns reads the visible columns of schema.table in table order; an empty
// schema is the session's current one.
func (c *conn) tableColumns(ctx context.Context, schema, table string) ([]dictColumn, error) {
	r, err := c.queryRows(ctx, tableColumnTypesQuery, []driver.NamedValue{
		{Ordinal: 1, Value: schema}, {Ordinal: 2, Value: table},
	})
	if err != nil {
		return nil, err
	}
	var columns []dictColumn
	dest := make([]driver.Value, 3)
	for {
		err := r.Next(dest)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.Join(err, r.Close())
		}
		col := dictColumn{name: fmt.Sprint(dest[0]), dataType: fmt.Sprint(dest[1]), fraction: -1}
		if strings.HasPrefix(col.dataType, "TIMESTAMP") && dest[2] != nil {
			if col.fraction, err = strconv.Atoi(fmt.Sprint(dest[2])); err != nil {
				return nil, errors.Join(fmt.Errorf("oracle: fractional seconds of %s: %w", col.name, err), r.Close())
			}
		}
		columns = append(columns, col)
	}
	return columns, r.Close()
}

// isTableRead reports whether the statement text is the table read the shared
// session writes unmarked for an export: SELECT * FROM a reference TableRef wrote,
// then nothing, a WHERE or an ORDER BY, and no set operator, which the export
// never writes (its columns are quoted) and which would join the serialized
// SELECT list to a query whose columns do not match it (ORA-01790).
func isTableRead(text string) bool {
	rest, ok := strings.CutPrefix(text, selectStarFrom)
	if !ok {
		return false
	}
	_, _, tail, err := splitTableRef(rest)
	if err != nil || (tail != "" && !strings.HasPrefix(tail, " WHERE ") && !strings.HasPrefix(tail, " ORDER BY ")) {
		return false
	}
	masked, err := maskLiterals(tail)
	if err != nil {
		return false
	}
	for _, t := range tokenize(masked) {
		if setOperators[t.text] {
			return false
		}
	}
	return true
}

var setOperators = map[string]bool{"UNION": true, "INTERSECT": true, "MINUS": true, "EXCEPT": true}

// splitTableRef reads the "SCHEMA"."TABLE" (or bare "TABLE") that TableRef wrote
// from the start of s, and returns what follows it.
func splitTableRef(s string) (schema, table, tail string, err error) {
	table, tail, err = readQuoted(s)
	if err != nil {
		return "", "", "", err
	}
	if rest, ok := strings.CutPrefix(tail, "."); ok {
		schema = table
		if table, tail, err = readQuoted(rest); err != nil {
			return "", "", "", err
		}
	}
	return schema, table, tail, nil
}

// readQuoted reads one quoted identifier ("" is a quote inside it).
func readQuoted(s string) (name, tail string, err error) {
	if !strings.HasPrefix(s, `"`) {
		return "", "", fmt.Errorf("oracle: table reference is not a quoted name: %.40s", s)
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '"' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '"' {
			b.WriteByte('"')
			i++
			continue
		}
		return b.String(), s[i+1:], nil
	}
	return "", "", fmt.Errorf("oracle: table reference has an unclosed quote: %.40s", s)
}
