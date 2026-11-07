package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var ErrSnapshotClosed = errors.New("read snapshot is closed")
var ErrSnapshotOverrun = errors.New("physical read snapshot exceeded deadline")
var ErrSnapshotBudget = errors.New("read window exceeds memory or fixed pool limits")

// Snapshots owns native read lifetimes, independently of consumer cooperation.
// Composition owns helper pool/cache/native memory until Close succeeds. The
// driver owns its private pool and uses the supplied shared helper connector.
// Only compiled SELECT statements from its closed registry can be invoked.
type Snapshots struct {
	mu        sync.Mutex
	closed    bool
	physical  sync.WaitGroup
	watchers  sync.WaitGroup
	running   map[*ReadScope]context.CancelFunc
	uncertain map[*ReadScope]struct{}
	db        snapshotPool
	owner     *snapshotOwnedConnector
	config    sqliteconn.Config
	governor  Governor
	authority *Authority
	slots     chan struct{}
	reads     map[stmt.ID]stmt.Statement
}
type SnapshotResult struct {
	Settings              sqliteconn.Settings
	Started, Ended        time.Time // physical acquisition through connection return
	Deadline, SQLDeadline time.Time
	Refusal               *Refusal
	NativeReservedBytes   int64
	GoReservedBytes       int64
}

func NewSnapshots(connector driver.Connector, c sqliteconn.Config, g Governor, a *Authority, registry *stmt.Registry) (*Snapshots, error) {
	if connector == nil || g == nil || a == nil || g != a.ledger || c.Role != sqliteconn.Reader || c.Connections <= 0 || c.CacheKiB <= 0 || c.ReadLimits.LengthBytes <= 0 || c.ReadLimits.Columns <= 0 || c.ReadLimits.Columns > 128 || registry == nil {
		return nil, sqliteconn.ErrPolicy
	}
	manifest := registry.Manifest()
	if len(manifest) == 0 || len(manifest) > 128 {
		return nil, sqliteconn.ErrPolicy
	}
	reads := make(map[stmt.ID]stmt.Statement, len(manifest))
	for _, entry := range manifest {
		words := strings.Fields(entry.SQL)
		// stmt.New enforces one statement and primitive bindings. Kind alone is
		// manifest metadata, so require a SELECT too. Leading WITH/PRAGMA/ATTACH,
		// transaction controls and every TEMP/main mutation are refused here.
		if entry.Kind != stmt.Read || len(entry.SQL) > 64<<10 || len(words) == 0 || !strings.EqualFold(words[0], "SELECT") {
			return nil, sqliteconn.ErrPolicy
		}
		statement, err := registry.Lookup(entry.ID)
		if err != nil {
			return nil, err
		}
		reads[entry.ID] = statement
	}
	owner := &snapshotOwnedConnector{Connector: connector, failed: make(map[*snapshotOwnedConn]error)}
	db := newSnapshotPool(owner, c.Connections)
	return &Snapshots{db: db, owner: owner, config: c, governor: g, authority: a, slots: make(chan struct{}, c.Connections), running: make(map[*ReadScope]context.CancelFunc), uncertain: make(map[*ReadScope]struct{}), reads: reads}, nil
}

// NativeRowReservation is the fixed upper bound for one result row's decode:
// configured columns times the pool's per-value limit. VM/temp/cache workspace
// is separate qualified custody; this does not claim a tighter native bound.
func (s *Snapshots) NativeRowReservation() int64 {
	return int64(s.config.ReadLimits.Columns) * int64(s.config.ReadLimits.LengthBytes)
}

// Read keeps output/consumer custody until the callback returns (including a
// panic). Expiry independently interrupts, rolls back and returns/discards the
// physical connection. No sql.Rows, borrowed bytes or Scanner enters a consumer.
// Values leaving the callback need separately charged output custody before
// its active grant retires. A stuck consumer remains charged and cannot pin a
// native snapshot or delay Close. It does not authorize refund of its buffers or other resource custody.
func (s *Snapshots) Read(parent context.Context, r Request, budget time.Duration, read func(*ReadScope) error) (SnapshotResult, error) {
	return s.read(parent, r, budget, SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: 8}, read)
}

