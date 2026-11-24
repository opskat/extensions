package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
)

// The other real-database tests call the driver in this process, where memory is
// plentiful. These load the extension the way the app does — built for wasip1,
// run by the host's runtime (extension.LoadPlugin) with its memory cap per
// instance — and drive it with the host's wire calls (extwire), so a value that
// does not fit the instance fails here as it would in the app. The host's own
// driver adapter (opskat internal/extreg/extdriver) cannot be imported from this
// module; wasmSession does what it does for a dump, a SQL file run, an export
// and a copy: one call per operation, a cursor read page by page on the
// instance it was opened on, row statements in parts of about
// extwire.DriverRowsBytes, the bytes of a binary value named in both directions,
// and a dump's table data written by the guest page by page when its session says
// it can (extwire.DriverOpened.TableStatements).
// The sizes and the bytes' encoding are the host's own (extwire.DriverStreamRows,
// RowParts, RowBytesOf, DriverStreamPage.RestoreBytes), so they cannot drift.

// buildWasm builds the extension's main.wasm as `make build-backend` does.
func buildWasm(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "main.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", out, ".")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build main.wasm: %s", output)
	wasm, err := os.ReadFile(out)
	require.NoError(t, err)
	return wasm
}

// peakMemory records the largest linear memory an instance grew to (a Go
// guest's memory never shrinks, so this is its peak), and its plugin's the
// largest of its instances'.
type peakMemory struct {
	peak   atomic.Uint64
	parent *peakMemory
}

func (p *peakMemory) Allocate(capacity, _ uint64) experimental.LinearMemory {
	m := &trackedMemory{peak: p, buf: make([]byte, 0, capacity)}
	return m
}

// MiB is the peak in MiB.
func (p *peakMemory) MiB() float64 { return float64(p.peak.Load()) / (1 << 20) }

type trackedMemory struct {
	peak *peakMemory
	buf  []byte
}

func (m *trackedMemory) Reallocate(size uint64) []byte {
	if size > uint64(cap(m.buf)) {
		grown := make([]byte, size)
		copy(grown, m.buf)
		m.buf = grown
	}
	m.buf = m.buf[:size]
	for p := m.peak; p != nil; p = p.parent {
		for {
			old := p.peak.Load()
			if size <= old || p.peak.CompareAndSwap(old, size) {
				break
			}
		}
	}
	return m.buf
}

func (m *trackedMemory) Free() { m.buf = nil }

// wasmDriver is the loaded extension and the memory its instances grew to: the
// largest, and per operation the largest an instance grew to during one.
type wasmDriver struct {
	plugin *extension.Plugin
	memory *peakMemory

	mu     sync.Mutex
	growth map[string]float64
}

// grew records that an instance grew to mib during op.
func (d *wasmDriver) grew(op string, mib float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.growth[op] = max(d.growth[op], mib)
}

// growthReport lists the operations an instance grew during, with the size it
// grew to, largest first.
func (d *wasmDriver) growthReport() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ops := make([]string, 0, len(d.growth))
	for op := range d.growth {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool { return d.growth[ops[i]] > d.growth[ops[j]] })
	parts := make([]string, len(ops))
	for i, op := range ops {
		parts[i] = fmt.Sprintf("%s %.1f", op, d.growth[op])
	}
	return strings.Join(parts, ", ")
}

// wasmCache keeps the compiled module between the plugins a test loads.
var wasmCache = wazero.NewCompilationCache()

// loadWasmDriver loads wasm with the extension's manifest.json, its describe()
// answer applied, as the app does.
func loadWasmDriver(t *testing.T, wasm []byte) *wasmDriver {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile("manifest.json")
	require.NoError(t, err)
	manifest, err := extension.ParseManifest(raw)
	require.NoError(t, err)
	host := func(m *extension.Manifest) extension.HostProvider {
		return extension.NewCapabilityHost(extension.NewDefaultHostProvider(extension.DefaultHostConfig{}), m, t.TempDir())
	}
	probe, err := extension.LoadPlugin(ctx, manifest, wasm, host(manifest), wasmCache)
	require.NoError(t, err)
	payload, err := probe.Describe(ctx)
	require.NoError(t, probe.Close(ctx))
	require.NoError(t, err)
	desc, err := extension.ParseDescriptor(payload)
	require.NoError(t, err)
	manifest = extension.Described(manifest.Manifest, desc)
	plugin, err := extension.LoadPlugin(ctx, manifest, wasm, host(manifest), wasmCache)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plugin.Close(context.Background()) })
	return &wasmDriver{plugin: plugin, memory: &peakMemory{}, growth: map[string]float64{}}
}

