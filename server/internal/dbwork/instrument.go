package dbwork

import (
	"context"
	"database/sql/driver"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Per-read instrumentation lives in the driver rather than in the helpers above
// it, for one reason: a statement issued through `s.db.Query` and a statement
// issued through dbwork.Query cost the database exactly the same, and a counter
// that only sees the well-behaved call sites would report the number we wish
// were true. The driver sees every statement, every transaction and every
// pooled-connection acquisition, and it sees them with the caller's context, so
// the cost of one request can be attributed to that request.
//
// The one honest limitation: `database/sql`'s context-free forms (`db.Query`,
// `db.QueryRow`) hand the driver `context.Background()`, so their statements
// reach the global counters but cannot be attributed to a request. Converting a
// read path to the context form is therefore also what makes it measurable.

// DriverName is the registered name of the observed SQLite driver. It wraps the
// pure-Go modernc driver; the underlying behaviour is unchanged.
const DriverName = "sqlite-observed"

// SlowReadThreshold is the duration above which a foreground read is logged.
// The old server logged at 250 ms and that number is still the right one: it is
// long enough that nothing healthy trips it and short enough to catch a query
// shape that has quietly become linear in library size.
const SlowReadThreshold = 250 * time.Millisecond

// Cost is what one unit of work asked of the database. Statements is the number
// the audit's gate is written against, because a latency budget can be met by a
// fast machine hiding a bad query shape and a statement count cannot be.
type Cost struct {
	Statements   int64 `json:"statements"`
	Transactions int64 `json:"transactions"`
	Acquisitions int64 `json:"acquisitions"`
	ReadMillis   int64 `json:"readMillis"`
	// VMSteps is the SQLite virtual-machine steps the reads ran: a measure of
	// work that does not depend on the machine, so a test can assert that a
	// read does not grow with the library.
	VMSteps int64 `json:"vmSteps"`
}

type counters struct {
	statements   atomic.Int64
	transactions atomic.Int64
	acquisitions atomic.Int64
	readNanos    atomic.Int64
	vmSteps      atomic.Int64
}

func (c *counters) cost() Cost {
	if c == nil {
		return Cost{}
	}
	return Cost{
		Statements:   c.statements.Load(),
		Transactions: c.transactions.Load(),
		Acquisitions: c.acquisitions.Load(),
		ReadMillis:   c.readNanos.Load() / int64(time.Millisecond),
		VMSteps:      c.vmSteps.Load(),
	}
}

type costKey struct{}

// Measure attaches a fresh cost counter to ctx and returns a reader for it. The
// HTTP layer calls it once per request; a test calls it around one call to read
// exactly what that call cost.
func Measure(ctx context.Context) (context.Context, func() Cost) {
	if ctx == nil {
		ctx = context.Background()
	}
	c := &counters{}
	return context.WithValue(ctx, costKey{}, c), c.cost
}

// CostFrom reports what has been spent under ctx so far, or a zero Cost when ctx
// carries no counter.
func CostFrom(ctx context.Context) Cost {
	return costFrom(ctx).cost()
}

func costFrom(ctx context.Context) *counters {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(costKey{}).(*counters)
	return value
}

// --- global observation ----------------------------------------------------

var global struct {
	statements   atomic.Uint64
	transactions atomic.Uint64
	acquisitions atomic.Uint64
	slow         atomic.Uint64
	heavy        atomic.Uint64
	heavyLogged  atomic.Int64

	mu      sync.Mutex
	byClass map[Class]*classReads
}

type classReads struct {
	statements uint64
	nanos      uint64
	maxNanos   uint64
}

// ClassReadStats is one work class's share of the read load.
type ClassReadStats struct {
	Class      string `json:"class"`
	Statements uint64 `json:"statements"`
	TotalMs    uint64 `json:"totalMillis"`
	MaxMs      uint64 `json:"maxMillis"`
}

// ReadStats is the process-wide statement picture. It is the counterpart to the
// pool and gate statistics: those say how long callers waited, this says how
// much work they asked for.
type ReadStats struct {
	Statements   uint64 `json:"statements"`
	Transactions uint64 `json:"transactions"`
	Acquisitions uint64 `json:"acquisitions"`
	SlowReads    uint64 `json:"slowReads"`
	// HeavyReads counts reads that ran more SQLite VM steps than their
	// ceiling (1,000,000 foreground, 250,000 background): the work-bound
	// guard that does not depend on the machine's speed. They are counted and
	// logged, never interrupted.
	HeavyReads     uint64              `json:"heavyReads"`
	ByClass        []ClassReadStats    `json:"byClass"`
	StatementCache StatementCacheStats `json:"statementCache"`
}

// Reads snapshots the process-wide statement counters.
func Reads() ReadStats {
	out := ReadStats{
		Statements:     global.statements.Load(),
		Transactions:   global.transactions.Load(),
		Acquisitions:   global.acquisitions.Load(),
		SlowReads:      global.slow.Load(),
		HeavyReads:     global.heavy.Load(),
		ByClass:        []ClassReadStats{},
		StatementCache: statementCacheStats(),
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	for _, class := range Classes() {
		entry := global.byClass[class]
		if entry == nil {
			continue
		}
		out.ByClass = append(out.ByClass, ClassReadStats{
			Class:      class.String(),
			Statements: entry.statements,
			TotalMs:    entry.nanos / uint64(time.Millisecond),
			MaxMs:      entry.maxNanos / uint64(time.Millisecond),
		})
	}
	return out
}

// ResetReads zeroes the process-wide counters. It exists for tests and for the
// load harness, which measures one run rather than the life of the process.
func ResetReads() {
	resetStatementCacheStats()
	global.statements.Store(0)
	global.transactions.Store(0)
	global.acquisitions.Store(0)
	global.slow.Store(0)
	global.heavy.Store(0)
	global.mu.Lock()
	global.byClass = nil
	global.mu.Unlock()
}

type traceKey struct{}

// traceBox collects statement text for one traced context.
type traceBox struct {
	mu      sync.Mutex
	lines   []string
	length  int
	maximum int
}

// TraceStatements collects the statements issued under ctx and returns a reader
// for them.
//
// It is opt-in per context and off everywhere else, which is what keeps it
// compatible with the rule that metrics never carry query text: nothing here
// reaches a metric, a log or the diagnostics endpoint. It exists because a
// statement count tells you a route is expensive and only the shape tells you
// which statement is repeated.
func TraceStatements(ctx context.Context) (context.Context, func() []string) {
	return traceStatements(ctx, 110, 4096)
}

// TraceQueryPlans retains complete parameterized statements for an opt-in
// EXPLAIN harness. It never records bound values or publishes query text to
// telemetry. Both statement size and statement count are bounded.
func TraceQueryPlans(ctx context.Context) (context.Context, func() []string) {
	return traceStatements(ctx, 64*1024, 1024)
}

func traceStatements(ctx context.Context, length, maximum int) (context.Context, func() []string) {
	box := &traceBox{length: length, maximum: maximum}
	return context.WithValue(ctx, traceKey{}, box), func() []string {
		box.mu.Lock()
		defer box.mu.Unlock()
		return append([]string{}, box.lines...)
	}
}

func traceStatement(ctx context.Context, query string) {
	if ctx == nil {
		return
	}
	box, _ := ctx.Value(traceKey{}).(*traceBox)
	if box == nil {
		return
	}
	q := strings.Join(strings.Fields(query), " ")
	if len(q) > box.length {
		q = q[:box.length]
	}
	box.mu.Lock()
	if len(box.lines) < box.maximum {
		box.lines = append(box.lines, q)
	}
	box.mu.Unlock()
}

func observe(ctx context.Context, query string, elapsed time.Duration, inTx bool) {
	profileStatement(query, elapsed)
	global.statements.Add(1)
	if !inTx {
		global.acquisitions.Add(1)
	}
	if c := costFrom(ctx); c != nil {
		c.statements.Add(1)
		c.readNanos.Add(int64(elapsed))
		if !inTx {
			c.acquisitions.Add(1)
		}
	}
	class := ClassFrom(ctx, ClassInteractive)
	global.mu.Lock()
	if global.byClass == nil {
		global.byClass = map[Class]*classReads{}
	}
	entry := global.byClass[class]
	if entry == nil {
		entry = &classReads{}
		global.byClass[class] = entry
	}
	entry.statements++
	entry.nanos += uint64(elapsed)
	if uint64(elapsed) > entry.maxNanos {
		entry.maxNanos = uint64(elapsed)
	}
	global.mu.Unlock()
	if elapsed >= SlowReadThreshold && !class.Background() {
		global.slow.Add(1)
		if slowReadLog.Load() {
			log.Printf("slow database read: %s on the %s class", elapsed.Round(time.Millisecond), class)
		}
	}
}

// Read work ceilings (F §4): a foreground query may run 1,000,000 VM steps and
// a background one 250,000 before it counts as heavy.
const (
	foregroundReadVMCeiling = 1_000_000
	backgroundReadVMCeiling = 250_000
)

func noteReadSteps(ctx context.Context, steps int64) {
	class := ClassFrom(ctx, ClassInteractive)
	ceiling := int64(foregroundReadVMCeiling)
	if class.Background() {
		ceiling = backgroundReadVMCeiling
	}
	if steps <= ceiling {
		return
	}
	global.heavy.Add(1)
	now := time.Now().UnixNano()
	if last := global.heavyLogged.Load(); slowReadLog.Load() && now-last >= int64(time.Minute) && global.heavyLogged.CompareAndSwap(last, now) {
		log.Printf("heavy database read: %d VM steps on the %s class (%d so far)", steps, class, global.heavy.Load())
	}
}

// slowReadLog keeps the slow-read line out of test output by default. The server
// turns it on at startup.
var slowReadLog atomic.Bool

// LogSlowReads turns the slow-read log line on or off.
func LogSlowReads(on bool) { slowReadLog.Store(on) }

func observeTransaction(ctx context.Context) {
	global.transactions.Add(1)
	global.acquisitions.Add(1)
	if c := costFrom(ctx); c != nil {
		c.transactions.Add(1)
		c.acquisitions.Add(1)
	}
}

// --- driver wrapper --------------------------------------------------------

type observedDriver struct{ inner driver.Driver }

func (d observedDriver) Open(name string) (driver.Conn, error) {
	name, role := connRoleOf(name)
	readers := acquireReaderScope(name)
	generation := readers.plannerGeneration.Load()
	inner, err := d.inner.Open(name)
	if err != nil {
		releaseReaderScope(readers)
		return nil, err
	}
	return &observedConn{inner: inner, readers: readers, plannerGeneration: generation, role: role}, nil
}

type observedConn struct {
	inner             driver.Conn
	inTx              bool
	changes           *changeSet
	statements        statementCache
	readers           *readerScope
	readerTx          *readerLease
	plannerGeneration uint64
	// authorityDirty is transaction-local: a reader may reauthorize against
	// the old snapshot after a statement bump and before its commit. Only an
	// authority-writing transaction needs a second bump when it commits.
	authorityDirty bool
	// role is which pool of an OpenHandle database this connection belongs
	// to; an unmanaged handle is not checked for ungated writes.
	role connRole
	// meter is the open gated transaction's budget meter, if it has one.
	meter *writeMeter
	// testTx is a write transaction a test opened itself on a read pool.
	testTx bool
	// rowsLease is the one reader lease implicit rows on this connection
	// share, counted by rowsRefs.
	rowsLease *readerLease
	rowsRefs  int
}

func (c *observedConn) Prepare(query string) (driver.Stmt, error) {
	inner, err := c.inner.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &observedStmt{inner: inner, conn: c, query: query}, nil
}

func (c *observedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	preparer, ok := c.inner.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}
	inner, err := preparer.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &observedStmt{inner: inner, conn: c, query: query}, nil
}

func (c *observedConn) Close() error {
	c.statements.close()
	err := c.inner.Close()
	c.finishReadTransaction()
	releaseReaderScope(c.readers)
	return err
}

// Unwrap exposes the driver connection underneath. `sql.Conn.Raw` hands out
// whatever connection the driver returned, and a wrapper that hid the real one
// would silently break the online-backup path, which reaches for a method only
// the concrete modernc connection has.
func (c *observedConn) Unwrap() driver.Conn { return c.inner }

func (c *observedConn) Begin() (driver.Tx, error) {
	lease, err := c.admitReader(context.Background(), true)
	if err != nil {
		return nil, err
	}
	inner, err := c.inner.Begin()
	if err != nil {
		lease.close()
		return nil, err
	}
	c.readerTx = lease
	observeTransaction(context.Background())
	c.inTx = true
	c.authorityDirty = false
	c.changes = nil
	return &observedTx{inner: inner, conn: c}, nil
}

func (c *observedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.inner.(driver.ConnBeginTx)
	if !ok {
		return c.Begin()
	}
	lease, err := c.admitReader(ctx, true)
	if err != nil {
		return nil, err
	}
	if !opts.ReadOnly {
		if err := c.checkWriter(ctx, "BEGIN"); err != nil {
			lease.close()
			return nil, err
		}
	}
	// Only the write transaction itself is metered: a read snapshot a helper
	// opens with the write's context is another connection's work.
	if meter, _ := ctx.Value(meterKey{}).(*writeMeter); meter != nil && !opts.ReadOnly && c.role != roleReader && !meter.started {
		meter.wal, _ = ctx.Value(walRequestKey{}).(*walRequest)
		c.meter = meter
		c.meter.begin(c.inner)
	}
	inner, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		lease.close()
		c.meter.end(c.inner)
		c.meter = nil
		return nil, err
	}
	c.readerTx = lease
	observeTransaction(ctx)
	c.inTx = true
	c.authorityDirty = false
	c.changes, _ = ctx.Value(changeKey{}).(*changeSet)
	return &observedTx{inner: inner, conn: c}, nil
}