func (s *Snapshots) read(parent context.Context, r Request, budget time.Duration, forecast SnapshotForecast, read func(*ReadScope) error) (out SnapshotResult, err error) {
	if read == nil || r.Writer || r.Need[Reader] != 1 || r.Need[MemoryBytes] <= 0 || budget <= 0 || r.Budget.RawRows == 0 || r.Budget.RawRows > 4096 {
		return out, ErrGrant
	}
	out.NativeReservedBytes = forecast.NativeRowBytes
	out.GoReservedBytes = r.Need[MemoryBytes] - out.NativeReservedBytes
	if out.GoReservedBytes <= 0 {
		return out, ErrSnapshotBudget
	}
	deadline := time.Now().Add(min(budget, time.Second))
	if r.Deadline.Before(deadline) {
		deadline = r.Deadline
	}
	if d, ok := parent.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	// Leave time for interrupt/rollback/Assert/discard inside the physical cap.
	reserve := min(25*time.Millisecond, time.Until(deadline)/4)
	if reserve <= 0 {
		return out, context.DeadlineExceeded
	}
	out.Deadline = deadline
	out.SQLDeadline = deadline.Add(-reserve)
	r.Deadline = out.SQLDeadline
	ctx, stop := context.WithDeadline(parent, out.SQLDeadline)
	defer stop()
	queries, cancelQueries := context.WithCancel(ctx)
	scope := &ReadScope{ctx: queries, cancel: cancelQueries, authority: s.authority, expected: r, reads: s.reads, config: s.config, maxQueries: int(forecast.MaxQueries), nativeRowBytes: forecast.NativeRowBytes}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancelQueries()
		return out, ErrSnapshotClosed
	}
	s.running[scope] = stop
	s.physical.Add(1)
	s.mu.Unlock()
	var once sync.Once
	done := make(chan struct{})
	var physicalErr error
	finish := func() {
		once.Do(func() {
			var safe bool
			physicalErr, safe = scope.closeNative(ctx.Err() != nil)
			if safe {
				s.authority.snapshotPhysicallySafe(scope)
			}
			out.Ended = time.Now()
			if !out.Started.IsZero() && out.Ended.After(deadline) {
				s.authority.EnterStatic()
				physicalErr = errors.Join(physicalErr, ErrSnapshotOverrun)
			}
			if !safe {
				s.authority.EnterStatic()
			}
			s.mu.Lock()
			if !safe {
				s.uncertain[scope] = struct{}{}
			}
			delete(s.running, scope)
			s.mu.Unlock()
			s.physical.Done()
			close(done)
		})
		<-done
	}
	defer func() {
		consumerPanic := recover()
		finish()
		err = errors.Join(err, physicalErr, ctx.Err())
		if scope.grant != nil {
			s.governor.Cancel(scope.grant)
			scope.consumerDone.Store(true)
			err = errors.Join(err, scope.retire())
		}
		if consumerPanic != nil {
			panic(consumerPanic)
		}
		if scope.owned != nil {
			if fault := scope.owned.panicError(); fault != nil {
				panic(fault.value)
			}
		}
	}()
	select {
	case s.slots <- struct{}{}:
		scope.slot = s.slots
		defer func() { finish(); scope.releaseSlot() }()
	default:
		return out, ErrBusy
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	d := s.authority.trySnapshotContext(ctx, r, scope)
	if d.Refusal != nil {
		out.Refusal = d.Refusal
		return out, nil
	}
	scope.grant = d.Grant
	scope.copyBudget, err = sqlite.NewRowBudget(r.Budget.RawRows, out.GoReservedBytes)
	if err != nil {
		return out, ErrSnapshotBudget
	}
	if err = s.authority.CheckContext(queries, scope.grant, r); err != nil {
		return out, err
	}
	out.Started = time.Now()
	if scope.conn, err = s.db.Conn(queries); err != nil {
		return out, err
	}
	if err = scope.conn.Raw(func(raw any) error {
		owned, ok := raw.(*snapshotOwnedConn)
		if !ok || !owned.ReadBudgetSupported() {
			return sqliteconn.ErrPolicy
		}
		scope.owned = owned
		return nil
	}); err != nil {
		scope.discard = true
		return out, err
	}
	if out.Settings, err = sqliteconn.Assert(queries, scope.conn, s.config); err != nil {
		scope.discard = true
		return out, err
	}
	if err = s.authority.CheckContext(queries, scope.grant, r); err != nil {
		return out, err
	}
	// Manual private transaction avoids database/sql's asynchronous rollback
	// hiding an error behind ErrTxDone. Nothing exposing Conn/Tx escapes.
	scope.began = true
	if _, err = scope.conn.ExecContext(queries, "BEGIN"); err != nil {
		return out, err
	}
	s.watchers.Add(1)
	supervise.Go("store.snapshot.deadline", func() {
		defer s.watchers.Done()
		select {
		case <-ctx.Done():
			finish()
		case <-done:
		}
	})
	err = read(scope)
	return out, err
}

