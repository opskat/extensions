package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Large LOBs through the extension as the app loads it (integration_guest_test.go):
// values up to the limit (maxLobBytes) dump, run back, export and (text) copy as
// INSERT within the instance's memory; a larger value stops the operation
// before it is read, naming the table, the column and the size. The values are
// made on the server: the link to the test databases is slow.

// The fixture's pieces. Each is a power of two in UTF-16 units, so whole pieces
// make up every size below.
const (
	// clobPiece: quotes, a backslash, Chinese (in the GBK 19c database too) and a
	// line break; 16 characters.
	clobPiece = `'a''\中文数据测试表格wxyz' || CHR(10)`
	// nclobPiece: mostly characters whose UNISTR escape is far longer than the
	// character (Chinese, emoji), a quote, a backslash, a line break; 16 UTF-16 units.
	nclobPiece = `UNISTR('a''\005C\4E2D\6587\6570\636E\6D4B\8BD5\8868\683C\D83D\DE00\D83D\DE00\000A')`
)

// textLobFill inserts row 1 of table with column holding piece repeated to
// units UTF-16 units, appended on the server 4096 units at a time.
func textLobFill(table, column, kind, piece string, units int) string {
	varType := "VARCHAR2(32767)"
	if kind == "NCLOB" {
		varType = "NVARCHAR2(16383)"
	}
	return fmt.Sprintf(`DECLARE l %[3]s; p %[4]s := %[5]s; BEGIN
  FOR i IN 1..8 LOOP p := p || p; END LOOP;
  INSERT INTO %[1]s (ID, %[2]s) VALUES (1, EMPTY_CLOB()) RETURNING %[2]s INTO l;
  FOR i IN 1..%[6]d LOOP DBMS_LOB.APPEND(l, TO_%[3]s(p)); END LOOP;
END;`, table, column, kind, varType, piece, units/4096)
}

// blobPiece builds p, 16384 bytes of every byte value in turn.
const blobPiece = `p RAW(32767); BEGIN
  FOR i IN 0..255 LOOP p := UTL_RAW.CONCAT(p, HEXTORAW(TO_CHAR(i, 'FM0X'))); END LOOP;
  FOR i IN 1..6 LOOP p := UTL_RAW.CONCAT(p, p); END LOOP;`

// blobFill inserts rows 1..rows of table with a BLOB of size bytes each (a
// multiple of 16384), each 16384 bytes marked with the row and its place.
func blobFill(table string, rows, size int) string {
	return fmt.Sprintf(`DECLARE l BLOB; %[1]s
  FOR r IN 1..%[3]d LOOP
    INSERT INTO %[2]s (ID, B) VALUES (r, EMPTY_BLOB()) RETURNING B INTO l;
    FOR i IN 1..%[4]d LOOP DBMS_LOB.WRITEAPPEND(l, 16384, UTL_RAW.OVERLAY(UTL_RAW.CAST_FROM_BINARY_INTEGER(r * 100000 + i), p)); END LOOP;
  END LOOP;
END;`, blobPiece, table, rows, size/16384)
}

const (
	limitBytes = 16 << 20 // the limit, as maxLobBytes
	overBytes  = 17 << 20
	rowBytes   = 256 << 10
	big2Bytes  = 2 << 20
)

// A dump of a row of large values holds about a block of each at a time (spec
// decision 14): the instance's peak memory stays within maxDumpPeakMiB for three
// values at the limit and grows by at most dumpPeakGrowthMiB from three of 2 MB.
const (
	maxDumpPeakMiB    = 160
	dumpPeakGrowthMiB = 32
)

