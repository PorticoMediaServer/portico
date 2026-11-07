package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

const n1UnitLimit = 128

var errN1LedgerFull = errors.New("N1 component unit ledger full; native call withheld")

type n1Native interface {
	VMOperations() int64
	sqlite.DBStatus
	WALIO() sqlite.WALIOStats
}

type n1Counter struct {
	Value  *int64 `json:"value"`
	Reason string `json:"unavailable_reason,omitempty"`
	value  int64
}

func (c *n1Counter) set(value int64) { c.value = value; c.Value = &c.value; c.Reason = "" }

// Names, units and SQLite's current/cumulative semantics are in the start
// manifest. None of these DBStatus operations supplies a memory peak.
var n1StatusOps = [...]struct {
	Name      string
	Unit      string
	Op        sqlite.DBStatusOp
	Semantics string
}{
	{"cache_used", "bytes", sqlite.DBStatusCacheUsed, "current"},
	{"schema_used", "bytes", sqlite.DBStatusSchemaUsed, "current"},
	{"statement_used", "bytes", sqlite.DBStatusStmtUsed, "current"},
	{"cache_hit", "count", sqlite.DBStatusCacheHit, "cumulative"},
	{"cache_miss", "count", sqlite.DBStatusCacheMiss, "cumulative"},
	{"cache_write", "count", sqlite.DBStatusCacheWrite, "cumulative"},
	{"cache_spill", "count", sqlite.DBStatusCacheSpill, "cumulative"},
	{"tempbuf_spill", "bytes", sqlite.DBStatusTempbufSpill, "cumulative"},
}

type n1NativeSnapshot struct {
	StartedNS    int64                       `json:"started_ns"`
	ReturnedNS   int64                       `json:"returned_ns"`
	ConnectionID uint64                      `json:"connection_id"`
	VMOps        n1Counter                   `json:"vm_operations"`
	DB           [len(n1StatusOps)]n1Counter `json:"db_status"`
	High         [len(n1StatusOps)]n1Counter `json:"db_status_raw_high"`
	WALIO        *sqlite.WALIOStats          `json:"wal_io"`
	wal          sqlite.WALIOStats
}

type n1Unit struct {
	Order            int     `json:"order"`
	UnitID           string  `json:"unit_id"`
	ID               stmt.ID `json:"statement_id"`
	UseIndex         int     `json:"use_index"`
	Phase            string  `json:"phase"`
	StartedNS        *int64  `json:"started_ns"`
	ReturnedNS       *int64  `json:"returned_ns"`
	ElapsedNS        *int64  `json:"elapsed_ns"`
	NativeStartedNS  *int64  `json:"native_started_ns"`
	NativeReturnedNS *int64  `json:"native_returned_ns"`
	NativeElapsedNS  *int64  `json:"native_elapsed_ns"`
	nativeStarted    time.Time
	Error            string            `json:"error"`
	RowsReturned     *int64            `json:"rows_returned"`
	RowsChanged      *int64            `json:"rows_changed"`
	RowsExamined     *int64            `json:"rows_examined"`
	RowsRejected     *int64            `json:"rows_rejected"`
	RowsClosed       bool              `json:"rows_closed"`
	RowsPresent      bool              `json:"rows_present"`
	VMOps            n1Counter         `json:"vm_operations"`
	Before           *n1NativeSnapshot `json:"before"`
	After            *n1NativeSnapshot `json:"after"`
	started          time.Time
	returned         time.Time
	rows             int64
	exclusive        bool
}

type n1Trace struct {
	nativeBegins    uint64 // all actual native W BEGIN attempts, including fixture preparation
	libraryVector   [6]int64
	libraryObserved bool
	origin          time.Time
	instrument      bool
	armed           bool
	units           []n1Unit // fixed backing storage is allocated BEFORE measured custody
	used            int
	overflow        bool
	ids             map[string]stmt.ID
	uses            map[stmt.ID]int
	connections     uint64
	closed          uint64
	activeRows      int
	conn            *n1MeasuredConn
	discardInjected bool
	failExec        string // closed deliberate-discard test cell; no SQL/config input
	failQuery       string // control-only injection; never a target/config input
	closes          [4]n1NativeClose
	closeCount      int
	closeOverflow   bool
}