func (c *observedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, ok := c.inner.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	if err := c.checkWriter(ctx, query); err != nil {
		return nil, err
	}
	start := time.Now()
	traceStatement(ctx, query)
	out, err := c.cachedExec(ctx, query, args)
	if err == nil {
		c.noteWrite(query)
	}
	observe(ctx, query, time.Since(start), c.inTx)
	return out, err
}

func (c *observedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	_, ok := c.inner.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	if err := c.checkWriter(ctx, query); err != nil {
		return nil, err
	}
	lease, err := c.admitReader(ctx, false)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	traceStatement(ctx, query)
	out, release, err := c.cachedQuery(ctx, query, args)
	if err != nil {
		lease.close()
		observe(ctx, query, time.Since(start), c.inTx)
		return nil, err
	}
	// A write that returns rows (UPDATE … RETURNING) arrives here, not through
	// ExecContext, and is as much an authority write: ending a session is one.
	c.noteWrite(query)
	return &observedRows{inner: out, ctx: ctx, query: query, start: start, inTx: c.inTx, release: release, reader: lease}, nil
}

func (c *observedConn) Ping(ctx context.Context) error {
	if pinger, ok := c.inner.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *observedConn) ResetSession(ctx context.Context) error {
	c.finishReadTransaction()
	c.inTx = false
	c.meter = nil
	c.testTx = false
	c.authorityDirty = false
	// SQLite caches statistics and prepared plans per physical connection.
	// Retire an idle connection only when it is borrowed again; a snapshot
	// already using it keeps its original transaction until it finishes.
	if c.readers != nil && c.plannerGeneration != c.readers.plannerGeneration.Load() {
		return driver.ErrBadConn
	}
	if resetter, ok := c.inner.(driver.SessionResetter); ok {
		return resetter.ResetSession(ctx)
	}
	return nil
}

