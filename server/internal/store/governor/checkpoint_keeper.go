package governor

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var ErrCheckpointKeeper = errors.New("checkpoint physical keeper unavailable")

const (
	keeperReady uint32 = 1 << iota
	keeperBroken
	keeperStopping
	keeperClosed
	keeperBound
)

type keeperRole uint8

const (
	keeperLifetime keeperRole = iota + 1
	keeperStartup
	keeperShutdown
)

// KeeperResources are existing SAME-ledger holdings supplied only by trusted
// composition. Lifetime funds the resident connection/cache/native workspace;
// Startup separately funds its actual opening/assertion IO/CPU. Neither is a
// new issuer or an inferred qualified profile. Keep never refunds either.
type KeeperResources struct {
	Lifetime, Startup               *Grant
	LifetimeRequest, StartupRequest Request
}

// CheckpointKeeper retains the checkpointer's ONE actual physical connection,
// idle without Rows/Tx, throughout writer service. No DB/Conn escapes. The
// lifetime holding is not retireable until all native closure is proven.
type CheckpointKeeper struct {
	cp               *Checkpointer
	resources        KeeperResources
	native           *snapshotOwnedConn // immutable after ready; CP.mu owns SQL
	device, inode    uint64
	flags            atomic.Uint32
	startupReturned  atomic.Bool
	shutdownDone     atomic.Bool
	service          atomic.Pointer[CheckpointWriterService]
	shutdownGrant    *Grant
	shutdownExpected Request
	shutdownStarted  bool // CP.mu; retry completes the SAME still-charged cleanup
}

// CheckpointWriterService is a trusted composition lifetime pin, NOT a grant.
// W stops ingress, closes/joins all of its physical connections, then Retired.
// Workers never receive this capability or the keeper itself.
type CheckpointWriterService struct {
	keeper  *CheckpointKeeper
	stopped atomic.Bool
	retired atomic.Bool
}

func validKeeperBudget(r Request, lifetime bool, cacheKiB int32) bool {
	if r.Writer || r.Class != Recovery || r.Need[WALBytes] != 0 || r.Need[MemoryBytes] <= 0 {
		return false
	}
	if lifetime {
		return r.Need[Reader] == 1 && r.Need[MemoryBytes] >= int64(cacheKiB)*1024 &&
			r.Need[CPUMilli] == 0 && r.Need[IOBytes] == 0 && len(r.IO) == 0
	}
	return r.Need[Reader] == 0 && r.Need[CPUMilli] > 0 && r.Need[IOBytes] > 0
}

// Keep is startup work outside any writer command. A nonnil keeper on error is
// a cleanup capability only: retain BOTH holdings and call Close until physical
// disposition is known. Startup/error/panic never silently reopens a connection.
func (c *Checkpointer) Keep(ctx context.Context, resources KeeperResources) (out *CheckpointKeeper, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.keeper != nil || c.wal.policy.HardBytes > math.MaxInt64/2 || resources.Lifetime == resources.Startup ||
		!validKeeperBudget(resources.LifetimeRequest, true, c.config.CacheKiB) ||
		!validKeeperBudget(resources.StartupRequest, false, c.config.CacheKiB) ||
		resources.StartupRequest.Need[IOBytes] < 2*c.wal.policy.HardBytes {
		return nil, ErrCheckpointKeeper
	}
	if err = errors.Join(c.authority.Check(resources.Lifetime, resources.LifetimeRequest), c.authority.Check(resources.Startup, resources.StartupRequest), ctx.Err()); err != nil {
		return nil, err
	}
	k := &CheckpointKeeper{cp: c, resources: resources}
	if err = k.bindHoldings(); err != nil {
		return nil, err
	}
	c.keeper, out = k, k
	ready := false
	defer func() {
		primary := recover()
		if !ready {
			k.fail()
			// Still under the original startup holding, with no writer service.
			cleanup := c.closeLocked()
			err = errors.Join(err, cleanup)
			k.closedIfSafe(cleanup)
		}
		if primary != nil {
			panic(primary)
		}
	}()
	state, err := ReadWAL(c.policy.DatabasePath)
	if err != nil || state.PhysicalBytes > c.wal.policy.HardBytes {
		return out, errors.Join(ErrWALObservation, err)
	}
	conn, err := c.pool.Conn(ctx)
	if err != nil {
		return out, err
	}
	returned := false
	defer func() {
		if !returned {
			if closed := conn.Close(); !errors.Is(closed, sql.ErrConnDone) {
				err = errors.Join(err, closed)
			}
		}
	}()
	if err = conn.Raw(func(raw any) error {
		owned, ok := raw.(*snapshotOwnedConn)
		if !ok {
			return sqliteconn.ErrPolicy
		}
		k.native = owned
		owned.keeper = k
		return nil
	}); err != nil {
		return out, err
	}
	if _, err = sqliteconn.Assert(ctx, conn, c.config); err != nil {
		return out, err
	}
	k.device, k.inode, err = k.native.SQLiteMainFileIdentity()
	if err != nil || k.inode == 0 {
		return out, errors.Join(ErrCheckpointKeeper, err)
	}
	info, err := os.Lstat(c.policy.DatabasePath)
	if err != nil {
		return out, err
	}
	device, inode, err := stateownership.SingleLinkedFileIdentity(info)
	if err != nil || device != k.device || inode != k.inode {
		return out, errors.Join(ErrComposition, err)
	}
	if err = errors.Join(c.authority.Check(resources.Lifetime, resources.LifetimeRequest), c.authority.Check(resources.Startup, resources.StartupRequest), ctx.Err()); err != nil {
		return out, err
	}
	if err = conn.Close(); err != nil || k.flags.Load()&keeperBroken != 0 {
		return out, errors.Join(ErrCheckpointKeeper, err)
	}
	returned = true
	if !k.flags.CompareAndSwap(0, keeperReady) {
		return out, ErrCheckpointKeeper
	}
	k.startupReturned.Store(true)
	ready = true
	return out, nil
}

