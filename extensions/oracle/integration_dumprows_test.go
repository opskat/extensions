package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emojiFill inserts row id of table with column (CLOB or NCLOB) holding "a" and
// then emoji to units UTF-16 units: after the "a" every even unit begins a
// surrogate pair, so a piece of an even number of units would end inside one.
// A GBK database stores a CLOB's emoji as "?".
func emojiFill(table, column, kind string, id, units int) string {
	return fmt.Sprintf(`DECLARE l %[3]s; p NVARCHAR2(16383) := UNISTR('%[5]s'); BEGIN
  FOR i IN 1..4 LOOP p := p || p; END LOOP;
  UPDATE %[1]s SET %[2]s = EMPTY_CLOB() WHERE ID = %[4]d RETURNING %[2]s INTO l;
  DBMS_LOB.APPEND(l, TO_%[3]s('a'));
  FOR i IN 1..%[6]d LOOP DBMS_LOB.APPEND(l, TO_%[3]s(p)); END LOOP;
END;`, table, column, kind, id, strings.Repeat(`\D83D\DE00`, 400), (units-1)/12800)
}

// A dump reads a row's large values in pieces after it read the row (spec
// decision 14), all in one read-only transaction: values another session changes
// and commits while the row's pieces are read — after its first page of blocks
// (the host's pager reads one statement ahead, so the CLOB's pieces are read by
// then), before the CLOB in a Unicode database's, the NCLOB's and the BLOB's — are
// dumped as they were when the table's read began, and a row inserted then is not
// dumped. The values are read through the guest's own
// connection in this process, which needs no privilege beyond the user's own
// tables. Pieces end where the read would split a surrogate pair of an NCLOB, or
// of a CLOB in a Unicode database, and the values run back the same.
func TestIntegrationDumpReadsAConsistentRow(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		admin := adminTarget(t, target).open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_LOB_S"+suffix, "OPSKAT_LOB_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		const units = 1 + 12800*90 // three pieces' reads
		fixture := []string{
			"CREATE TABLE T (ID NUMBER PRIMARY KEY, C CLOB, CE CLOB, NC NCLOB, B BLOB)",
			"INSERT INTO T (ID) VALUES (1)",
			"INSERT INTO T (ID) VALUES (9)",
			"CREATE TABLE T2 (ID NUMBER PRIMARY KEY, C CLOB)",
			textLobFill("T2", "C", "CLOB", clobPiece, 1<<20),
			"UPDATE T SET C = (SELECT C FROM T2) WHERE ID = 1",
			"DROP TABLE T2",
			emojiFill("T", "CE", "CLOB", 1, units),
			emojiFill("T", "NC", "NCLOB", 1, units),
			"CREATE TABLE T3 (ID NUMBER PRIMARY KEY, B BLOB)",
			blobFill("T3", 1, 2<<20),
			"UPDATE T SET B = (SELECT B FROM T3) WHERE ID = 1",
			"DROP TABLE T3",
			"CREATE TABLE ORIG AS SELECT * FROM T",
			// What another session makes of row 1 while it is dumped: values of the
			// same sizes, other text and bytes.
			"CREATE TABLE ALT AS SELECT ID, REPLACE(C, 'a', 'b') C, REPLACE(CE, 'a', 'b') CE, REPLACE(NC, N'a', N'b') NC, " +
				"B FROM T WHERE ID = 1",
			`DECLARE l BLOB; BEGIN SELECT B INTO l FROM ALT FOR UPDATE; DBMS_LOB.WRITE(l, 4, 1048000, HEXTORAW('FFFFFFFF')); END;`,
		}
		for _, statement := range fixture {
			_, err := srcDB.ExecContext(lobContext(t), statement)
			require.NoError(t, err, statement)
		}
		_, err := dstDB.ExecContext(lobContext(t), "CREATE TABLE T (ID NUMBER PRIMARY KEY, C CLOB, CE CLOB, NC NCLOB, B BLOB)")
		require.NoError(t, err)

		srcUser := target
		srcUser.username, srcUser.password = srcName, dumpUserPassword
		db, dialect, err := open(lobContext(t), srcUser.openRequest(srcName))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		source, err := sqldb.NewSession(db, dialect).TableStatements()
		require.NoError(t, err)
		pages, err := source.TableStatements(lobContext(t), srcName, "T", dbdriver.TableStatementsRequest{
			Columns: []string{"ID", "C", "CE", "NC", "B"}, OmitDatabase: true, OwnRows: true, PageBytes: 1, BatchRows: 500})
		require.NoError(t, err)
		first, err := pages.Next()
		require.NoError(t, err)
		require.Len(t, first.Statements, 1)
		assert.Zero(t, first.Rows, "the row is several blocks")

		// Another session changes the row and adds one, and commits.
		_, err = admin.ExecContext(lobContext(t), fmt.Sprintf(
			"UPDATE %[1]s.T t SET (C, CE, NC, B) = (SELECT C, CE, NC, B FROM %[1]s.ALT) WHERE ID = 1", quoteIdent(srcName)))
		require.NoError(t, err)
		_, err = admin.ExecContext(lobContext(t), fmt.Sprintf("INSERT INTO %s.T (ID, C) VALUES (2, 'new')", quoteIdent(srcName)))
		require.NoError(t, err)

		statements, rows := first.Statements, 0
		for {
			page, err := pages.Next()
			require.NoError(t, err)
			statements, rows = append(statements, page.Statements...), rows+page.Rows
			if page.Done {
				break
			}
		}
		require.NoError(t, pages.Close())
		assert.Equal(t, 2, rows)
		assert.Greater(t, len(statements), 5)
		for _, statement := range statements {
			_, err := dstDB.ExecContext(lobContext(t), statement)
			require.NoError(t, err, "%.200s", statement)
		}
		assert.Equal(t, []string{"1|0|0|0|0", "9||||"}, textRows(t, admin, fmt.Sprintf(
			"SELECT d.ID, DBMS_LOB.COMPARE(d.C, o.C), DBMS_LOB.COMPARE(d.CE, o.CE), DBMS_LOB.COMPARE(d.NC, o.NC), "+
				"DBMS_LOB.COMPARE(d.B, o.B) FROM %s.T d LEFT JOIN %s.ORIG o ON o.ID = d.ID AND o.ID = 1 ORDER BY d.ID",
			quoteIdent(dstName), quoteIdent(srcName))))
		// The change is real: the source's row now differs from what was dumped.
		assert.Equal(t, []string{"1|1|1"}, textRows(t, admin, fmt.Sprintf(
			"SELECT SIGN(ABS(DBMS_LOB.COMPARE(s.C, o.C))), SIGN(ABS(DBMS_LOB.COMPARE(s.NC, o.NC))), SIGN(ABS(DBMS_LOB.COMPARE(s.B, o.B))) "+
				"FROM %[1]s.T s JOIN %[1]s.ORIG o ON o.ID = s.ID WHERE s.ID = 1", quoteIdent(srcName))))
		assert.Equal(t, []string{fmt.Sprint(units)}, textRows(t, admin, fmt.Sprintf(
			"SELECT DBMS_LOB.GETLENGTH(NC) FROM %s.T WHERE ID = 1", quoteIdent(dstName))))
		require.NoError(t, db.PingContext(lobContext(t)))
	})
}

