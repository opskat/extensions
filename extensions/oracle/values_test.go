package main

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	goora "github.com/sijms/go-ora/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	utc      = time.UTC
	plus8    = time.FixedZone("", 8*3600)
	moment   = time.Date(2026, 10, 23, 1, 2, 3, 100_000_000, utc)
	wholeSec = time.Date(2026, 10, 23, 1, 2, 3, 0, utc)
)

func TestDisplayTemporalValues(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		typeName string
		fraction int
		want     any
	}{
		{"DATE has no fraction", moment, "DATE", -1, "2026-10-23 01:02:03"},
		{"TIMESTAMP shows the column's precision", moment, "TIMESTAMP", 3, "2026-10-23 01:02:03.100"},
		{"TIMESTAMP keeps zeros of the precision", wholeSec, "TIMESTAMP", 6, "2026-10-23 01:02:03.000000"},
		{"TIMESTAMP(0) has no point", wholeSec, "TimeStampDTY", 0, "2026-10-23 01:02:03"},
		{"TIMESTAMP(9)", time.Date(2026, 10, 23, 1, 2, 3, 123_456_789, utc), "TIMESTAMP", 9, "2026-10-23 01:02:03.123456789"},
		{"unknown precision trims zeros", moment, "TIMESTAMP", -1, "2026-10-23 01:02:03.1"},
		{"unknown precision without fraction", wholeSec, "TIMESTAMP", -1, "2026-10-23 01:02:03"},
		{"WITH TIME ZONE shows the offset", moment.In(plus8), "TimeStampTZ_DTY", 3, "2026-10-23 09:02:03.100 +08:00"},
		{"WITH TIME ZONE at UTC", wholeSec, "TIMESTAMPTZ", 0, "2026-10-23 01:02:03 +00:00"},
		{"WITH LOCAL TIME ZONE shows the offset", moment.In(plus8), "TimeStampeLTZ", 6, "2026-10-23 09:02:03.100000 +08:00"},
		{"NULL", nil, "TIMESTAMP", 6, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, displayValue(tc.value, tc.typeName, tc.fraction))
		})
	}
}

// vectorType is the name go-ora reports for a VECTOR column: its type list has no
// name for VECTOR, so it is the number.
var vectorType = goora.VECTOR.String()

func TestDisplayOtherValues(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		typeName string
		want     any
	}{
		{"NUMBER is go-ora's exact text", "12345678901234567890.123456789", "NUMBER", "12345678901234567890.123456789"},
		{"CHAR keeps its padding", "ab   ", "NCHAR", "ab   "},
		{"INTERVAL is text", "+01 02:03:04.000000", "IntervalDS_DTY", "+01 02:03:04.000000"},
		{"BINARY_FLOAT is its shortest text", float32(0.1), "IBFloat", "0.1"},
		{"BINARY_DOUBLE is its shortest text", 0.1, "IBDouble", "0.1"},
		{"BINARY_DOUBLE large", 1e300, "IBDouble", "1e+300"},
		{"BINARY_DOUBLE NaN", math.NaN(), "IBDouble", "Nan"},
		{"BINARY_DOUBLE +Inf", math.Inf(1), "IBDouble", "Inf"},
		{"BINARY_DOUBLE -Inf", math.Inf(-1), "IBDouble", "-Inf"},
		{"BOOLEAN true", true, "Boolean", "true"},
		{"BOOLEAN false", false, "Boolean", "false"},
		{"VECTOR float32", []float32{1, 2.5}, vectorType, "[1,2.5]"},
		{"VECTOR float64", []float64{0.1, -3}, vectorType, "[0.1,-3]"},
		{"VECTOR int8 is signed", []byte{0xff, 1}, vectorType, "[-1,1]"},
		{"VECTOR empty", []float32{}, vectorType, "[]"},
		{"RAW stays its bytes", []byte{0x00, 'A', 0x7f}, "RAW", []byte{0x00, 'A', 0x7f}},
		{"BLOB stays its bytes", []byte("blob"), "OCIBlobLocator", []byte("blob")},
		{"NULL", nil, "RAW", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, displayValue(tc.value, tc.typeName, -1))
		})
	}
}

