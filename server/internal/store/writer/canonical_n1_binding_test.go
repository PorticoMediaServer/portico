package writer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

const canonicalN1Recipe = "canonical-n1-fresh-active-current-v1"
const canonicalN1Warmup = 8

// Closed target recipe only; command Work, preparation/reserve and WAL caps
// remain those of the ONE existing component owner. Maintenance has its OWN
// explicitly funded partition, lifetime, startup, active and shutdown custody.
func canonicalN1Maintenance() governedFixtureMaintenance {
	return governedFixtureMaintenance{
		policy: governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second, ConcurrentPassive: true, CheckpointMaxHold: 5 * time.Second},
		need:   governor.Amounts{governor.Reader: 2, governor.MemoryBytes: 8 << 20, governor.CPUMilli: 2000, governor.IOBytes: 2 * (2 * (1 << 29))},
	}
}

type canonicalN1CheckpointClock struct {
	StartedNS          *int64 `json:"started_ns"`
	NativeStartedNS    *int64 `json:"native_started_ns"`
	NativeEndedNS      *int64 `json:"native_ended_ns"`
	PhysicalReturnedNS *int64 `json:"physical_returned_ns"`
	RetiredNS          *int64 `json:"retired_ns"`
}

type canonicalN1Checkpoint struct {
	Result governor.CheckpointResult  `json:"result"`
	Error  string                     `json:"error"`
	Clock  canonicalN1CheckpointClock `json:"owner_clock"`
}

// Derive from the original in-process time.Time values BEFORE JSON discards
// monotonic data. Same origin as W owner_clock; zero-time phases remain null.
func (r *canonicalN1Checkpoint) observeClock(origin time.Time) {
	r.Clock = canonicalN1CheckpointClock{
		StartedNS:          n1NS(origin, r.Result.Started),
		NativeStartedNS:    n1NS(origin, r.Result.NativeStarted),
		NativeEndedNS:      n1NS(origin, r.Result.NativeEnded),
		PhysicalReturnedNS: n1NS(origin, r.Result.PhysicalReturned),
		RetiredNS:          n1NS(origin, r.Result.RetiredAt),
	}
}

type canonicalN1MaintenanceOwner struct {
	opening            *scanOpening
	fixture            *inventoryProgressiveFixture
	cp                 *governor.Checkpointer
	keeper             *governor.CheckpointKeeper
	lifetime           *governor.Grant
	shutdown           *governor.Grant
	shutdownRequest    governor.Request
	stop               chan struct{}
	done               chan struct{}
	results            chan canonicalN1Checkpoint
	once               sync.Once
	workerStarted      bool
	closed             bool
	startup            *governor.Grant
	overflow           bool // written by worker; read only AFTER done
	started, completed int  // main owns these counters while draining records
	incomplete         bool
}

