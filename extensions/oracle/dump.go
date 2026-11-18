package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Dump SQL File. The file runs in whichever user it is run as: nothing in it
// names the schema it came from, and no storage, tablespace or segment attribute
// is written. Oracle cannot turn foreign key checks off for a session, so the
// foreign keys are added after all data and dropped first in the drop section;
// it has no DROP … IF EXISTS before 23ai, so every drop is a PL/SQL block that
// ignores "does not exist" (one spelling for 19c to 23ai).
var (
	_ sqldb.DumpDialect      = dialect{}
	_ sqldb.DumpFileWriter   = dialect{}
	_ sqldb.DumpObjectWriter = dialect{}
)

// DumpSyntax: the file needs no session setting (its values do not depend on
// the importing session's NLS) and has no foreign key switch.
func (dialect) DumpSyntax() sqldb.DumpSyntax { return sqldb.DumpSyntax{} }

// Oracle errors of a drop whose object is not there.
const (
	oraTableMissing      = 942  // ORA-00942: table or view does not exist
	oraSequenceMissing   = 2289 // ORA-02289: sequence does not exist
	oraConstraintMissing = 2443 // ORA-02443: Cannot drop constraint - nonexistent constraint
)

// dropBlock runs statement and ignores the Oracle errors missing (their positive
// numbers) that say the object is not there; any other error stops the run.
func dropBlock(statement string, missing ...int) string {
	codes := make([]string, len(missing))
	for i, code := range missing {
		codes[i] = "-" + strconv.Itoa(code)
	}
	return "BEGIN EXECUTE IMMEDIATE " + sqlString(statement) + "; EXCEPTION WHEN OTHERS THEN IF SQLCODE NOT IN (" +
		strings.Join(codes, ", ") + ") THEN RAISE; END IF; END;\n/"
}

// DumpFile writes the user's own sequences (an identity column's is created with
// its table): all of the schema's, or for selected tables the ones their column
// defaults use, without which the tables cannot be created.
func (dialect) DumpFile(ctx context.Context, conn *sql.Conn, scope dbdriver.DumpScope, script *dbdriver.DumpFileScript) error {
	sequences, err := querySequences(ctx, conn, scope.Database)
	if err != nil {
		return sqldb.Wrap("sequences", err)
	}
	if !scope.Whole {
		used, err := sequencesUsed(ctx, conn, scope.Database, scope.Tables)
		if err != nil {
			return sqldb.Wrap("sequences the tables use", err)
		}
		sequences = slices.DeleteFunc(sequences, func(s sequence) bool { return !used[s.name] })
	}
	for _, s := range sequences {
		script.Types = append(script.Types, s.create())
		script.DropTypes = append(script.DropTypes, dropBlock("DROP SEQUENCE "+quoteIdent(s.name), oraSequenceMissing))
	}
	return nil
}

// sequence is a row of ALL_SEQUENCES, the numbers as text (they exceed int64).
type sequence struct {
	name, minValue, maxValue, increment, cycle, order, cache, lastNumber string
}

// create starts the sequence at LAST_NUMBER: with a cache it is the last value
// written to disk, past every value handed out, so the restored sequence gives
// none of them again.
func (s sequence) create() string {
	cycle, order, cache := "NOCYCLE", "NOORDER", "NOCACHE"
	if s.cycle == "Y" {
		cycle = "CYCLE"
	}
	if s.order == "Y" {
		order = "ORDER"
	}
	if s.cache != "0" {
		cache = "CACHE " + s.cache
	}
	return fmt.Sprintf("CREATE SEQUENCE %s MINVALUE %s MAXVALUE %s INCREMENT BY %s START WITH %s %s %s %s;",
		quoteIdent(s.name), s.minValue, s.maxValue, s.increment, s.lastNumber, cache, order, cycle)
}

