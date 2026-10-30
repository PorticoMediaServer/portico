package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sqlitedrv "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// A WritePlan is the work one gated write transaction is allowed: the rows it
// changes (counting every trigger and foreign-key action), the cache-page writes it makes, and the SQLite VM operations it runs.
// Actual WAL bytes (including spills/rollback/reuse) have a separate VFS
// counter and source-qualified reservation ledger in wal_reservation.go. The writer is a single
// non-preemptive connection, so one transaction that does a thousand times the
// usual work holds every other writer — sign-in, playback stop, a security
// fence — for a thousand times as long.
//
// A plan is measured, never enforced against a production write. Refusing or
// rolling back a write because it was bigger than expected would drop the
// write, which is its own unreliability. Instead:
//   - every committed gated transaction is measured against its plan;
//   - an overrun is counted, kept in a small ring for diagnostics and logged
//     at most once a minute;
//   - in a test binary an overrun of more than twice the plan from server code
//     panics, so a change that makes a write unbounded fails its own tests
//     rather than a server under load. Unbounded work overshoots by orders of
//     magnitude; the factor of two leaves room for a controller that steers
//     batches to the plan from an average. Work a test file starts directly
//     (fixture seeding) is exempt.
//
// Most writes use their class's default plan. An operation that genuinely has
// to be bigger declares a named plan with a reason and passes it with WithPlan
// on the context of its outermost write; the static census in
// plan_census_test.go records which call sites do.
type WritePlan struct {
	// Name identifies the plan in telemetry and logs. Default plans are named
	// after their class.
	Name string
	// Rows bounds rows inserted, updated or deleted, including trigger and
	// foreign-key programs. Zero means unbounded.
	Rows int64
	// Pages budgets SQLite cache-page writes. It is telemetry, not actual
	// WAL-byte accounting or a source proof. Zero means unbounded.
	Pages int64
	// VMOps bounds SQLite VM operations across every statement of the
	// transaction. Zero means unbounded.
	VMOps int64
	// Why is required for a named plan: the reason this operation cannot be
	// split into class-sized transactions.
	Why string
}

// Default budgets, from the writer-discipline design (F §4): at most 32
// accepted units and 128 child effects, so 160 changed rows; a WAL reservation
// of 256 KiB for a security fence, 512 KiB for user work and 1 MiB for
// background work; and 250,000 VM operations. The design's 128 KiB fence was
// too small for this schema: identity fences touch many indexed tables and
// measure 34–66 pages (136–264 KiB), so the fence budget is 256 KiB. Budgets
// are telemetry, never a refusal.
const (
	planRows          = 160
	planVMOps         = 250_000
	planPagesSecurity = 256 * 1024 / 4096
	planPagesUser     = 512 * 1024 / 4096
	planPagesBulk     = 1024 * 1024 / 4096
)

// DefaultPlan is the budget a write of class gets when it declares none.
func DefaultPlan(class Class) WritePlan {
	plan := WritePlan{Name: class.String(), Rows: planRows, VMOps: planVMOps, Pages: planPagesUser}
	switch {
	case class == ClassSecurityFence:
		plan.Pages = planPagesSecurity
	case class.Background():
		plan.Pages = planPagesBulk
	}
	return plan
}

type planKey struct{}

// WithPlan declares the plan for the write begun with the returned context. It
// applies to the outermost gated transaction only: a helper that joins a
// transaction already open shares that transaction's plan.
func WithPlan(ctx context.Context, plan WritePlan) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, planKey{}, plan)
}

// UsePlan replaces the plan of the live gated transaction tx, for work that
// learns its own shape only once it is running: a batch that turned out to be
// one indivisible unit, say. The transaction is measured against the plan in
// force when it commits. An untracked or finished transaction is refused.
func UsePlan(tx *sql.Tx, plan WritePlan) error {
	value, ok := liveWrites.Load(tx)
	if !ok {
		return errors.New("dbwork: a plan requires a live gated transaction")
	}
	w := value.(*Write)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done || w.owned == nil {
		return errors.New("dbwork: plan transaction already finished")
	}
	w.plan = plan
	return nil
}