// ---- through the connection ----

func TestQueryResultsShowOracleValues(t *testing.T) {
	s, _, mock := newExecSession(t)
	rows := sqlmock.NewRowsWithColumnDefinition(
		sqlmock.NewColumn("D").OfType("DATE", time.Time{}),
		sqlmock.NewColumn("TS").OfType("TIMESTAMP", time.Time{}),
		sqlmock.NewColumn("TSTZ").OfType("TimeStampTZ_DTY", time.Time{}),
		sqlmock.NewColumn("R").OfType("RAW", []byte{}),
		sqlmock.NewColumn("B").OfType("Boolean", false),
		sqlmock.NewColumn("N").OfType("NUMBER", ""),
		sqlmock.NewColumn("C").OfType("NCHAR", ""),
		sqlmock.NewColumn("X").OfType("NCHAR", ""),
	).AddRow(moment, moment, moment.In(plus8), []byte{0x00, 'A', 0x7f}, true, "0.30000000000000000001", "ab   ", nil)
	mock.ExpectQuery("SELECT * FROM ALL_TYPES").WillReturnRows(rows)

	res, err := s.Execute(context.Background(), "SELECT * FROM ALL_TYPES")
	require.NoError(t, err)
	require.Len(t, res.Rows.Rows, 1)
	assert.Equal(t, []any{
		"2026-10-23 01:02:03",
		"2026-10-23 01:02:03.1",
		"2026-10-23 09:02:03.1 +08:00",
		"\x00A\x7f", // binary is its bytes: Literals.Binary hex-encodes the displayed text
		"true",
		"0.30000000000000000001",
		"ab   ",
		nil,
	}, res.Rows.Rows[0])
	require.NoError(t, mock.ExpectationsWereMet())
}

const emp = `"HR"."EMP"`

// expectFilterColumnLookup is the column and literal-rule lookup a filter with a
// value sends first (dialect.TypedFilterValues).
func expectFilterColumnLookup(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(tableColumnsQuery).WithArgs("HR", "EMP").
		WillReturnRows(columnRows().AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0"))
}

func expectTableColumns(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery(tableColumnTypesQuery).WithArgs("HR", "EMP").WillReturnRows(rows)
}

func tableColumnTypes() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "DATA_SCALE"})
}