var lobFixture = []string{
	"CREATE TABLE LOB_C (ID NUMBER PRIMARY KEY, C CLOB)",
	"CREATE TABLE LOB_NC (ID NUMBER PRIMARY KEY, NC NCLOB)",
	"CREATE TABLE LOB_B (ID NUMBER PRIMARY KEY, B BLOB)",
	"CREATE TABLE OVER_NC (ID NUMBER PRIMARY KEY, NC NCLOB)",
	"CREATE TABLE OVER_B (ID NUMBER PRIMARY KEY, B BLOB)",
	"CREATE TABLE ROWS256 (ID NUMBER PRIMARY KEY, B BLOB)",
	"CREATE TABLE OVER_X (ID NUMBER PRIMARY KEY, X XMLTYPE)",
	// A row of three values at the limit, in a table with a primary key, and one
	// of three values of 2 MB.
	"CREATE TABLE BIG16 (ID NUMBER PRIMARY KEY, C CLOB, NC NCLOB, B BLOB)",
	"CREATE TABLE BIG2 (ID NUMBER PRIMARY KEY, C CLOB, NC NCLOB, B BLOB)",
	"CREATE TABLE BIG2_B (ID NUMBER PRIMARY KEY, B BLOB)",
	// No key that finds a row: a unique key with a nullable column does not.
	"CREATE TABLE NOKEY_B (ID NUMBER UNIQUE, B BLOB)",
	"CREATE TABLE NOKEY_OVER (ID NUMBER, B BLOB, B2 BLOB)",
	// A unique key of NOT NULL columns of a RAW, national text and a DATE, and one
	// with a nullable column that does not find a row.
	"CREATE TABLE UKEY (ID NUMBER UNIQUE, CODE RAW(16) NOT NULL, NAME NVARCHAR2(20) NOT NULL, D DATE NOT NULL, B BLOB, " +
		"CONSTRAINT UKEY_K UNIQUE (CODE, NAME, D))",
	// At the limit: a character counts 2 bytes.
	textLobFill("LOB_C", "C", "CLOB", clobPiece, limitBytes/2),
	textLobFill("LOB_NC", "NC", "NCLOB", nclobPiece, limitBytes/2),
	blobFill("LOB_B", 1, limitBytes),
	textLobFill("OVER_NC", "NC", "NCLOB", nclobPiece, overBytes/2),
	blobFill("OVER_B", 1, overBytes),
	blobFill("ROWS256", 500, rowBytes),
	"INSERT INTO BIG16 SELECT 1, (SELECT C FROM LOB_C), (SELECT NC FROM LOB_NC), (SELECT B FROM LOB_B) FROM dual",
	blobFill("BIG2_B", 1, big2Bytes),
	fmt.Sprintf("INSERT INTO BIG2 SELECT 1, SUBSTR((SELECT C FROM LOB_C), 1, %[1]d), SUBSTR((SELECT NC FROM LOB_NC), 1, %[1]d), "+
		"(SELECT B FROM BIG2_B) FROM dual", big2Bytes/2),
	"DROP TABLE BIG2_B",
	"INSERT INTO NOKEY_B SELECT 1, B FROM LOB_B",
	"INSERT INTO NOKEY_OVER SELECT 1, B, B FROM LOB_B",
	// 2 MB: two blocks' appends and more.
	fmt.Sprintf(`DECLARE l BLOB; %s
  INSERT INTO UKEY VALUES (NULL, HEXTORAW('00FF10'), UNISTR('\4E2D\6587\D83D\DE00'),
    TO_DATE('2024-02-29 13:14:15', 'YYYY-MM-DD HH24:MI:SS'), EMPTY_BLOB()) RETURNING B INTO l;
  FOR i IN 1..128 LOOP DBMS_LOB.WRITEAPPEND(l, 16384, UTL_RAW.OVERLAY(UTL_RAW.CAST_FROM_BINARY_INTEGER(i), p)); END LOOP;
END;`, blobPiece),
	// An XMLType whose text is past the limit: 2176 elements of 4096 characters.
	`DECLARE l CLOB; p VARCHAR2(4096) := '<b>' || RPAD('x', 4089, 'x') || '</b>'; BEGIN
  DBMS_LOB.CREATETEMPORARY(l, TRUE); DBMS_LOB.APPEND(l, TO_CLOB('<a>'));
  FOR i IN 1..2176 LOOP DBMS_LOB.APPEND(l, TO_CLOB(p)); END LOOP;
  DBMS_LOB.APPEND(l, TO_CLOB('</a>')); INSERT INTO OVER_X VALUES (1, XMLTYPE(l)); DBMS_LOB.FREETEMPORARY(l);
END;`,
}