func planFor(ctx context.Context, class Class) WritePlan {
	if ctx != nil {
		if plan, ok := ctx.Value(planKey{}).(WritePlan); ok {
			return plan
		}
	}
	return DefaultPlan(class)
}

// writeMeter is one transaction's VM and page counters at BEGIN and at its
// end, filled in by the observed driver on the writer connection.
type writeMeter struct {
	vmStart, pagesStart             int64
	vmEnd, pagesEnd                 int64
	started, ended                  bool
	walStart                        uint64
	walKnown                        bool
	walIOStart                      sqlitedrv.WALIOStats
	walBytesWritten, walFrames      uint64
	walFramesKnown, walWrittenKnown bool
	wal                             *walRequest
}

type meterKey struct{}

// connCounters reads a driver connection's cumulative VM operations and cache
// page writes. A connection that cannot answer reports ok false and is not
// measured.
func connCounters(inner any) (vm, pages int64, ok bool) {
	if observed, isObserved := inner.(*observedConn); isObserved {
		inner = observed.inner
	}
	counter, isCounter := inner.(interface{ VMOperations() int64 })
	status, isStatus := inner.(sqlitedrv.DBStatus)
	if !isCounter || !isStatus {
		return 0, 0, false
	}
	written, _, err := status.Status(sqlitedrv.DBStatusCacheWrite, false)
	if err != nil {
		return 0, 0, false
	}
	return counter.VMOperations(), int64(written), true
}

func (m *writeMeter) begin(inner any) {
	if m == nil {
		return
	}
	m.vmStart, m.pagesStart, m.started = connCounters(inner)
	if c, ok := inner.(*observedConn); ok {
		inner = c.inner
	}
	if counter, ok := inner.(interface{ WALIO() sqlitedrv.WALIOStats }); ok {
		m.walIOStart = counter.WALIO()
		m.walStart, m.walKnown = m.walIOStart.AttemptedBytes, m.walIOStart.Qualified
	} else if counter, ok := inner.(interface{ WALWriteBytes() (uint64, bool) }); ok {
		m.walStart, m.walKnown = counter.WALWriteBytes()
	}
}

func (m *writeMeter) end(inner any) {
	if m == nil || !m.started || m.ended {
		return
	}
	m.vmEnd, m.pagesEnd, m.ended = connCounters(inner)
	if c, ok := inner.(*observedConn); ok {
		inner = c.inner
	}
	var attempted uint64
	var known bool
	var native sqlitedrv.WALIOStats
	if counter, ok := inner.(interface{ WALIO() sqlitedrv.WALIOStats }); ok {
		native = counter.WALIO()
		attempted, known = native.AttemptedBytes, native.Qualified
	} else if counter, ok := inner.(interface{ WALWriteBytes() (uint64, bool) }); ok {
		attempted, known = counter.WALWriteBytes()
	}
	known = known && m.walKnown && attempted >= m.walStart
	if known {
		attempted -= m.walStart
	}
	m.wal.measured(attempted, known)
	if native.Qualified {
		m.wal.retained(native.CurrentEnd)
	}
	if m.walIOStart.Qualified && native.Qualified && native.Errors == m.walIOStart.Errors && native.WrittenBytes >= m.walIOStart.WrittenBytes && native.HeaderWrites >= m.walIOStart.HeaderWrites {
		m.walWrittenKnown = true
		m.walBytesWritten = native.WrittenBytes - m.walIOStart.WrittenBytes
		if m.wal != nil && m.wal.ledger != nil {
			m.walFrames, m.walFramesKnown = observedWALFrames(m.walBytesWritten, native.HeaderWrites-m.walIOStart.HeaderWrites, m.wal.ledger.pageSize)
		}
	}
}

// WriteCost is what one gated write transaction did.
type WriteCost struct {
	Rows  int64 `json:"rows"`
	Pages int64 `json:"pages"`
	VMOps int64 `json:"vmOps"`
}