func (k *CheckpointKeeper) bindHoldings() error {
	g, w := k.cp.authority.ledger, k.cp.wal
	g.mu.Lock()
	defer g.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if g.config.MaxGrantsPerClass < 4 || w.writer || w.checkpoint || (w.keeper != nil && w.keeper.flags.Load()&keeperClosed == 0) {
		return ErrBusy
	}
	for _, binding := range []struct {
		p    *Grant
		r    Request
		role keeperRole
	}{{k.resources.Lifetime, k.resources.LifetimeRequest, keeperLifetime}, {k.resources.Startup, k.resources.StartupRequest, keeperStartup}} {
		h := g.find(binding.p)
		if h == nil || h.keeper != nil || binding.p.cancelled.Load() || binding.p.epoch != g.state.Load() ||
			!h.request.Deadline.After(g.config.Now()) || !sameRequest(h.request, binding.r) {
			return ErrGrant
		}
	}
	for resource, n := range k.resources.StartupRequest.Need {
		available := g.config.Partitions[Recovery][resource] - k.resources.LifetimeRequest.Need[resource]
		if n > available/2 {
			return ErrCheckpointBudget // startup/CP plus a nonborrowable shutdown floor
		}
	}
	g.find(k.resources.Lifetime).keeper, g.find(k.resources.Lifetime).keeperRole = k, keeperLifetime
	g.find(k.resources.Startup).keeper, g.find(k.resources.Startup).keeperRole = k, keeperStartup
	k.resources.LifetimeRequest = g.find(k.resources.Lifetime).request
	k.resources.StartupRequest = g.find(k.resources.Startup).request
	w.keeper = k // sticky: writers refuse while opening, broken, stopping, closed
	g.keeper = k
	return nil
}

func (k *CheckpointKeeper) fail() {
	k.flags.Or(keeperBroken)
	k.cp.authority.EnterStatic()
}

// Called BEFORE any physical close, including database/sql's bad-conn path.
// The owner retains raw retry custody if a writer is still physically active.
func (k *CheckpointKeeper) beforeNativeClose() error {
	w := k.cp.wal
	w.mu.Lock()
	defer w.mu.Unlock()
	if k.flags.Load()&keeperStopping == 0 {
		k.fail()
	}
	service := k.service.Load()
	if w.writer || (service != nil && !service.retired.Load()) {
		return ErrBusy
	}
	return nil
}

// Caller holds ledger.mu and WALGate.mu. No I/O, callback or second authority.
func (k *CheckpointKeeper) admits(g *ledger, now time.Time) bool {
	p := k.resources.Lifetime
	return k.cp.authority.ledger == g && k.active(now) && g.find(p) != nil
}

func (k *CheckpointKeeper) active(now time.Time) bool {
	f := k.flags.Load()
	p := k.resources.Lifetime
	service := k.service.Load()
	return f == keeperReady|keeperBound && service != nil &&
		!service.stopped.Load() && !service.retired.Load() &&
		!p.cancelled.Load() && p.epoch == k.cp.authority.ledger.state.Load() && k.resources.LifetimeRequest.Deadline.After(now)
}

func (k *CheckpointKeeper) BindWriter(g Governor, a *Authority, wal *WALGate, device, inode uint64) (*CheckpointWriterService, error) {
	if k == nil || k.cp == nil || a != k.cp.authority || a.VerifyComposition(g, wal) != nil ||
		device != k.device || inode == 0 || inode != k.inode {
		return nil, ErrComposition
	}
	k.cp.mu.Lock()
	defer k.cp.mu.Unlock()
	if k.flags.Load() != keeperReady || k.service.Load() != nil || a.Check(k.resources.Lifetime, k.resources.LifetimeRequest) != nil {
		return nil, ErrCheckpointKeeper
	}
	s := &CheckpointWriterService{keeper: k}
	k.service.Store(s)
	k.flags.Or(keeperBound)
	return s, nil
}

