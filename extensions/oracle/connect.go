package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	goora "github.com/sijms/go-ora/v2"
	// The guest has no system certificate store (wasip1 reads no files), so
	// without these roots a server certificate from a public CA would verify only
	// with the CA pasted into the asset. They are used only where the system
	// offers none.
	_ "golang.org/x/crypto/x509roots/fallback"
)

// minMajorVersion is the oldest Oracle release the driver supports: 19c brings
// OFFSET … FETCH, 128-byte identifiers and ORACLE_MAINTAINED, which the driver
// uses throughout.
const minMajorVersion = 19

// versionQuery reads the database release ("19.0.0.0.0"). PRODUCT_COMPONENT_VERSION
// is readable by every user, unlike V$INSTANCE.
const versionQuery = `SELECT VERSION FROM PRODUCT_COMPONENT_VERSION WHERE PRODUCT LIKE 'Oracle%' AND ROWNUM = 1`

// dateFormatsStatement makes the session convert text to DATE, TIMESTAMP and
// TIMESTAMP WITH TIME ZONE in the layout the grid shows them (values.go), so a
// shown value written back or filtered by as a quoted literal converts exactly: FF9
// reads any fraction up to nanoseconds, and a value shown without one too. With a
// time zone the fraction's dot is X: under "SS.FF9" the "." took the sign of a
// negative offset when no fraction precedes it (-05:30 read as +05:30).
const dateFormatsStatement = `ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD HH24:MI:SS' ` +
	`NLS_TIMESTAMP_FORMAT = 'YYYY-MM-DD HH24:MI:SS.FF9' NLS_TIMESTAMP_TZ_FORMAT = 'YYYY-MM-DD HH24:MI:SSXFF9 TZH:TZM'`

// open logs in to Oracle through the reach path the host handed over and
// confirms the database answers. Every connection of the returned pool is one
// go-ora login made by connector: the session is per schema (the host opens one
// per Config.Database), so each connection sets that schema as its current one.
func open(ctx context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) {
	login, err := goOraLogin(req)
	if err != nil {
		return nil, nil, err
	}
	db := sql.OpenDB(&connector{login: login, schema: req.Config.Database})
	if err := sqldb.Establish(ctx, db, nil); err != nil {
		return nil, nil, err
	}
	return db, dialect{}, nil
}

// connectURL is the go-ora connection URL of req: the service name, or the SID
// when the asset connects by SID, the login role, Kerberos and TCPS. The schema
// (Config.Database) is not part of it; connector sets it after the login.
func connectURL(req dbdriver.OpenRequest) string {
	cfg := req.Config
	service, _ := req.DriverOptions[optServiceName].(string)
	opts := map[string]string{}
	if useSID, _ := req.DriverOptions[optUseSID].(bool); useSID {
		opts["SID"] = service
		service = ""
	}
	if role, _ := req.DriverOptions[optRole].(string); role != "" && role != roleNormal {
		opts["DBA PRIVILEGE"] = role
	}
	if req.Kerberos != nil {
		opts["AUTH TYPE"] = "KERBEROS"
	}
	if req.TLS != nil {
		opts["SSL"] = "true"
	}
	return goora.BuildUrl(cfg.Host, cfg.Port, service, cfg.Username, req.Password, opts)
}

// goOraLogin returns the function that logs in one go-ora connection of req:
// dialing through req.Transport.Dial (the asset's tunnel / proxy, also for an
// address a listener redirects to), doing TCPS with the asset's certificates and
// asking the host for Kerberos tickets. Each login gets its own connector, so the
// TLS config go-ora rewrites and the Kerberos context belong to that login only.
func goOraLogin(req dbdriver.OpenRequest) (func(ctx context.Context) (driver.Conn, error), error) {
	url := connectURL(req)
	dial := req.Transport.Dial
	var tlsConf *tls.Config
	if req.TLS != nil {
		var err error
		if tlsConf, err = tlsConfig(req.TLS, req.Config.Host); err != nil {
			return nil, err
		}
		dial = requireTLS(dial)
	}
	return func(ctx context.Context) (driver.Conn, error) {
		c, ok := goora.NewConnector(url).(*goora.OracleConnector)
		if !ok {
			return nil, errors.New("oracle: go-ora returned an unexpected connector type")
		}
		c.Dialer(dial)
		if tlsConf != nil {
			c.WithTLSConfig(tlsConf.Clone())
		}
		if req.Kerberos != nil {
			c.WithKerberosAuth(kerberosAuth{ctx: ctx, source: req.Kerberos})
		}
		return c.Connect(ctx)
	}, nil
}

// kerberosAuth is go-ora's Kerberos authenticator: in the login handshake it asks
// the host for a ticket of the service principal the database names.
type kerberosAuth struct {
	ctx    context.Context
	source *dbdriver.Kerberos
}

// Authenticate is called by go-ora with the database host first, then the
// service name.
func (a kerberosAuth) Authenticate(server, service string) ([]byte, error) {
	return a.source.Ticket(a.ctx, dbdriver.KerberosSPN(service, server))
}

