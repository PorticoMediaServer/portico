package dbwork

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ReaderLifetimes measures ownership, not SQLite's private read marks. BEGIN
// may precede the first read, and a scalar query may never read the WAL. These
// are conservative lifetimes: rows remain counted through native finalization,
// and snapshots through Commit/Rollback. No SQL, bindings or identity is kept.
type ReaderLifetimeStats struct {
	Snapshots           int    `json:"snapshots"`
	ImplicitRows        int    `json:"implicitRows"`
	OldestSnapshotMs    int64  `json:"oldestSnapshotMs"`
	OldestImplicitMs    int64  `json:"oldestImplicitRowsMs"`
	MaxSnapshotMs       int64  `json:"maxSnapshotMs"`
	MaxImplicitMs       int64  `json:"maxImplicitRowsMs"`
	OldestSnapshotClass string `json:"oldestSnapshotClass,omitempty"`
	OldestImplicitClass string `json:"oldestImplicitRowsClass,omitempty"`
	AdmissionWaiting    int    `json:"admissionWaiting"`
	AdmissionPaused     bool   `json:"admissionPaused"`
	AdmissionWaits      uint64 `json:"admissionWaits"`
	AdmissionWaitMs     int64  `json:"admissionWaitMs"`
	// AdmissionRefused counts new reads refused with ErrReadPressure.
	AdmissionRefused uint64 `json:"admissionRefused"`
	// OverLifetime counts read leases that outlived their snapshot lifetime
	// (2 s foreground, 500 ms background), process-wide.
	OverLifetimeForeground uint64 `json:"overLifetimeForeground"`
	OverLifetimeBackground uint64 `json:"overLifetimeBackground"`
}

type readerScope struct {
	wal                      *walLedger // shared while any pool/overlapping OpenHandle remains alive
	plannerGeneration        atomic.Uint64
	mu                       sync.Mutex
	key                      string
	connections              int // guarded by readerScopes.mu
	active                   map[*readerLease]struct{}
	resume                   chan struct{}
	pressure                 chan struct{} // owner survives reopening readers for out-of-gate copying
	drained                  chan struct{}
	waiting                  int
	waitingForeground        int
	waitingBackground        int
	waits                    uint64
	waitNanos                int64
	maxSnapshot, maxImplicit int64
	nextPressure             time.Time
	pressureFailures         int
	refused                  uint64
}

// ReaderPressureWait is how long a new read waits behind a WAL reset's reader
// pause before it is refused with ErrReadPressure. A pause normally ends within
// CheckpointReaderDrainDeadline.
const ReaderPressureWait = time.Second

const readerForegroundWaiterLimit = 24
const readerBackgroundWaiterLimit = 2

// ErrReadPressure is a new read refused while a WAL reset paused reader
// admission for longer than ReaderPressureWait. Nothing was read; the same
// request can be sent again shortly.
var ErrReadPressure = errors.New("dbwork: the database is recovering its write-ahead log")

type readerLease struct {
	scope    *readerScope
	start    time.Time
	class    Class
	snapshot bool
	priority bool // contributes active/queued demand to the cold-read probe
	// long marks an owner-long read; site is who opened the lease, captured
	// only in test binaries for the lifetime census.
	long bool
	site callSite
	once sync.Once
	// base and conn mark a reference to a connection's shared implicit-rows
	// lease: one physical connection holds at most one reader lease.
	base *readerLease
	conn *observedConn
}

var readerScopes = struct {
	sync.Mutex
	byName map[string]*readerScope
}{byName: make(map[string]*readerScope)}

// Pools with different connection-local policies still share one WAL. The
// filename part of the DSN joins them; it is never exported in diagnostics.
func acquireReaderScope(name string) *readerScope {
	key, _, _ := strings.Cut(name, "?")
	readerScopes.Lock()
	defer readerScopes.Unlock()
	s := readerScopes.byName[key]
	if s == nil {
		s = &readerScope{key: key, active: make(map[*readerLease]struct{})}
		readerScopes.byName[key] = s
	}
	s.connections++
	return s
}