// open opens a session of the oracle driver as target's user on schema.
func (d *wasmDriver) open(t *testing.T, target oracleTarget, schema string) *wasmSession {
	t.Helper()
	req := target.openRequest(schema)
	scope := &extension.Scope{
		Asset:     &extension.AssetRef{ID: 1, Type: "database"},
		Ops:       []string{extension.OpLog, extension.IOOpenOp("tcp"), extension.OpIOSetDeadline, extension.OpToolShouldStop},
		Endpoints: []extension.Endpoint{{Host: target.host, Port: strconv.Itoa(target.port)}},
		Route:     &extension.Route{Dial: extension.DialContextFunc(req.Transport.Dial), Direct: true},
	}
	s := &wasmSession{driver: d, open: req, scope: scope}
	inst, err := s.acquire(integrationContext(t))
	require.NoError(t, err)
	s.release(inst)
	t.Cleanup(s.close)
	return s
}

// wasmSession is a session of the driver in the guest, on instances of its own.
type wasmSession struct {
	driver *wasmDriver
	open   dbdriver.OpenRequest
	scope  *extension.Scope

	// tableStatements is what the first login answered for
	// extwire.DriverOpened.TableStatements.
	tableStatements bool

	mu   sync.Mutex
	idle []*wasmInstance
	all  []*wasmInstance
}

var (
	_ dbdriver.DumpSource   = (*wasmSession)(nil)
	_ dbdriver.DumpDialect  = (*wasmSession)(nil)
	_ dbdriver.ScriptRunner = (*wasmSession)(nil)
)

// wasmInstance is one instance logged in to the database.
type wasmInstance struct {
	inst    *extension.Instance
	driver  *wasmDriver
	memory  *peakMemory
	session uint64
	mu      sync.Mutex
	// broken is the failure (a trap, running out of memory) that left the
	// instance unusable.
	broken error
}

// acquire takes an idle instance or logs in a new one.
func (s *wasmSession) acquire(ctx context.Context) (*wasmInstance, error) {
	s.mu.Lock()
	if n := len(s.idle); n > 0 {
		inst := s.idle[n-1]
		s.idle = s.idle[:n-1]
		s.mu.Unlock()
		return inst, nil
	}
	s.mu.Unlock()
	memory := &peakMemory{parent: s.driver.memory}
	leased, err := s.driver.plugin.Lease(experimental.WithMemoryAllocator(ctx, memory), s.scope)
	if err != nil {
		return nil, err
	}
	inst := &wasmInstance{inst: leased, driver: s.driver, memory: memory}
	var opened extwire.DriverOpened
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpOpen, Args: extwire.DriverArgs{
		Driver: driverID, Config: s.open.Config, Password: s.open.Password, DriverOptions: s.open.DriverOptions,
	}}, &opened)
	if err != nil {
		_ = leased.Close(context.Background())
		return nil, err
	}
	inst.session = opened.Session
	s.mu.Lock()
	if len(s.all) == 0 {
		s.tableStatements = opened.TableStatements
	}
	s.all = append(s.all, inst)
	s.mu.Unlock()
	return inst, nil
}

func (s *wasmSession) release(inst *wasmInstance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst.broken == nil {
		s.idle = append(s.idle, inst)
	}
}

// healthy reports an instance a fault left unusable, and otherwise whether the
// session still answers.
func (s *wasmSession) healthy(ctx context.Context) error {
	s.mu.Lock()
	for _, inst := range s.all {
		if inst.broken != nil {
			s.mu.Unlock()
			return inst.broken
		}
	}
	s.mu.Unlock()
	_, err := s.ListTables(ctx, s.open.Config.Database)
	return err
}

func (s *wasmSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, inst := range s.all {
		_ = inst.inst.Close(context.Background())
	}
	s.all, s.idle = nil, nil
}

// call runs one operation and decodes its result into out; the operation's own
// failure is the returned error. A failure of the guest itself (a trap) leaves
// the instance broken.
func (i *wasmInstance) call(ctx context.Context, call extwire.DriverCall, out any) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.broken != nil {
		return fmt.Errorf("instance broken earlier: %w", i.broken)
	}
	input, err := json.Marshal(call)
	if err != nil {
		return err
	}
	before := i.memory.MiB()
	raw, err := i.inst.Invoke(ctx, extwire.DriverFunction, input)
	if after := i.memory.MiB(); after > before {
		i.driver.grew(call.Op, after)
	}
	if err != nil {
		i.broken = err
		return fmt.Errorf("extension fault in %s: %w", call.Op, err)
	}
	var reply extwire.DriverReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return err
	}
	if len(reply.Result) > 0 && out != nil {
		dec := json.NewDecoder(bytes.NewReader(reply.Result))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return err
		}
	}
	if reply.Error != nil {
		return errors.New(reply.Error.Message)
	}
	return nil
}