// Close joins physical cleanup, never an arbitrary blocked consumer. Its still
// retained buffers/grant stay charged until that consumer actually returns.
func (s *Snapshots) Close() error {
	s.mu.Lock()
	s.closed = true
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	s.physical.Wait()
	s.watchers.Wait()
	// Failed disposal stays owned and charged. Close retries retained raw handles;
	// the composition must not retire pool memory while this reports an error.
	s.mu.Lock()
	pending := make([]*ReadScope, 0, len(s.uncertain))
	for scope := range s.uncertain {
		pending = append(pending, scope)
	}
	s.mu.Unlock()
	var err error
	for _, scope := range pending {
		scope.op.Lock()
		var closeErr error
		if scope.nativeSafe.Load() {
			closeErr = nil
		} else if scope.failedRaw == nil {
			closeErr = errors.New("snapshot physical exit remains unproven")
		} else {
			closeErr = scope.failedRaw.Close()
		}
		if closeErr == nil {
			scope.failedRaw = nil
			s.authority.snapshotPhysicallySafe(scope)
			scope.releaseSlot()
		}
		scope.op.Unlock()
		if closeErr == nil {
			s.mu.Lock()
			delete(s.uncertain, scope)
			s.mu.Unlock()
			closeErr = scope.retire()
		}
		err = errors.Join(err, closeErr)
	}
	return errors.Join(err, s.db.Close(), s.owner.retry())
}

type ReadScope struct {
	cleanupContext           func() (context.Context, context.CancelFunc) // CP clock seam; nil keeps snapshot cleanup policy
	op                       sync.Mutex                                   // native operations only; never held across consumer code
	closed                   atomic.Bool
	conn                     *sql.Conn
	began, discard           bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	authority                *Authority
	grant                    *Grant
	expected                 Request
	config                   sqliteconn.Config
	reads                    map[stmt.ID]stmt.Statement
	rows                     uint32
	calls                    int
	maxQueries               int
	nativeRowBytes           int64
	failedRaw                driver.Conn
	owned                    *snapshotOwnedConn
	copyBudget               *sqlite.RowBudget
	nativeSafe, consumerDone atomic.Bool
	retireMu                 sync.Mutex
	retired                  bool
	retireErr                error
	slot                     chan struct{}
	slotRelease              sync.Once
}

func (s *ReadScope) releaseSlot() {
	if s.nativeSafe.Load() {
		s.slotRelease.Do(func() { <-s.slot })
	}
}

func (s *ReadScope) retire() error {
	s.retireMu.Lock()
	defer s.retireMu.Unlock()
	if s.grant != nil && s.nativeSafe.Load() && s.consumerDone.Load() && !s.retired {
		s.retired = true
		s.retireErr = s.authority.Retired(s.grant)
	}
	return s.retireErr
}

func (s *ReadScope) Context() context.Context { return s.ctx }
func (s *ReadScope) charge(n int64) error {
	if err := s.copyBudget.ReserveBytes(n); err != nil {
		return errors.Join(ErrSnapshotBudget, err)
	}
	return nil
}