// sequencesQuery lists the schema's sequences but the ones identity columns own.
const sequencesQuery = "SELECT s.sequence_name, TO_CHAR(s.min_value), TO_CHAR(s.max_value), TO_CHAR(s.increment_by), " +
	"s.cycle_flag, s.order_flag, TO_CHAR(s.cache_size), TO_CHAR(s.last_number) FROM all_sequences s " +
	"WHERE s.sequence_owner = " + ownerIs + " AND NOT EXISTS (SELECT 1 FROM all_tab_identity_cols i " +
	"WHERE i.owner = s.sequence_owner AND i.sequence_name = s.sequence_name) ORDER BY s.sequence_name"

func querySequences(ctx context.Context, conn *sql.Conn, schema string) ([]sequence, error) {
	rows, err := conn.QueryContext(ctx, sequencesQuery, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sequence
	for rows.Next() {
		var s sequence
		if err := rows.Scan(&s.name, &s.minValue, &s.maxValue, &s.increment, &s.cycle, &s.order, &s.cache, &s.lastNumber); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// sequenceUsesQuery reads the column defaults of the schema's tables (an
// identity column's names its own sequence). DATA_DEFAULT is a LONG, which no
// SQL condition takes, so the sequences are found in the text (sequenceUses).
const sequenceUsesQuery = "SELECT c.table_name, c.owner, c.data_default FROM all_tab_cols c " +
	"WHERE c.owner = " + ownerIs + " AND c.default_length > 0 AND c.identity_column = 'NO'"

// sequenceRef is a sequence a default names, as Oracle stores it:
// "SCHEMA"."S"."NEXTVAL" (or CURRVAL), whatever the user typed.
var sequenceRef = regexp.MustCompile(`"((?:[^"]|"")+)"\."((?:[^"]|"")+)"\."(?:NEXTVAL|CURRVAL)"`)

// sequenceUses maps each table (only table when it is not empty) to the
// sequences of its own schema its column defaults use, and gives the schema's
// name as Oracle spells it.
func sequenceUses(ctx context.Context, conn *sql.Conn, schema, table string) (map[string][]string, string, error) {
	query, args := sequenceUsesQuery, []any{schema}
	if table != "" {
		query, args = query+" AND c.table_name = :2", append(args, table)
	}
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	uses := map[string][]string{}
	var owner string
	for rows.Next() {
		var name string
		var def sql.NullString
		if err := rows.Scan(&name, &owner, &def); err != nil {
			return nil, "", err
		}
		for _, match := range sequenceRef.FindAllStringSubmatch(def.String, -1) {
			sequenceOwner, sequence := strings.ReplaceAll(match[1], `""`, `"`), strings.ReplaceAll(match[2], `""`, `"`)
			if sequenceOwner == owner && !slices.Contains(uses[name], sequence) {
				uses[name] = append(uses[name], sequence)
			}
		}
	}
	return uses, owner, rows.Err()
}

// sequencesUsed is the set of the schema's sequences that tables use.
func sequencesUsed(ctx context.Context, conn *sql.Conn, schema string, tables []string) (map[string]bool, error) {
	uses, _, err := sequenceUses(ctx, conn, schema, "")
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, table := range tables {
		for _, sequence := range uses[table] {
			used[sequence] = true
		}
	}
	return used, nil
}

// DumpObject writes a table as the dump needs it; views, functions, procedures
// and triggers keep the session's default for now.
func (dialect) DumpObject(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	switch object.Kind {
	case dbdriver.DumpTable:
		return dumpTable(ctx, conn, object, script)
	}
	return nil
}

// dumpTable writes a table without its schema or storage: the CREATE TABLE with
// its columns, defaults, NOT NULL, primary key, unique and check constraints and
// identity definitions, its comments and the indexes no constraint creates
// (unique ones always, a foreign key may need them; the rest with
// Options.Indexes). The foreign keys are added after all data. With data, a
// GENERATED ALWAYS identity column takes the rows' values as BY DEFAULT and goes
// back to ALWAYS after all data; every identity column then continues past the
// rows (Oracle has no OVERRIDING).
func dumpTable(ctx context.Context, conn *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	schema, table := object.Database, object.Name
	ref := quoteIdent(table)
	var create string
	var indexes []string
	if err := withDumpTransforms(ctx, conn, func() error {
		var err error
		if create, err = metadataDDL(ctx, conn, schema, table, "TABLE"); err != nil {
			return err
		}
		indexes, err = indexDDL(ctx, conn, schema, table, object.Options.Indexes)
		return err
	}); err != nil {
		return err
	}
	create, err := withoutSequenceSchema(ctx, conn, schema, table, create)
	if err != nil {
		return sqldb.Wrap("sequences", err)
	}
	comments, err := tableComments(ctx, conn, schema, table)
	if err != nil {
		return sqldb.Wrap("comments", err)
	}
	foreignKeys, err := queryForeignKeys(ctx, conn, schema, table)
	if err != nil {
		return sqldb.Wrap("foreign keys", err)
	}

	script.Drop = []string{dropBlock("DROP TABLE "+ref, oraTableMissing)}
	script.Definition = slices.Concat([]string{sqldb.TerminateStatement(create)}, comments, indexes)
	for _, fk := range foreignKeys {
		script.ForeignKeys = append(script.ForeignKeys, fk.add(ref))
		script.DropForeignKeys = append(script.DropForeignKeys,
			dropBlock("ALTER TABLE "+ref+" DROP CONSTRAINT "+quoteIdent(fk.name), oraTableMissing, oraConstraintMissing))
	}
	if !object.WithData {
		return nil
	}
	columns, err := dumpColumns(ctx, conn, schema, table)
	if err != nil {
		return sqldb.Wrap("columns", err)
	}
	for _, c := range columns {
		if !c.virtual {
			script.DataColumns = append(script.DataColumns, c.name)
		}
		if c.generation == "" {
			continue
		}
		column := quoteIdent(c.name)
		if c.generation == "ALWAYS" {
			script.DataPrologue = append(script.DataPrologue,
				"ALTER TABLE "+ref+" MODIFY ("+column+" GENERATED BY DEFAULT AS IDENTITY);")
		}
		generated := c.generation
		if c.onNull {
			generated += " ON NULL"
		}
		script.PostData = append(script.PostData,
			"ALTER TABLE "+ref+" MODIFY ("+column+" GENERATED "+generated+" AS IDENTITY (START WITH LIMIT VALUE));")
	}
	return nil
}

// DBMS_METADATA's session transforms for the dump's DDL: no schema, no storage,
// tablespace or segment attributes, and no foreign keys (they come after the
// data).
const (
	setDumpTransforms = "BEGIN " +
		"DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'SEGMENT_ATTRIBUTES', FALSE); " +
		"DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'STORAGE', FALSE); " +
		"DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'TABLESPACE', FALSE); " +
		"DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'EMIT_SCHEMA', FALSE); " +
		"DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'REF_CONSTRAINTS', FALSE); " +
		"END;"
	resetDumpTransforms = "BEGIN DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'DEFAULT'); END;"
)

// resetTimeout bounds resetting the transforms, which runs also after ctx ended.
const resetTimeout = 30 * time.Second

// withDumpTransforms runs read with the dump's transforms set on conn. They are
// session state of a pooled connection, which View DDL and the dump's
// definitions read later with the database's own DDL: they are reset after
// read, whatever happened, and a connection they could not be reset on is
// closed instead of going back to the pool.
func withDumpTransforms(ctx context.Context, conn *sql.Conn, read func() error) (err error) {
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resetTimeout)
		defer cancel()
		if _, resetErr := conn.ExecContext(resetCtx, resetDumpTransforms); resetErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fmt.Errorf("reset DBMS_METADATA transforms: %w", resetErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, setDumpTransforms); err != nil {
		return fmt.Errorf("set DBMS_METADATA transforms: %w", err)
	}
	return read()
}

// indexesQuery gives the DDL of a table's indexes that no primary key or unique
// constraint creates (the CREATE TABLE has those), leaving out LOB and IOT
// indexes and the ones Oracle generates.
const indexesQuery = "SELECT DBMS_METADATA.GET_DDL('INDEX', i.index_name, i.owner) AS DDL FROM all_indexes i " +
	"WHERE i.table_owner = " + ownerIs + " AND i.table_name = :2 AND i.owner = i.table_owner " +
	"AND i.index_type NOT IN ('LOB', 'IOT - TOP') AND i.generated = 'N' " +
	"AND NOT EXISTS (SELECT 1 FROM all_constraints c WHERE c.owner = i.table_owner AND c.table_name = i.table_name " +
	"AND c.index_name = i.index_name AND c.constraint_type IN ('P', 'U'))"

// indexDDL is the table's separate indexes, each ending in ";": the unique ones,
// and the others too with all.
func indexDDL(ctx context.Context, conn *sql.Conn, schema, table string, all bool) ([]string, error) {
	query := indexesQuery
	if !all {
		query += " AND i.uniqueness = 'UNIQUE'"
	}
	ddl, err := sqldb.QueryNames(ctx, conn, query+" ORDER BY i.index_name", schema, table)
	if err != nil {
		return nil, sqldb.Wrap("indexes", err)
	}
	for i, statement := range ddl {
		ddl[i] = sqldb.TerminateStatement(strings.TrimSpace(statement))
	}
	return ddl, nil
}

// withoutSequenceSchema drops the schema from the table's own-schema sequences
// in ddl: Oracle stores a default s.NEXTVAL as "SCHEMA"."S"."NEXTVAL", which
// would make the copy in another user use the original's sequence.
func withoutSequenceSchema(ctx context.Context, conn *sql.Conn, schema, table, ddl string) (string, error) {
	uses, owner, err := sequenceUses(ctx, conn, schema, table)
	if err != nil {
		return "", err
	}
	for _, sequence := range uses[table] {
		ddl = strings.ReplaceAll(ddl, quoteIdent(owner)+"."+quoteIdent(sequence)+".", quoteIdent(sequence)+".")
	}
	return ddl, nil
}

// commentsQuery lists the table's comment (no column name) and its columns', in
// column order.
const commentsQuery = "WITH t AS (SELECT " + ownerIs + " AS o, :2 AS n FROM dual) " +
	"SELECT column_name, comments FROM (" +
	"SELECT 0 AS position, NULL AS column_name, tc.comments FROM t JOIN all_tab_comments tc ON tc.owner = t.o AND tc.table_name = t.n " +
	"WHERE tc.comments IS NOT NULL " +
	"UNION ALL " +
	"SELECT c.column_id, cc.column_name, cc.comments FROM t JOIN all_col_comments cc ON cc.owner = t.o AND cc.table_name = t.n " +
	"JOIN all_tab_cols c ON c.owner = cc.owner AND c.table_name = cc.table_name AND c.column_name = cc.column_name " +
	"WHERE cc.comments IS NOT NULL) ORDER BY position"

// tableComments is a COMMENT ON statement per comment of the table and its
// columns.
func tableComments(ctx context.Context, conn *sql.Conn, schema, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, commentsQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ref := quoteIdent(table)
	var out []string
	for rows.Next() {
		var column sql.NullString
		var comment string
		if err := rows.Scan(&column, &comment); err != nil {
			return nil, err
		}
		if column.Valid {
			out = append(out, "COMMENT ON COLUMN "+ref+"."+quoteIdent(column.String)+" IS "+sqlString(comment)+";")
		} else {
			out = append(out, "COMMENT ON TABLE "+ref+" IS "+sqlString(comment)+";")
		}
	}
	return out, rows.Err()
}

const foreignKeysQuery = "SELECT c.constraint_name, cc.column_name, c.owner, rc.owner, rc.table_name, rcc.column_name, " +
	"c.delete_rule, c.deferrable, c.deferred, c.status, c.validated FROM all_constraints c " +
	"JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name AND cc.table_name = c.table_name " +
	"JOIN all_constraints rc ON rc.owner = c.r_owner AND rc.constraint_name = c.r_constraint_name " +
	"JOIN all_cons_columns rcc ON rcc.owner = rc.owner AND rcc.constraint_name = rc.constraint_name AND rcc.position = cc.position " +
	"WHERE c.constraint_type = 'R' AND c.owner = " + ownerIs + " AND c.table_name = :2 " +
	"ORDER BY c.constraint_name, cc.position"

// foreignKey is a table's foreign key as ALL_CONSTRAINTS describes it.
type foreignKey struct {
	name                 string
	columns, refColumns  []string
	owner, refOwner      string
	refTable, deleteRule string
	deferrable, deferred string
	status, validated    string
}

// add is the ALTER TABLE adding the foreign key with its name (a system
// generated one too, so a later dump's drop finds it), its delete rule,
// deferral and state. A parent table in the same schema is named without it; one
// in another schema is that schema's.
func (fk foreignKey) add(ref string) string {
	parent := quoteIdent(fk.refTable)
	if fk.refOwner != fk.owner {
		parent = quoteIdent(fk.refOwner) + "." + parent
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		ref, quoteIdent(fk.name), quotedNames(fk.columns), parent, quotedNames(fk.refColumns))
	if fk.deleteRule != "NO ACTION" {
		b.WriteString(" ON DELETE " + fk.deleteRule)
	}
	if fk.deferrable == "DEFERRABLE" {
		b.WriteString(" DEFERRABLE")
		if fk.deferred == "DEFERRED" {
			b.WriteString(" INITIALLY DEFERRED")
		}
	}
	if fk.status == "DISABLED" {
		b.WriteString(" DISABLE")
	} else if fk.validated == "NOT VALIDATED" {
		b.WriteString(" ENABLE NOVALIDATE")
	}
	b.WriteString(";")
	return b.String()
}

func quotedNames(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = quoteIdent(name)
	}
	return strings.Join(quoted, ", ")
}

func queryForeignKeys(ctx context.Context, conn *sql.Conn, schema, table string) ([]foreignKey, error) {
	rows, err := conn.QueryContext(ctx, foreignKeysQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []foreignKey
	for rows.Next() {
		var fk foreignKey
		var column, refColumn string
		if err := rows.Scan(&fk.name, &column, &fk.owner, &fk.refOwner, &fk.refTable, &refColumn,
			&fk.deleteRule, &fk.deferrable, &fk.deferred, &fk.status, &fk.validated); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].name == fk.name {
			out[n-1].columns = append(out[n-1].columns, column)
			out[n-1].refColumns = append(out[n-1].refColumns, refColumn)
			continue
		}
		fk.columns, fk.refColumns = []string{column}, []string{refColumn}
		out = append(out, fk)
	}
	return out, rows.Err()
}

// dumpColumnsQuery lists the columns SELECT * reads, in table order, with
// whether each is virtual and how an identity column generates its values.
const dumpColumnsQuery = "SELECT c.column_name, c.virtual_column, ic.generation_type, c.default_on_null FROM all_tab_cols c " +
	"LEFT JOIN all_tab_identity_cols ic ON ic.owner = c.owner AND ic.table_name = c.table_name AND ic.column_name = c.column_name " +
	"WHERE c.owner = " + ownerIs + " AND c.table_name = :2 AND c.hidden_column = 'NO' ORDER BY c.column_id"

// dumpColumn is a column of the table's rows.
type dumpColumn struct {
	name    string
	virtual bool
	// generation is an identity column's ALWAYS or BY DEFAULT, else empty; onNull
	// is DEFAULT ON NULL.
	generation string
	onNull     bool
}

func dumpColumns(ctx context.Context, conn *sql.Conn, schema, table string) ([]dumpColumn, error) {
	rows, err := conn.QueryContext(ctx, dumpColumnsQuery, schema, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []dumpColumn
	for rows.Next() {
		var c dumpColumn
		var virtual, onNull string
		var generation sql.NullString
		if err := rows.Scan(&c.name, &virtual, &generation, &onNull); err != nil {
			return nil, err
		}
		c.virtual, c.generation, c.onNull = virtual == "YES", generation.String, onNull == "YES"
		out = append(out, c)
	}
	return out, rows.Err()
}
