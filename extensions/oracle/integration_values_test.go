package main

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valuesTable creates a table with one column of each type whose display is
// specified and one row of values, dropped when the test ends. The 23ai columns
// (BOOLEAN, JSON, VECTOR) exist only on 23ai.
func valuesTable(t *testing.T, target oracleTarget) (*sqldb.Session, string, string) {
	t.Helper()
	db := target.open(t, "")
	db.SetMaxOpenConns(1)
	s := sqldb.NewSession(db, dialect{})
	table := fmt.Sprintf("OPSKAT_VAL_%d", time.Now().UnixNano()%1_000_000_000)
	columns := `ID NUMBER(10) PRIMARY KEY, N NUMBER, D DATE, TS TIMESTAMP(3), TSTZ TIMESTAMP(3) WITH TIME ZONE,
		TSLTZ TIMESTAMP(6) WITH LOCAL TIME ZONE, IV INTERVAL DAY(2) TO SECOND(3), IYM INTERVAL YEAR(2) TO MONTH,
		CH CHAR(6), VC VARCHAR2(20), NVC NVARCHAR2(20), CL CLOB, NCL NCLOB, RW RAW(4), BL BLOB, XML XMLTYPE`
	values := `1, 12345678901234567890.123456789, DATE '2026-10-23' + 3723/86400, TIMESTAMP '2026-10-23 01:02:03.100',
		TIMESTAMP '2026-10-23 01:02:03.100 +08:00', TIMESTAMP '2026-10-23 01:02:03.123456',
		INTERVAL '1 02:03:04.500' DAY TO SECOND, INTERVAL '1-2' YEAR TO MONTH,
		'ab', 'text', N'text', 'clob text', N'nclob text', HEXTORAW('00417F10'), HEXTORAW('DEADBEEF'),
		XMLTYPE('<a><b>1</b></a>')`
	names := `ID, N, D, TS, TSTZ, TSLTZ, IV, IYM, CH, VC, NVC, CL, NCL, RW, BL, XML`
	if target.name == "23ai" {
		columns += `, BO BOOLEAN, J JSON, V VECTOR(3, FLOAT32)`
		values += `, TRUE, JSON('{"k":1}'), TO_VECTOR('[1,2.5,3]')`
		names += `, BO, J, V`
	}
	ctx := integrationContext(t)
	_, err := s.Execute(ctx, "CREATE TABLE "+table+" ("+columns+")")
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := s.Execute(ctx, "DROP TABLE "+table+" PURGE")
		assert.NoError(t, err)
	})
	_, err = s.Execute(ctx, "INSERT INTO "+table+" ("+names+") VALUES ("+values+")")
	require.NoError(t, err)
	_, err = s.Execute(ctx, "INSERT INTO "+table+" (ID) VALUES (2)")
	require.NoError(t, err)
	var schema string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT USER FROM DUAL`).Scan(&schema))
	return s, table, schema
}

func columnValues(rs *dbdriver.RowSet, row int) map[string]any {
	out := map[string]any{}
	for i, name := range rs.Columns {
		out[name] = rs.Rows[row][i]
	}
	return out
}

func TestIntegrationTableViewValues(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table, schema := valuesTable(t, target)
		ctx := integrationContext(t)
		rs, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{
			Sorts: []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
		}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		require.Len(t, rs.Rows, 2)

		got := columnValues(rs, 0)
		assert.Equal(t, "12345678901234567890.123456789", got["N"])
		assert.Equal(t, "2026-10-23 01:02:03", got["D"])
		assert.Equal(t, "2026-10-23 01:02:03.100", got["TS"])
		assert.Regexp(t, `^2026-10-23 01:02:03\.100 \+08:00$`, got["TSTZ"])
		// shown as the instant, in UTC: the date follows the session time zone the row was written in
		assert.Regexp(t, `^2026-10-2[23] \d\d:\d\d:\d\d\.123456 [+-]\d\d:\d\d$`, got["TSLTZ"])
		assert.Contains(t, got["IV"], "+01 02:03:04.5")
		assert.Contains(t, got["IYM"], "+01-02")
		assert.Equal(t, "ab    ", got["CH"], "CHAR keeps its padding")
		assert.Equal(t, "text", got["VC"])
		assert.Equal(t, "text", got["NVC"])
		assert.Equal(t, "clob text", got["CL"])
		assert.Equal(t, "nclob text", got["NCL"])
		assert.Equal(t, "\x00A\x7f\x10", got["RW"], "RAW is its bytes")
		assert.Contains(t, got["XML"], "<b>1</b>", "XMLType is serialized")
		if target.name == "23ai" {
			assert.Equal(t, "true", got["BO"])
			assert.JSONEq(t, `{"k":1}`, fmt.Sprint(got["J"]))
			assert.Contains(t, got["V"], "2.5")
		}

		for name, v := range columnValues(rs, 1) {
			if name != "ID" {
				assert.Nil(t, v, name)
			}
		}
	})
}

// Exporting the table streams every row with the values the table view shows:
// XMLType (and on 23ai JSON) as its text.
func TestIntegrationTableExportValues(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table, schema := valuesTable(t, target)
		stream, err := s.StreamTableRows(integrationContext(t), schema, table, dbdriver.TableQuery{
			Sorts: []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = stream.Close() })
		var rows []map[string]any
		for stream.Next() {
			row := map[string]any{}
			for i, name := range stream.Columns() {
				row[name] = stream.Row()[i]
			}
			rows = append(rows, row)
		}
		require.NoError(t, stream.Err())
		require.Len(t, rows, 2)
		assert.Contains(t, rows[0]["XML"], "<b>1</b>", "XMLType is serialized")
		assert.Equal(t, "2026-10-23 01:02:03.100", rows[0]["TS"])
		if target.name == "23ai" {
			assert.JSONEq(t, `{"k":1}`, fmt.Sprint(rows[0]["J"]))
			assert.Equal(t, "true", rows[0]["BO"], "BOOLEAN is true / false in the table view")
			assert.Nil(t, rows[1]["BO"])
		}
		assert.Nil(t, rows[1]["XML"])
	})
}

// Sorting the table view by a column it serializes or spells out sorts by the
// column itself (a 23ai JSON column sorts natively, its serialized CLOB could not)
// and the columns keep the table's names.
func TestIntegrationTableViewSortsBySpelledOutColumns(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		if target.name != "23ai" {
			t.Skip("JSON and BOOLEAN columns need Oracle 23ai")
		}
		s, table, schema := valuesTable(t, target)
		ctx := integrationContext(t)
		_, err := s.Execute(ctx, "UPDATE "+table+` SET J = JSON('{"k":0}'), BO = FALSE WHERE ID = 2`)
		require.NoError(t, err)
		for column, first := range map[string]string{"J": "2", "BO": "2"} {
			rs, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{
				Sorts: []dbdriver.SortItem{{Column: column, Direction: dbdriver.SortAscending}},
			}, dbdriver.Page{Index: 0, Size: 10})
			require.NoError(t, err, column)
			require.Len(t, rs.Rows, 2, column)
			assert.Equal(t, first, columnValues(rs, 0)["ID"], column)
			assert.Contains(t, rs.Columns, column)
		}
	})
}

// Filtering the table view by a time zone timestamp's shown value finds its row,
// a negative offset without a fraction included (the session's format reads its
// sign as the offset's, connect.go).
func TestIntegrationTableViewFiltersByShownTimeZoneValue(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_VAL_TZF",
			"ID NUMBER(10), TZ0 TIMESTAMP(0) WITH TIME ZONE, TZ3 TIMESTAMP(3) WITH TIME ZONE")
		ctx := integrationContext(t)
		_, err := s.Execute(ctx, "INSERT INTO "+table+" VALUES (1, TIMESTAMP '2026-10-23 01:02:03 -05:30', "+
			"TIMESTAMP '2026-10-23 01:02:03.100 -05:30')")
		require.NoError(t, err)
		page, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		require.Len(t, page.Rows, 1)
		shown := columnValues(page, 0)
		require.Equal(t, "2026-10-23 01:02:03 -05:30", shown["TZ0"])
		for _, column := range []string{"TZ0", "TZ3"} {
			rs, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{Filters: []dbdriver.FilterItem{{
				Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: column, Operator: dbdriver.OpEqual, Value: fmt.Sprint(shown[column]),
			}}}, dbdriver.Page{Index: 0, Size: 10})
			require.NoError(t, err, column)
			assert.Len(t, rs.Rows, 1, column)
		}
	})
}

// A TIMESTAMP(3) without a fraction still shows its three digits.
func TestIntegrationTableViewShowsColumnPrecision(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table, schema := valuesTable(t, target)
		ctx := integrationContext(t)
		_, err := s.Execute(ctx, "UPDATE "+table+" SET TSLTZ = NULL, TS = TIMESTAMP '2026-10-23 01:02:03.000' WHERE ID = 1")
		require.NoError(t, err)
		rs, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{
			Sorts: []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
		}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		assert.Equal(t, "2026-10-23 01:02:03.000", columnValues(rs, 0)["TS"])
	})
}

// Free SQL shows the same values for the types go-ora reads; the unspecified
// fraction of a TIMESTAMP shows only its non-zero digits.
func TestIntegrationQueryPanelValues(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, table, _ := valuesTable(t, target)
		res, err := s.Execute(integrationContext(t),
			"SELECT N, D, TS, CH, RW, BL, CL FROM "+table+" WHERE ID = 1")
		require.NoError(t, err)
		got := columnValues(res.Rows, 0)
		assert.Equal(t, "12345678901234567890.123456789", got["N"])
		assert.Equal(t, "2026-10-23 01:02:03", got["D"])
		assert.Equal(t, "2026-10-23 01:02:03.1", got["TS"])
		assert.Equal(t, "ab    ", got["CH"])
		assert.Equal(t, "\x00A\x7f\x10", got["RW"])
		assert.Equal(t, "\xde\xad\xbe\xef", got["BL"])
		assert.Equal(t, "clob text", got["CL"])
	})
}

// Filtering the table view by the shown value of a time zone or local time zone
// timestamp finds its rows with =, between and in-list, in the page, the count
// and the export read: the filter values are written like grid-edit values, as
// TO_TIMESTAMP_TZ, since a plain string needs the session's format (ORA-01830).
func TestIntegrationTableViewFiltersByShownZonedTimestamps(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		s, schema, table := writeSession(t, target, "OPSKAT_VAL_ZF",
			"ID NUMBER(10), LT TIMESTAMP(0) WITH LOCAL TIME ZONE, TZ TIMESTAMP(0) WITH TIME ZONE")
		ctx := integrationContext(t)
		for _, row := range []string{
			"(1, TIMESTAMP '2026-10-26 08:00:00 +00:00', TIMESTAMP '2026-10-26 08:00:00 +08:00')",
			"(2, TIMESTAMP '2026-10-27 08:00:00 +00:00', TIMESTAMP '2026-10-27 08:00:00 +08:00')",
			"(3, TIMESTAMP '2026-10-28 08:00:00 +00:00', TIMESTAMP '2026-10-28 08:00:00 +08:00')",
		} {
			_, err := s.Execute(ctx, "INSERT INTO "+table+" VALUES "+row)
			require.NoError(t, err)
		}
		page, err := s.TableRows(ctx, schema, table, dbdriver.TableQuery{
			Sorts: []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}},
		}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		shown := func(column string) []any {
			var values []any
			for i := range page.Rows {
				values = append(values, columnValues(page, i)[column])
			}
			return values
		}
		for _, column := range []string{"LT", "TZ"} {
			values := shown(column)
			require.Len(t, values, 3, column)
			condition := func(operator dbdriver.FilterOperator, value any) []dbdriver.FilterItem {
				return []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: column, Operator: operator, Value: value}}
			}
			for name, c := range map[string]struct {
				filters []dbdriver.FilterItem
				ids     []string
			}{
				"=":       {condition(dbdriver.OpEqual, values[1]), []string{"2"}},
				"between": {condition(dbdriver.OpBetween, []any{values[0], values[1]}), []string{"1", "2"}},
				"in_list": {condition(dbdriver.OpInList, []any{values[0], values[2]}), []string{"1", "3"}},
			} {
				label := column + " " + name
				query := dbdriver.TableQuery{Filters: c.filters, Sorts: []dbdriver.SortItem{{Column: "ID", Direction: dbdriver.SortAscending}}}
				rs, err := s.TableRows(ctx, schema, table, query, dbdriver.Page{Index: 0, Size: 10})
				require.NoError(t, err, label)
				var ids []string
				for i := range rs.Rows {
					ids = append(ids, fmt.Sprint(columnValues(rs, i)["ID"]))
				}
				assert.Equal(t, c.ids, ids, label)

				count, err := s.CountTableRows(ctx, schema, table, c.filters)
				require.NoError(t, err, label)
				assert.Equal(t, int64(len(c.ids)), count, label)

				stream, err := s.StreamTableRows(ctx, schema, table, query)
				require.NoError(t, err, label)
				idColumn := slices.Index(stream.Columns(), "ID")
				require.GreaterOrEqual(t, idColumn, 0, label)
				var streamed []string
				for stream.Next() {
					streamed = append(streamed, fmt.Sprint(stream.Row()[idColumn]))
				}
				require.NoError(t, stream.Err(), label)
				require.NoError(t, stream.Close(), label)
				assert.Equal(t, c.ids, streamed, label)
			}
		}
	})
}