// lobContext bounds one large step: the link is slow.
func lobContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestIntegrationLargeLobs(t *testing.T) {
	var build sync.Once
	var wasm []byte
	load := func(t *testing.T) *wasmDriver {
		build.Do(func() { wasm = buildWasm(t) })
		require.NotEmpty(t, wasm)
		d := loadWasmDriver(t, wasm)
		t.Cleanup(func() { t.Logf("guest memory peak %.1f MiB; grew in: %s (MiB)", d.memory.MiB(), d.growthReport()) })
		return d
	}
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		admin := adminTarget(t, target).open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_LOB_S"+suffix, "OPSKAT_LOB_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		createDumpUser(t, admin, target, dstName)
		srcUser, dstUser := target, target
		srcUser.username, srcUser.password = srcName, dumpUserPassword
		dstUser.username, dstUser.password = dstName, dumpUserPassword

		start := time.Now()
		for _, statement := range lobFixture {
			_, err := srcDB.ExecContext(lobContext(t), statement)
			require.NoError(t, err, statement)
		}
		assert.Equal(t, []string{"8388608|8388608|16777216|8912896|17825792|500"}, textRows(t, admin, fmt.Sprintf(
			"SELECT (SELECT DBMS_LOB.GETLENGTH(C) FROM %[1]s.LOB_C), (SELECT DBMS_LOB.GETLENGTH(NC) FROM %[1]s.LOB_NC), "+
				"(SELECT DBMS_LOB.GETLENGTH(B) FROM %[1]s.LOB_B), (SELECT DBMS_LOB.GETLENGTH(NC) FROM %[1]s.OVER_NC), "+
				"(SELECT DBMS_LOB.GETLENGTH(B) FROM %[1]s.OVER_B), (SELECT COUNT(*) FROM %[1]s.ROWS256) FROM dual", quoteIdent(srcName))))
		t.Logf("fixture made in %s", time.Since(start).Round(time.Second))

		t.Run("over the limit", func(t *testing.T) {
			d := load(t)
			src := d.open(t, srcUser, srcName)
			ref := func(table string) string { return quoteIdent(srcName) + "." + quoteIdent(table) }

			_, err := src.dump(lobContext(t), dbdriver.DumpRequest{Database: srcName, Tables: []string{"OVER_B"}, WithData: true, Writer: &strings.Builder{}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `column "B" of table `+ref("OVER_B")+` holds a value of 17.0 MB (17825792 bytes)`)

			_, err = src.dump(lobContext(t), dbdriver.DumpRequest{Database: srcName, Tables: []string{"OVER_NC"}, WithData: true, Writer: &strings.Builder{}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `column "NC" of table `+ref("OVER_NC")+` holds a value of 17.0 MB (8912896 characters)`)

			// An XMLType is measured as its text (as XMLSERIALIZE writes it), which is read as a CLOB.
			_, err = src.dump(lobContext(t), dbdriver.DumpRequest{Database: srcName, Tables: []string{"OVER_X"}, WithData: true, Writer: &strings.Builder{}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `column "X" of table `+ref("OVER_X")+` holds a value of 17.0 MB (`)

			// The export's read and the table view's page stop the same way.
			stream, err := src.StreamTableRows(lobContext(t), srcName, "OVER_NC", dbdriver.TableQuery{})
			require.NoError(t, err)
			assert.False(t, stream.Next())
			require.Error(t, stream.Err())
			assert.Contains(t, stream.Err().Error(), `column "NC" of table `+ref("OVER_NC"))
			require.NoError(t, stream.Close())
			_, err = src.TableRows(lobContext(t), srcName, "OVER_B", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 100})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `column "B" of table `+ref("OVER_B"))

			// Nothing trapped: every instance still answers.
			require.NoError(t, src.healthy(lobContext(t)))
			t.Logf("peak guest memory %.1f MiB", d.memory.MiB())
		})

		// Each kind at the limit: dump, run the dump into the other user, compare;
		// copy the row as INSERT from the table view's page.
		for _, tc := range []struct{ table, column string }{{"LOB_C", "C"}, {"LOB_NC", "NC"}, {"LOB_B", "B"}} {
			t.Run("16 MB "+tc.table, func(t *testing.T) {
				d := load(t)
				src, dst := d.open(t, srcUser, srcName), d.open(t, dstUser, dstName)
				file := filepath.Join(t.TempDir(), tc.table+".sql")
				start := time.Now()
				dumpToFile(t, src, dbdriver.DumpRequest{Database: srcName, Tables: []string{tc.table}, WithData: true}, file)
				dumped, dumpPeak := time.Since(start), d.memory.MiB()
				info, err := os.Stat(file)
				require.NoError(t, err)

				start = time.Now()
				f, err := os.Open(file)
				require.NoError(t, err)
				result, err := dst.runScript(lobContext(t), dbdriver.ScriptRequest{Database: dstName, Script: f, Size: info.Size()})
				_ = f.Close()
				require.NoError(t, err)
				require.Nil(t, result.Failure)
				ran, runPeak := time.Since(start), d.memory.MiB()
				assert.Equal(t, []string{"0"}, textRows(t, admin, fmt.Sprintf("SELECT DBMS_LOB.COMPARE(s.%[1]s, d.%[1]s) FROM %[2]s.%[3]s s JOIN %[4]s.%[3]s d ON d.ID = s.ID",
					tc.column, quoteIdent(srcName), tc.table, quoteIdent(dstName))))

				require.NoError(t, src.healthy(lobContext(t)))
				require.NoError(t, dst.healthy(lobContext(t)))
				t.Logf("%s: dump %s (%.1f MB file), run %s; peak guest memory after the dump %.1f MiB, after the run %.1f MiB",
					tc.table, dumped.Round(time.Second), float64(info.Size())/1e6, ran.Round(time.Second), dumpPeak, runPeak)
				if tc.table == "LOB_B" {
					// The grid's JSON cannot carry a binary value's bytes (values.go),
					// so a BLOB copied from the grid is not its value at any size.
					return
				}

				// Copy as INSERT, from a table view opened afresh on the same instance: the value as the
				// grid hands it back (its page, through JSON) is the dump's statement.
				// It runs on the instance the dump and the run above used, whose memory
				// has already grown for them: the copy must fit in what is left.
				src = d.open(t, srcUser, srcName)
				start = time.Now()
				page, err := src.TableRows(lobContext(t), srcName, tc.table, dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 100})
				require.NoError(t, err)
				require.Len(t, page.Rows, 1)
				writer, err := src.PrepareRowStatements(lobContext(t), srcName, tc.table)
				require.NoError(t, err)
				copied, err := writer.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: page.Columns, Rows: sentRows(t, page)})
				require.NoError(t, err)
				require.Len(t, copied, 1)
				dump, err := os.ReadFile(file)
				require.NoError(t, err)
				assert.True(t, strings.Contains(string(dump), strings.ReplaceAll(copied[0], quoteIdent(srcName)+".", "")),
					"copy as INSERT differs from the dump's statement")
				require.NoError(t, src.healthy(lobContext(t)))
				t.Logf("%s: copy %s; peak guest memory %.1f MiB", tc.table, time.Since(start).Round(time.Second), d.memory.MiB())
			})
		}

		// A row of three values of 2 MB and one of three at the limit (about 62 MB
		// of appends) are blocks of about 1 MB, the later ones finding the row by its
		// primary key: the dump reads the values in pieces as it writes the blocks,
		// so its memory hardly depends on their size, and the run reports progress
		// as it goes and holds no more than a block of it at a time.
		var big2Peak float64
		for _, tc := range []struct {
			name, table string
			minBlocks   int
		}{{"2 MB", "BIG2", 6}, {"16 MB", "BIG16", 50}} {
			t.Run(tc.name+" CLOB, NCLOB and BLOB in one row", func(t *testing.T) {
				d := load(t)
				src, dst := d.open(t, srcUser, srcName), d.open(t, dstUser, dstName)
				file := filepath.Join(t.TempDir(), tc.table+".sql")
				start := time.Now()
				dumpToFile(t, src, dbdriver.DumpRequest{Database: srcName, Tables: []string{tc.table}, WithData: true}, file)
				dumped, dumpPeak := time.Since(start), d.memory.MiB()
				blocks, largest := blockSizes(t, file)
				assert.Greater(t, blocks, tc.minBlocks)
				assert.Less(t, largest, 2<<20)
				dump, err := os.ReadFile(file)
				require.NoError(t, err)
				assert.Contains(t, string(dump), `  SELECT "C" INTO l1 FROM "`+tc.table+`" WHERE "ID" = 1 FOR UPDATE;`)
				size := len(dump)

				ran, runPeak, progress, gap := runFile(t, dst, dstName, file)
				assert.GreaterOrEqual(t, progress, blocks/2)
				assert.Equal(t, []string{"0|0|0"}, textRows(t, admin, fmt.Sprintf(
					"SELECT DBMS_LOB.COMPARE(s.C, d.C), DBMS_LOB.COMPARE(s.NC, d.NC), DBMS_LOB.COMPARE(s.B, d.B) "+
						"FROM %[1]s.%[3]s s JOIN %[2]s.%[3]s d ON d.ID = s.ID", quoteIdent(srcName), quoteIdent(dstName), tc.table)))
				require.NoError(t, src.healthy(lobContext(t)))
				require.NoError(t, dst.healthy(lobContext(t)))
				t.Logf("%s: dump %s (%.1f MB file, %d blocks, largest %.2f MB), run %s with %d progress reports, longest gap %s; "+
					"peak guest memory after the dump %.1f MiB, after the run %.1f MiB",
					tc.table, dumped.Round(time.Second), float64(size)/1e6, blocks, float64(largest)/1e6, ran.Round(time.Second), progress,
					gap.Round(time.Millisecond), dumpPeak, runPeak)
				assert.LessOrEqual(t, dumpPeak, float64(maxDumpPeakMiB), "%s dump peak", tc.table)
				if tc.table == "BIG2" {
					big2Peak = dumpPeak
					return
				}
				require.NotZero(t, big2Peak, "the 2 MB row's dump ran first")
				assert.LessOrEqual(t, dumpPeak-big2Peak, float64(dumpPeakGrowthMiB), "BIG16 %.1f MiB, BIG2 %.1f MiB", dumpPeak, big2Peak)
				t.Logf("dump peak: BIG16 %.1f MiB, BIG2 %.1f MiB, difference %.1f MiB", dumpPeak, big2Peak, dumpPeak-big2Peak)
			})
		}

		// A unique key of NOT NULL columns finds the row when there is no primary
		// key; its RAW, national and DATE values are the INSERT's literals.
		t.Run("unique key", func(t *testing.T) {
			d := load(t)
			src, dst := d.open(t, srcUser, srcName), d.open(t, dstUser, dstName)
			file := filepath.Join(t.TempDir(), "ukey.sql")
			dumpToFile(t, src, dbdriver.DumpRequest{Database: srcName, Tables: []string{"UKEY"}, WithData: true}, file)
			blocks, _ := blockSizes(t, file)
			assert.Equal(t, 3, blocks)
			dump, err := os.ReadFile(file)
			require.NoError(t, err)
			assert.Contains(t, string(dump), `  SELECT "B" INTO l1 FROM "UKEY" WHERE "CODE" = HEXTORAW('00ff10') AND `+
				`"NAME" = UNISTR('\4E2D\6587\D83D\DE00') AND "D" = TO_DATE('2024-02-29 13:14:15', 'YYYY-MM-DD HH24:MI:SS') FOR UPDATE;`)
			runFile(t, dst, dstName, file)
			assert.Equal(t, []string{"0|1"}, textRows(t, admin, fmt.Sprintf(
				"SELECT DBMS_LOB.COMPARE(s.B, d.B), COUNT(*) OVER () FROM %s.UKEY s JOIN %s.UKEY d "+
					"ON d.CODE = s.CODE AND d.NAME = s.NAME AND d.D = s.D", quoteIdent(srcName), quoteIdent(dstName))))
			require.NoError(t, dst.healthy(lobContext(t)))
		})

		// Without a key a row is one block: a 16 MB BLOB's (about 23 MB) runs; one
		// over 24 MB stops the dump before any of the row is written, naming the
		// table and the size, and traps nothing.
		t.Run("no key", func(t *testing.T) {
			d := load(t)
			src, dst := d.open(t, srcUser, srcName), d.open(t, dstUser, dstName)
			file := filepath.Join(t.TempDir(), "nokey.sql")
			start := time.Now()
			dumpToFile(t, src, dbdriver.DumpRequest{Database: srcName, Tables: []string{"NOKEY_B"}, WithData: true}, file)
			dumped := time.Since(start)
			blocks, largest := blockSizes(t, file)
			assert.Equal(t, 1, blocks)
			assert.Greater(t, largest, 22_000_000)
			ran, runPeak, _, _ := runFile(t, dst, dstName, file)
			assert.Equal(t, []string{"0"}, textRows(t, admin, fmt.Sprintf(
				"SELECT DBMS_LOB.COMPARE(s.B, d.B) FROM %s.NOKEY_B s JOIN %s.NOKEY_B d ON d.ID = s.ID",
				quoteIdent(srcName), quoteIdent(dstName))))
			t.Logf("NOKEY_B: dump %s, one block of %.1f MB, run %s; peak guest memory %.1f MiB",
				dumped.Round(time.Second), float64(largest)/1e6, ran.Round(time.Second), runPeak)

			var out strings.Builder
			_, err := src.dump(lobContext(t), dbdriver.DumpRequest{Database: srcName, Tables: []string{"NOKEY_OVER"}, WithData: true, Writer: &out})
			require.Error(t, err)
			assert.Regexp(t, `a row of table "NOKEY_OVER" would be a PL/SQL block of about 4\d\.\d MB \(\d+ bytes\) of SQL text, `+
				`more than the 24 MB \(25165824 bytes\) one block may take: the table has neither a primary key nor a unique key`, err.Error())
			assert.NotContains(t, out.String(), `INSERT INTO "NOKEY_OVER"`)
			require.NoError(t, src.healthy(lobContext(t)))
			require.NoError(t, dst.healthy(lobContext(t)))
			t.Logf("NOKEY_OVER: %v; peak guest memory %.1f MiB", err, d.memory.MiB())
		})

		t.Run("export 30 rows of 256 KB", func(t *testing.T) {
			d := load(t)
			src := d.open(t, srcUser, srcName)
			start := time.Now()
			writer, err := src.PrepareRowStatements(lobContext(t), srcName, "ROWS256")
			require.NoError(t, err)
			stream, err := src.StreamTableRows(lobContext(t), srcName, "ROWS256", dbdriver.TableQuery{Filters: []dbdriver.FilterItem{
				{Kind: dbdriver.FilterCondition, Column: "ID", Operator: dbdriver.OpLessOrEqual, Value: "30"},
			}})
			require.NoError(t, err)
			page := &dbdriver.RowSet{Columns: stream.Columns()}
			for stream.Next() {
				page.Rows = append(page.Rows, stream.Row())
			}
			require.NoError(t, stream.Err())
			require.NoError(t, stream.Close())
			statements, err := writer.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: page.Columns, Rows: sentRows(t, page)})
			require.NoError(t, err)
			assert.Len(t, statements, 30)
			require.NoError(t, src.healthy(lobContext(t)))
			t.Logf("export %s; peak guest memory %.1f MiB", time.Since(start).Round(time.Second), d.memory.MiB())
		})

		t.Run("dump 500 rows of 256 KB", func(t *testing.T) {
			d := load(t)
			src := d.open(t, srcUser, srcName)
			file := filepath.Join(t.TempDir(), "rows256.sql")
			start := time.Now()
			dumpToFile(t, src, dbdriver.DumpRequest{Database: srcName, Tables: []string{"ROWS256"}, WithData: true}, file)
			assert.Equal(t, 500, countBlocks(t, file))
			require.NoError(t, src.healthy(lobContext(t)))
			t.Logf("dump %s; peak guest memory %.1f MiB", time.Since(start).Round(time.Second), d.memory.MiB())
		})
	})
}