func newN1Trace(origin time.Time, instrument bool) *n1Trace {
	t := &n1Trace{origin: origin, instrument: instrument, units: make([]n1Unit, n1UnitLimit), ids: map[string]stmt.ID{}, uses: map[stmt.ID]int{}}
	for _, d := range scanInventoryFreshDefinitions() {
		t.ids[d.SQL] = d.ID
		t.uses[d.ID] = 0
	}
	return t
}

// Bind exact SQL/IDs from the ACTUAL complete captured registry, not a copied
// effects-only list. All allocation/reset happens outside physical custody.
func (t *n1Trace) useRegistry(registry *stmt.Registry) {
	t.ids, t.uses = map[string]stmt.ID{}, map[stmt.ID]int{}
	for _, entry := range registry.Manifest() {
		t.ids[entry.SQL], t.uses[entry.ID] = entry.ID, 0
	}
}

func (t *n1Trace) nextTurn() {
	t.armed = false
	t.libraryObserved = false
	t.units, t.used, t.overflow = make([]n1Unit, n1UnitLimit), 0, false
	for id := range t.uses {
		t.uses[id] = 0
	}
}

// This decorates the existing pool, on the SAME executor/file. It neither
// opens nor admits a command. Native access stays private to this test binary.
func (t *n1Trace) decorate(conn driver.Conn) driver.Conn {
	t.connections++
	owned := conn.(*ownedPhysicalConn)
	native, _ := owned.writerDriverConn.(n1Native)
	owned.writerDriverConn = &n1NativeCloseConn{writerDriverConn: owned.writerDriverConn, trace: t, id: t.connections}
	c := &n1MeasuredConn{writerDriverConn: owned, trace: t, native: native, id: t.connections}
	t.conn = c
	return c
}

type n1MeasuredConn struct {
	writerDriverConn
	trace  *n1Trace
	native n1Native
	id     uint64
	closed bool
}

func (c *n1MeasuredConn) snapshot(out *n1NativeSnapshot, withinHold bool) {
	out.StartedNS = time.Since(c.trace.origin).Nanoseconds()
	out.ConnectionID = c.id
	if c.native == nil || c.closed {
		out.VMOps.Reason = "native diagnostics unavailable or connection closed"
		for i := range out.DB {
			out.DB[i].Reason = out.VMOps.Reason
			out.High[i].Reason = out.VMOps.Reason
		}
	} else {
		out.VMOps.set(c.native.VMOperations())
		for i, op := range n1StatusOps {
			if withinHold && i < 3 {
				out.DB[i].Reason = "schema/cache/statement memory sampled outside HOLD only; native schema walk is intrusive"
				out.High[i].Reason = out.DB[i].Reason
				continue
			}
			current, high, err := c.native.Status(op.Op, false) // NEVER reset a counter
			if err != nil {
				out.DB[i].Reason = err.Error()
				out.High[i].Reason = err.Error()
			} else {
				out.DB[i].set(int64(current))
				out.High[i].set(int64(high))
			}
		}
		out.wal = c.native.WALIO()
		out.WALIO = &out.wal // retain Qualified=false as raw evidence, never zero-fill
	}
	out.ReturnedNS = time.Since(c.trace.origin).Nanoseconds()
}

func (c *n1MeasuredConn) start(query string) *n1Unit {
	t := c.trace
	if !t.armed {
		return nil
	}
	if t.used == len(t.units) || (t.used == len(t.units)-1 && query != "ROLLBACK") {
		t.overflow = true
		return nil
	}
	u := &t.units[t.used]
	t.used++
	u.Order, u.UnitID = t.used, query
	u.VMOps.Reason = "native request/attribution did not return"
	u.exclusive = t.activeRows == 0
	u.Phase = "setup"
	if id, ok := t.ids[query]; ok {
		u.ID, u.UnitID, u.Phase = id, string(id), "domain"
		t.uses[id]++
		u.UseIndex = t.uses[id]
	} else if query == "COMMIT" {
		u.Phase = "commit"
	} else if query == "ROLLBACK" {
		u.Phase = "rollback"
	}
	u.started = time.Now()
	u.StartedNS = n1NS(t.origin, u.started)
	if t.instrument {
		u.Before = &n1NativeSnapshot{}
		c.snapshot(u.Before, true)
	}
	u.nativeStarted = time.Now()
	u.NativeStartedNS = n1NS(t.origin, u.nativeStarted)
	return u
}