// An index-organized table's rows have logical ROWIDs (UROWID), which go-ora
// reads as text the server does not take back (ORA-01410): the dump reads a
// row's ROWID as the server writes it as text, so a large value of such a table
// is read in pieces by it like a heap table's, and runs back the same.
func TestIntegrationDumpReadsLargeValuesOfAnIndexOrganizedTable(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		admin := adminTarget(t, target).open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_IOT_S"+suffix, "OPSKAT_IOT_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		const create = "CREATE TABLE T (ID NUMBER PRIMARY KEY, C CLOB, B BLOB) ORGANIZATION INDEX"
		for _, statement := range []string{create, "INSERT INTO T (ID, C) VALUES (3, 'small')", "INSERT INTO T (ID) VALUES (2)",
			textLobFill("T", "C", "CLOB", clobPiece, 300_000)} {
			_, err := srcDB.ExecContext(lobContext(t), statement)
			require.NoError(t, err, statement)
		}
		_, err := dstDB.ExecContext(lobContext(t), create)
		require.NoError(t, err)

		srcUser := target
		srcUser.username, srcUser.password = srcName, dumpUserPassword
		db, dialect, err := open(lobContext(t), srcUser.openRequest(srcName))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		source, err := sqldb.NewSession(db, dialect).TableStatements()
		require.NoError(t, err)
		pages, err := source.TableStatements(lobContext(t), srcName, "T", dbdriver.TableStatementsRequest{
			Columns: []string{"ID", "C", "B"}, OmitDatabase: true, OwnRows: true, PageBytes: dbdriver.DumpPageBytes, BatchRows: 500})
		require.NoError(t, err)
		var statements []string
		rows := 0
		for {
			page, err := pages.Next()
			require.NoError(t, err)
			statements, rows = append(statements, page.Statements...), rows+page.Rows
			if page.Done {
				break
			}
		}
		require.NoError(t, pages.Close())
		assert.Equal(t, 3, rows)
		for _, statement := range statements {
			_, err := dstDB.ExecContext(lobContext(t), statement)
			require.NoError(t, err, "%.200s", statement)
		}
		assert.Equal(t, []string{"1|0|", "2||", "3|0|"}, textRows(t, admin, fmt.Sprintf(
			"SELECT d.ID, DBMS_LOB.COMPARE(d.C, s.C), DBMS_LOB.COMPARE(d.B, s.B) FROM %s.T d JOIN %s.T s ON s.ID = d.ID ORDER BY d.ID",
			quoteIdent(dstName), quoteIdent(srcName))))
	})
}