func (c *observedConn) IsValid() bool {
	if validator, ok := c.inner.(driver.Validator); ok {
		return validator.IsValid()
	}
	return true
}

type observedTx struct {
	inner driver.Tx
	conn  *observedConn
}

func (t *observedTx) Commit() error {
	err := t.inner.Commit()
	if t.conn.meter != nil {
		t.conn.meter.end(t.conn.inner)
		t.conn.meter = nil
	}
	if err == nil && t.conn.authorityDirty {
		// Invalidate verdicts obtained after the statement bump while other
		// connections could still see the old committed authority.
		BumpAuthority()
	}
	t.conn.inTx = false
	t.conn.authorityDirty = false
	t.conn.testTx = false
	t.conn.finishReadTransaction()
	return err
}

func (t *observedTx) Rollback() error {
	t.conn.inTx = false
	t.conn.testTx = false
	err := t.inner.Rollback()
	t.conn.meter.end(t.conn.inner)
	t.conn.meter = nil
	t.conn.authorityDirty = false
	t.conn.finishReadTransaction()
	return err
}

type observedStmt struct {
	inner driver.Stmt
	conn  *observedConn
	query string
}

func (s *observedStmt) Close() error  { return s.inner.Close() }
func (s *observedStmt) NumInput() int { return s.inner.NumInput() }

