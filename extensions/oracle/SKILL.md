---
name: oracle
description: Oracle Database (19c and later) as a driver of SQL database assets. Read it before running SQL on an asset whose driver is oracle, or when setting up such an asset.
---

# Oracle

An Oracle asset is a SQL database asset (`database`) whose driver is `oracle`. It
connects to one database service; what OpsKat calls a "database" of the asset is an
Oracle **schema** (a user's objects), and the schema you pick is the session's
current schema, so unqualified names resolve in it. Qualify with `"OWNER"."TABLE"`
to reach another schema's objects.

Only Oracle Database 19c and later is supported. Connecting to an older release
fails with an error naming the version the database reported.

## Connection settings

The service is set in the driver's own fields (`driver_options` for opsctl and the
AI), not in the generic database field:

- `service_name` (required) — the service name, such as `ORCLPDB1`.
- `use_sid` — `true` to treat `service_name` as an instance SID instead.
- `role` — `NORMAL` (default), `SYSDBA` or `SYSOPER`.

Authentication is the asset's password, or Kerberos (the asset's Kerberos settings;
the username may then be left empty). The SSH tunnel / proxy settings apply to every
connection, including the address a listener redirects to (shared server
dispatchers, SCAN / RAC local listeners).

**TCPS** uses the asset's TLS certificate settings, which take PEM only. An Oracle
wallet (`cwallet.sso` / `ewallet.p12`) has to be exported first, for example:

```bash
orapki wallet export -wallet /path/to/wallet -dn "CN=root CA" -cert ca.pem
orapki wallet export -wallet /path/to/wallet -dn "CN=client" -cert client.pem
openssl pkcs12 -in /path/to/wallet/ewallet.p12 -nocerts -nodes -out client.key
```

With no server name set, the certificate is checked against the asset's host name;
with no CA certificate set, against the public certificate authorities.
A certificate that fails the check or cannot be read is an error; the connection is
never made without TLS.

**Known limitation (go-ora):** on a database whose character set is not Unicode (such as
ZHS16GBK), a password containing non-ASCII characters cannot log in (ORA-01017).
Use an ASCII password there; data in any language reads and writes correctly.

## Writing SQL for Oracle

- A text may hold several statements; they run one at a time. End a plain statement
  with `;` (the driver removes it before running the statement). A PL/SQL block —
  `BEGIN` / `DECLARE`, or `CREATE [OR REPLACE] PROCEDURE / FUNCTION / PACKAGE /
  TRIGGER / TYPE` — keeps its closing `;` and runs until a line holding only `/`, or
  the end of the text: put that `/` line after a block that more statements follow.
  A query that declares functions in its WITH clause (`WITH FUNCTION … SELECT …`)
  counts as a PL/SQL block and also runs to such a `/` line or the end of the text.
- Page with `OFFSET n ROWS FETCH NEXT m ROWS ONLY`; there is no `LIMIT`.
- A subquery alias takes no `AS` (`SELECT * FROM (…) t`), and an alias must not
  start with `_`.
- Bind variables are `:1` / `:name`, never `?`.
- An empty string `''` is `NULL` in Oracle.
- Outside an explicit transaction every statement commits on its own; a later
  `ROLLBACK` does not undo it.
- On 19c an `XMLType` column may read as `NULL` (a known limitation of go-ora, the
  Oracle client the driver uses); select it as `XMLSERIALIZE(CONTENT col AS CLOB)`
  to see its value. The table view shows it serialized.
- Selecting a 23ai `BOOLEAN` column in the query panel shows `1` / `0`: go-ora
  reports a BOOLEAN as a plain NUMBER (a known limitation). The table view shows
  `true` / `false`.
- Oracle errors come back as they are, with their `ORA-` number.