// The table view serializes the types go-ora cannot read (XMLType reads back as
// NULL, JSON does not decode) in the SELECT list, and shows a TIMESTAMP with the
// column's precision.
func TestTableViewSerializesXMLJSONAndVectorColumns(t *testing.T) {
	s, _, mock := newExecSession(t)
	expectFilterColumnLookup(mock)
	expectTableColumns(mock, tableColumnTypes().
		AddRow("ID", "NUMBER", nil).
		AddRow("DOC", "XMLTYPE", nil).
		AddRow("TS", "TIMESTAMP(3)", "3").
		AddRow(`C"FG`, "JSON", nil).
		AddRow("EMB", "VECTOR", nil))
	mock.ExpectQuery(`SELECT "ID", XMLSERIALIZE(CONTENT "DOC" AS CLOB), "TS", ` +
		`JSON_SERIALIZE("C""FG" RETURNING CLOB), FROM_VECTOR("EMB" RETURNING CLOB) ` +
		`FROM ` + emp + ` WHERE "ID" = '1' ORDER BY "ID" ASC OFFSET 20 ROWS FETCH NEXT 20 ROWS ONLY`).
		WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
			sqlmock.NewColumn("ID").OfType("NUMBER", ""),
			sqlmock.NewColumn(`XMLSERIALIZE(CONTENT"DOC"ASCLOB)`).OfType("OCIClobLocator", ""),
			sqlmock.NewColumn("TS").OfType("TIMESTAMP", time.Time{}),
			sqlmock.NewColumn(`JSON_SERIALIZE("C""FG"RETURNINGCLOB)`).OfType("OCIClobLocator", ""),
			sqlmock.NewColumn(`FROM_VECTOR("EMB"RETURNINGCLOB)`).OfType("OCIClobLocator", ""),
		).AddRow("1", "<a>1</a>", wholeSec, `{"k":1}`, "[1,2]"))

	rows, err := s.TableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "ID", Operator: dbdriver.OpEqual, Value: "1"}},
		Sorts:   []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
	}, dbdriver.Page{Index: 1, Size: 20})
	require.NoError(t, err)
	assert.Equal(t, []string{"ID", "DOC", "TS", `C"FG`, "EMB"}, rows.Columns)
	assert.Equal(t, [][]any{{"1", "<a>1</a>", "2026-10-23 01:02:03.000", `{"k":1}`, "[1,2]"}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Exporting a table (CSV / Excel / SQL) reads it in one unpaged query that the
// shared session writes unmarked; it reads the columns the way the table view
// does, so an exported XMLType is its text rather than NULL and a JSON column
// does not fail to decode. A filter value is a literal, whatever it spells.
func TestTableExportSerializesXMLJSONAndVectorColumns(t *testing.T) {
	s, _, mock := newExecSession(t)
	expectFilterColumnLookup(mock)
	expectTableColumns(mock, tableColumnTypes().
		AddRow("ID", "NUMBER", nil).
		AddRow("DOC", "XMLTYPE", nil).
		AddRow("TS", "TIMESTAMP(3)", "3").
		AddRow("CFG", "JSON", nil))
	mock.ExpectQuery(`SELECT "ID", XMLSERIALIZE(CONTENT "DOC" AS CLOB), "TS", ` +
		`JSON_SERIALIZE("CFG" RETURNING CLOB) FROM ` + emp + ` WHERE "ID" = 'union' ORDER BY "ID" ASC`).
		WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
			sqlmock.NewColumn("ID").OfType("NUMBER", ""),
			sqlmock.NewColumn(`XMLSERIALIZE(CONTENT"DOC"ASCLOB)`).OfType("OCIClobLocator", ""),
			sqlmock.NewColumn("TS").OfType("TIMESTAMP", time.Time{}),
			sqlmock.NewColumn(`JSON_SERIALIZE("CFG"RETURNINGCLOB)`).OfType("OCIClobLocator", ""),
		).AddRow("1", "<a>1</a>", wholeSec, `{"k":1}`))

	stream, err := s.StreamTableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "ID", Operator: dbdriver.OpEqual, Value: "union"}},
		Sorts:   []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = stream.Close() })
	assert.Equal(t, []string{"ID", "DOC", "TS", "CFG"}, stream.Columns())
	require.True(t, stream.Next(), "stream error: %v", stream.Err())
	assert.Equal(t, []any{"1", "<a>1</a>", "2026-10-23 01:02:03.000", `{"k":1}`}, stream.Row())
	assert.False(t, stream.Next())
	require.NoError(t, stream.Err())
	require.NoError(t, mock.ExpectationsWereMet())
}