// dumpToFile writes the dump of req into file.
func dumpToFile(t *testing.T, s *wasmSession, req dbdriver.DumpRequest, file string) {
	t.Helper()
	f, err := os.Create(file)
	require.NoError(t, err)
	w := bufio.NewWriter(f)
	req.Writer = w
	_, err = s.dump(lobContext(t), req)
	require.NoError(t, err)
	require.NoError(t, w.Flush())
	require.NoError(t, f.Close())
}

// countBlocks counts the lines holding only / in file: one per PL/SQL block.
func countBlocks(t *testing.T, file string) int {
	t.Helper()
	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	n := 0
	for scanner.Scan() {
		if scanner.Text() == "/" {
			n++
		}
	}
	require.NoError(t, scanner.Err())
	return n
}

// blockSizes counts the lines holding only / in file (one per PL/SQL block) and
// the bytes of the most text up to one (a block and the statements before it).
func blockSizes(t *testing.T, file string) (blocks, largest int) {
	t.Helper()
	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	size := 0
	for scanner.Scan() {
		line := scanner.Text()
		size += len(line) + 1
		if line == "/" {
			blocks, largest, size = blocks+1, max(largest, size), 0
		}
	}
	require.NoError(t, scanner.Err())
	return blocks, largest
}

// runFile runs file into schema as Run SQL File does, requiring
// every statement to succeed, and returns how long it took, the instance's peak
// memory after it, how often it reported progress and the longest wait for a report.
func runFile(t *testing.T, s *wasmSession, schema, file string) (time.Duration, float64, int, time.Duration) {
	t.Helper()
	info, err := os.Stat(file)
	require.NoError(t, err)
	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	start := time.Now()
	last, reports, gap := start, 0, time.Duration(0)
	result, err := s.runScript(lobContext(t), dbdriver.ScriptRequest{Database: schema, Script: f, Size: info.Size(),
		Progress: func(dbdriver.ScriptProgress) {
			now := time.Now()
			reports, gap, last = reports+1, max(gap, now.Sub(last)), now
		}})
	require.NoError(t, err)
	require.Nil(t, result.Failure)
	return time.Since(start), s.driver.memory.MiB(), reports, gap
}

// sentRows are page's rows as the app hands them back to RowStatements: by
// column name, through JSON (the grid's, the export's).
func sentRows(t *testing.T, page *dbdriver.RowSet) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, len(page.Rows))
	for i, values := range page.Rows {
		rows[i] = make(map[string]any, len(page.Columns))
		for j, column := range page.Columns {
			rows[i][column] = values[j]
		}
	}
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	var sent []map[string]any
	require.NoError(t, json.Unmarshal(raw, &sent))
	return sent
}
