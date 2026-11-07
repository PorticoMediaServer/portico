package governor

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
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
	Recovery                                                         bool   // exceptional exclusive TRUNCATE, never the normal PASSIVE path
	BeforeUnavailable                                                string // nonempty means Before is unavailable, not a zero-sized WAL
	SampleUnavailable                                                string
	RecoveryDrainStarted, RecoveryDrainEnded                         time.Time
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
	mu                  ledgerMutex
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
	readWAL             func(context.Context, string) (WALState, error)
	deadlineContext     func(context.Context, time.Time) (context.Context, context.CancelFunc)
	pending             *ReadScope // shared reviewed physical cleanup protocol, no read callback
	keeper              *CheckpointKeeper
}

func NewCheckpointer(connector driver.Connector, cfg sqliteconn.Config, g Governor, a *Authority, policy CheckpointPolicy) (*Checkpointer, error) {
	if connector == nil || a == nil || a.ledger == nil ||
		a.VerifyComposition(g, a.ledger.config.WAL) != nil || cfg.Role != sqliteconn.Checkpointer || cfg.Connections != 1 ||
		cfg.CacheKiB <= 0 || cfg.Synchronous != sqliteconn.Normal || cfg.ReadLimits != (sqliteconn.ReadLimits{}) ||
		!filepath.IsAbs(policy.DatabasePath) || strings.ContainsRune(policy.DatabasePath, 0) ||
		policy.Interval < 50*time.Millisecond || policy.MaxHold <= 0 || policy.MaxHold > a.ledger.maxHold(true) || policy.MaxWait < 50*time.Millisecond || policy.MaxWait > 75*time.Millisecond {
		return nil, sqliteconn.ErrPolicy
	}
	owner := &snapshotOwnedConnector{Connector: connector, failed: make(map[*snapshotOwnedConn]error)}
	return &Checkpointer{pool: newSnapshotPool(owner, 1), owner: owner, config: cfg, policy: policy, governor: g, authority: a, wal: a.ledger.config.WAL, deadlineContext: context.WithDeadline, readWAL: ReadWALContext}, nil
}

func (c *Checkpointer) Step(parent context.Context, r Request) (CheckpointResult, error) {
	return c.step(parent, r, false)
}

// RecoverWAL is recovery-only TRUNCATE, never a synthetic writer or a normal
// checkpoint mode. It drains physical reader/writer holdings under a bounded
// admission barrier and requires actual hard-pressure state before native entry.
func (c *Checkpointer) RecoverWAL(parent context.Context, r Request) (CheckpointResult, error) {
	return c.step(parent, r, true)
}