func (s *CheckpointWriterService) Check() error {
	if s == nil || s.keeper == nil || s.keeper.service.Load() != s || s.stopped.Load() || s.retired.Load() ||
		s.keeper.flags.Load() != keeperReady|keeperBound {
		return ErrCheckpointKeeper
	}
	err := s.keeper.cp.authority.Check(s.keeper.resources.Lifetime, s.keeper.resources.LifetimeRequest)
	if err != nil && !errors.Is(err, ErrBusy) {
		s.keeper.fail()
	}
	return err
}

// KeeperClosed is physical closure proof for the file/ownership lifetime owner.
// Service retirement alone must not release the process's database file lease.
func (s *CheckpointWriterService) KeeperClosed() bool {
	return s != nil && s.keeper != nil && s.keeper.service.Load() == s &&
		s.keeper.flags.Load()&keeperClosed != 0
}

// Stop revokes new ingress, never refunds old work or closes native SQLite.
func (s *CheckpointWriterService) Stop() {
	if s != nil {
		s.stopped.Store(true)
	}
}

// Retired is a trusted physical W-owner attestation, analogous to Authority's
// Retired: call ONLY after all W native connections and closing work joined.
func (s *CheckpointWriterService) Retired() error {
	if s == nil || s.keeper == nil || s.keeper.service.Load() != s || !s.stopped.Load() {
		return ErrCheckpointKeeper
	}
	w := s.keeper.cp.wal
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writer {
		return ErrBusy
	}
	s.retired.Store(true)
	return nil
}

func (k *CheckpointKeeper) canRetire(role keeperRole) bool {
	switch role {
	case keeperLifetime:
		return k.flags.Load()&keeperClosed != 0
	case keeperStartup:
		return k.startupReturned.Load()
	case keeperShutdown:
		return k.shutdownDone.Load()
	}
	return false
}

func (k *CheckpointKeeper) closedIfSafe(err error) {
	if err == nil && k.cp.pool.Stats().OpenConnections == 0 {
		k.flags.Or(keeperClosed)
		k.startupReturned.Store(true)
		k.shutdownDone.Store(true)
	}
}

// Close charges final last-close to an explicit shutdown/recovery IO holding,
// after W's service pin is retired. On uncertain disposal retain all holdings;
// a later cleanup retry uses the SAME still-charged shutdown grant.
func (k *CheckpointKeeper) Close(grant *Grant, expected Request) error {
	if k == nil || k.cp == nil || !validKeeperBudget(expected, false, k.cp.config.CacheKiB) ||
		expected.Need[IOBytes] < 2*k.cp.wal.policy.HardBytes {
		return ErrCheckpointKeeper
	}
	c := k.cp
	c.mu.Lock()
	defer c.mu.Unlock()
	if service := k.service.Load(); service != nil && !service.retired.Load() {
		return ErrBusy
	}
	if k.flags.Load()&keeperClosed != 0 {
		return nil
	}
	if grant != k.shutdownGrant || !sameRequest(expected, k.shutdownExpected) {
		return ErrGrant
	}
	if !k.shutdownStarted {
		if err := c.authority.Check(grant, expected); err != nil {
			return err
		}
		k.shutdownStarted = true
	}
	k.flags.Or(keeperStopping)
	err := c.closeLocked()
	k.closedIfSafe(err)
	return err
}

func (k *CheckpointKeeper) shutdownShape(r Request) bool {
	return r.Class == Recovery && !r.Writer && r.Need == k.resources.StartupRequest.Need &&
		slices.Equal(r.IO, k.resources.StartupRequest.IO)
}

// AdmitShutdown uses the SAME ordinary ledger/issuer. Only this keeper's typed
// cleanup capability can spend the reserved floor; workers cannot select it.
func (k *CheckpointKeeper) AdmitShutdown(r Request) Decision {
	refuse := func() Decision { return Decision{Refusal: &Refusal{Code: Capacity}} }
	if k == nil || k.cp == nil || !k.cp.mu.TryLock() {
		return refuse()
	}
	defer k.cp.mu.Unlock()
	if k.shutdownGrant != nil || k.flags.Load()&keeperClosed != 0 {
		return refuse()
	}
	if service := k.service.Load(); service != nil && !service.retired.Load() {
		return refuse()
	}
	d := k.cp.authority.ledger.tryReserved(r, false, nil, nil, k, nil)
	if d.Grant != nil {
		k.shutdownGrant = d.Grant
		r.IO = slices.Clone(r.IO)
		r.Footprint = slices.Clone(r.Footprint)
		k.shutdownExpected = r
	}
	return d
}