func (c *n1MeasuredConn) finish(u *n1Unit, err error) {
	if u == nil || u.ReturnedNS != nil {
		return
	}
	nativeReturned := time.Now()
	u.NativeReturnedNS, u.NativeElapsedNS = n1NS(c.trace.origin, nativeReturned), n1Duration(u.nativeStarted, nativeReturned)
	if err != nil {
		u.Error = err.Error()
	}
	u.exclusive = u.exclusive && c.trace.used == u.Order
	if c.trace.instrument {
		u.After = &n1NativeSnapshot{}
		c.snapshot(u.After, true)
		if u.exclusive && c.trace.used == u.Order && u.Before != nil && u.Before.VMOps.Value != nil && u.After.VMOps.Value != nil && *u.After.VMOps.Value >= *u.Before.VMOps.Value {
			u.VMOps.set(*u.After.VMOps.Value - *u.Before.VMOps.Value)
		} else {
			u.VMOps.Reason = "overlapping, missing or regressing native VM counter"
		}
	} else {
		u.VMOps.Reason = "timing cohort does not sample native counters inside HOLD"
	}
	u.returned = time.Now()
	u.ReturnedNS, u.ElapsedNS = n1NS(c.trace.origin, u.returned), n1Duration(u.started, u.returned)
}

func (c *n1MeasuredConn) SQLiteDQS() (ddl, dml int, err error) {
	u := c.start("native.SQLiteDQS")
	ddl, dml, err = c.writerDriverConn.SQLiteDQS()
	if u != nil {
		u.RowsClosed = true
	}
	c.finish(u, err)
	return
}

func (c *n1MeasuredConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	u := c.start(query)
	if c.trace.overflow && query != "ROLLBACK" {
		return nil, errN1LedgerFull
	}
	if c.trace.armed && c.trace.failExec != "" && query == c.trace.failExec {
		c.trace.discardInjected = true
		err := errN1Discard
		c.finish(u, err)
		if u != nil {
			u.exclusive = false
			u.Before = nil
			u.After = nil
			u.NativeStartedNS = nil
			u.NativeReturnedNS = nil
			u.NativeElapsedNS = nil
			u.VMOps = n1Counter{Reason: "deliberate fault before native entry"}
		}
		return nil, err
	}
	if query == "BEGIN IMMEDIATE" {
		c.trace.nativeBegins++
	}
	r, err := c.writerDriverConn.ExecContext(ctx, query, args)
	if u != nil && r != nil {
		if n, e := r.RowsAffected(); e == nil {
			u.RowsChanged = &n
		}
	}
	if u != nil {
		u.RowsClosed = true
	}
	c.finish(u, err)
	return r, err
}

func (c *n1MeasuredConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	u := c.start(query)
	if c.trace.overflow {
		return nil, errN1LedgerFull
	}
	if c.trace.armed && c.trace.failQuery != "" && query == c.trace.failQuery {
		err := errors.New("injected N1 component native query failure")
		c.finish(u, err)
		return nil, err
	}
	r, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err != nil || r == nil {
		c.finish(u, err)
		return r, err
	}
	if u == nil {
		return r, nil
	}
	u.RowsReturned = &u.rows
	u.RowsPresent = true
	c.trace.activeRows++
	return &n1MeasuredRows{Rows: r, conn: c, unit: u}, nil
}

func (c *n1MeasuredConn) Close() error {
	err := c.writerDriverConn.Close()
	if !c.closed && err == nil {
		c.closed = true
		c.trace.closed++
	}
	return err
}

type n1MeasuredRows struct {
	driver.Rows
	conn   *n1MeasuredConn
	unit   *n1Unit
	err    error
	closed bool
}

func (r *n1MeasuredRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		r.unit.rows++
		if r.unit.ID == scanFreshLibrary && len(dest) == 6 {
			var vector [6]int64
			known := true
			for i, v := range dest {
				n, ok := v.(int64)
				known = known && ok
				vector[i] = n
			}
			if known {
				r.conn.trace.libraryVector = vector
				r.conn.trace.libraryObserved = true
			}
		}
	} else if !errors.Is(err, io.EOF) {
		r.err = errors.Join(r.err, err)
	}
	return err
}

func (r *n1MeasuredRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.Rows.Close()
	r.unit.RowsClosed = err == nil
	r.conn.finish(r.unit, errors.Join(r.err, err))
	r.conn.trace.activeRows--
	return err
}
