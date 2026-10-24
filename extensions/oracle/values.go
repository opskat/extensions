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
// SELECT list) and the precision of each TIMESTAMP. The dialect marks the query
// (tableMark) and the connection resolves it here; the page is still
// "SELECT * FROM <table> ..." when no column needs serializing. The export's read
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

func (c *conn) queryTable(ctx context.Context, text string) (driver.Rows, error) {
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
		fractions[col.name] = col.fraction
	}
	if serialized {
		query = "SELECT " + strings.Join(list, ", ") + " FROM " + rest
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
