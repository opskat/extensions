package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegrationConnect(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "")
		var user, schema string
		require.NoError(t, db.QueryRowContext(integrationContext(t),
			`SELECT USER, SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM DUAL`).Scan(&user, &schema))
		assert.Equal(t, strings.ToUpper(target.username), user)
		assert.Equal(t, user, schema, "without a schema the login user's own is current")
	})
}

// The session the host opens for a schema resolves unqualified names there, on
// every connection of its pool.
func TestIntegrationConnectSetsCurrentSchema(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		db := target.open(t, "SYS")
		db.SetMaxIdleConns(0) // each query logs in a new connection
		for range 2 {
			var schema string
			require.NoError(t, db.QueryRowContext(integrationContext(t),
				`SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM DUAL`).Scan(&schema))
			assert.Equal(t, "SYS", schema)
		}
	})
}

func TestIntegrationConnectUnknownSchema(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		_, _, err := open(integrationContext(t), target.openRequest("NO_SUCH_SCHEMA_OPSKAT"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-")
	})
}

func TestIntegrationConnectWrongPassword(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		req := target.openRequest("")
		req.Password += "-wrong"
		_, _, err := open(integrationContext(t), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-01017")
	})
}

func TestIntegrationConnectUnknownService(t *testing.T) {
	forEachOracle(t, func(t *testing.T, target oracleTarget) {
		req := target.openRequest("")
		req.DriverOptions[optServiceName] = "NO_SUCH_SERVICE_OPSKAT"
		_, _, err := open(integrationContext(t), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ORA-12514")
	})
}