func (s *observedStmt) Exec(args []driver.Value) (driver.Result, error) {
	if err := s.conn.checkWriter(context.Background(), s.query); err != nil {
		return nil, err
	}
	if invalidatingStatement(s.query) {
		s.conn.statements.invalidate()
	}
	start := time.Now()
	out, err := s.inner.Exec(args)
	if err == nil {
		s.conn.noteWrite(s.query)
	}
	observe(context.Background(), "", time.Since(start), s.conn.inTx)
	return out, err
}

func (s *observedStmt) Query(args []driver.Value) (driver.Rows, error) {
	if err := s.conn.checkWriter(context.Background(), s.query); err != nil {
		return nil, err
	}
	if invalidatingStatement(s.query) {
		s.conn.statements.invalidate()
	}
	lease, err := s.conn.admitReader(context.Background(), false)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	out, err := s.inner.Query(args)
	if err != nil {
		lease.close()
		observe(context.Background(), "", time.Since(start), s.conn.inTx)
		return nil, err
	}
	s.conn.noteWrite(s.query)
	return &observedRows{inner: out, ctx: context.Background(), start: start, inTx: s.conn.inTx, reader: lease}, nil
}

func (s *observedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.conn.checkWriter(ctx, s.query); err != nil {
		return nil, err
	}
	if invalidatingStatement(s.query) {
		s.conn.statements.invalidate()
	}
	execer, ok := s.inner.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	start := time.Now()
	out, err := execer.ExecContext(ctx, args)
	if err == nil {
		s.conn.noteWrite(s.query)
	}
	observe(ctx, "", time.Since(start), s.conn.inTx)
	return out, err
}

