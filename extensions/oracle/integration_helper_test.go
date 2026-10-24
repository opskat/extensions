package main

import (
	"context"
	"database/sql"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/require"
)

// Real-database tests run against the Oracle releases named below, each only when
// its variable is set; otherwise they skip (CI never sets them). A variable holds
//
//	oracle://USER:PASSWORD@HOST:PORT/SERVICE
//
// with the user and password URL-escaped, for example
// ORACLE_TEST_19C=oracle://opskat:secret@192.168.8.141:11526/ORCLCDB.
var integrationTargets = []struct{ name, env string }{
	{"19c", "ORACLE_TEST_19C"},
	{"21c", "ORACLE_TEST_21C"},
	{"23ai", "ORACLE_TEST_23AI"},
}

// oracleTarget is one configured real database. It never prints its password.
type oracleTarget struct {
	name     string
	host     string
	port     int
	username string
	password string
	service  string
}

// forEachOracle runs fn as a subtest per configured target and skips the targets
// whose variable is unset.
func forEachOracle(t *testing.T, fn func(t *testing.T, target oracleTarget)) {
	t.Helper()
	for _, tc := range integrationTargets {
		t.Run(tc.name, func(t *testing.T) {
			raw := os.Getenv(tc.env)
			if raw == "" {
				t.Skipf("%s is not set", tc.env)
			}
			fn(t, parseTarget(t, tc.name, tc.env, raw))
		})
	}
}

func parseTarget(t *testing.T, name, env, raw string) oracleTarget {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "oracle" || u.User == nil {
		// The parse error would echo the value, password included.
		t.Fatalf("%s is not oracle://USER:PASSWORD@HOST:PORT/SERVICE", env)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("%s has no numeric port", env)
	}
	password, _ := u.User.Password()
	return oracleTarget{
		name:     name,
		host:     u.Hostname(),
		port:     port,
		username: u.User.Username(),
		password: password,
		service:  strings.Trim(u.Path, "/"),
	}
}

// openRequest is the request the host would send for the target's asset, opened
// for schema ("" for the login user's own), connecting directly (the test's own
// dialer stands in for the host's reach path).
func (o oracleTarget) openRequest(schema string) dbdriver.OpenRequest {
	return dbdriver.OpenRequest{
		Config: &dbdriver.Config{
			Driver: driverID, Host: o.host, Port: o.port, Username: o.username, Database: schema,
		},
		Password:      o.password,
		DriverOptions: map[string]any{optServiceName: o.service, optUseSID: false, optRole: roleNormal},
		Transport:     dbdriver.Transport{Dial: (&net.Dialer{}).DialContext},
	}
}

// open opens a session's database for schema through the driver's open and
// closes it when the test ends.
func (o oracleTarget) open(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, _, err := open(integrationContext(t), o.openRequest(schema))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// integrationContext bounds one real-database step.
func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