// do runs op on an instance of the session.
func (s *wasmSession) do(ctx context.Context, op string, args extwire.DriverArgs, request, out any) error {
	if request != nil {
		raw, err := json.Marshal(request)
		if err != nil {
			return err
		}
		args.Request = raw
	}
	inst, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer s.release(inst)
	return inst.call(ctx, extwire.DriverCall{Op: op, Session: inst.session, Args: args}, out)
}

func (s *wasmSession) ListTables(ctx context.Context, database string) ([]string, error) {
	var out []string
	return out, s.do(ctx, extwire.DriverOpListTables, extwire.DriverArgs{Database: database}, nil, &out)
}

func (s *wasmSession) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	var out *dbdriver.DatabaseObjects
	return out, s.do(ctx, extwire.DriverOpListObjects, extwire.DriverArgs{Database: database}, nil, &out)
}

func (s *wasmSession) TableDDL(ctx context.Context, database, table string) (string, error) {
	var out string
	return out, s.do(ctx, extwire.DriverOpTableDDL, extwire.DriverArgs{Database: database, Table: table}, nil, &out)
}

func (s *wasmSession) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	var out string
	return out, s.do(ctx, extwire.DriverOpObjectSource, extwire.DriverArgs{
		Database: database, ObjectType: objType, Name: name, ObjectTable: table,
	}, nil, &out)
}

func (s *wasmSession) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	var out *dbdriver.RowSet
	return out, s.do(ctx, extwire.DriverOpTableRows, extwire.DriverArgs{Database: database, Table: table, Query: query, Page: page}, nil, &out)
}

func (s *wasmSession) DumpFile(ctx context.Context, scope dbdriver.DumpScope) (*dbdriver.DumpFileScript, error) {
	var out *dbdriver.DumpFileScript
	return out, s.do(ctx, extwire.DriverOpDumpFile, extwire.DriverArgs{}, scope, &out)
}

func (s *wasmSession) DumpObject(ctx context.Context, object dbdriver.DumpObject) (*dbdriver.DumpObjectScript, error) {
	var out *dbdriver.DumpObjectScript
	return out, s.do(ctx, extwire.DriverOpDumpObject, extwire.DriverArgs{}, object, &out)
}

// dump writes the dump of req as the app's Dump SQL File does: a table's data
// written by the guest when its session says it can.
func (s *wasmSession) dump(ctx context.Context, req dbdriver.DumpRequest) (*dbdriver.DumpResult, error) {
	var source dbdriver.DumpSource = s
	if s.tableStatements {
		source = guestStatements{s}
	}
	return dbdriver.Dump(ctx, req, source, s)
}

// guestStatements is a session whose guest writes a dump's table data itself
// (dbdriver.TableStatementSource): the stream is opened on an instance it holds
// until it is closed, and read a page per call.
type guestStatements struct{ *wasmSession }

func (s guestStatements) TableStatements(ctx context.Context, database, table string, req dbdriver.TableStatementsRequest) (dbdriver.TableStatementPages, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	inst, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var opened extwire.DriverStreamOpened
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpTableStatements, Session: inst.session, Args: extwire.DriverArgs{
		Database: database, Table: table, Request: raw,
	}}, &opened)
	if err != nil {
		s.release(inst)
		return nil, err
	}
	return &wasmStatementPages{ctx: ctx, session: s.wasmSession, inst: inst, id: opened.Stream}, nil
}

type wasmStatementPages struct {
	ctx     context.Context
	session *wasmSession
	inst    *wasmInstance
	id      uint64
	closed  bool
}

func (p *wasmStatementPages) Next() (*dbdriver.TableStatementPage, error) {
	var page dbdriver.TableStatementPage
	err := p.inst.call(p.ctx, extwire.DriverCall{Op: extwire.DriverOpStatementsNext, Args: extwire.DriverArgs{Stream: p.id}}, &page)
	return &page, err
}

func (p *wasmStatementPages) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	defer p.session.release(p.inst)
	if p.inst.broken != nil {
		return nil
	}
	return p.inst.call(p.ctx, extwire.DriverCall{Op: extwire.DriverOpStreamClose, Args: extwire.DriverArgs{Stream: p.id}}, nil)
}

// runScript runs a SQL file as the app's Run SQL File does.
func (s *wasmSession) runScript(ctx context.Context, req dbdriver.ScriptRequest) (*dbdriver.ScriptResult, error) {
	return dbdriver.RunScript(ctx, req, s)
}

// The run's executor and its splits share one instance, as the host's do.
var runInstance sync.Map // *wasmSession → *wasmInstance

func (s *wasmSession) SplitScript(ctx context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	held, ok := runInstance.Load(s)
	if !ok {
		return nil, errors.New("split before the executor opened")
	}
	raw, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	inst := held.(*wasmInstance)
	var split *dbdriver.ScriptSplit
	return split, inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpSplitScript, Session: inst.session, Args: extwire.DriverArgs{Request: raw}}, &split)
}