func (m *writeMeter) cost(rows int64) (WriteCost, bool) {
	if m == nil || !m.ended {
		return WriteCost{Rows: rows}, false
	}
	return WriteCost{Rows: rows, Pages: m.pagesEnd - m.pagesStart, VMOps: m.vmEnd - m.vmStart}, true
}

// over names the dimensions cost exceeds.
func (p WritePlan) over(cost WriteCost) []string { return p.overBy(cost, 1) }

// planAssertFactor is how far past its plan a write from server code may go in
// a test binary before the overrun fails the test.
const planAssertFactor = 2

// overBy names the dimensions cost exceeds by more than factor times the plan.
func (p WritePlan) overBy(cost WriteCost, factor int64) []string {
	var out []string
	if p.Rows > 0 && cost.Rows > factor*p.Rows {
		out = append(out, "rows")
	}
	if p.Pages > 0 && cost.Pages > factor*p.Pages {
		out = append(out, "pages")
	}
	if p.VMOps > 0 && cost.VMOps > factor*p.VMOps {
		out = append(out, "vmOps")
	}
	return out
}

// callSite is the stack of whoever began a write, captured cheaply at BEGIN
// and resolved only when the write overruns.
type callSite struct {
	pcs [20]uintptr
	n   int
}

func captureSite(skip int) callSite {
	var site callSite
	site.n = runtime.Callers(skip+1, site.pcs[:])
	return site
}

// resolve returns the first frame outside dbwork and database/sql, and whether
// that frame is in a test file.
func (s callSite) resolve() (function, file string, line int, inTest bool) {
	if s.n == 0 {
		return "unknown", "", 0, false
	}
	frames := runtime.CallersFrames(s.pcs[:s.n])
	for {
		frame, more := frames.Next()
		infrastructure := strings.Contains(frame.Function, "/internal/dbwork.") && !strings.HasSuffix(frame.File, "_test.go") ||
			strings.HasPrefix(frame.Function, "database/sql.")
		if !infrastructure || !more {
			return frame.Function, frame.File, frame.Line, testCode(frame.File)
		}
	}
}

// chain lists up to four callers outside dbwork, innermost first, for the
// census report.
func (s callSite) chain() string {
	if s.n == 0 {
		return ""
	}
	var out []string
	frames := runtime.CallersFrames(s.pcs[:s.n])
	for len(out) < 4 {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "/internal/dbwork.") || testCode(frame.File) {
			name := frame.Function
			if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
				name = name[slash+1:]
			}
			out = append(out, name)
		}
		if !more {
			break
		}
	}
	return strings.Join(out, " < ")
}

// PlanOverrun is one recorded write that exceeded its plan.
type PlanOverrun struct {
	Site   string    `json:"site"`
	Class  string    `json:"class"`
	Plan   string    `json:"plan"`
	Over   []string  `json:"over"`
	Cost   WriteCost `json:"cost"`
	Budget WriteCost `json:"budget"`
	AtMs   int64     `json:"atMs"`
}

// PlanStats is the process-wide picture of write budgets.
type PlanStats struct {
	// Measured counts committed gated transactions whose cost was read.
	Measured       uint64              `json:"measured"`
	MusicBrainzWAL *AccountingWALStats `json:"musicBrainzWal,omitempty"`
	// Overruns counts those that exceeded their plan, in total, by class and
	// by dimension (rows, pages, vmOps).
	Overruns            uint64            `json:"overruns"`
	OverrunsByClass     map[string]uint64 `json:"overrunsByClass"`
	OverrunsByDimension map[string]uint64 `json:"overrunsByDimension"`
	// Largest is the largest value seen in each dimension, overrun or not.
	Largest WriteCost `json:"largest"`
	// Recent is the last few overruns, newest last.
	Recent []PlanOverrun `json:"recent"`
}

const planRecent = 16

var plans struct {
	measured atomic.Uint64
	overruns atomic.Uint64
	logged   atomic.Int64

	mu          sync.Mutex
	byClass     map[Class]uint64
	byDimension map[string]uint64
	largest     WriteCost
	recent      []PlanOverrun
}

