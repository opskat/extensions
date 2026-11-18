package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valuesTableDDL is a table with a column of each type a dump writes a value for.
const valuesTableDDL = "CREATE TABLE VALS (ID NUMBER PRIMARY KEY, N NUMBER, NP NUMBER(12,4), FL FLOAT(126), " +
	"BF BINARY_FLOAT, BD BINARY_DOUBLE, D DATE, TS TIMESTAMP(6), TS9 TIMESTAMP(9), TS0 TIMESTAMP(0), " +
	"TSTZ TIMESTAMP(6) WITH TIME ZONE, TZ0 TIMESTAMP(0) WITH TIME ZONE, LTZ TIMESTAMP(6) WITH LOCAL TIME ZONE, " +
	"DS INTERVAL DAY(3) TO SECOND(6), YM INTERVAL YEAR(3) TO MONTH, CH CHAR(6 CHAR), VC VARCHAR2(100 CHAR), " +
	"NCH NCHAR(6), NVC NVARCHAR2(100), C CLOB, NC NCLOB, RW RAW(32), B BLOB)"

// valuesFixture fills VALS: fractions to nanoseconds, positive, negative and
// region-name zones, negative intervals, the binary floats' extremes and special
// values, Chinese text (in the 19c GBK database too), NULL and the empty string
// (NULL to Oracle), and in row 9 a CLOB, an NCLOB and a BLOB of a few hundred
// KB each, with multibyte characters, quotes and line breaks all along them.
var valuesFixture = []string{
	"INSERT INTO VALS VALUES (1, 12345678901234567890.123456789, -12.5, 0.1, 1.5f, 0.1d, " +
		"TIMESTAMP '2026-10-23 01:02:03', TIMESTAMP '2026-10-23 01:02:03.123456', TIMESTAMP '2026-10-23 01:02:03.123456789', " +
		"TIMESTAMP '2026-10-23 01:02:03', TIMESTAMP '2026-10-23 01:02:03.100200 +08:00', TIMESTAMP '2026-10-23 01:02:03 -05:30', " +
		"TIMESTAMP '2026-10-23 01:02:03.654321 +02:00', INTERVAL '123 04:05:06.789012' DAY(3) TO SECOND(6), " +
		"INTERVAL '120-11' YEAR(3) TO MONTH, 'ab', 'O''Reilly 中文', N'中文', N'名字''s 数据', 'clob 文本', N'nclob 文本', " +
		"HEXTORAW('00FF417F'), HEXTORAW('DEADBEEF'))",
	"INSERT INTO VALS (ID, N, BF, BD, TSTZ, TZ0, LTZ, DS, YM, VC) VALUES (2, -0.000000001234, BINARY_FLOAT_NAN, " +
		"-BINARY_DOUBLE_INFINITY, TIMESTAMP '2026-01-01 10:00:00.5 America/Los_Angeles', TIMESTAMP '1999-12-31 23:59:59 -09:30', " +
		"TIMESTAMP '2026-03-29 02:30:00 -05:30', INTERVAL '-0 00:00:01.25' DAY TO SECOND, INTERVAL '-3-11' YEAR TO MONTH, '')",
	"INSERT INTO VALS (ID, N, BF, BD) VALUES (3, 1e125, BINARY_FLOAT_INFINITY, BINARY_DOUBLE_NAN)",
	"INSERT INTO VALS (ID, N, BF, BD) VALUES (4, -9.99e-120, -BINARY_FLOAT_INFINITY, BINARY_DOUBLE_INFINITY)",
	"INSERT INTO VALS (ID, BF, BD) VALUES (5, 3.40282347e+38f, 1.7976931348623157e+308d)",
	"INSERT INTO VALS (ID, BF, BD) VALUES (6, 1.40129846e-45f, 4.9406564584124654e-324d)",
	"INSERT INTO VALS (ID, D, TS) VALUES (7, TIMESTAMP '0001-01-01 00:00:00', TIMESTAMP '9999-12-31 23:59:59.999999')",
	"INSERT INTO VALS (ID, VC, NVC, C, NC) VALUES (8, '', '', '', '')",
	"DECLARE c CLOB; n NCLOB; b BLOB; BEGIN " +
		"DBMS_LOB.CREATETEMPORARY(c, TRUE); DBMS_LOB.CREATETEMPORARY(n, TRUE); DBMS_LOB.CREATETEMPORARY(b, TRUE); " +
		"FOR i IN 1..6000 LOOP DBMS_LOB.APPEND(c, TO_CLOB('第' || i || '行 data ''q'' 数据' || CASE WHEN MOD(i, 7) = 0 THEN CHR(10) END)); END LOOP; " +
		"FOR i IN 1..4000 LOOP DBMS_LOB.APPEND(n, TO_NCLOB(N'名字' || i || N' ''n'' 数' || CASE WHEN MOD(i, 5) = 0 THEN CHR(10) END)); END LOOP; " +
		"FOR i IN 1..25000 LOOP DBMS_LOB.WRITEAPPEND(b, 4, UTL_RAW.CAST_FROM_BINARY_INTEGER(MOD(i * 2654435761, 4294967296) - 2147483648)); END LOOP; " +
		"INSERT INTO VALS (ID, C, NC, B, VC) VALUES (9, c, n, b, 'large'); END;",
}