func (s *wasmSession) OpenScriptExecutor(ctx context.Context, database string) (dbdriver.ScriptExecutor, error) {
	inst, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var opened *extwire.DriverScriptExecutorOpened
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpOpenScriptExecutor, Session: inst.session, Args: extwire.DriverArgs{Database: database}}, &opened)
	if err != nil {
		s.release(inst)
		return nil, err
	}
	runInstance.Store(s, inst)
	return &wasmExecutor{session: s, inst: inst, id: opened.Executor}, nil
}

type wasmExecutor struct {
	session *wasmSession
	inst    *wasmInstance
	id      uint64
}

func (e *wasmExecutor) ExecuteScriptBatch(ctx context.Context, batch dbdriver.ScriptBatch) (*dbdriver.BatchResult, error) {
	raw, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	var result *dbdriver.BatchResult
	err = e.inst.call(extension.WithStopSignal(ctx, batch.Stop), extwire.DriverCall{Op: extwire.DriverOpExecuteScriptBatch,
		Args: extwire.DriverArgs{Executor: e.id, Request: raw}}, &result)
	return result, err
}

func (e *wasmExecutor) Close() error {
	defer runInstance.Delete(e.session)
	defer e.session.release(e.inst)
	if e.inst.broken != nil {
		return nil
	}
	return e.inst.call(context.Background(), extwire.DriverCall{Op: extwire.DriverOpCloseScriptExecutor, Args: extwire.DriverArgs{Executor: e.id}}, nil)
}

// StreamTableRows opens the cursor in the guest and reads it page by page on the
// instance it was opened on, as the host does (up to 256 rows a page, bytes
// accepted).
func (s *wasmSession) StreamTableRows(ctx context.Context, database, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	inst, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var opened extwire.DriverStreamOpened
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpStreamTableRows, Session: inst.session, Args: extwire.DriverArgs{
		Database: database, Table: table, Query: query,
	}}, &opened)
	if err != nil {
		s.release(inst)
		return nil, err
	}
	return &wasmStream{ctx: ctx, session: s, inst: inst, id: opened.Stream, columns: opened.Columns}, nil
}

type wasmStream struct {
	ctx     context.Context
	session *wasmSession
	inst    *wasmInstance
	id      uint64
	columns []string
	rows    [][]any
	row     []any
	done    bool
	err     error
	closed  bool
}

func (r *wasmStream) Columns() []string { return r.columns }
func (r *wasmStream) Row() []any        { return r.row }
func (r *wasmStream) Err() error        { return r.err }

func (r *wasmStream) Next() bool {
	for len(r.rows) == 0 {
		if r.done || r.err != nil {
			return false
		}
		var page extwire.DriverStreamPage
		r.err = r.inst.call(r.ctx, extwire.DriverCall{Op: extwire.DriverOpStreamNext, Args: extwire.DriverArgs{
			Stream: r.id, Max: extwire.DriverStreamRows, AcceptBytes: true,
		}}, &page)
		if err := page.RestoreBytes(); err != nil {
			r.err = errors.Join(r.err, err)
			return false
		}
		r.rows, r.done = page.Rows, page.Done
	}
	r.row, r.rows = r.rows[0], r.rows[1:]
	return true
}

func (r *wasmStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	defer r.session.release(r.inst)
	if r.inst.broken != nil {
		return nil
	}
	return r.inst.call(r.ctx, extwire.DriverCall{Op: extwire.DriverOpStreamClose, Args: extwire.DriverArgs{Stream: r.id}}, nil)
}

// PrepareRowStatements prepares the writer in the guest; its calls go to the
// instance it was prepared on.
func (s *wasmSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	inst, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer s.release(inst)
	var prepared extwire.DriverWriterPrepared
	err = inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpPrepareRowStatements, Session: inst.session, Args: extwire.DriverArgs{
		Database: database, Table: table,
	}}, &prepared)
	if err != nil {
		return nil, err
	}
	return &wasmWriter{inst: inst, id: prepared.Writer}, nil
}

type wasmWriter struct {
	inst *wasmInstance
	id   uint64
}

// RowStatements sends the rows in the host's parts (extwire.RowParts) and joins
// the parts' statements.
func (w *wasmWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	var out []string
	for _, rows := range extwire.RowParts(req.Rows) {
		part := req
		part.Rows = rows
		statements, err := w.rowStatements(part)
		if err != nil {
			return nil, err
		}
		out = append(out, statements...)
	}
	return out, nil
}

func (w *wasmWriter) rowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out []string
	return out, w.inst.call(context.Background(), extwire.DriverCall{Op: extwire.DriverOpRowStatements, Args: extwire.DriverArgs{
		Writer: w.id, Request: raw, RowBytes: extwire.RowBytesOf(req.Rows),
	}}, &out)
}
