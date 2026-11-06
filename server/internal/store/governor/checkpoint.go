package governor

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var (
	ErrCheckpointClosed    = errors.New("checkpoint driver closed")
	ErrCheckpointCadence   = errors.New("checkpoint minimum start spacing")
	ErrCheckpointBudget    = errors.New("checkpoint whole-prefix forecast exceeded")
	ErrCheckpointOverrun   = errors.New("checkpoint physical holding exceeded forecast")
	ErrCheckpointUncertain = errors.New("checkpoint physical disposal remains unproven")
)

type CheckpointPolicy struct {
	DatabasePath string
	Interval     time.Duration // timer and pressure calls share this >=50ms spacing
	MaxHold      time.Duration // qualification forecast, not native preemption
	MaxWait      time.Duration // bounded metadata wait incl <=25ms writer + dispatch guard
}

type CheckpointResult struct {
	Requested                                                        time.Time
	EligibleInterval                                                 time.Duration
	Sequence                                                         uint64
	Key                                                              JobKey
	Budget                                                           UnitBudget
	MaxHold                                                          time.Duration
	IOBytes, PrefixLimit                                             int64
	Started, NativeStarted, NativeEnded, PhysicalReturned, RetiredAt time.Time
	SampleStarted, SampleEnded                                       time.Time // composition-owned sampling, AFTER retirement
	Busy, Frames, Backfilled                                         int
	Before, After                                                    WALState
	Settings                                                         sqliteconn.Settings
	Refusal                                                          *Refusal
	DatabaseMatched                                                  bool
	PhysicalSafe                                                     bool
}

// Checkpointer owns a fixed one-connection shared-helper pool. Composition keeps
// startup/cache/native/idle and post-return observer custody until Close succeeds.
// No worker SQL, raw DB, timer goroutine or public retirement capability escapes.
// SQLite PASSIVE cannot be page-sliced or preempted: qualify the entire WAL hard
// prefix together with MaxHold. A physical overrun fences, never refunds early.
type Checkpointer struct {
	mu                  sync.Mutex
	pool                snapshotPool
	owner               *snapshotOwnedConnector
	config              sqliteconn.Config
	policy              CheckpointPolicy
	governor            Governor
	authority           *Authority
	wal                 *WALGate
	lastStart           time.Time
	sequence            uint64
	closed, unqualified bool
	deadlineContext     func(context.Context, time.Time) (context.Context, context.CancelFunc)
	pending             *ReadScope // shared reviewed physical cleanup protocol, no read callback
}

func NewCheckpointer(connector driver.Connector, cfg sqliteconn.Config, g Governor, a *Authority, policy CheckpointPolicy) (*Checkpointer, error) {
	if connector == nil || a == nil || a.ledger == nil ||
		a.VerifyComposition(g, a.ledger.config.WAL) != nil || cfg.Role != sqliteconn.Checkpointer || cfg.Connections != 1 ||
		cfg.CacheKiB <= 0 || cfg.Synchronous != sqliteconn.Normal || cfg.ReadLimits != (sqliteconn.ReadLimits{}) ||
		!filepath.IsAbs(policy.DatabasePath) || strings.ContainsRune(policy.DatabasePath, 0) ||
		policy.Interval < 50*time.Millisecond || policy.MaxHold <= 0 || policy.MaxHold > 25*time.Millisecond || policy.MaxWait < 50*time.Millisecond || policy.MaxWait > 75*time.Millisecond {
		return nil, sqliteconn.ErrPolicy
	}
	owner := &snapshotOwnedConnector{Connector: connector, failed: make(map[*snapshotOwnedConn]error)}
	return &Checkpointer{pool: newSnapshotPool(owner, 1), owner: owner, config: cfg, policy: policy, governor: g, authority: a, wal: a.ledger.config.WAL, deadlineContext: context.WithDeadline}, nil
}