// valueRows is owner's VALS, each value in a form that tells it exactly: numbers
// and dates in full, binary floats as their bits, zones as offsets (a region
// name is shown, and so dumped, as its offset at that instant), local time zones
// as the instant in UTC, text with its byte length, LOBs by their length.
func valueRows(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	return textRows(t, admin, "SELECT ID, TO_CHAR(N, 'TM'), TO_CHAR(NP, 'TM'), TO_CHAR(FL, 'TM'), "+
		"RAWTOHEX(UTL_RAW.CAST_FROM_BINARY_FLOAT(BF)), RAWTOHEX(UTL_RAW.CAST_FROM_BINARY_DOUBLE(BD)), "+
		"TO_CHAR(D, 'SYYYY-MM-DD HH24:MI:SS'), TO_CHAR(TS, 'SYYYY-MM-DD HH24:MI:SS.FF9'), TO_CHAR(TS9, 'YYYY-MM-DD HH24:MI:SS.FF9'), "+
		"TO_CHAR(TS0, 'YYYY-MM-DD HH24:MI:SS.FF9'), TO_CHAR(TSTZ, 'YYYY-MM-DD HH24:MI:SS.FF9 TZH:TZM'), "+
		"TO_CHAR(TZ0, 'YYYY-MM-DD HH24:MI:SS.FF9 TZH:TZM'), TO_CHAR(SYS_EXTRACT_UTC(LTZ), 'YYYY-MM-DD HH24:MI:SS.FF9'), "+
		"TO_CHAR(DS), TO_CHAR(YM), CH, LENGTHB(CH), VC, LENGTHB(VC), NCH, NVC, LENGTHB(NVC), "+
		"DBMS_LOB.GETLENGTH(C), DBMS_LOB.GETLENGTH(NC), DBMS_LOB.GETLENGTH(B), RAWTOHEX(RW), "+
		"DBMS_LOB.SUBSTR(C, 30, 1), DBMS_LOB.SUBSTR(NC, 30, 1) FROM "+quoteIdent(owner)+".VALS ORDER BY ID")
}

// sameLobs is, per row, whether each LOB of dst's VALS has the same content as
// src's ("same", or "null" when both are NULL).
func sameLobs(t *testing.T, admin *sql.DB, src, dst string) []string {
	t.Helper()
	same := func(column string) string {
		return fmt.Sprintf("CASE WHEN s.%[1]s IS NULL AND d.%[1]s IS NULL THEN 'null' "+
			"WHEN DBMS_LOB.COMPARE(s.%[1]s, d.%[1]s) = 0 THEN 'same' ELSE 'differs' END", column)
	}
	return textRows(t, admin, "SELECT s.ID, "+same("C")+", "+same("NC")+", "+same("B")+" FROM "+
		quoteIdent(src)+".VALS s LEFT JOIN "+quoteIdent(dst)+".VALS d ON d.ID = s.ID ORDER BY s.ID")
}