func (s *observedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.conn.checkWriter(ctx, s.query); err != nil {
		return nil, err
	}
	if invalidatingStatement(s.query) {
		s.conn.statements.invalidate()
	}
	queryer, ok := s.inner.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	lease, err := s.conn.admitReader(ctx, false)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	out, err := queryer.QueryContext(ctx, args)
	if err != nil {
		lease.close()
		observe(ctx, "", time.Since(start), s.conn.inTx)
		return nil, err
	}
	s.conn.noteWrite(s.query)
	return &observedRows{inner: out, ctx: ctx, start: start, inTx: s.conn.inTx, reader: lease}, nil
}

// observedRows times a read to the end of iteration rather than to the return of
// Query. A statement that streams its rows does most of its work in Next, so
// timing the call alone would report the expensive shapes as free.
type observedRows struct {
	inner    driver.Rows
	ctx      context.Context
	query    string
	start    time.Time
	inTx     bool
	observed bool
	release  func(error) error
	closed   bool
	closeErr error
	nextErr  error
	reader   *readerLease
}

func (r *observedRows) Columns() []string { return r.inner.Columns() }

func (r *observedRows) Next(dest []driver.Value) error {
	err := r.inner.Next(dest)
	if err != nil && err != io.EOF {
		r.nextErr = err
	}
	return err
}

