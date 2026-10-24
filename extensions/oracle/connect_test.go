package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- login options -> go-ora connection URL ----

func openRequest(opts map[string]any) dbdriver.OpenRequest {
	return dbdriver.OpenRequest{
		Config:        &dbdriver.Config{Driver: "oracle", Host: "db.example", Port: 1522, Username: "scott", Database: "HR"},
		Password:      "p@ss/word",
		DriverOptions: opts,
	}
}

func parseURL(t *testing.T, req dbdriver.OpenRequest) *url.URL {
	t.Helper()
	u, err := url.Parse(connectURL(req))
	require.NoError(t, err)
	return u
}

func TestConnectURLServiceName(t *testing.T) {
	u := parseURL(t, openRequest(map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": "NORMAL"}))
	assert.Equal(t, "db.example:1522", u.Host)
	assert.Equal(t, "/ORCLPDB1", u.Path)
	assert.Equal(t, "scott", u.User.Username())
	pass, _ := u.User.Password()
	assert.Equal(t, "p@ss/word", pass)
	q := u.Query()
	assert.Empty(t, q.Get("SID"))
	assert.Empty(t, q.Get("DBA PRIVILEGE"))
	assert.Empty(t, q.Get("AUTH TYPE"))
	assert.Empty(t, q.Get("SSL"))
}

// The schema (Config.Database) never reaches the connection URL: the service
// name is a driver field.
func TestConnectURLIgnoresSchema(t *testing.T) {
	u := parseURL(t, openRequest(map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": "NORMAL"}))
	assert.NotContains(t, u.String(), "HR")
}

func TestConnectURLSID(t *testing.T) {
	u := parseURL(t, openRequest(map[string]any{"service_name": "ORCLCDB", "use_sid": true, "role": "NORMAL"}))
	assert.Equal(t, "/", u.Path)
	assert.Equal(t, "ORCLCDB", u.Query().Get("SID"))
}

func TestConnectURLRole(t *testing.T) {
	for _, role := range []string{"SYSDBA", "SYSOPER"} {
		u := parseURL(t, openRequest(map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": role}))
		assert.Equal(t, role, u.Query().Get("DBA PRIVILEGE"))
	}
}

func TestConnectURLKerberos(t *testing.T) {
	req := openRequest(map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": "NORMAL"})
	req.Password = ""
	req.Kerberos = &dbdriver.Kerberos{Ticket: func(context.Context, string) ([]byte, error) { return nil, nil }}
	u := parseURL(t, req)
	assert.Equal(t, "KERBEROS", u.Query().Get("AUTH TYPE"))
}

func TestConnectURLTLS(t *testing.T) {
	req := openRequest(map[string]any{"service_name": "ORCLPDB1", "use_sid": false, "role": "NORMAL"})
	req.TLS = &dbdriver.TLSMaterial{Insecure: true}
	assert.Equal(t, "true", parseURL(t, req).Query().Get("SSL"))
}

// ---- Kerberos ----

// go-ora's Authenticate(server, service) names the database host first; the host
// hands out tickets for service/host.
func TestKerberosAuthAsksForServiceSlashHost(t *testing.T) {
	var asked string
	auth := kerberosAuth{ctx: context.Background(), source: &dbdriver.Kerberos{Ticket: func(_ context.Context, spn string) ([]byte, error) {
		asked = spn
		return []byte("ticket"), nil
	}}}
	ticket, err := auth.Authenticate("db.example", "oracle")
	require.NoError(t, err)
	assert.Equal(t, []byte("ticket"), ticket)
	assert.Equal(t, "oracle/db.example", asked)
}

// ---- TLS ----

type testPKI struct {
	caPEM      []byte
	caKey      *ecdsa.PrivateKey
	ca         *x509.Certificate
	serverCert tls.Certificate
}

func newTestPKI(t *testing.T, serverNames ...string) *testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	p := &testPKI{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), caKey: caKey, ca: ca}
	certPEM, keyPEM := p.issue(t, x509.ExtKeyUsageServerAuth, serverNames...)
	p.serverCert, err = tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return p
}

func (p *testPKI) issue(t *testing.T, usage x509.ExtKeyUsage, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     names,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// handshake runs a TLS handshake of the client config against a server with the
// server config, over loopback TCP (net.Pipe has no buffer, so an alert the
// client sends mid-flight would block). Like go-ora, it first overwrites
// ServerName with the address it dialed.
func handshake(t *testing.T, client *tls.Config, server *tls.Config) error {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = s.Close() }()
		_ = s.SetDeadline(time.Now().Add(5 * time.Second))
		_ = tls.Server(s, server).Handshake()
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	require.NoError(t, c.SetDeadline(time.Now().Add(5*time.Second)))
	client.ServerName = "10.0.0.7"
	err = tls.Client(c, client).Handshake()
	_ = c.Close()
	<-serverDone
	return err
}

func TestTLSVerifiesAssetHostWhenServerNameEmpty(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	server := &tls.Config{Certificates: []tls.Certificate{pki.serverCert}}

	conf, err := tlsConfig(&dbdriver.TLSMaterial{CA: pki.caPEM}, "db.example")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, conf, server))

	conf, err = tlsConfig(&dbdriver.TLSMaterial{CA: pki.caPEM}, "other.example")
	require.NoError(t, err)
	assert.Error(t, handshake(t, conf, server))
}

func TestTLSVerifiesConfiguredServerName(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	server := &tls.Config{Certificates: []tls.Certificate{pki.serverCert}}

	conf, err := tlsConfig(&dbdriver.TLSMaterial{CA: pki.caPEM, ServerName: "db.example"}, "192.0.2.10")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, conf, server))

	conf, err = tlsConfig(&dbdriver.TLSMaterial{CA: pki.caPEM, ServerName: "other.example"}, "db.example")
	require.NoError(t, err)
	assert.Error(t, handshake(t, conf, server))
}

func TestTLSRejectsUntrustedServer(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	other := newTestPKI(t, "db.example")
	conf, err := tlsConfig(&dbdriver.TLSMaterial{CA: other.caPEM}, "db.example")
	require.NoError(t, err)
	assert.Error(t, handshake(t, conf, &tls.Config{Certificates: []tls.Certificate{pki.serverCert}}))
}

func TestTLSInsecureSkipsVerification(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	conf, err := tlsConfig(&dbdriver.TLSMaterial{Insecure: true}, "other.example")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, conf, &tls.Config{Certificates: []tls.Certificate{pki.serverCert}}))
}

