package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

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
// changed), national text as UNISTR escapes, binary floats' special values as constants,
// and the row with large LOBs as a PL/SQL block (its national text and bytes in
// base64). The same block, copied as INSERT and run as a statement, recreates the
// row.
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
			`UNISTR('\4E2D\6587`, "BINARY_FLOAT_NAN", "-BINARY_DOUBLE_INFINITY", "EMPTY_BLOB()",
			"DBMS_LOB.APPEND(l2, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(UTL_ENCODE.BASE64_DECODE(UTL_RAW.CAST_TO_RAW('",
			"DBMS_LOB.WRITEAPPEND(l3, 3000, UTL_ENCODE.BASE64_DECODE(UTL_RAW.CAST_TO_RAW('", "\nEND;\n/\n"} {
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

// nationalTableDDL is a table of the national character set's types.
const nationalTableDDL = "CREATE TABLE NATS (ID NUMBER PRIMARY KEY, NCH NCHAR(10), NVC NVARCHAR2(2000), NC NCLOB)"

// nationalFixture fills NATS without N'...' literals, which a ZHS16GBK database
// converts through GBK: in UNISTR escapes, an emoji, ï and ø (outside GBK), a
// backslash, quotes and Chinese; in row 2 an NVARCHAR2 at its 2000 characters
// (4000 bytes) of them, and an NCLOB of about 66,000 characters whose repeated
// part escapes to 27 bytes, so that a 4000-byte piece ends 4 bytes short of an
// emoji's 10, with runs of 1500 ASCII characters among it; in row 3 ASCII text
// with backslashes.
var nationalFixture = []string{
	`INSERT INTO NATS VALUES (1, UNISTR('\00EF\00F8\D83D\DE00\005C\4E2D'), ` +
		`UNISTR('\5F20\4E09\D83D\DE00 \00EF\00F8 a\005Cb ''q'' \4E2D\6587'), TO_NCLOB(UNISTR('\D83D\DE00 \00EF\005C\00F8 \4E2D')))`,
	`DECLARE v NVARCHAR2(2000); n NCLOB; BEGIN DBMS_LOB.CREATETEMPORARY(n, TRUE); ` +
		`FOR i IN 1..666 LOOP v := v || UNISTR('\00EF\D83D\DE00'); END LOOP; v := v || UNISTR('\4E2D\005C'); ` +
		`FOR i IN 1..3000 LOOP DBMS_LOB.APPEND(n, TO_NCLOB(UNISTR('\D83D\DE00\00EF\4E2Dab\005C'))); ` +
		`IF MOD(i, 100) = 0 THEN DBMS_LOB.APPEND(n, TO_NCLOB(RPAD(UNISTR('x'), 1500, UNISTR('x')))); END IF; END LOOP; ` +
		`INSERT INTO NATS VALUES (2, UNISTR('\00F8'), v, n); END;`,
	`INSERT INTO NATS (ID, NCH, NVC) VALUES (3, UNISTR('a\005Cb'), UNISTR('C:\005Cpath\005C''x'''))`,
}

// nationalValues is what nationalFixture writes, per row its NCHAR (padded to
// 10 UTF-16 units), NVARCHAR2 and NCLOB ("" for NULL).
func nationalValues() [][3]string {
	var nclob strings.Builder
	for i := 1; i <= 3000; i++ {
		nclob.WriteString(`😀ï中ab\`)
		if i%100 == 0 {
			nclob.WriteString(strings.Repeat("x", 1500))
		}
	}
	nchar := func(text string) string { return text + strings.Repeat(" ", 10-len(utf16.Encode([]rune(text)))) }
	return [][3]string{
		{nchar(`ïø😀\中`), `张三😀 ïø a\b 'q' 中文`, `😀 ï\ø 中`},
		{nchar("ø"), strings.Repeat("ï😀", 666) + `中\`, nclob.String()},
		{nchar(`a\b`), `C:\path\'x'`, ""},
	}
}

// nationalHash is the SHA-256 of text in AL16UTF16, as STANDARD_HASH gives it of
// an NCHAR / NVARCHAR2 value.
func nationalHash(text string) string {
	var b []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		b = append(b, byte(unit>>8), byte(unit))
	}
	sum := sha256.Sum256(b)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// nationalRows is owner's NATS by the national bytes of its NCHAR and NVARCHAR2
// values, the NVARCHAR2's length and the NCLOB's, in UTF-16 units.
func nationalRows(t *testing.T, admin *sql.DB, owner string) []string {
	t.Helper()
	return textRows(t, admin, "SELECT ID, RAWTOHEX(STANDARD_HASH(NCH, 'SHA256')), RAWTOHEX(STANDARD_HASH(NVC, 'SHA256')), "+
		"LENGTH(NVC), DBMS_LOB.GETLENGTH(NC) FROM "+quoteIdent(owner)+".NATS ORDER BY ID")
}

// sameNclobs is, per row, whether dst's NATS NCLOB has the same content as src's.
func sameNclobs(t *testing.T, admin *sql.DB, src, dst string) []string {
	t.Helper()
	return textRows(t, admin, "SELECT s.ID, CASE WHEN s.NC IS NULL AND d.NC IS NULL THEN 'null' "+
		"WHEN DBMS_LOB.COMPARE(s.NC, d.NC) = 0 THEN 'same' ELSE 'differs' END FROM "+
		quoteIdent(src)+".NATS s LEFT JOIN "+quoteIdent(dst)+".NATS d ON d.ID = s.ID ORDER BY s.ID")
}

// National text survives a database whose character set is not Unicode (19c
// ZHS16GBK): NCHAR / NVARCHAR2 / NCLOB values with emoji, characters outside GBK,
// backslashes and Chinese, an NVARCHAR2 at its maximum length and an NCLOB too
// large for one literal, dumped and run into another user, hold the same national
// bytes. So does the large row copied as INSERT and run; a filter on the long
// NVARCHAR2 finds its row, and a grid edit locating the row by it sets a national value.
func TestIntegrationDumpNationalValuesRoundTrip(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		adminInfo := adminTarget(t, target)
		admin := adminInfo.open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_VAL_NS"+suffix, "OPSKAT_VAL_ND"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		src, dst := sqldb.NewSession(srcDB, dialect{}), sqldb.NewSession(dstDB, dialect{})
		for _, statement := range append([]string{nationalTableDDL}, nationalFixture...) {
			_, err := src.Execute(integrationContext(t), statement)
			require.NoError(t, err, statement)
		}

		// The source holds the values themselves, not what GBK made of them.
		values := nationalValues()
		var want []string
		for i, v := range values {
			nclob := ""
			if v[2] != "" {
				nclob = fmt.Sprint(len(utf16.Encode([]rune(v[2]))))
			}
			want = append(want, fmt.Sprintf("%d|%s|%s|%d|%s", i+1, nationalHash(v[0]), nationalHash(v[1]),
				len(utf16.Encode([]rune(v[1]))), nclob))
		}
		rows := nationalRows(t, admin, srcName)
		require.Equal(t, want, rows)
		require.Equal(t, "2000", strings.Split(rows[1], "|")[3])
		nclobs := textRows(t, admin, "SELECT NC FROM "+quoteIdent(srcName)+".NATS ORDER BY ID")
		require.Len(t, nclobs, len(values))
		for i, v := range values {
			require.Equal(t, v[2], nclobs[i], "NCLOB of row %d", i+1)
		}

		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err := src.Dump(ctx, dbdriver.DumpRequest{Database: srcName, Whole: true, WithData: true, Writer: &out})
		require.NoError(t, err)
		file := out.String()
		for _, want := range []string{`UNISTR('\00EF\00F8\D83D\DE00\005C\4E2D`, `') || UNISTR('`,
			"DBMS_LOB.APPEND(l1, TO_NCLOB(UTL_I18N.RAW_TO_NCHAR(UTL_ENCODE.BASE64_DECODE(", `N'C:\path\''x'''`} {
			assert.Contains(t, file, want)
		}
		runOK(t, dst, dstName, file)
		assert.Equal(t, rows, nationalRows(t, admin, dstName))
		same := []string{"1|same", "2|same", "3|null"}
		assert.Equal(t, same, sameNclobs(t, admin, srcName, dstName))

		// A filter on the NVARCHAR2 at its maximum length finds its row.
		page, err := src.TableRows(integrationContext(t), srcName, "NATS", dbdriver.TableQuery{
			Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Column: "NVC", Operator: dbdriver.OpEqual, Value: values[1][1]}},
		}, dbdriver.Page{Index: 0, Size: 10})
		require.NoError(t, err)
		require.Len(t, page.Rows, 1)

		// A filter on the NCHAR with its value as typed, not blank-padded, finds its
		// row too, as N'...' and a plain string compare blank-padded with NCHAR.
		for _, value := range []string{`ïø😀\中`, "ø"} {
			page, err := src.TableRows(integrationContext(t), srcName, "NATS", dbdriver.TableQuery{
				Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Column: "NCH", Operator: dbdriver.OpEqual, Value: value}},
			}, dbdriver.Page{Index: 0, Size: 10})
			require.NoError(t, err)
			assert.Len(t, page.Rows, 1, "NCH = %q", value)
		}

		// Copy as INSERT of that row, run in the query editor.
		writer, err := src.PrepareRowStatements(integrationContext(t), srcName, "NATS")
		require.NoError(t, err)
		inserts, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: dbdriver.RowInsert, Columns: page.Columns, Rows: []map[string]any{columnValues(page, 0)}, OmitDatabase: true,
		})
		require.NoError(t, err)
		require.Len(t, inserts, 1)
		require.True(t, strings.HasPrefix(inserts[0], "DECLARE\n"), "%.80s", inserts[0])
		_, err = dst.Execute(integrationContext(t), "DELETE FROM NATS WHERE ID = 2")
		require.NoError(t, err)
		_, err = dst.Execute(integrationContext(t), inserts[0])
		require.NoError(t, err)
		assert.Equal(t, rows, nationalRows(t, admin, dstName))
		assert.Equal(t, same, sameNclobs(t, admin, srcName, dstName))

		// A grid edit that locates the row by the long NVARCHAR2 sets a national value.
		edits, err := dst.GridEditStatements(integrationContext(t), dstName, "NATS", dbdriver.GridEditRequest{
			Columns: []string{"ID", "NVC"},
			Updates: []dbdriver.GridUpdate{{Row: map[string]any{"ID": "2", "NVC": values[1][1]},
				Set: []dbdriver.ColumnValue{{Column: "NCH", Value: "ø😀"}}}},
		})
		require.NoError(t, err)
		require.Len(t, edits.Updates, 1)
		_, err = dst.Execute(integrationContext(t), edits.Updates[0])
		require.NoError(t, err)
		assert.Equal(t, []string{nationalHash("ø😀" + strings.Repeat(" ", 7))}, textRows(t, admin,
			"SELECT RAWTOHEX(STANDARD_HASH(NCH, 'SHA256')) FROM "+quoteIdent(dstName)+".NATS WHERE ID = 2"))
	})
}