func (r *observedRows) Close() error {
	if r.closed {
		return r.closeErr
	}
	r.closed = true
	if counter, ok := r.inner.(interface{ VMSteps() int64 }); ok {
		steps := counter.VMSteps()
		if c := costFrom(r.ctx); c != nil {
			c.vmSteps.Add(steps)
		}
		noteReadSteps(r.ctx, steps)
	}
	if !r.observed {
		r.observed = true
		observe(r.ctx, r.query, time.Since(r.start), r.inTx)
	}
	r.closeErr = r.inner.Close()
	if r.release != nil {
		err := r.closeErr
		if err == nil {
			err = r.nextErr
		}
		if err == nil {
			err = r.ctx.Err()
		}
		if closeErr := r.release(err); r.closeErr == nil {
			r.closeErr = closeErr
		}
	}
	r.reader.close()
	return r.closeErr
}

func (r *observedRows) ColumnTypeDatabaseTypeName(index int) string {
	if inner, ok := r.inner.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return inner.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

func (r *observedRows) ColumnTypeLength(index int) (int64, bool) {
	if inner, ok := r.inner.(driver.RowsColumnTypeLength); ok {
		return inner.ColumnTypeLength(index)
	}
	return 0, false
}

func (r *observedRows) ColumnTypeNullable(index int) (bool, bool) {
	if inner, ok := r.inner.(driver.RowsColumnTypeNullable); ok {
		return inner.ColumnTypeNullable(index)
	}
	return false, false
}

func (r *observedRows) ColumnTypePrecisionScale(index int) (int64, int64, bool) {
	if inner, ok := r.inner.(driver.RowsColumnTypePrecisionScale); ok {
		return inner.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}

var anyType = reflect.TypeOf(new(any)).Elem()

func (r *observedRows) ColumnTypeScanType(index int) reflect.Type {
	if inner, ok := r.inner.(driver.RowsColumnTypeScanType); ok {
		return inner.ColumnTypeScanType(index)
	}
	return anyType
}

var (
	_ driver.Conn                           = (*observedConn)(nil)
	_ driver.ConnBeginTx                    = (*observedConn)(nil)
	_ driver.ConnPrepareContext             = (*observedConn)(nil)
	_ driver.ExecerContext                  = (*observedConn)(nil)
	_ driver.QueryerContext                 = (*observedConn)(nil)
	_ driver.Pinger                         = (*observedConn)(nil)
	_ driver.SessionResetter                = (*observedConn)(nil)
	_ driver.Validator                      = (*observedConn)(nil)
	_ driver.Stmt                           = (*observedStmt)(nil)
	_ driver.StmtExecContext                = (*observedStmt)(nil)
	_ driver.StmtQueryContext               = (*observedStmt)(nil)
	_ driver.Rows                           = (*observedRows)(nil)
	_ driver.RowsColumnTypeDatabaseTypeName = (*observedRows)(nil)
	_ driver.RowsColumnTypeLength           = (*observedRows)(nil)
	_ driver.RowsColumnTypeNullable         = (*observedRows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*observedRows)(nil)
	_ driver.RowsColumnTypeScanType         = (*observedRows)(nil)
)

// --- authority fencing -----------------------------------------------------

// `authority.go` moves the authority generation when a *gated* write commits.
// That covers every write the server makes through `internal/dbwork`, which is
// all of them — and "all of them" is exactly the kind of claim that is true
// until it is not. The old server's rule (audit principle 22) was two signals,
// not one: the caller's declared intent *and* a marker in the SQL itself, so
// that a write which slips past the first is still caught by the second.
//
// The driver is where the second signal lives, because it sees every statement
// whatever helper issued it. A direct `db.Exec` that revokes a session takes the
// gate nowhere and moves no class, and this is what stops it leaving a cached
// principal live.

// authorityTables are the tables whose contents decide whether a principal is
// still who it says it is, or may still see what it used to. The list is
// deliberately short and deliberately over-inclusive: a false bump costs a cache
// miss, and a missed bump costs a revoked session.
// They are named exactly, because the name is now read off the head of the
// write rather than looked for anywhere in its text. A near-miss costs a missed
// bump, so the list is wide: everything identity, membership, restriction,
// enrolment and access-policy lives in, plus the two playback tables that decide
// whether a viewer may play at all. `internal/identity` takes the security-fence
// class for all of these anyway, which is the declared signal; this is the
// textual one, for the paths that take their class from a background loop.
var authorityTables = registeredAuthorityTables()

// noteWrite preserves statement-time invalidation and remembers authority writes
// until commit. database/sql serializes driver calls on each connection.
func (c *observedConn) noteWrite(query string) {
	if noticeAuthorityWrite(query) && c.inTx {
		c.authorityDirty = true
	}
	c.changes.note(query)
}

// noticeAuthorityWrite bumps the generation and reports whether a statement
// writes an authority table. Reads are ignored: the check runs only for a
// statement that begins with a mutation keyword, which is a one-character
// comparison for the overwhelming majority of traffic.
func noticeAuthorityWrite(query string) bool {
	if len(query) < 6 {
		return false
	}
	switch query[0] {
	case 'i', 'I', 'u', 'U', 'd', 'D', 'r', 'R':
	default:
		return false
	}
	if headOf(query).authority {
		BumpAuthority()
		return true
	}
	return false
}

// authorityWrite is whether a statement that begins with a mutation keyword's
// first letter writes an authority table.
func authorityWrite(query string) bool {
	lowered := strings.ToLower(query)
	if !strings.HasPrefix(lowered, "insert") && !strings.HasPrefix(lowered, "update") &&
		!strings.HasPrefix(lowered, "delete") && !strings.HasPrefix(lowered, "replace") {
		return false
	}
	// The table a write names is the table it writes. Reading it off the
	// statement's head is both cheaper and far more precise than looking for the
	// name anywhere in the text: `playback_viewer_leases SET status='revoked'`
	// and `playback_delivery_plans(…,policy,…)` are not authority writes, and
	// under a substring rule they were — measured on the smoke tier, the playback
	// leg alone moved the authority generation a hundred and thirty-eight times
	// in a second, which is the principal, restriction and library-grant caches
	// never holding.
	if target, ok := writtenTable(lowered); ok {
		for _, table := range authorityTables {
			if target == table {
				return true
			}
		}
		return false
	}
	// A shape this cannot read — a CTE in front of the write, a form a future
	// driver introduces — falls back to the older, over-inclusive rule. A false
	// bump costs a cache miss; a missed bump costs a revoked session.
	for _, table := range authorityTables {
		if namesTable(lowered, table) {
			return true
		}
	}
	return false
}

// statementHead is what a statement's text says about what it writes. It is
// read once per distinct text: statements are a bounded set of constants, and
// lower-casing and splitting a long one on every execution was a measurable
// share of the cost of building a library.
type statementHead struct {
	table     string // the table its head names (writtenTable of its trimmed text)
	named     bool
	unknown   bool // a CTE or several statements: the scope can't be proven
	authority bool // a write to an authority table (noticeAuthorityWrite)
	// plainSelect is a single, comment-free plain SELECT: the one read shape an
	// exact wake may decline. It is a negative classification, so every shape it
	// cannot read keeps the old write-like behaviour.
	//
	// A SELECT is only safe to decline because no registered SQL function,
	// collation, connection hook, module or extension in this server can write.
	// That is a census of this build, not a property of SQL: the driver's
	// arbitrary-callback registration (thirdparty/sqlite, FunctionImpl.Scalar)
	// would hide a write. Adding any such registration, or enabling
	// load_extension, requires updating TestNoArbitrarySQLCallbackSurfaces (which
	// pins the census) and re-reading this exemption before it ships.
	plainSelect bool
}

var statementHeads struct {
	sync.Map
	count atomic.Int64
}

// statementHeadLimit bounds the memo; a text past it is read each time.
const statementHeadLimit = 8192

func headOf(query string) statementHead {
	if cached, ok := statementHeads.Load(query); ok {
		return cached.(statementHead)
	}
	text := strings.ToLower(strings.TrimSpace(query))
	h := statementHead{unknown: strings.HasPrefix(text, "with ") || strings.Contains(strings.TrimSuffix(text, ";"), ";")}
	h.table, h.named = writtenTable(text)
	// A plain SELECT writes nothing, so an exact wake that fires for it is a
	// false one. Only the ASCII-space spelling after existing trim/case
	// normalisation is accepted: a comment anywhere, an internal semicolon or a
	// CTE head all keep the old conservative behaviour, even when the same text
	// would still execute as a read. INSERT … SELECT begins INSERT and is never
	// exempt, and a statement-level readonly bit would not prove an arbitrary
	// callback harmless either (see plainSelect).
	h.plainSelect = strings.HasPrefix(text, "select ") && !h.unknown &&
		!strings.Contains(text, "--") && !strings.Contains(text, "/*")
	if len(query) >= 6 {
		switch query[0] {
		case 'i', 'I', 'u', 'U', 'd', 'D', 'r', 'R':
			h.authority = authorityWrite(query)
		}
	}
	if statementHeads.count.Load() < statementHeadLimit {
		if _, loaded := statementHeads.LoadOrStore(strings.Clone(query), h); !loaded {
			statementHeads.count.Add(1)
		}
	}
	return h
}

// writtenTable reads the target of a single-table write off its head:
// `insert [or …] into X`, `replace into X`, `update [or …] X`, `delete from X`.
// It reports false for anything else, which the caller treats conservatively.
func writtenTable(statement string) (string, bool) {
	fields := strings.Fields(statement)
	for index := 0; index < len(fields) && index < 6; index++ {
		switch fields[index] {
		case "into", "from":
			if index+1 < len(fields) {
				return bareName(fields[index+1]), true
			}
			return "", false
		case "update":
			// `update X set …`, and `update or replace X set …`.
			next := index + 1
			if next < len(fields) && fields[next] == "or" {
				next += 2
			}
			if next < len(fields) {
				return bareName(fields[next]), true
			}
			return "", false
		}
	}
	return "", false
}

// bareName strips what a table name can be written with and still be that
// table: a trailing column list, quotes, brackets, a schema qualifier.
func bareName(token string) string {
	if cut := strings.IndexAny(token, "(,;"); cut >= 0 {
		token = token[:cut]
	}
	token = strings.Trim(token, "`\"[]")
	if dot := strings.LastIndexByte(token, '.'); dot >= 0 {
		token = token[dot+1:]
	}
	return token
}

// namesTable reports whether a statement names this table, rather than merely
// containing its spelling.
//
// The list is over-inclusive on purpose — a false bump costs a cache miss and a
// missed bump costs a revoked session — but a substring match is over-inclusive
// in a way that costs more than a cache miss. `playback_sessions` contains
// `sessions`, so every playback session created, reported on or stopped bumped
// the authority generation and flushed the principal, restriction and
// library-grant caches for every viewer on the server. Under two hundred people
// watching, that is the caches never holding at all: measured on the release
// tier, the playback leg alone bumped the generation about seventeen hundred
// times in two minutes.
//
// An identifier boundary before the name is what distinguishes the two. The
// list's own entries stay exactly as they are.
func namesTable(statement, table string) bool {
	from := 0
	for {
		at := strings.Index(statement[from:], table)
		if at < 0 {
			return false
		}
		at += from
		if at == 0 || !identifierByte(statement[at-1]) {
			return true
		}
		from = at + 1
	}
}

func identifierByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