func (c *Checkpointer) step(parent context.Context, r Request, recovery bool) (out CheckpointResult, err error) {
	out.Recovery = recovery
	out.Key = r.Key
	out.Budget = r.Budget
	out.MaxHold = r.MaxHold
	out.IOBytes = r.Need[IOBytes]
	out.PrefixLimit = c.wal.policy.HardBytes
	if err := c.mu.lockUntil(parent, r.Deadline, c.authority.ledger.config.Now()); err != nil {
		return out, admissionError(CauseDeadline, err)
	}
	defer c.mu.Unlock()
	if c.wal.policy.ConcurrentPassive && c.keeper == nil {
		return out, ErrCheckpointKeeper
	}
	if c.closed {
		return out, ErrCheckpointClosed
	}
	if c.keeper != nil {
		if c.keeper.flags.Load()&(keeperBroken|keeperStopping|keeperClosed) != 0 {
			return out, ErrCheckpointKeeper
		}
		if err := c.authority.checkContextUntil(parent, c.keeper.resources.Lifetime, c.keeper.resources.LifetimeRequest, time.Time{}, r.Deadline); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				c.keeper.fail()
			}
			return out, err
		}
	}
	if c.pending != nil {
		return out, ErrCheckpointUncertain
	}
	if c.unqualified {
		return out, ErrCheckpointOverrun
	}
	if !validRequest(r) || r.Writer || r.Class != Recovery || r.MaxHold <= 0 || r.Need[Reader] != 1 {
		return out, ErrGrant
	}
	if r.MaxHold > c.policy.MaxHold {
		return out, ErrCheckpointBudget
	}
	out.Requested = c.authority.ledger.config.Now()
	out.EligibleInterval = checkpointSpacing(c.policy)
	if c.wal.policy.ConcurrentPassive {
		out.EligibleInterval = c.policy.Interval
	}
	if recovery {
		if !c.wal.policy.ConcurrentPassive {
			return out, ErrCheckpointBudget
		}
		out.RecoveryDrainStarted = c.authority.ledger.config.Now()
		release, beginErr := c.beginRecovery(parent, r.Deadline)
		out.RecoveryDrainEnded = c.authority.ledger.config.Now()
		if release != nil {
			defer release()
		}
		if beginErr != nil {
			return out, beginErr
		}
	}
	until := minTime(c.authority.ledger.config.Now().Add(c.policy.MaxWait), r.Deadline)
	if parentDeadline, ok := parent.Deadline(); ok {
		until = minTime(until, parentDeadline)
	}
	ticket, ticketErr := c.authority.queueCheckpointContext(parent, until, out.EligibleInterval, recovery)
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
	// Own the admitted unit BEFORE any fallible context/clock/driver call.
	ctx := context.Background()
	var stop context.CancelFunc = func() {}
	defer func() { stop() }()
	scope := &ReadScope{ctx: ctx, cancel: func() {}, authority: c.authority, grant: d.Grant, expected: r, config: c.config}
	var primary any
	var physicallySafe bool
	c.pending = scope
	defer func() {
		if p := recover(); p != nil {
			c.authority.EnterStatic()
			if c.keeper != nil {
				c.keeper.fail()
			}
			if scope.conn == nil || physicallySafe {
				scope.nativeSafe.Store(true)
				scope.consumerDone.Store(true)
				if scope.retire() == nil {
					c.pending = nil
				}
			} else if scope.owned != nil {
				scope.failedRaw = scope.owned
			}
			if primary != nil {
				panic(primary)
			}
			panic(p)
		}
	}()
	scope.cleanupContext = func() (context.Context, context.CancelFunc) {
		return c.deadlineContext(context.Background(), c.authority.ledger.config.Now().Add(25*time.Millisecond))
	}
	defer func() {
		original := recover()
		primary = original
		c.governor.Cancel(d.Grant)
		cleanup, safe := scope.closeNative(original != nil || (!c.wal.policy.ConcurrentPassive && (err != nil || ctx.Err() != nil)))
		physicallySafe = safe
		out.PhysicalReturned = c.authority.ledger.config.Now()
		out.PhysicalSafe = safe
		err = errors.Join(err, cleanup, ctx.Err())
		scope.nativeSafe.Store(safe)
		scope.consumerDone.Store(true)
		if out.PhysicalReturned.Sub(out.Started) > r.MaxHold {
			c.unqualified = true
			if c.keeper != nil {
				c.keeper.fail()
			}
			c.authority.EnterStatic()
			err = errors.Join(err, ErrCheckpointOverrun)
		}
		if safe {
			retirement := scope.retire()
			err = errors.Join(err, retirement)
			if retirement != nil {
				c.authority.EnterStatic()
				c.pending = scope
				if c.keeper != nil {
					c.keeper.fail()
				}
			} else {
				c.pending = nil
				out.RetiredAt = c.authority.ledger.config.Now()
				// A separate composition observation budget covers this work, as for W.
				// Old headroom remains invalid on failure/staleness. Sampling is not hidden
				// inside the native CP interval or charged to a grant already retired.
				if out.DatabaseMatched {
					out.SampleStarted = c.authority.ledger.config.Now()
					token := c.wal.Observation()
					after, sampleErr := c.readWAL(parent, c.policy.DatabasePath)
					if sampleErr != nil {
						after = WALState{}
					}
					var publishErr error
					if !c.wal.policy.ConcurrentPassive || recovery {
						publishErr = c.wal.Publish(token, after, c.authority.ledger.config.Now())
					}
					out.After = after
					out.SampleEnded = c.authority.ledger.config.Now()
					if c.wal.policy.ConcurrentPassive && !recovery {
						// Only the serialized writer publishes its anchor. CP observations
						// are telemetry and cannot replace a ready writer's exact risk.
						out.SampleUnavailable = "checkpoint telemetry only; writer owns admission anchor"
						if sampleErr != nil && !c.wal.observationCurrent(token) {
							out.SampleUnavailable = "concurrent writer generation; checkpoint sample unavailable"
						} else if sampleErr != nil {
							err = errors.Join(err, sampleErr)
							if !errors.Is(sampleErr, context.Canceled) && !errors.Is(sampleErr, context.DeadlineExceeded) {
								_ = c.wal.Publish(token, WALState{}, time.Time{})
								c.authority.EnterStatic()
							}
						}
					} else {
						err = errors.Join(err, sampleErr, publishErr)
					}

					if recovery && err == nil && !out.NativeStarted.IsZero() && (sampleErr != nil || publishErr != nil || after.ActiveBytes != 0 || after.PhysicalBytes != 0) {
						c.authority.EnterStatic()
						if c.keeper != nil {
							c.keeper.fail()
						}
						err = errors.Join(err, ErrWALObservation)
					}
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
	deadline := minTime(out.Started.Add(r.MaxHold), r.Deadline)
	ctx, stop = c.deadlineContext(parent, deadline)
	queries, cancel := context.WithCancel(ctx)
	scope.ctx, scope.cancel = queries, cancel
	if err = c.authority.CheckContext(queries, d.Grant, r); err != nil {
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
		if c.keeper != nil && c.keeper.native != owned {
			c.keeper.fail()
			return ErrCheckpointKeeper
		}
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
	if c.keeper != nil {
		device, inode, identityErr := scope.owned.SQLiteMainFileIdentity()
		expectedDevice, expectedInode, fileErr := stateownership.SingleLinkedFileIdentity(expected)
		if identityErr != nil || fileErr != nil || device != expectedDevice || inode != expectedInode || device != c.keeper.device || inode != c.keeper.inode {
			c.keeper.fail()
			return out, errors.Join(ErrCheckpointKeeper, identityErr, fileErr)
		}
	}
	out.DatabaseMatched = true
	if err = c.authority.CheckContext(queries, d.Grant, r); err != nil {
		return out, err
	}
	beforeToken := c.wal.Observation()
	pageSize := uint32(0)
	if out.Before, err = c.readWAL(queries, c.policy.DatabasePath); err != nil {
		// Concurrent observations are not native authority. A known writer race
		// can use the previously validated page size for the WHOLE-hard-prefix
		// forecast, without inventing current frames/backfill or append credit.
		var raced bool
		if c.wal.policy.ConcurrentPassive && !recovery && errors.Is(err, ErrWALObservation) {
			pageSize, raced = c.wal.checkpointPageSize(beforeToken)
		}
		if !raced {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				c.authority.EnterStatic()
				_ = c.wal.Publish(beforeToken, WALState{}, time.Time{})
			}
			return out, err
		}
		out.BeforeUnavailable = "concurrent writer generation after bounded WAL resampling; whole-hard-prefix forecast"
		err = nil
	} else {
		pageSize = out.Before.PageSize
	}
	// Serialized mode stabilizes input. Concurrent mode reserves the entire hard
	// prefix because a writer can append before SQLite captures its CP snapshot.
	prefix, frames := out.Before.ActiveBytes, uint64(out.Before.Frames)
	if c.wal.policy.ConcurrentPassive {
		prefix = out.PrefixLimit // W may append until native CP takes its snapshot
		frames = uint64((prefix - 32) / (int64(pageSize) + 24))
	}
	if recovery && !c.wal.recoveryPressure(out.Before) {
		return out, ErrCheckpointBudget
	}
	if prefix > out.PrefixLimit || prefix > r.Need[IOBytes]/2 ||
		2*frames+1 > uint64(r.Budget.Pages) || c.sequence == math.MaxUint64 {
		return out, ErrCheckpointBudget
	}
	if err = c.authority.CheckContext(queries, d.Grant, r); err != nil {
		return out, err
	}
	c.sequence++
	out.Sequence = c.sequence
	out.NativeStarted = c.authority.ledger.config.Now()
	c.lastStart = out.NativeStarted
	if recovery {
		err = scope.conn.QueryRowContext(queries, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&out.Busy, &out.Frames, &out.Backfilled)
	} else {
		err = scope.conn.QueryRowContext(queries, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&out.Busy, &out.Frames, &out.Backfilled)
	}
	out.NativeEnded = c.authority.ledger.config.Now()
	if err == nil && (out.Busy < 0 || out.Busy > 1 || out.Frames < 0 || out.Backfilled < 0 || out.Backfilled > out.Frames) {
		err = ErrWALObservation
	}
	if recovery && err == nil && (out.Busy != 0 || out.Frames != 0 || out.Backfilled != 0) {
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
	if c.keeper != nil && c.keeper.flags.Load()&keeperClosed == 0 && c.keeper.startupReturned.Load() {
		return ErrCheckpointKeeper // use keeper.Close with shutdown IO, after W drain
	}
	err := c.closeLocked()
	if c.keeper != nil {
		c.keeper.closedIfSafe(err)
	}
	return err
}

func (c *Checkpointer) closeLocked() error {
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