// Invoked after the exact-t population attachment. Existing fixture clone,
// seed journal, canonical opening and registry Capture are reused, not cloned.
func newCanonicalN1Fixture(t *testing.T, count int, trace *n1Trace) (*inventoryProgressiveFixture, *canonicalN1MaintenanceOwner) {
	t.Helper()
	if count < 1 || count > 1000000+canonicalN1Warmup {
		t.Fatal("bounded canonical original census required")
	}
	seed, path, _ := testExecutor(t)
	original := seedInventoryFreshPopulation(t, seed, true)
	if err := seed.close(); err != nil {
		t.Fatal(err)
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	w := opening.executor
	// Existing test-only decorator seam, AFTER the unobserved opening pool joins.
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	w.physicalFaults.mu.Lock()
	failed := w.physicalFaults.failed
	w.physicalFaults.mu.Unlock()
	stats := w.db.Stats()
	if failed || stats.InUse != 0 || stats.OpenConnections != 0 {
		t.Fatal("initial canonical opening not physically joined")
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	w.openPool(connector, trace.decorate)
	registry := nativeInventoryFixtureRegistry(t, w, inventoryCommandDefinitions(), "scan_fresh_test.go")
	f := inventoryProgressiveOwner(t, w, original, registry, count, time.Now, canonicalN1Maintenance())
	m := &canonicalN1MaintenanceOwner{opening: opening, fixture: f, stop: make(chan struct{}), done: make(chan struct{}), results: make(chan canonicalN1Checkpoint, 128)}
	// Installed before any fallible keeper setup; retain partial cleanup on Fatal.
	t.Cleanup(func() {
		if err := m.close(); err != nil {
			t.Error(err)
		}
	})
	cfg := sqliteconn.Config{Role: sqliteconn.Checkpointer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err = sqliteconn.Connector(path, cfg, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	m.cp, err = governor.NewCheckpointer(connector, cfg, f.owner.governor, f.owner.authority, governor.CheckpointPolicy{DatabasePath: path, Interval: 50 * time.Millisecond, MaxHold: 5 * time.Second, MaxWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	resources := governor.KeeperResources{LifetimeRequest: m.request("lifetime"), StartupRequest: m.request("startup")}
	resources.LifetimeRequest.Need = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 4 << 20}
	resources.LifetimeRequest.IO = nil
	resources.Lifetime = m.grant(t, resources.LifetimeRequest)
	m.lifetime = resources.Lifetime
	resources.Startup = m.grant(t, resources.StartupRequest)
	m.startup = resources.Startup
	m.keeper, err = m.cp.Keep(t.Context(), resources)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.owner.authority.Retired(resources.Startup); err != nil {
		t.Fatal(err)
	}
	m.startup = nil
	if err = opening.bindCheckpointKeeper(m.keeper, f.owner.governor, f.owner.authority, f.owner.wal); err != nil {
		t.Fatal(err)
	}
	f.sharedMaintenance = true
	return f, m
}
func (m *canonicalN1MaintenanceOwner) request(role string) governor.Request {
	r := governor.Request{Key: governor.JobKey{Domain: uint16(InventoryAccept), ID: "canonical-n1-cp/" + role}, Owner: 1, Class: governor.Recovery, Need: governor.Amounts{governor.MemoryBytes: 64 << 10, governor.CPUMilli: 1000, governor.IOBytes: 2 * (1 << 29)}, Deadline: time.Now().Add(time.Hour)}
	r.IO = []governor.DeviceIO{{DeviceID: fmt.Sprintf("filesystem-device:%d", m.opening.binding.device), Class: governor.Sequential, Outstanding: 1, Bytes: r.Need[governor.IOBytes]}}
	if role == "passive" {
		r.Need[governor.Reader] = 1
		r.MaxHold = 5 * time.Second
		r.Budget = governor.UnitBudget{RawRows: 1, VMOps: 10000000, Pages: uint32(2*((1<<29)/4120) + 1)}
		r.Deadline = time.Now().Add(6 * time.Second)
	}
	return r
}
func (m *canonicalN1MaintenanceOwner) grant(t *testing.T, r governor.Request) *governor.Grant {
	t.Helper()
	d := m.fixture.owner.governor.Try(r)
	if d.Grant == nil {
		t.Fatal("canonical maintenance funding", d.Refusal)
	}
	return d.Grant
}
func (m *canonicalN1MaintenanceOwner) step(ctx context.Context) canonicalN1Checkpoint {
	out, err := m.cp.Step(ctx, m.request("passive"))
	return canonicalN1Checkpoint{Result: out, Error: checkpointError(err)}
}
func (m *canonicalN1MaintenanceOwner) start(ctx context.Context) {
	m.workerStarted = true
	go func() {
		defer close(m.done)
		for {
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-m.stop:
				timer.Stop()
				return
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			record := m.step(ctx) // no file sink/callback in native custody or another W HOLD
			select {
			case m.results <- record:
			default:
				m.overflow = true
				return
			}
			if record.Error != "" || record.Result.Refusal != nil || !record.Result.PhysicalSafe {
				return
			}
		}
	}()
}
func (m *canonicalN1MaintenanceOwner) drain(stream *n1Stream) error {
	for {
		select {
		case record := <-m.results:
			record.observeClock(stream.origin)
			m.started++
			if record.Error == "" && record.Result.Refusal == nil && record.Result.PhysicalSafe {
				m.completed++
			} else {
				m.incomplete = true
			}
			if err := stream.append("checkpoint", map[string]any{"checkpoint_ordinal": m.started, "checkpoint": record, "scope": "separately funded concurrent PASSIVE; not a writer HOLD", "qualification": false}); err != nil {
				return err
			}
		default:
			if m.incomplete {
				return errors.New("canonical checkpoint incomplete; retain raw harness/composition gap")
			}
			return nil
		}
	}
}
func (m *canonicalN1MaintenanceOwner) joinWorker() {
	if m.workerStarted {
		m.once.Do(func() { close(m.stop); <-m.done })
	}
}
func (m *canonicalN1MaintenanceOwner) close() error {
	// Caller joins worker before W.close; native keeper closes only AFTER W's
	// actual Retired witness. Cleanup is deliberately idempotent via real owners.
	if m.closed {
		return nil
	}
	m.joinWorker()
	f := m.fixture
	if m.keeper == nil {
		err := f.owner.executor.close()
		if m.cp != nil {
			err = errors.Join(err, m.cp.Close())
		}
		if err != nil {
			return err
		} // uncertain native holdings stay charged
		for _, p := range []**governor.Grant{&m.startup, &m.lifetime} {
			if *p != nil {
				if e := f.owner.authority.Retired(*p); e != nil {
					return e
				}
				*p = nil
			}
		}
		m.closed = true
		return nil
	}
	err := m.opening.close()
	if err != nil && !errors.Is(err, ErrBusy) {
		return err
	}
	if m.shutdown == nil {
		m.shutdownRequest = m.request("shutdown")
		d := m.keeper.AdmitShutdown(m.shutdownRequest)
		if d.Grant == nil {
			return fmt.Errorf("canonical keeper shutdown: %v", d.Refusal)
		}
		m.shutdown = d.Grant
	}
	// On uncertain native close retain and reuse THIS same cleanup holding.
	if err = m.keeper.Close(m.shutdown, m.shutdownRequest); err != nil {
		return err
	}
	for _, p := range []**governor.Grant{&m.shutdown, &m.lifetime, &m.startup} {
		if *p != nil {
			if e := f.owner.authority.Retired(*p); e != nil {
				return e
			}
			*p = nil
		}
	}
	err = m.opening.close()
	m.closed = err == nil
	return err
}

func runCanonicalHeldN1(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest) (err error) {
	summary := &n1SteadySummary{}
	defer func() {
		err = errors.Join(err, stream.append("final", map[string]any{"complete": summary.Complete, "steady": summary, "recipe": canonicalN1Recipe, "report": nil, "qualification": false, "qualified_profile_ready": false, "error": checkpointError(err)}))
	}()
	runN1Steady(t, ctx, stream, cfg.Attempts, canonicalN1Warmup, "timing", n1SteadyControls{clock: time.Now, canonical: &manifest}, summary)
	if !summary.Complete {
		return fmt.Errorf("canonical N1 incomplete: %s", summary.StopReason)
	}
	return nil
}