// Query completely drains/closes native rows before returning owned primitive
// values. sql.RawBytes/custom Scanner/Valuer cannot run under a SQL lock. The
// compiled statement recipe must also bound native row/VM/page/materialization
// cost; these output limits do not turn arbitrary SQL into a memory quota.
func (s *ReadScope) Query(id stmt.ID, args ...any) (values [][]any, err error) {
	s.op.Lock()
	defer s.op.Unlock()
	defer func() {
		var native *sqlite.Error
		if errors.Is(err, sqlite.ErrRowBudget) || (errors.As(err, &native) && native.Code()&255 == sqlite3.SQLITE_TOOBIG) {
			err = errors.Join(ErrSnapshotBudget, err)
		}
	}()
	if s.closed.Load() {
		return nil, ErrSnapshotClosed
	}
	if err = s.ctx.Err(); err != nil {
		return nil, err
	}
	if err = s.authority.CheckContext(s.ctx, s.grant, s.expected); err != nil {
		return nil, err
	}
	limit := s.maxQueries
	if limit == 0 {
		limit = 8
	} // preserve existing internal/test scopes
	if s.calls >= limit {
		return nil, ErrBusy
	}
	s.calls++
	statement, ok := s.reads[id]
	if !ok {
		return nil, sqliteconn.ErrPolicy
	}
	for _, arg := range args {
		size := int64(32)
		switch v := arg.(type) {
		case nil, int64, float64, bool:
		case string:
			size += 2 * int64(len(v))
		case []byte:
			size += 3 * int64(len(v))
		default:
			return nil, sqliteconn.ErrPolicy
		}
		if err = s.charge(size); err != nil {
			return nil, err
		}
	}
	// Fixed query/row/context overhead is reserved before database/sql or the
	// driver allocates it; per-column Go-copy peak is checked before copying.
	if err = s.charge(4096); err != nil {
		return nil, err
	}
	bound, err := statement.Bind(args...)
	if err != nil {
		return nil, err
	}
	rows, err := bound.QueryContext(sqlite.WithRowBudget(s.ctx, s.copyBudget), s.conn)
	if err != nil {
		return nil, err
	}
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil {
			s.discard = true
		}
		err = errors.Join(err, closeErr)
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(columns) > 128 {
		return nil, ErrSnapshotBudget
	}
	for rows.Next() {
		if err = s.ctx.Err(); err != nil {
			return nil, err
		}
		if s.rows == s.expected.Budget.RawRows {
			return nil, ErrSnapshotBudget
		}
		s.rows++
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i, v := range row {
			switch x := v.(type) {
			case nil, int64, float64, bool, time.Time:
			case string:
				row[i] = strings.Clone(x)
			case []byte:
				row[i] = append(make([]byte, 0, len(x)), x...)
			default:
				return nil, sqliteconn.ErrPolicy
			}
		}
		values = append(values, row)
	}
	return values, rows.Err()
}
func (s *ReadScope) closeNative(forceDiscard bool) (err error, safe bool) {
	s.closed.Store(true)
	s.cancel()
	s.op.Lock()
	defer s.op.Unlock()
	if s.owned != nil {
		s.owned.safetyMu.Lock()
		pending := len(s.owned.resources) != 0
		s.owned.safetyMu.Unlock()
		if s.owned.panicError() != nil || pending {
			s.authority.EnterStatic()
			forceDiscard = true
		}
	}
	s.discard = s.discard || forceDiscard
	if s.conn == nil {
		return nil, true
	}
	cleanupContext := s.cleanupContext
	if cleanupContext == nil {
		cleanupContext = func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 25*time.Millisecond)
		}
	}
	cleanup, cancel := cleanupContext()
	defer cancel()
	if s.owned != nil {
		s.failedRaw = s.owned
	}
	exited := !s.began
	if s.began {
		_, rollback := s.conn.ExecContext(cleanup, "ROLLBACK")
		err = errors.Join(err, rollback)
		exited = rollback == nil
	}
	if exited && !s.discard {
		_, policy := sqliteconn.Assert(cleanup, s.conn, s.config)
		err = errors.Join(err, policy)
		s.discard = policy != nil
	}
	if !exited || s.discard {
		var closeErr error
		called := false
		rawErr := s.conn.Raw(func(raw any) error {
			called = true
			c, ok := raw.(driver.Conn)
			if !ok {
				closeErr = sqliteconn.ErrPolicy
			} else {
				s.failedRaw = c
				closeErr = c.Close()
				if closeErr == nil {
					s.failedRaw = nil
				}
			}
			return driver.ErrBadConn
		})
		if !called && s.owned != nil {
			closeErr = s.owned.Close()
			called = true
		}
		safe = called && closeErr == nil
		if safe {
			s.failedRaw = nil
		}
		if !errors.Is(rawErr, driver.ErrBadConn) {
			err = errors.Join(err, rawErr)
		}
		err = errors.Join(err, closeErr)
	} else {
		safe = true
	}
	closeErr := s.conn.Close()
	if closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
		err = errors.Join(err, closeErr)
		safe = false
	}
	return err, safe
}