func TestTLSClientCertificate(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	certPEM, keyPEM := pki.issue(t, x509.ExtKeyUsageClientAuth, "client")
	roots := x509.NewCertPool()
	roots.AddCert(pki.ca)
	server := &tls.Config{Certificates: []tls.Certificate{pki.serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}

	conf, err := tlsConfig(&dbdriver.TLSMaterial{CA: pki.caPEM, Cert: certPEM, Key: keyPEM}, "db.example")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, conf, server))
}

func TestTLSUnreadableMaterialFails(t *testing.T) {
	pki := newTestPKI(t, "db.example")
	certPEM, _ := pki.issue(t, x509.ExtKeyUsageClientAuth, "client")

	_, err := tlsConfig(&dbdriver.TLSMaterial{CA: []byte("not a certificate")}, "db.example")
	assert.Error(t, err)
	_, err = tlsConfig(&dbdriver.TLSMaterial{Cert: certPEM}, "db.example")
	assert.Error(t, err)
}

// A TLS asset's connection never carries plaintext: go-ora turns TLS off for a
// redirect address whose protocol is tcp, so the dial refuses it.
func TestTLSConnectionRefusesPlaintext(t *testing.T) {
	dialed := func(context.Context, string, string) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			buf := make([]byte, 64)
			for {
				if _, err := s.Read(buf); err != nil {
					return
				}
			}
		}()
		return c, nil
	}
	dial := requireTLS(dialed)

	conn, err := dial(context.Background(), "tcp", "db.example:2484")
	require.NoError(t, err)
	_, err = conn.Write([]byte{0x00, 0x3a, 0x00, 0x00, 0x01, 0x00}) // a TNS CONNECT packet header
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plaintext")

	conn, err = dial(context.Background(), "tcp", "db.example:2484")
	require.NoError(t, err)
	_, err = conn.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01}) // a TLS ClientHello record
	require.NoError(t, err)
	_, err = conn.Write([]byte{0x00, 0x3a}) // later writes are TLS records of the same stream
	require.NoError(t, err)
}

// ---- session preparation: version check, date formats and current schema ----