func releaseReaderScope(s *readerScope) {
	if s == nil {
		return
	}
	readerScopes.Lock()
	defer readerScopes.Unlock()
	s.connections--
	if s.connections == 0 {
		delete(readerScopes.byName, s.key)
		releaseWALLedger(s)
	}
}

type checkpointReaderBypassKey struct{}

func (c *observedConn) admitReader(ctx context.Context, snapshot bool) (*readerLease, error) {
	if c.readers == nil || (!snapshot && c.inTx) || ctx.Value(changeKey{}) != nil || ctx.Value(checkpointReaderBypassKey{}) != nil {
		return nil, nil
	}
	if !snapshot {
		// Rows already open on this connection already hold its lease; a
		// second set shares it. database/sql serializes calls on a connection.
		if c.rowsLease == nil {
			lease, err := c.readers.admit(ctx, false)
			if err != nil {
				return nil, err
			}
			c.rowsLease = lease
		}
		c.rowsRefs++
		return &readerLease{base: c.rowsLease, conn: c}, nil
	}
	return c.readers.admit(ctx, snapshot)
}

func (s *readerScope) admit(ctx context.Context, snapshot bool) (*readerLease, error) {
	s.mu.Lock()
	var waited time.Time
	class := ClassFrom(ctx, ClassInteractive)
	waitingClass, waitingLimit := &s.waitingForeground, readerForegroundWaiterLimit
	if class.Background() {
		waitingClass, waitingLimit = &s.waitingBackground, readerBackgroundWaiterLimit
	}
	priority := class.Priority() < ClassMaintenance.Priority()
	if priority {
		coldReadDemand.Add(1)
	}
	admitted := false
	defer func() {
		if priority && !admitted {
			coldReadDemand.Add(-1)
		}
	}()
	// Fresh authority reads share the security class's priority. Keep tracking
	// their lifetime, but let them pass: a security flood may defer the reset;
	// it must not wait behind maintenance. Other foreground classes still wait.
	var refuse <-chan time.Time
	for s.resume != nil && class != ClassSecurityFence {
		if waited.IsZero() {
			if *waitingClass >= waitingLimit {
				s.refused++
				s.mu.Unlock()
				return nil, ErrReadPressure
			}
			waited = time.Now()
			s.waits++
			s.waiting++
			(*waitingClass)++
			timer := time.NewTimer(ReaderPressureWait)
			defer timer.Stop()
			refuse = timer.C
		}
		resume := s.resume
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.waiting--
			(*waitingClass)--
			s.waitNanos += int64(time.Since(waited))
			s.mu.Unlock()
			return nil, ctx.Err()
		case <-refuse:
			// A reset that keeps new reads waiting this long has not found its
			// gap; answer with retry guidance rather than hold the request.
			s.mu.Lock()
			s.waiting--
			(*waitingClass)--
			s.waitNanos += int64(time.Since(waited))
			s.refused++
			s.mu.Unlock()
			return nil, ErrReadPressure
		case <-resume:
		}
		s.mu.Lock()
	}
	if !waited.IsZero() {
		s.waiting--
		(*waitingClass)--
		s.waitNanos += int64(time.Since(waited))
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	lease := &readerLease{scope: s, start: time.Now(), class: class, snapshot: snapshot, priority: priority, long: longRead(ctx)}
	if testing.Testing() {
		lease.site = captureSite(2)
	}
	s.active[lease] = struct{}{}
	admitted = true
	s.mu.Unlock()
	return lease, nil
}