// A table with every value type dumped with its data runs into another user and
// holds the same values: the statements spell each value with an explicit format
// (the file also runs after the session's NLS formats and decimal separator are
// changed), national text with N, binary floats' special values as constants,
// and the row with large LOBs as a PL/SQL block. The same block, copied as
// INSERT and run as a statement, recreates the row.
func TestIntegrationDumpValuesRoundTrip(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		adminInfo := adminTarget(t, target)
		admin := adminInfo.open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_VAL_S"+suffix, "OPSKAT_VAL_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		src, dst := sqldb.NewSession(srcDB, dialect{}), sqldb.NewSession(dstDB, dialect{})
		for _, statement := range append([]string{valuesTableDDL}, valuesFixture...) {
			_, err := src.Execute(integrationContext(t), statement)
			require.NoError(t, err, statement)
		}
		rows := valueRows(t, admin, srcName)
		require.Len(t, rows, 9)

		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := src.Dump(ctx, dbdriver.DumpRequest{Database: srcName, Whole: true, WithData: true, Writer: &out})
		require.NoError(t, err)
		file := out.String()
		for _, want := range []string{"TO_DATE('", "TO_TIMESTAMP('", "TO_TIMESTAMP_TZ('", "TO_DSINTERVAL('", "TO_YMINTERVAL('",
			"N'中文", "BINARY_FLOAT_NAN", "-BINARY_DOUBLE_INFINITY", "EMPTY_BLOB()", "DBMS_LOB.APPEND(l2, TO_NCLOB(N'",
			"DBMS_LOB.WRITEAPPEND(l3, 2000, HEXTORAW('", "\nEND;\n/\n"} {
			assert.Contains(t, file, want)
		}

		// Formats the file must not depend on: other date layouts, a comma as the
		// decimal separator.
		const otherNLS = "ALTER SESSION SET NLS_DATE_FORMAT = 'DD.MM.RR' NLS_TIMESTAMP_FORMAT = 'DD.MM.RR HH.MI.SSXFF AM' " +
			"NLS_TIMESTAMP_TZ_FORMAT = 'DD.MM.RR HH.MI.SSXFF AM TZR' NLS_NUMERIC_CHARACTERS = ',.';\n"
		runOK(t, dst, dstName, otherNLS+file)
		assert.Equal(t, rows, valueRows(t, admin, dstName))
		same := []string{"1|same|same|same", "2|null|null|null", "3|null|null|null", "4|null|null|null",
			"5|null|null|null", "6|null|null|null", "7|null|null|null", "8|null|null|null", "9|same|same|same"}
		assert.Equal(t, same, sameLobs(t, admin, srcName, dstName))

		// Copy as INSERT of the large row, run in the query editor.
		page, err := src.TableRows(integrationContext(t), srcName, "VALS", dbdriver.TableQuery{
			Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Column: "ID", Operator: dbdriver.OpEqual, Value: "9"}},
		}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		require.Len(t, page.Rows, 1)
		writer, err := src.PrepareRowStatements(integrationContext(t), srcName, "VALS")
		require.NoError(t, err)
		inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: dbdriver.RowInsert, Columns: page.Columns, Rows: []map[string]any{columnValues(page, 0)}, OmitDatabase: true,
		})
		require.NoError(t, err)
		require.Len(t, inserts, 1)
		require.True(t, strings.HasPrefix(inserts[0], "DECLARE\n"), "%.80s", inserts[0])
		_, err = dst.Execute(integrationContext(t), "DELETE FROM VALS WHERE ID = 9")
		require.NoError(t, err)
		_, err = dst.Execute(integrationContext(t), inserts[0])
		require.NoError(t, err)
		assert.Equal(t, rows, valueRows(t, admin, dstName))
		assert.Equal(t, same, sameLobs(t, admin, srcName, dstName))
	})
}
