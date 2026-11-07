package writer

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type governedFixtureProfile struct {
	recipe      turnRecipe
	memoryBytes int64
}

func governedFixture(t *testing.T, now func() time.Time, profiles ...governedFixtureProfile) (governor.Governor, *governor.Authority, *governor.WALGate, governor.Request) {
	return governedFixtureResourcesAndForeground(t, now, governor.Amounts{}, nil, profiles...)
}

func governedFixtureResources(t *testing.T, now func() time.Time, extra governor.Amounts, profiles ...governedFixtureProfile) (governor.Governor, *governor.Authority, *governor.WALGate, governor.Request) {
	return governedFixtureResourcesAndForeground(t, now, extra, nil, profiles...)
}

func governedFixtureWithForeground(t *testing.T, now func() time.Time, foreground *governor.Config, profiles ...governedFixtureProfile) (governor.Governor, *governor.Authority, *governor.WALGate, governor.Request) {
	return governedFixtureResourcesAndForeground(t, now, governor.Amounts{}, foreground, profiles...)
}

func governedFixtureResourcesAndForeground(t *testing.T, now func() time.Time, extra governor.Amounts, foreground *governor.Config, profiles ...governedFixtureProfile) (governor.Governor, *governor.Authority, *governor.WALGate, governor.Request) {
	t.Helper()
	profile := governedFixtureProfile{testRecipe(), 65536}
	if len(profiles) > 2 {
		t.Fatal("multiple test profiles")
	}
	if len(profiles) >= 1 {
		profile = profiles[0]
	}
	if profile.memoryBytes <= 0 {
		t.Fatal("invalid test memory")
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := governor.Request{Key: governor.JobKey{Domain: uint16(InventoryAccept), ID: "synthetic/run"}, Owner: 1, Class: governor.ScanMinimum, Writer: true, MaxHold: profile.recipe.limit.Hold, Deadline: now().Add(time.Second),
		Budget: governor.UnitBudget{RawRows: uint32(profile.recipe.limit.Rows), VMOps: profile.recipe.limit.VMOps, Pages: uint32(profile.recipe.limit.Pages)}, Footprint: []governor.Scope{{Library: 1, Domain: 1, Source: 1}}}
	r.Need[governor.WALBytes] = int64(profile.recipe.limit.WALBytes)
	r.Need[governor.MemoryBytes] = profile.memoryBytes
	r.Need[governor.CPUMilli] = extra[governor.CPUMilli]
	r.Need[governor.IOBytes] = extra[governor.IOBytes]
	if r.Need[governor.IOBytes] > 0 {
		r.IO = []governor.DeviceIO{{DeviceID: "protocol", Class: governor.Seeking, Outstanding: 1, Bytes: r.Need[governor.IOBytes]}}
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = r.Need
		if len(profiles) == 2 {
			if profiles[1] != profile {
				t.Fatal("extra fixture unit must be identical")
			}
			for resource, n := range partitions[i] {
				if n > int64(^uint64(0)>>1)/2 {
					t.Fatal("fixture capacity overflow")
				}
				partitions[i][resource] = n * 2
			}
		}
	}
	cfg := governor.Config{Partitions: partitions, MaxGrantsPerClass: 4, Now: now, WAL: wal}
	if foreground != nil {
		cfg.Foreground = foreground.Foreground
		cfg.Partitions[governor.Foreground] = foreground.Partitions[governor.Foreground]
		cfg.MaxGrantsPerClass = foreground.MaxGrantsPerClass
	}
	g, a, err := governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g, a, wal, r
}

func TestGovernedTurnNativeReturnAndWALRefresh(t *testing.T) {
	// One canonical fixture, virtual protocol clock: no latency qualification.
	w, path, clock := testExecutor(t)
	now := func() time.Time { return *clock }
	g, a, wal, r := governedFixture(t, now)
	if err := refreshWriterWAL(path, wal, now); err != nil {
		t.Fatal(err)
	}
	d := g.Try(r)
	if d.Grant == nil {
		t.Fatal(d.Refusal)
	}
	c, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, d.Grant, r)
	if err != nil {
		t.Fatal(err)
	}
	// The ledger and W both retain the original footprint despite mutation.
	r.Footprint[0].Library = 99
	if err := c.check(InventoryAccept, testRecipe().limit); err != nil {
		t.Fatal("mutable caller footprint rebased grant", err)
	}
	r.Footprint[0].Library = 1
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	if a.Usage()[governor.ScanMinimum][governor.MemoryBytes] != r.Need[governor.MemoryBytes] {
		t.Fatal("physical turn refunded before return")
	}
	mutateFixture(t, tx)
	physical, err := tx.commit()
	if err != nil || physical.state != committed {
		t.Fatalf("synthetic commit: %+v %v", physical, err)
	}
	if w.db.Stats().InUse != 0 || a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) {
		t.Fatal("physical/ledger custody remained")
	}
	if err := c.retired(); err != nil {
		t.Fatal("exact retirement not idempotent", err)
	}
	if err := c.check(InventoryAccept, testRecipe().limit); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("retired grant reusable", err)
	}
	if d := g.Try(r); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.FailSafe {
		t.Fatal("reused pre-return WAL observation", d)
	}
	if err := refreshWriterWAL(path, wal, now); err != nil {
		t.Fatal(err)
	}
	d = g.Try(r)
	if d.Grant == nil {
		t.Fatal("fresh post-return sample refused", d.Refusal)
	}
	c, err = bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, d.Grant, r)
	if err != nil {
		t.Fatal(err)
	}
	g.Cancel(d.Grant)
	if a.Usage()[governor.ScanMinimum][governor.MemoryBytes] == 0 {
		t.Fatal("cancel refunded capacity")
	}
	if _, err := w.begin(context.Background(), InventoryAccept, testRecipe(), c); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("cancelled grant entered writer", err)
	}
	if a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("cancelled pre-BEGIN holding stranded")
	}
}