// A text holding the table's query and another statement is not an export read:
// it runs as the statements it holds.
func TestTableQueryInAScriptIsNotRewritten(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectExec(`SELECT * FROM ` + emp).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT 2 FROM DUAL`).WillReturnRows(sqlmock.NewRows([]string{"2"}).AddRow("2"))
	_, err := s.Execute(context.Background(), `SELECT * FROM `+emp+`; SELECT 2 FROM DUAL`)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A query of the export's shape combined with another by a set operator is the
// user's own: it is not rewritten, since the serialized SELECT list would not match
// the other query's columns (ORA-01790).
func TestTableQueryWithSetOperatorIsNotRewritten(t *testing.T) {
	for _, op := range []string{"UNION ALL", "union", "INTERSECT", "MINUS", "EXCEPT"} {
		t.Run(op, func(t *testing.T) {
			s, _, mock := newExecSession(t)
			query := `SELECT * FROM ` + emp + ` WHERE "ID" = '1' ` + op + ` SELECT * FROM "HR"."EMP2"`
			mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"ID"}).AddRow("1"))
			_, err := s.Execute(context.Background(), query)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// A table without such a column is read as it is: SELECT *.
func TestTableViewKeepsSelectStarWithoutSpecialColumns(t *testing.T) {
	s, _, mock := newExecSession(t)
	expectTableColumns(mock, tableColumnTypes().
		AddRow("ID", "NUMBER", nil).
		AddRow("TS", "TIMESTAMP(6)", "6").
		AddRow("TSZ", "TIMESTAMP(2) WITH TIME ZONE", "2").
		AddRow("D", "DATE", nil))
	mock.ExpectQuery(`SELECT * FROM ` + emp + ` OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY`).
		WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
			sqlmock.NewColumn("ID").OfType("NUMBER", ""),
			sqlmock.NewColumn("TS").OfType("TIMESTAMP", time.Time{}),
			sqlmock.NewColumn("TSZ").OfType("TimeStampTZ_DTY", time.Time{}),
			sqlmock.NewColumn("D").OfType("DATE", time.Time{}),
		).AddRow("1", wholeSec, moment.In(plus8), moment))

	rows, err := s.TableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 20})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"1", "2026-10-23 01:02:03.000000", "2026-10-23 09:02:03.10 +08:00", "2026-10-23 01:02:03"}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The table view shows a 23ai BOOLEAN column as true / false: go-ora reports it as
// a NUMBER (1 / 0), so the dictionary's BOOLEAN says which SELECT-list column to
// spell out. A NUMBER column is left as it is.
func TestTableViewShowsBooleanColumnsAsTrueAndFalse(t *testing.T) {
	s, _, mock := newExecSession(t)
	expectTableColumns(mock, tableColumnTypes().
		AddRow("FLAG", "BOOLEAN", nil).
		AddRow("N", "NUMBER", nil))
	mock.ExpectQuery(`SELECT CASE WHEN "FLAG" THEN 'true' WHEN NOT "FLAG" THEN 'false' END, "N" FROM ` + emp +
		` OFFSET 0 ROWS FETCH NEXT 20 ROWS ONLY`).
		WillReturnRows(sqlmock.NewRows([]string{`CASEWHEN"FLAG"THEN'TRUE'WHENNOT"FLAG"THEN'FALSE'END`, "N"}).AddRow("true", "1"))

	rows, err := s.TableRows(context.Background(), "HR", "EMP", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 20})
	require.NoError(t, err)
	assert.Equal(t, []string{"FLAG", "N"}, rows.Columns)
	assert.Equal(t, [][]any{{"true", "1"}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Opening a table reads its first page the same way.
func TestOpenTableFirstPageIsShownLikeTheTableView(t *testing.T) {
	s, _, mock := newExecSession(t)
	mock.ExpectQuery(primaryKeysQuery).WithArgs("HR", "EMP").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("ID"))
	mock.ExpectQuery(tableColumnsQuery).WithArgs("HR", "EMP").WillReturnRows(columnRows().
		AddRow("ID", "NUMBER(10)", "NO", nil, nil, "0", "0", "0").
		AddRow("DOC", "XMLTYPE", "YES", nil, nil, "0", "0", "0"))
	mock.ExpectQuery(`SELECT COUNT(*) FROM ` + emp).WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow("1"))
	expectTableColumns(mock, tableColumnTypes().AddRow("ID", "NUMBER", nil).AddRow("DOC", "XMLTYPE", nil))
	mock.ExpectQuery(`SELECT "ID", XMLSERIALIZE(CONTENT "DOC" AS CLOB) FROM ` + emp + ` FETCH FIRST 50 ROWS ONLY`).
		WillReturnRows(sqlmock.NewRows([]string{"ID", `XMLSERIALIZE(CONTENT"DOC"ASCLOB)`}).AddRow("1", "<a/>"))

	opened, err := s.OpenTable(context.Background(), "HR", "EMP", 50)
	require.NoError(t, err)
	assert.Equal(t, []string{"ID", "DOC"}, opened.FirstPage.Columns)
	assert.Equal(t, [][]any{{"1", "<a/>"}}, opened.FirstPage.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// The SQL editor's starter query and a query typed by the user are the user's own
// text: they are not rewritten.
func TestStarterAndFreeSQLAreNotRewritten(t *testing.T) {
	s, _, mock := newExecSession(t)
	query, err := s.StarterQuery(context.Background(), "HR", "EMP", 100)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM `+emp+` FETCH FIRST 100 ROWS ONLY`, query)

	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"ID"}).AddRow("1"))
	_, err = s.Execute(context.Background(), query)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