// mockLogin returns a login that hands out the sqlmock connection, standing in for
// a go-ora login.
func mockLogin(t *testing.T) (func(context.Context) (driver.Conn, error), sqlmock.Sqlmock) {
	t.Helper()
	dsn := t.Name()
	db, mock, err := sqlmock.NewWithDSN(dsn, sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return func(context.Context) (driver.Conn, error) { return db.Driver().Open(dsn) }, mock
}

func expectVersion(mock sqlmock.Sqlmock, version string) {
	mock.ExpectQuery(versionQuery).WillReturnRows(sqlmock.NewRows([]string{"VERSION"}).AddRow(version))
}

// expectDateFormats expects the statement every session runs after the version check.
func expectDateFormats(mock sqlmock.Sqlmock) {
	mock.ExpectExec(dateFormatsStatement).WillReturnResult(sqlmock.NewResult(0, 0))
}

// A date or timestamp the grid shows ("2026-10-23 01:02:03", its fraction up to
// nanoseconds, " +08:00") is written back as a quoted literal (write.go), so the
// session converts text in exactly that layout.
func TestConnectSetsDateFormats(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	expectDateFormats(mock)

	db := sql.OpenDB(&connector{login: login})
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectReturnsDateFormatError(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	formatErr := errors.New("ORA-01821: date format not recognized")
	mock.ExpectExec(dateFormatsStatement).WillReturnError(formatErr)
	mock.ExpectClose()

	db := sql.OpenDB(&connector{login: login, schema: "HR"})
	defer func() { _ = db.Close() }()
	assert.ErrorIs(t, db.PingContext(context.Background()), formatErr)
	assert.NoError(t, mock.ExpectationsWereMet(), "the connection is closed, no schema is set")
}

func TestConnectSetsCurrentSchema(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	expectDateFormats(mock)
	mock.ExpectExec(`ALTER SESSION SET CURRENT_SCHEMA = "HR"`).WillReturnResult(sqlmock.NewResult(0, 0))

	db := sql.OpenDB(&connector{login: login, schema: "HR"})
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectQuotesSchema(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "23.0.0.0.0")
	expectDateFormats(mock)
	mock.ExpectExec(`ALTER SESSION SET CURRENT_SCHEMA = "my""Schema"`).WillReturnResult(sqlmock.NewResult(0, 0))

	db := sql.OpenDB(&connector{login: login, schema: `my"Schema`})
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectWithoutSchemaKeepsLoginSchema(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "21.0.0.0.0")
	expectDateFormats(mock)

	db := sql.OpenDB(&connector{login: login})
	defer func() { _ = db.Close() }()
	require.NoError(t, db.PingContext(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectRejectsServerBelow19(t *testing.T) {
	for _, version := range []string{"11.2.0.2.0", "12.1.0.2.0", "18.0.0.0.0"} {
		t.Run(version, func(t *testing.T) {
			login, mock := mockLogin(t)
			expectVersion(mock, version)
			mock.ExpectClose()

			db := sql.OpenDB(&connector{login: login, schema: "HR"})
			defer func() { _ = db.Close() }()
			err := db.PingContext(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), version)
			assert.Contains(t, err.Error(), "19c")
			assert.NoError(t, mock.ExpectationsWereMet(), "the connection is closed, no schema is set")
		})
	}
}

func TestConnectRejectsUnreadableVersion(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "unknown")
	mock.ExpectClose()

	db := sql.OpenDB(&connector{login: login})
	defer func() { _ = db.Close() }()
	err := db.PingContext(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"unknown"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConnectReturnsLoginErrorAsIs(t *testing.T) {
	loginErr := errors.New("ORA-01017: invalid username/password; logon denied")
	db := sql.OpenDB(&connector{login: func(context.Context) (driver.Conn, error) { return nil, loginErr }})
	defer func() { _ = db.Close() }()
	err := db.PingContext(context.Background())
	assert.ErrorIs(t, err, loginErr)
}

func TestConnectReturnsSchemaError(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	expectDateFormats(mock)
	schemaErr := errors.New("ORA-01435: user does not exist")
	mock.ExpectExec(`ALTER SESSION SET CURRENT_SCHEMA = "NOPE"`).WillReturnError(schemaErr)
	mock.ExpectClose()

	db := sql.OpenDB(&connector{login: login, schema: "NOPE"})
	defer func() { _ = db.Close() }()
	assert.ErrorIs(t, db.PingContext(context.Background()), schemaErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The wrapping connection passes statements through to the go-ora connection.
func TestConnPassesStatementsThrough(t *testing.T) {
	login, mock := mockLogin(t)
	expectVersion(mock, "19.0.0.0.0")
	expectDateFormats(mock)
	mock.ExpectQuery("SELECT 1 FROM DUAL").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow("1"))
	mock.ExpectExec("DELETE FROM T").WillReturnResult(sqlmock.NewResult(0, 3))

	db := sql.OpenDB(&connector{login: login})
	defer func() { _ = db.Close() }()
	var one string
	require.NoError(t, db.QueryRowContext(context.Background(), "SELECT 1 FROM DUAL").Scan(&one))
	assert.Equal(t, "1", one)
	res, err := db.ExecContext(context.Background(), "DELETE FROM T")
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}