// tlsConfig builds the TCPS configuration from the asset's certificate settings;
// without a CA the chain is checked against the public roots. go-ora overwrites
// ServerName with the address it dials, so the chain is verified here against the
// configured server name, or the asset host when none is configured — also after
// a redirect to another address. Material that cannot be read is an error: the
// asset never falls back to an unencrypted connection.
func tlsConfig(m *dbdriver.TLSMaterial, assetHost string) (*tls.Config, error) {
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	var roots *x509.CertPool
	if len(m.CA) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(m.CA) {
			return nil, errors.New("oracle: the CA certificate is not a PEM certificate (export a wallet with orapki first)")
		}
	}
	if len(m.Cert) > 0 || len(m.Key) > 0 {
		pair, err := tls.X509KeyPair(m.Cert, m.Key)
		if err != nil {
			return nil, fmt.Errorf("oracle: client certificate: %w", err)
		}
		conf.Certificates = []tls.Certificate{pair}
	}
	// Go's own verification would check the name go-ora wrote into ServerName;
	// VerifyConnection checks the chain against the right name instead.
	conf.InsecureSkipVerify = true
	if m.Insecure {
		return conf, nil
	}
	name := m.ServerName
	if name == "" {
		name = assetHost
	}
	conf.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("oracle: the server sent no certificate")
		}
		intermediates := x509.NewCertPool()
		for _, c := range cs.PeerCertificates[1:] {
			intermediates.AddCert(c)
		}
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: intermediates})
		return err
	}
	return conf, nil
}

// requireTLS wraps dial so that a connection refuses to send anything but TLS:
// go-ora turns TCPS off for a redirect address whose protocol is tcp, which on a
// TCPS asset would send the login in plaintext.
func requireTLS(dial dbdriver.DialFunc) dbdriver.DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &tlsOnlyConn{Conn: c}, nil
	}
}

// tlsRecordHandshake is the content type of the TLS record that opens every TLS
// connection (the ClientHello).
const tlsRecordHandshake = 0x16

type tlsOnlyConn struct {
	net.Conn
	started bool
}

func (c *tlsOnlyConn) Write(p []byte) (int, error) {
	if !c.started && len(p) > 0 {
		if p[0] != tlsRecordHandshake {
			return 0, fmt.Errorf("oracle: refusing to send plaintext to %s on a TCPS asset", c.RemoteAddr())
		}
		c.started = true
	}
	return c.Conn.Write(p)
}

// connector logs in one connection per Connect and prepares its session: it
// rejects a database older than 19c, sets the date formats and makes the
// session's schema current.
type connector struct {
	// login logs in one go-ora connection (goOraLogin).
	login func(ctx context.Context) (driver.Conn, error)
	// schema is the schema the host opened the session for (Config.Database);
	// empty keeps the login user's own.
	schema string
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.prepare(ctx, inner); err != nil {
		return nil, errors.Join(err, inner.Close())
	}
	return &conn{inner: inner}, nil
}

func (c *connector) Driver() driver.Driver { return goora.NewDriver() }

func (c *connector) prepare(ctx context.Context, inner driver.Conn) error {
	version, err := queryString(ctx, inner, versionQuery)
	if err != nil {
		return fmt.Errorf("oracle: read the database version: %w", err)
	}
	if err := checkVersion(version); err != nil {
		return err
	}
	if _, err := execContext(ctx, inner, dateFormatsStatement, nil); err != nil {
		return err
	}
	if c.schema == "" {
		return nil
	}
	_, err = execContext(ctx, inner, "ALTER SESSION SET CURRENT_SCHEMA = "+quoteIdent(c.schema), nil)
	return err
}

// checkVersion rejects a database release older than minMajorVersion, naming it.
func checkVersion(version string) error {
	major, _, _ := strings.Cut(strings.TrimSpace(version), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return fmt.Errorf("oracle: cannot read the database version %q; OpsKat supports Oracle Database 19c and later", version)
	}
	if n < minMajorVersion {
		return fmt.Errorf("oracle: the database is Oracle %s; OpsKat supports Oracle Database 19c and later", version)
	}
	return nil
}

// quoteIdent quotes an Oracle identifier exactly as given (case-sensitive).
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// queryString reads the first column of the first row of query on a raw
// connection, before database/sql has the connection.
func queryString(ctx context.Context, c driver.Conn, query string) (string, error) {
	q, ok := c.(driver.QueryerContext)
	if !ok {
		return "", errors.New("oracle: connection cannot run queries")
	}
	rows, err := q.QueryContext(ctx, query, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	dest := make([]driver.Value, len(rows.Columns()))
	if len(dest) == 0 {
		return "", errors.New("oracle: query returned no columns")
	}
	if err := rows.Next(dest); err != nil {
		return "", err
	}
	switch v := dest[0].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func execContext(ctx context.Context, c driver.Conn, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.(driver.ExecerContext)
	if !ok {
		return nil, errors.New("oracle: connection cannot execute statements")
	}
	return e.ExecContext(ctx, query, args)
}

// conn is the driver's connection over the go-ora connection it wraps. Queries
// and executions go through exec.go (statement splitting, paging, the result
// rows); the rest passes through.
type conn struct {
	inner driver.Conn
}

var (
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)
)

func (c *conn) Prepare(query string) (driver.Stmt, error) { return c.inner.Prepare(query) }
func (c *conn) Close() error                              { return c.inner.Close() }

// Begin is part of driver.Conn; database/sql calls BeginTx.
func (c *conn) Begin() (driver.Tx, error) { return c.inner.Begin() } //nolint:staticcheck // see above

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.inner.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, query)
	}
	return c.inner.Prepare(query)
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.inner.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Begin()
}

func (c *conn) Ping(ctx context.Context) error {
	if p, ok := c.inner.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *conn) ResetSession(ctx context.Context) error {
	if r, ok := c.inner.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *conn) CheckNamedValue(nv *driver.NamedValue) error {
	if n, ok := c.inner.(driver.NamedValueChecker); ok {
		return n.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}