// Plans snapshots write-budget telemetry.
func Plans() PlanStats {
	accounting := AccountingWAL()
	out := PlanStats{Measured: plans.measured.Load(), Overruns: plans.overruns.Load(), OverrunsByClass: map[string]uint64{}, OverrunsByDimension: map[string]uint64{}, MusicBrainzWAL: &accounting}
	plans.mu.Lock()
	defer plans.mu.Unlock()
	for class, count := range plans.byClass {
		out.OverrunsByClass[class.String()] = count
	}
	for dimension, count := range plans.byDimension {
		out.OverrunsByDimension[dimension] = count
	}
	out.Largest = plans.largest
	out.Recent = append([]PlanOverrun{}, plans.recent...)
	return out
}

// ResetPlans zeroes write-budget telemetry, for tests and the load harness.
func ResetPlans() {
	resetAccountingWAL()
	plans.measured.Store(0)
	plans.overruns.Store(0)
	plans.mu.Lock()
	plans.byClass, plans.byDimension, plans.largest, plans.recent = nil, nil, WriteCost{}, nil
	plans.mu.Unlock()
}

// PlanReportEnv names a file that test binaries append overruns to instead of
// panicking. It exists for the census run that finds every overrun at once;
// ordinary test runs leave it unset and fail on the first.
const PlanReportEnv = "PORTICO_WRITEPLAN_REPORT"

var planReport sync.Mutex

// notePlan measures one finished write against its plan. It runs after the
// commit and after the gate is released, so nothing it does — not even the
// test-mode panic — can hold another writer.
func notePlan(class Class, plan WritePlan, cost WriteCost, measured bool, site callSite) {
	if !measured {
		return
	}
	plans.measured.Add(1)
	over := plan.over(cost)
	plans.mu.Lock()
	plans.largest.Rows = max(plans.largest.Rows, cost.Rows)
	plans.largest.Pages = max(plans.largest.Pages, cost.Pages)
	plans.largest.VMOps = max(plans.largest.VMOps, cost.VMOps)
	if len(over) == 0 {
		plans.mu.Unlock()
		return
	}
	function, file, line, inTest := site.resolve()
	record := PlanOverrun{Site: function, Class: class.String(), Plan: plan.Name, Over: over, Cost: cost, Budget: WriteCost{Rows: plan.Rows, Pages: plan.Pages, VMOps: plan.VMOps}, AtMs: time.Now().UnixMilli()}
	if plans.byClass == nil {
		plans.byClass, plans.byDimension = map[Class]uint64{}, map[string]uint64{}
	}
	plans.byClass[class]++
	for _, dimension := range over {
		plans.byDimension[dimension]++
	}
	plans.recent = append(plans.recent, record)
	if len(plans.recent) > planRecent {
		plans.recent = plans.recent[len(plans.recent)-planRecent:]
	}
	plans.mu.Unlock()
	plans.overruns.Add(1)
	message := fmt.Sprintf("dbwork: a %s write in %s exceeded its %q plan on %s: %d rows, %d pages, %d VM operations (plan %d, %d, %d)",
		class, function, plan.Name, strings.Join(over, ", "), cost.Rows, cost.Pages, cost.VMOps, plan.Rows, plan.Pages, plan.VMOps)
	if testing.Testing() {
		if inTest || len(plan.overBy(cost, planAssertFactor)) == 0 {
			return
		}
		if path := os.Getenv(PlanReportEnv); path != "" {
			planReport.Lock()
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				fmt.Fprintf(f, "%s\t%s:%d\t%s\n", message, file, line, site.chain())
				f.Close()
			}
			planReport.Unlock()
			return
		}
		panic(message + " at " + file + ":" + fmt.Sprint(line) + ", more than twice its plan; split the work or declare a named WritePlan with a reason")
	}
	now := time.Now().UnixNano()
	if last := plans.logged.Load(); now-last >= int64(time.Minute) && plans.logged.CompareAndSwap(last, now) {
		log.Printf("%s (%d overruns so far)", message, plans.overruns.Load())
	}
}
