package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var ErrSnapshotClosed = errors.New("read snapshot is closed")
var ErrSnapshotOverrun = errors.New("physical read snapshot exceeded one second")

// Snapshots uses a helper-opened Reader pool (mode=ro AND query_only=1). Trusted
// composition holds the explicit idle-pool memory budget until the pool closes.
// Each Read admits the active reader/output vector before acquiring a connection.
// Call Close, then close the pool, before retiring its lifetime memory custody.
type Snapshots struct {
	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
	db        *sql.DB
	config    sqliteconn.Config
	governor  Governor
	authority *Authority
	slots     chan struct{}
}

type SnapshotResult struct {
	Settings       sqliteconn.Settings
	Started, Ended time.Time
	Deadline       time.Time
	Refusal        *Refusal
}

func NewSnapshots(db *sql.DB, c sqliteconn.Config, g Governor, a *Authority) (*Snapshots, error) {
	if db == nil || g == nil || a == nil || c.Role != sqliteconn.Reader || c.Connections <= 0 || c.CacheKiB <= 0 || g != a.ledger || db.Stats().MaxOpenConnections != c.Connections {
		return nil, sqliteconn.ErrPolicy
	}
	return &Snapshots{db: db, config: c, governor: g, authority: a, slots: make(chan struct{}, c.Connections)}, nil
}

// Read is one keyset window. SQL values must leave this callback before any
// network/client wait. The deadline covers acquisition, Assert, BEGIN and reads;
// actual closure is measured independently. Context cancellation cannot force a
// native call to exit: its resource debit remains until close/rollback/return.
func (s *Snapshots) Read(ctx context.Context, r Request, routeBudget time.Duration, read func(*ReadScope) error) (out SnapshotResult, err error) {
	if read == nil || r.Writer || r.Need[Reader] != 1 || r.Need[MemoryBytes] <= 0 || routeBudget <= 0 {
		return out, ErrGrant
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return out, ErrSnapshotClosed
	}
	s.active.Add(1)
	s.mu.Unlock()
	defer s.active.Done()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return out, ErrBusy
	}
	if routeBudget > time.Second {
		routeBudget = time.Second
	}
	deadline := time.Now().Add(routeBudget)
	if r.Deadline.Before(deadline) {
		deadline = r.Deadline
	}
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	r.Deadline = deadline
	out.Deadline = deadline
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	d := s.governor.Try(r)
	if d.Refusal != nil {
		out.Refusal = d.Refusal
		return out, nil
	}
	p := d.Grant
	var conn *sql.Conn
	var scope *ReadScope
	defer func() {
		contextErr := ctx.Err()
		cancel()
		s.governor.Cancel(p)
		if scope != nil {
			err = errors.Join(err, scope.close())
		}
		// Conn.Close joins database/sql's cancellation rollback, even if a
		// concurrent automatic rollback made Tx.Rollback return ErrTxDone.
		if conn != nil {
			err = errors.Join(err, conn.Close())
		}
		out.Ended = time.Now()
		if !out.Started.IsZero() && out.Ended.Sub(out.Started) > time.Second {
			s.authority.EnterStatic()
			err = errors.Join(err, ErrSnapshotOverrun)
		}
		err = errors.Join(err, contextErr, s.authority.Retired(p))
	}()
	if err = s.authority.Check(p, r); err != nil {
		return out, err
	}
	if conn, err = s.db.Conn(ctx); err != nil {
		return out, err
	}
	if out.Settings, err = sqliteconn.Assert(ctx, conn, s.config); err != nil {
		// Assert never repairs drift. Dispose of an inconsistent physical
		// connection so the shared immutable connector restores its policy.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return out, err
	}
	if err = s.authority.Check(p, r); err != nil {
		return out, err
	}
	out.Started = time.Now()
	tx, beginErr := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if beginErr != nil {
		return out, beginErr
	}
	scope = &ReadScope{tx: tx, ctx: ctx, cancel: cancel, authority: s.authority, grant: p, expected: r, rows: make(map[*sql.Rows]struct{})}
	err = read(scope)
	return out, err
}

// Close prevents admission and joins all active callbacks/physical retirement.
// Callers cancel their request contexts to supersede work before joining Close.
func (s *Snapshots) Close() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.active.Wait() }

// ReadScope exposes no Tx/Conn/Raw/Exec handle. Its context and grant checks
// cannot be bypassed by passing a longer-lived context to a query. Keeping this
// pointer or a Rows after Read returns grants no new SQL or continued snapshot.
type ReadScope struct {
	mu        sync.Mutex
	closed    atomic.Bool
	tx        *sql.Tx
	ctx       context.Context
	cancel    context.CancelFunc
	authority *Authority
	grant     *Grant
	expected  Request
	rows      map[*sql.Rows]struct{}
}

// Context exposes the clamped lifetime to bounded decoders/reducers, without
// allowing the caller to extend the context used by any SQL operation.
func (s *ReadScope) Context() context.Context { return s.ctx }

func (s *ReadScope) Query(query string, args ...any) (*SnapshotRows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return nil, ErrSnapshotClosed
	}
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.authority.Check(s.grant, s.expected); err != nil {
		return nil, err
	}
	if len(s.rows) >= 8 {
		return nil, ErrBusy
	}
	rows, err := s.tx.QueryContext(s.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	s.rows[rows] = struct{}{}
	return &SnapshotRows{rows: rows, scope: s}, nil
}

func (s *ReadScope) close() error {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed.Store(true)
	var err error
	for rows := range s.rows {
		err = errors.Join(err, rows.Close())
	}
	s.rows = nil
	if rollback := s.tx.Rollback(); !errors.Is(rollback, sql.ErrTxDone) {
		err = errors.Join(err, rollback)
	}
	return err
}

type SnapshotRows struct {
	rows  *sql.Rows
	scope *ReadScope
}

func (r *SnapshotRows) Next() bool { return !r.scope.closed.Load() && r.rows.Next() }
func (r *SnapshotRows) Scan(dest ...any) error {
	if r.scope.closed.Load() {
		return ErrSnapshotClosed
	}
	return r.rows.Scan(dest...)
}
func (r *SnapshotRows) Err() error { return r.rows.Err() }
func (r *SnapshotRows) Close() error {
	r.scope.mu.Lock()
	defer r.scope.mu.Unlock()
	err := r.rows.Close()
	delete(r.scope.rows, r.rows)
	return err
}
