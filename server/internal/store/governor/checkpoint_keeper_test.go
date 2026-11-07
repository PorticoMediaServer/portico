package governor

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type keeperFixture struct {
	g         Governor
	a         *Authority
	clock     *testClock
	wal       *WALGate
	cp        *Checkpointer
	k         *CheckpointKeeper
	service   *CheckpointWriterService
	resources KeeperResources
	path      string
	writer    *sql.Conn
}

func newKeeperFixture(t *testing.T) *keeperFixture { return newKeeperFixtureMode(t, false) }
func newKeeperFixtureMode(t *testing.T, concurrent bool, faults ...*snapshotConnector) *keeperFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native file identity is explicitly unsupported on Windows")
	}
	path, writer := snapshotFixture(t)
	base, _, _ := nativeCheckpointGovernor(t)
	cfg := base.(*ledger).config
	for i := range cfg.Partitions {
		cfg.Partitions[i][MemoryBytes] = 16 << 20
		cfg.Partitions[i][IOBytes] = 8 << 20
		cfg.Partitions[i][CPUMilli] = 2000
		cfg.Partitions[i][Helper] = 1
	}
	if concurrent {
		p := cfg.WAL.policy
		p.ConcurrentPassive = true
		p.CheckpointMaxHold = 5 * time.Second
		cfg.WAL, _ = NewWALGate(p)
	}
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clock, _ := checkpointClocks.Load(base)
	f := &keeperFixture{g: g, a: a, clock: clock.(*testClock), wal: cfg.WAL, path: path, writer: writer}
	connection := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, connection)
	if err != nil {
		t.Fatal(err)
	}
	if len(faults) > 0 {
		faults[0].Connector = connector
		connector = faults[0]
	}
	f.cp, err = functionalCheckpointer(connector, connection, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: g.(*ledger).maxHold(true), MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	f.resources.LifetimeRequest = Request{Key: JobKey{Domain: 1, ID: "keeper-life"}, Owner: 1, Class: Recovery, Need: Amounts{Reader: 1, MemoryBytes: 4 << 20}, Deadline: cfg.Now().Add(time.Hour)}
	f.resources.StartupRequest = f.ioRequest("keeper-start")
	f.resources.Lifetime = grant(t, g, f.resources.LifetimeRequest)
	f.resources.Startup = grant(t, g, f.resources.StartupRequest)
	f.k, err = f.cp.Keep(t.Context(), f.resources)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Retired(f.resources.Startup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.service != nil {
			f.service.Stop()
			if err := f.service.Retired(); err != nil {
				t.Error(err)
			}
		}
		shutdown := f.ioRequest("keeper-shutdown")
		d := f.k.AdmitShutdown(shutdown)
		p := d.Grant
		if p == nil {
			t.Error("shutdown refused", d.Refusal)
			return
		}
		if err := f.k.Close(p, shutdown); err != nil {
			t.Error(err)
			return
		}
		if err := a.Retired(p); err != nil {
			t.Error(err)
		}
		if err := a.Retired(f.resources.Lifetime); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *keeperFixture) ioRequest(id string) Request {
	return Request{Key: JobKey{Domain: 1, ID: id}, Owner: 1, Class: Recovery,
		Need: Amounts{MemoryBytes: 64 << 10, CPUMilli: 1000, IOBytes: 2 * f.wal.policy.HardBytes}, Deadline: f.g.(*ledger).config.Now().Add(time.Hour)}
}

func (f *keeperFixture) bind(t *testing.T) {
	t.Helper()
	var err error
	f.service, err = f.k.BindWriter(f.g, f.a, f.wal, f.k.device, f.k.inode)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *keeperFixture) sample(t *testing.T) {
	t.Helper()
	token := f.wal.Observation()
	s, err := ReadWAL(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.wal.Publish(token, s, f.g.(*ledger).config.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestKeeperPhysicalLifetimeAndSerializedReuse(t *testing.T) {
	f := newKeeperFixture(t)
	if err := f.a.Retired(f.resources.Lifetime); err != ErrRetirement {
		t.Fatal("early lifetime refund", err)
	}
	f.sample(t)
	r := checkpointWriter(f.clock, Foreground)
	refused(t, f.g, r, FailSafe) // a physical connection alone isn't a writer-service bind
	if _, err := f.k.BindWriter(f.g, f.a, f.wal, f.k.device, f.k.inode+1); err == nil {
		t.Fatal("foreign physical file bound")
	}
	f.bind(t)
	if err := f.service.Check(); err != nil {
		t.Fatal(err)
	}
	before := f.k.native
	stats := f.cp.pool.Stats()
	if stats.OpenConnections != 1 || stats.Idle != 1 {
		t.Fatal("no idle physical keeper", stats)
	}
	writer := grant(t, f.g, r)
	decision := f.a.tryCheckpoint(nativeCheckpointRequest(f.g))
	if decision.Grant != nil || decision.Refusal == nil || decision.Refusal.Code != Capacity {
		t.Fatal("CP overlapped active writer", decision)
	}
	retire(t, f.a, writer)
	out, err := f.cp.Step(t.Context(), nativeCheckpointRequest(f.g))
	if err != nil || !out.PhysicalSafe || out.Refusal != nil || out.Frames != out.Backfilled {
		t.Fatal("real kept PASSIVE", out, err)
	}
	if f.k.native != before || before.closed || f.cp.pool.Stats().Idle != 1 {
		t.Fatal("keeper replaced/closed between steps")
	}
	if err = f.cp.Close(); err != ErrCheckpointKeeper {
		t.Fatal("unbudgeted keeper close", err)
	}
	shutdown := f.ioRequest("early-close")
	p := grant(t, f.g, shutdown)
	if err = f.k.Close(p, shutdown); err != ErrBusy {
		t.Fatal("keeper closed before W physical-drain attestation", err)
	}
	retire(t, f.a, p)
	if before.closed {
		t.Fatal("early close touched native")
	}
	f.service.Stop()
	f.sample(t)
	r = checkpointWriter(f.clock, Recovery)
	refused(t, f.g, r, FailSafe)
}

func TestKeeperUnknownCloseRetainsNativeAndFences(t *testing.T) {
	f := newKeeperFixture(t)
	f.bind(t)
	f.sample(t)
	p := grant(t, f.g, checkpointWriter(f.clock, Foreground))
	if err := f.k.native.Close(); err != ErrBusy {
		t.Fatal("unsafe close not refused", err)
	}
	if f.k.native.closed || len(f.cp.owner.failed) != 1 {
		t.Fatal("lost raw custody")
	}
	if f.service.Check() == nil {
		t.Fatal("unknown keeper allowed service")
	}
	if f.g.(*ledger).state.Load()&1 == 0 {
		t.Fatal("unknown keeper did not fence")
	}
	if f.a.Retired(f.resources.Lifetime) != ErrRetirement {
		t.Fatal("unknown keeper refunded")
	}
	retire(t, f.a, p)
	f.sample(t)
	refused(t, f.g, checkpointWriter(f.clock, Recovery), FailSafe)
	if _, err := f.cp.Step(t.Context(), nativeCheckpointRequest(f.g)); err != ErrCheckpointKeeper {
		t.Fatal("unknown keeper silently replaced", err)
	}
}

func TestKeeperLifetimeCancelAndForeignLedgerRefuse(t *testing.T) {
	f := newKeeperFixture(t)
	f.bind(t)
	f.sample(t)
	foreign, _, err := New(f.g.(*ledger).config)
	if err != nil {
		t.Fatal(err)
	}
	refused(t, foreign, checkpointWriter(f.clock, Recovery), FailSafe)
	f.g.Cancel(f.resources.Lifetime)
	refused(t, f.g, checkpointWriter(f.clock, Recovery), FailSafe)
	if f.service.Check() == nil || f.k.native.closed {
		t.Fatal("cancel refunded keeper or allowed service")
	}
	if f.a.Retired(f.resources.Lifetime) != ErrRetirement {
		t.Fatal("canceled keeper refunded")
	}
}

func TestKeeperFailedStartupKeepsCleanupCapability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native file identity unsupported")
	}
	path, _ := snapshotFixture(t)
	g, a, _ := nativeCheckpointGovernor(t)
	clock, _ := checkpointClocks.Load(g)
	cfg := g.(*ledger).config
	for i := range cfg.Partitions {
		cfg.Partitions[i][MemoryBytes] = 16 << 20
		cfg.Partitions[i][CPUMilli] = 2000
		cfg.Partitions[i][IOBytes] = 8 << 20
	}
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cc := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, cc)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &snapshotConnector{Connector: connector, mainIdentityError: errors.New("unknown native identity")}
	cp, err := functionalCheckpointer(wrapped, cc, g, a, CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 25 * time.Millisecond, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	f := &keeperFixture{g: g, a: a, clock: clock.(*testClock), wal: cfg.WAL}
	r := KeeperResources{LifetimeRequest: Request{Key: JobKey{Domain: 1, ID: "life"}, Owner: 1, Class: Recovery, Need: Amounts{Reader: 1, MemoryBytes: 4 << 20}, Deadline: cfg.Now().Add(time.Hour)}, StartupRequest: f.ioRequest("startup")}
	r.Lifetime, r.Startup = grant(t, g, r.LifetimeRequest), grant(t, g, r.StartupRequest)
	k, err := cp.Keep(context.Background(), r)
	if err == nil || k == nil || k.flags.Load()&keeperClosed == 0 || wrapped.opened.Load() != wrapped.closed.Load() {
		t.Fatal("failed startup lost physical cleanup", k, err)
	}
	if err := a.Retired(r.Startup); err != nil {
		t.Fatal(err)
	}
	if err := a.Retired(r.Lifetime); err != nil {
		t.Fatal(err)
	}
}

func TestKeeperShutdownFloorCannotBeSpentByOtherRecovery(t *testing.T) {
	for _, resource := range []Resource{CPUMilli, IOBytes} {
		t.Run(map[Resource]string{CPUMilli: "cpu", IOBytes: "io"}[resource], func(t *testing.T) {
			f := newKeeperFixture(t)
			f.bind(t)
			r := f.ioRequest("other-recovery")
			// This otherwise-valid work fits the whole Recovery partition, but would
			// consume terminal custody required to close the already resident keeper.
			r.Need[resource] = f.g.(*ledger).config.Partitions[Recovery][resource]
			decision := f.g.Try(r)
			if decision.Grant != nil {
				retire(t, f.a, decision.Grant)
				t.Fatal("other recovery spent reserved shutdown floor")
			}
			if decision.Refusal == nil || decision.Refusal.Code != Capacity {
				t.Fatal(decision)
			}
			// The ordinary refusal cannot damage actual final cleanup funding.
			if f.a.Usage()[Recovery] != f.resources.LifetimeRequest.Need {
				t.Fatal("unused request charged resources")
			}
		})
	}
}