func (r *readerLease) close() {
	if r == nil {
		return
	}
	if r.base != nil {
		r.once.Do(func() {
			c := r.conn
			c.rowsRefs--
			if c.rowsRefs == 0 && c.rowsLease == r.base {
				c.rowsLease = nil
				r.base.close()
			}
		})
		return
	}
	r.once.Do(func() {
		s := r.scope
		s.mu.Lock()
		delete(s.active, r)
		if r.priority {
			coldReadDemand.Add(-1)
		}
		elapsed := int64(time.Since(r.start))
		noteReaderLifetime(r, time.Duration(elapsed))
		if r.snapshot {
			s.maxSnapshot = max(s.maxSnapshot, elapsed)
		} else {
			s.maxImplicit = max(s.maxImplicit, elapsed)
		}
		if len(s.active) == 0 && s.drained != nil {
			close(s.drained)
			s.drained = nil
		}
		s.mu.Unlock()
	})
}

func (c *observedConn) finishReadTransaction() {
	c.readerTx.close()
	c.readerTx = nil
}

// ReaderLifetimes includes implicit rows from direct database/sql calls as well
// as every explicit transaction not declared a gated writer. The live map grows
// only with active readers, and is removed when the last connection closes.
func ReaderLifetimes() ReaderLifetimeStats {
	readerScopes.Lock()
	defer readerScopes.Unlock()
	out := ReaderLifetimeStats{}
	for _, s := range readerScopes.byName {
		mergeReaderStats(&out, s.stats())
	}
	return out
}

func (s *readerScope) stats() ReaderLifetimeStats {
	if s == nil {
		return ReaderLifetimeStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ReaderLifetimeStats{AdmissionPaused: s.resume != nil, AdmissionWaiting: s.waiting, AdmissionWaits: s.waits, AdmissionWaitMs: s.waitNanos / int64(time.Millisecond), AdmissionRefused: s.refused, MaxSnapshotMs: s.maxSnapshot / int64(time.Millisecond), MaxImplicitMs: s.maxImplicit / int64(time.Millisecond)}
	out.OverLifetimeForeground, out.OverLifetimeBackground = ReadersOverLifetime()
	now := time.Now()
	for lease := range s.active {
		age := now.Sub(lease.start).Milliseconds()
		if lease.snapshot {
			out.Snapshots++
			if out.OldestSnapshotClass == "" || age > out.OldestSnapshotMs {
				out.OldestSnapshotMs, out.OldestSnapshotClass = age, lease.class.String()
			}
		} else {
			out.ImplicitRows++
			if out.OldestImplicitClass == "" || age > out.OldestImplicitMs {
				out.OldestImplicitMs, out.OldestImplicitClass = age, lease.class.String()
			}
		}
	}
	out.MaxSnapshotMs = max(out.MaxSnapshotMs, out.OldestSnapshotMs)
	out.MaxImplicitMs = max(out.MaxImplicitMs, out.OldestImplicitMs)
	return out
}

func mergeReaderStats(out *ReaderLifetimeStats, next ReaderLifetimeStats) {
	out.Snapshots += next.Snapshots
	out.ImplicitRows += next.ImplicitRows
	if out.OldestSnapshotClass == "" || next.OldestSnapshotMs > out.OldestSnapshotMs {
		out.OldestSnapshotMs, out.OldestSnapshotClass = next.OldestSnapshotMs, next.OldestSnapshotClass
	}
	if out.OldestImplicitClass == "" || next.OldestImplicitMs > out.OldestImplicitMs {
		out.OldestImplicitMs, out.OldestImplicitClass = next.OldestImplicitMs, next.OldestImplicitClass
	}
	out.MaxSnapshotMs = max(out.MaxSnapshotMs, next.MaxSnapshotMs)
	out.MaxImplicitMs = max(out.MaxImplicitMs, next.MaxImplicitMs)
	out.AdmissionWaiting += next.AdmissionWaiting
	out.AdmissionPaused = out.AdmissionPaused || next.AdmissionPaused
	out.AdmissionWaits += next.AdmissionWaits
	out.AdmissionWaitMs += next.AdmissionWaitMs
	out.AdmissionRefused += next.AdmissionRefused
	out.OverLifetimeForeground, out.OverLifetimeBackground = next.OverLifetimeForeground, next.OverLifetimeBackground
}