func TestGovernedTurnExactCompositionPurposeAndForecast(t *testing.T) {
	clock := time.Now().Add(time.Hour)
	now := func() time.Time { return clock }
	g, a, wal, r := governedFixture(t, now)
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, now()); err != nil {
		t.Fatal(err)
	}
	d := g.Try(r)
	if d.Grant == nil {
		t.Fatal(d.Refusal)
	}
	defer a.Retired(d.Grant) // binder failures retain ownership with this driver
	other, _, otherWAL, _ := governedFixture(t, now)
	for _, test := range []struct {
		g   governor.Governor
		wal *governor.WALGate
	}{{other, wal}, {g, otherWAL}, {g, nil}} {
		if _, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, test.g, a, test.wal, d.Grant, r); !errors.Is(err, governor.ErrComposition) {
			t.Fatal("unbound composition", err)
		}
	}
	if _, err := bindGovernedTurn(SecurityAccept, testRecipe().limit, g, a, wal, d.Grant, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("scan grant acquired security class", err)
	}
	if _, err := bindGovernedTurn(BasicAccept, testRecipe().limit, g, a, wal, d.Grant, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("same-class grant borrowed by another command purpose", err)
	}
	for _, change := range []func(*Work){
		func(v *Work) { v.Rows++ }, func(v *Work) { v.Pages++ }, func(v *Work) { v.VMOps++ }, func(v *Work) { v.WALBytes++ }, func(v *Work) { v.Hold++ },
		func(v *Work) { v.Rows = uint64(math.MaxUint32) + 1 }, func(v *Work) { v.Pages = uint64(math.MaxUint32) + 1 }, func(v *Work) { v.WALBytes = uint64(math.MaxInt64) + 1 },
	} {
		v := testRecipe().limit
		change(&v)
		if _, err := bindGovernedTurn(InventoryAccept, v, g, a, wal, d.Grant, r); !errors.Is(err, ErrInvalid) {
			t.Fatal("forecast mismatch accepted", v, err)
		}
	}
	wrong := r
	wrong.Owner++
	if _, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, d.Grant, wrong); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("foreign expected owner accepted", err)
	}
	c, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, d.Grant, r)
	if err != nil {
		t.Fatal(err)
	}
	c.fence()
	if err := c.check(InventoryAccept, testRecipe().limit); !errors.Is(err, governor.ErrGrant) {
		t.Fatal("fenced grant still authorizes", err)
	}
	if a.Usage()[governor.ScanMinimum][governor.MemoryBytes] == 0 {
		t.Fatal("fence refunded physical holding")
	}
}

func TestCommandClassesAreClosedAndExplicit(t *testing.T) {
	for _, c := range []struct {
		id    CommandID
		class governor.Class
	}{
		{InventoryAccept, governor.ScanMinimum}, {BasicAccept, governor.ScanMinimum},
		{InventoryCreate, governor.ScanMinimum}, {InventoryClaim, governor.ScanMinimum},
		{BasicStage, governor.ScanMinimum}, {BasicSeal, governor.ScanMinimum},
		{InventoryDirectoryDiscover, governor.ScanMinimum}, {InventoryDirectoryEOF, governor.ScanMinimum},
		{ProgressAccept, governor.Foreground}, {SecurityAccept, governor.Recovery},
		{EvidenceStage, governor.Background}, {CreditStage, governor.Background},
		{PeopleStage, governor.Background}, {EnrichAccept, governor.Background},
		{SearchFragmentStage, governor.Background}, {SearchHeadPublish, governor.Background},
		{SearchFragmentReclaim, governor.Background}, {SearchImageReclaim, governor.Background},
		{SearchGenerationAdvance, governor.Background}, {SearchGenerationFlip, governor.Background},
		{PeopleHeadPublish, governor.Background}, {SearchImageStage, governor.Background},
		{SearchSeal, governor.Background}, {EffectiveStage, governor.Background},
	} {
		if got, err := commandClass(c.id); err != nil || got != c.class {
			t.Fatal(c.id, got, err)
		}
	}
	for _, id := range []CommandID{0, InventoryDirectoryEOF + 1, 255} {
		if _, err := commandClass(id); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown purpose acquired a class", id, err)
		}
	}
}

func TestGovernedTurnWALReadFailureInvalidatesOldSample(t *testing.T) {
	clock := time.Now().Add(time.Hour)
	now := func() time.Time { return clock }
	g, _, wal, request := governedFixture(t, now)
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, now()); err != nil {
		t.Fatal(err)
	}
	if err := refreshWriterWAL(t.TempDir()+"/missing", wal, now); err == nil {
		t.Fatal("missing physical WAL files accepted")
	}
	if d := g.Try(request); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.FailSafe {
		t.Fatal("failed read left old WAL headroom usable", d)
	}
}

func TestGovernedTurnWALClockPanicInvalidatesOldSample(t *testing.T) {
	_, path, clock := testExecutor(t)
	now := func() time.Time { return *clock }
	g, _, wal, request := governedFixture(t, now)
	if err := refreshWriterWAL(path, wal, now); err != nil {
		t.Fatal(err)
	}
	primary := errors.New("WAL observation clock panic")
	func() {
		defer func() {
			if got := recover(); got != primary {
				t.Fatal("primary replaced", got)
			}
		}()
		_, _ = observeWriterWAL(path, wal, func() time.Time { panic(primary) })
		t.Fatal("clock panic swallowed")
	}()
	if d := g.Try(request); d.Grant != nil || d.Refusal == nil || d.Refusal.Code != governor.FailSafe {
		t.Fatal("observer panic left old headroom reusable", d)
	}
}