func (c *Checkpointer) Step(parent context.Context, r Request) (out CheckpointResult, err error) {
	out.Key = r.Key
	out.Budget = r.Budget
	out.MaxHold = r.MaxHold
	out.IOBytes = r.Need[IOBytes]
	out.PrefixLimit = c.wal.policy.HardBytes
	if !c.mu.TryLock() {
		return out, ErrBusy
	}
	defer c.mu.Unlock()
	if c.closed {
		return out, ErrCheckpointClosed
	}
	if c.pending != nil {
		return out, ErrCheckpointUncertain
	}
	if c.unqualified {
		return out, ErrCheckpointOverrun
	}
	if r.MaxHold > c.policy.MaxHold {
		return out, ErrCheckpointBudget
	}
	out.Requested = c.authority.ledger.config.Now()
	out.EligibleInterval = checkpointSpacing(c.policy)
	until := minTime(out.Requested.Add(c.policy.MaxWait), r.Deadline)
	if parentDeadline, ok := parent.Deadline(); ok {
		until = minTime(until, parentDeadline)
	}
	ticket, ticketErr := c.authority.queueCheckpoint(until, out.EligibleInterval)
	if ticketErr != nil {
		return out, ticketErr
	}
	defer c.authority.cancelCheckpointTicket(ticket)
	wait, stopWait := c.deadlineContext(parent, until)
	defer stopWait()
	select {
	case <-ticket.ready:
	case <-wait.Done():
		return out, wait.Err()
	}
	if err = wait.Err(); err != nil {
		return out, err
	}
	turnStart := c.authority.ledger.config.Now()
	d := c.authority.tryCheckpointTurn(r, ticket)
	if d.Refusal != nil {
		out.Refusal = d.Refusal
		return out, nil
	}
	out.Started = turnStart
	deadline := minTime(out.Started.Add(r.MaxHold), r.Deadline)
	ctx, stop := c.deadlineContext(parent, deadline)
	defer stop()
	queries, cancel := context.WithCancel(ctx)
	scope := &ReadScope{ctx: queries, cancel: cancel, authority: c.authority, grant: d.Grant, expected: r, config: c.config}
	scope.cleanupContext = func() (context.Context, context.CancelFunc) {
		return c.deadlineContext(context.Background(), c.authority.ledger.config.Now().Add(25*time.Millisecond))
	}
	defer func() {
		original := recover()
		c.governor.Cancel(d.Grant)
		cleanup, safe := scope.closeNative(err != nil || ctx.Err() != nil)
		out.PhysicalReturned = c.authority.ledger.config.Now()
		out.PhysicalSafe = safe
		err = errors.Join(err, cleanup, ctx.Err())
		scope.nativeSafe.Store(safe)
		scope.consumerDone.Store(true)
		if out.PhysicalReturned.Sub(out.Started) > r.MaxHold {
			c.unqualified = true
			c.authority.EnterStatic()
			err = errors.Join(err, ErrCheckpointOverrun)
		}
		if safe {
			retirement := scope.retire()
			err = errors.Join(err, retirement)
			if retirement != nil {
				c.authority.EnterStatic()
				c.pending = scope
			} else {
				out.RetiredAt = c.authority.ledger.config.Now()
				// A separate composition observation budget covers this work, as for W.
				// Old headroom remains invalid on failure/staleness. Sampling is not hidden
				// inside the native CP interval or charged to a grant already retired.
				if out.DatabaseMatched {
					out.SampleStarted = c.authority.ledger.config.Now()
					token := c.wal.Observation()
					after, sampleErr := ReadWAL(c.policy.DatabasePath)
					if sampleErr != nil {
						after = WALState{}
					}
					publishErr := c.wal.Publish(token, after, c.authority.ledger.config.Now())
					out.After = after
					out.SampleEnded = c.authority.ledger.config.Now()
					err = errors.Join(err, sampleErr, publishErr)
				}
			}
		} else {
			c.pending = scope
			c.authority.EnterStatic()
			err = errors.Join(err, ErrCheckpointUncertain)
		}
		if original != nil {
			panic(original)
		}
		if scope.owned != nil {
			if fault := scope.owned.panicError(); fault != nil {
				panic(fault.value)
			}
		}
	}()
	if err = c.authority.Check(d.Grant, r); err != nil {
		return out, err
	}
	if scope.conn, err = c.pool.Conn(queries); err != nil {
		return out, err
	}
	if err = scope.conn.Raw(func(raw any) error {
		owned, ok := raw.(*snapshotOwnedConn)
		if !ok {
			return sqliteconn.ErrPolicy
		}
		scope.owned = owned
		return nil
	}); err != nil {
		return out, err
	}
	if out.Settings, err = sqliteconn.Assert(queries, scope.conn, c.config); err != nil {
		return out, err
	}
	var sequence int
	var name, path string
	if err = scope.conn.QueryRowContext(queries, "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		return out, err
	}
	actual, statErr := os.Stat(path)
	if statErr != nil {
		return out, statErr
	}
	expected, statErr := os.Stat(c.policy.DatabasePath)
	if statErr != nil {
		return out, statErr
	}
	if sequence != 0 || name != "main" || !os.SameFile(actual, expected) {
		return out, sqliteconn.ErrPolicy
	}
	out.DatabaseMatched = true
	if err = c.authority.Check(d.Grant, r); err != nil {
		return out, err
	}
	if out.Before, err = ReadWAL(c.policy.DatabasePath); err != nil {
		return out, err
	}
	// Gate exclusion stabilizes the full native input. PrefixLimit is the WAL
	// hard cap itself: a smaller CP cap would strand otherwise admitted WAL.
	if out.Before.ActiveBytes > out.PrefixLimit || out.Before.ActiveBytes > r.Need[IOBytes]/2 ||
		2*uint64(out.Before.Frames)+1 > uint64(r.Budget.Pages) || c.sequence == math.MaxUint64 {
		return out, ErrCheckpointBudget
	}
	if err = c.authority.Check(d.Grant, r); err != nil {
		return out, err
	}
	c.sequence++
	out.Sequence = c.sequence
	out.NativeStarted = c.authority.ledger.config.Now()
	c.lastStart = out.NativeStarted
	err = scope.conn.QueryRowContext(queries, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&out.Busy, &out.Frames, &out.Backfilled)
	out.NativeEnded = c.authority.ledger.config.Now()
	if err == nil && (out.Busy < 0 || out.Busy > 1 || out.Frames < 0 || out.Backfilled < 0 || out.Backfilled > out.Frames) {
		err = ErrWALObservation
	}
	return out, err
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Close joins the physical step, retries owned disposal and closes the pool.
// Uncertain native state keeps the checkpoint grant/interlock and ALL custody.
func (c *Checkpointer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var err error
	if scope := c.pending; scope != nil {
		if !scope.nativeSafe.Load() {
			if scope.failedRaw == nil {
				return ErrCheckpointUncertain
			}
			err = scope.failedRaw.Close()
			if err == nil {
				scope.failedRaw = nil
				scope.nativeSafe.Store(true)
			}
		}
		if scope.nativeSafe.Load() {
			err = errors.Join(err, scope.retire())
			if err == nil {
				c.pending = nil
			}
		}
	}
	return errors.Join(err, c.pool.Close(), c.owner.retry())
}
