package main

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An index-organized table's overflow segment and mapping table are internal to
// it: the table list names the table only, and a whole-schema dump run into
// another user restores the table as an index-organized table with an overflow
// segment (and a mapping table) and the same rows.
func TestIntegrationDumpRestoresIndexOrganizedTableWithOverflow(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		admin := adminTarget(t, target).open(t, "")
		suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000)
		srcName, dstName := "OPSKAT_OVF_S"+suffix, "OPSKAT_OVF_D"+suffix
		srcDB := createDumpUser(t, admin, target, srcName)
		dstDB := createDumpUser(t, admin, target, dstName)
		src, dst := sqldb.NewSession(srcDB, dialect{}), sqldb.NewSession(dstDB, dialect{})
		for _, statement := range []string{
			"CREATE TABLE IOT_OVF (ID NUMBER PRIMARY KEY, NOTE VARCHAR2(400)) ORGANIZATION INDEX INCLUDING ID OVERFLOW",
			"CREATE TABLE IOT_MAP (ID NUMBER PRIMARY KEY, NOTE VARCHAR2(100)) ORGANIZATION INDEX MAPPING TABLE",
			"INSERT INTO IOT_OVF VALUES (1, 'first')",
			"INSERT INTO IOT_OVF VALUES (2, RPAD('x', 300, 'y'))",
			"INSERT INTO IOT_MAP VALUES (1, 'mapped')",
		} {
			_, err := src.Execute(integrationContext(t), statement)
			require.NoError(t, err, statement)
		}

		tables, err := src.ListTables(integrationContext(t), srcName)
		require.NoError(t, err)
		assert.Equal(t, []string{"IOT_MAP", "IOT_OVF"}, tables)

		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_, err = src.Dump(ctx, dbdriver.DumpRequest{
			Database: srcName, Whole: true, WithData: true, Writer: &out,
			Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true},
		})
		require.NoError(t, err)
		file := out.String()
		for _, present := range []string{"ORGANIZATION INDEX", "INCLUDING", "OVERFLOW", "MAPPING TABLE"} {
			assert.Contains(t, file, present)
		}
		for _, absent := range []string{"SYS_IOT_OVER", "SYS_IOT_MAP", "TABLESPACE", "STORAGE", "SEGMENT CREATION"} {
			assert.NotContains(t, file, absent)
		}

		runOK(t, dst, dstName, file)
		owner := sqlString(dstName)
		assert.Equal(t, []string{"IOT|", "IOT|", "IOT_MAPPING|IOT_MAP", "IOT_OVERFLOW|IOT_OVF"},
			textRows(t, admin, "SELECT iot_type, iot_name FROM all_tables WHERE owner = "+owner+
				" AND dropped = 'NO' AND iot_type IS NOT NULL ORDER BY iot_type, iot_name"))
		assert.Equal(t, textRows(t, admin, "SELECT TO_CHAR(ID), NOTE FROM "+quoteIdent(srcName)+".IOT_OVF ORDER BY ID"),
			textRows(t, admin, "SELECT TO_CHAR(ID), NOTE FROM "+quoteIdent(dstName)+".IOT_OVF ORDER BY ID"))
		assert.Equal(t, []string{"1|mapped"}, textRows(t, admin, "SELECT TO_CHAR(ID), NOTE FROM "+quoteIdent(dstName)+".IOT_MAP"))
	})
}
