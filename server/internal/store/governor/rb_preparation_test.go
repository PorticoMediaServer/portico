package governor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func rbPreparationFixture(t *testing.T) (Governor, *Authority, *testClock) {
	t.Helper()
	clock := &testClock{}
	cfg := Config{Now: clock.now, MaxGrantsPerClass: 32, Preparation: &PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}}
	for c := range cfg.Partitions {
		cfg.Partitions[c] = Amounts{Reader: 4, MemoryBytes: 96 << 20, WALBytes: 1 << 20}
	}
	g, a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g, a, clock
}
func rbPreparationRequest(c *testClock, owner OwnerID, library int64) PreparationRequest {
	return PreparationRequest{Key: JobKey{Domain: 90, ID: fmt.Sprint(owner)}, Owner: owner, Deadline: c.now().Add(time.Minute), ReservedBytes: 16 << 20, Dependencies: PublicationFootprint{{Library: library}}}
}
func rbBegin(t *testing.T, a *Authority, c *testClock, owner OwnerID, library int64) *PreparationLease {
	t.Helper()
	d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, owner, library))
	if d.Refusal != nil || d.Lease == nil {
		t.Fatal(d)
	}
	t.Cleanup(func() {
		if err := a.RetirePreparation(d.Lease); err != nil {
			t.Error(err)
		}
	})
	return d.Lease
}
func rbWriter(c *testClock, class Class, library int64) Request {
	return Request{Key: JobKey{Domain: 91, ID: "publication"}, Owner: 99, Class: class, Writer: true, MaxHold: 25 * time.Millisecond, Deadline: c.now().Add(time.Minute), Budget: UnitBudget{RawRows: 1, VMOps: 1, Pages: 1}, Need: Amounts{MemoryBytes: 4096, WALBytes: 4096}, Footprint: []Scope{{Library: library}}}
}

// The F-04 handoff adversary: cache insertion happens, the client stops polling,
// and a scan remains runnable. Ready must NOT release conflict protection.
func TestRBPreparationReadyHandoffAdversary(t *testing.T) {
	g, a, c := rbPreparationFixture(t)
	p := rbBegin(t, a, c, 1, 7)
	if err := a.HandoffPreparation(p); !errors.Is(err, ErrPreparation) {
		t.Fatal("unready handed off", err)
	}
	if err := a.ReadyPreparation(p); err != nil {
		t.Fatal(err)
	}
	c.ms.Store(11000) // parked until a late poll, before original12s cap
	for n := 0; n < 3; n++ {
		refused(t, g, rbWriter(c, ScanMinimum, 7), Conflict)
	}
	if err := a.CheckPreparation(p); err != nil {
		t.Fatal("ready cache invalidated by scan", err)
	}
	// A trusted fresh response snapshot has authorized and copied the matching
	// values/fence into independent output custody before this linearization.
	if err := a.HandoffPreparation(p); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("handoff did not signal release")
	}
	if a.Usage()[Foreground][MemoryBytes] != 16<<20 {
		t.Fatal("handoff refunded live cache bytes")
	}
	d := g.Try(rbWriter(c, ScanMinimum, 7))
	if d.Grant == nil {
		t.Fatal("scan did not resume", d.Refusal)
	}
	retire(t, a, d.Grant)
	if err := a.HandoffPreparation(p); !errors.Is(err, ErrPreparation) {
		t.Fatal("repeated handoff", err)
	}
}

func TestRBPreparationExpiryPollsAndCancellation(t *testing.T) {
	for _, mode := range []string{"expiry", "cancel", "stale", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			g, a, c := rbPreparationFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			q := rbPreparationRequest(c, 1, 7)
			d := a.BeginPreparation(ctx, q)
			if d.Lease == nil {
				t.Fatal(d.Refusal)
			}
			p := d.Lease
			defer func() {
				if err := a.RetirePreparation(p); err != nil {
					t.Error(err)
				}
			}()
			if !p.Deadline().Equal(c.now().Add(12*time.Second)) || p.PayloadBytes() != 16<<20-PreparationMetadataBytes {
				t.Fatal("absolute cap/bytes")
			}
			if err := a.ReadyPreparation(p); err != nil {
				t.Fatal(err)
			}
			for n := int64(1); n <= 11; n++ {
				c.ms.Store(n * 1000)
				if err := a.CheckPreparation(p); err != nil {
					t.Fatal(err)
				}
				q.Deadline = c.now().Add(time.Hour)
				if again := a.BeginPreparation(ctx, q); again.Lease != nil || again.Refusal.Code != Conflict {
					t.Fatal("poll renewed job", again)
				}
			}
			want := ErrPreparationExpired
			switch mode {
			case "expiry":
				c.ms.Store(12000)
			case "cancel":
				cancel()
				want = context.Canceled
			case "stale":
				if err := a.InterruptPreparations(PublicationFootprint{{Library: 7}}); err != nil {
					t.Fatal(err)
				}
				want = ErrPreparationStale
			case "epoch":
				a.EnterStatic()
				want = ErrPreparationStale
			}
			if err := a.HandoffPreparation(p); !errors.Is(err, want) {
				t.Fatal("released result escaped", err, want)
			}
			if a.Usage()[Foreground][MemoryBytes] != q.ReservedBytes {
				t.Fatal("logical exit refunded physical memory")
			}
			if mode != "epoch" {
				d := g.Try(rbWriter(c, Background, 7))
				if d.Grant == nil {
					t.Fatal("publication stayed blocked", d.Refusal)
				}
				retire(t, a, d.Grant)
			}
		})
	}
}

func TestRBPreparationOwnersScopeAndPhysicalCaps(t *testing.T) {
	g, a, c := rbPreparationFixture(t)
	p := rbBegin(t, a, c, 1, 1)
	// Same owner is one active preparation, even for a disjoint library.
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 1, 2)); d.Lease != nil || d.Refusal.Code != Conflict {
		t.Fatal(d)
	}
	// Same scope cannot be leased to a competing owner.
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 2, 1)); d.Lease != nil || d.Refusal.Code != Conflict {
		t.Fatal(d)
	}
	for n := 2; n <= 4; n++ {
		rbBegin(t, a, c, OwnerID(n), int64(n))
	}
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 5, 5)); d.Lease != nil || d.Refusal.Code != Capacity {
		t.Fatal("fifth admitted", d)
	}
	if err := a.CancelPreparation(p); err != nil {
		t.Fatal(err)
	}
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 5, 5)); d.Lease != nil || d.Refusal.Code != Capacity {
		t.Fatal("unexited debt recycled", d)
	}
	if a.Usage()[Foreground][MemoryBytes] != 64<<20 {
		t.Fatal("four-job bytes")
	}
	// All four prep buffers coexist with the nonloanable16MiB foreground floor.
	r := request(c, Foreground)
	r.Need = Amounts{MemoryBytes: 16 << 20}
	grant := grant(t, g, r)
	retire(t, a, grant)
}

func TestRBPreparationPublicationPriorityAndDrain(t *testing.T) {
	for _, class := range []Class{Background, ScanMinimum, Foreground, Recovery} {
		t.Run(fmt.Sprint(class), func(t *testing.T) {
			g, a, c := rbPreparationFixture(t)
			p := rbBegin(t, a, c, 1, 7)
			r := rbWriter(c, class, 8)
			d := g.Try(r)
			if d.Grant == nil {
				t.Fatal("nonconflicting publication", d.Refusal)
			}
			retire(t, a, d.Grant)
			r.Footprint = nil // omitted declaration is GLOBAL, not no-effects
			d = g.Try(r)
			if class == Background || class == ScanMinimum {
				if d.Grant != nil || d.Refusal.Code != Conflict {
					t.Fatal("undeclared publication bypassed", d)
				}
				if err := a.CheckPreparation(p); err != nil {
					t.Fatal(err)
				}
			} else {
				if d.Grant == nil {
					t.Fatal("foreground waited for browse", d.Refusal)
				}
				if err := a.CheckPreparation(p); !errors.Is(err, ErrPreparationStale) {
					t.Fatal("priority publication failed to invalidate", err)
				}
				retire(t, a, d.Grant)
			}
		})
	}
	g, a, c := rbPreparationFixture(t)
	r := rbWriter(c, Background, 7)
	held := grant(t, g, r)
	g.Cancel(held) // cancellation is not physical writer exit
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 1, 7)); d.Lease != nil || d.Refusal.Code != Conflict {
		t.Fatal("captured while writer unexited", d)
	}
	retire(t, a, held)
	rbBegin(t, a, c, 1, 7)
}

func TestRBPreparationFootprintCoverageAndCommit(t *testing.T) {
	for _, test := range []struct {
		declared, actual PublicationFootprint
		want             bool
	}{
		{nil, PublicationFootprint{{Library: 7}}, true},
		{PublicationFootprint{{Library: 7}}, nil, false},
		{PublicationFootprint{{Library: 7}}, PublicationFootprint{{Library: 7, Domain: 1, Source: 2}}, true},
		{PublicationFootprint{{Library: 7, Source: 2}}, PublicationFootprint{{Library: 7}}, false},
		{PublicationFootprint{{Library: 7}, {Library: 8}}, PublicationFootprint{{Library: 0}}, false},
		{PublicationFootprint{{Library: 7}}, PublicationFootprint{{Library: 8}}, false},
		{PublicationFootprint{{Library: -1}}, nil, false},
	} {
		if got := PublicationCovered(test.declared, test.actual); got != test.want {
			t.Fatal(test, got)
		}
	}
	g, a, c := rbPreparationFixture(t)
	p := rbBegin(t, a, c, 1, 7)
	r := rbWriter(c, Background, 8)
	held := grant(t, g, r)
	defer retire(t, a, held)
	if err := a.CheckPublication(held, r, PublicationFootprint{{Library: 7}}); !errors.Is(err, ErrPublicationFootprint) {
		t.Fatal("wider resolution published", err)
	}
	if err := a.CheckPublication(held, r, PublicationFootprint{{Library: 8}}); err != nil {
		t.Fatal("covered resolution", err)
	}
	if err := a.CheckPreparation(p); err != nil {
		t.Fatal("unrelated commit invalidated", err)
	}
}

func TestRBPreparationOffAndProfileValidation(t *testing.T) {
	g, a, c := setup(t)
	if d := a.BeginPreparation(t.Context(), rbPreparationRequest(c, 1, 7)); d.Lease != nil || d.Refusal.Code != Unqualified {
		t.Fatal("phase1 enabled by default", d)
	}
	// No added validation of phase1a footprint semantics, even when a command
	// uses pre-existing command-ID scopes rather than phase2 fact dependencies.
	if err := a.CheckPublication(nil, Request{}, nil); err != nil {
		t.Fatal("phase1 commit semantics changed", err)
	}
	_ = g
	for _, p := range []PreparationProfile{
		{5, 64 << 20, 16 << 20, 1}, {4, 65 << 20, 16 << 20, 1}, {4, 64 << 20, 17 << 20, 1}, {4, 64 << 20, 16 << 20, 0},
	} {
		cfg := Config{Now: c.now, MaxGrantsPerClass: 32, Preparation: &p}
		cfg.Partitions[Foreground][MemoryBytes] = 128 << 20
		if _, _, err := New(cfg); err == nil {
			t.Fatal("invalid profile", p)
		}
	}
}

func TestRBPreparationAdmissionPublicationRace(t *testing.T) {
	// Both operations arbitrate under the governor mutex. Whichever wins,
	// conflicting discretionary publication and protection NEVER coexist.
	for n := 0; n < 20; n++ {
		g, a, c := rbPreparationFixture(t)
		var wg sync.WaitGroup
		wg.Add(2)
		var pd PreparationDecision
		var wd Decision
		start := make(chan struct{})
		go func() { defer wg.Done(); <-start; pd = a.BeginPreparation(t.Context(), rbPreparationRequest(c, 1, 7)) }()
		go func() { defer wg.Done(); <-start; wd = g.Try(rbWriter(c, Background, 7)) }()
		close(start)
		wg.Wait()
		if pd.Lease != nil && wd.Grant != nil {
			t.Fatal("conflicting custody coexisted")
		}
		if pd.Lease != nil {
			if err := a.RetirePreparation(pd.Lease); err != nil {
				t.Fatal(err)
			}
		}
		if wd.Grant != nil {
			retire(t, a, wd.Grant)
		}
		if a.Usage()[Foreground] != (Amounts{}) {
			t.Fatal("race leaked memory")
		}
	}
}

func TestRBPreparationImmutableInputsAndRejectedForeground(t *testing.T) {
	g, a, c := rbPreparationFixture(t)
	q := rbPreparationRequest(c, 1, 7)
	d := a.BeginPreparation(t.Context(), q)
	if d.Lease == nil {
		t.Fatal(d.Refusal)
	}
	defer func() {
		if err := a.RetirePreparation(d.Lease); err != nil {
			t.Error(err)
		}
	}()
	q.Dependencies[0].Library = 8
	refused(t, g, rbWriter(c, Background, 7), Conflict)
	// Admission failure cannot invalidate a legitimate ready job.
	r := rbWriter(c, Foreground, 7)
	r.Need[MemoryBytes] = 128 << 20
	refused(t, g, r, Capacity)
	if err := a.CheckPreparation(d.Lease); err != nil {
		t.Fatal("rejected foreground evicted work", err)
	}
	// Domain and source scopes are conservative exact dependencies.
	if footprintsOverlap([]Scope{{Library: 7, Domain: 1, Source: 2}}, []Scope{{Library: 7, Domain: 2, Source: 2}}) ||
		footprintsOverlap([]Scope{{Library: 7, Domain: 1, Source: 2}}, []Scope{{Library: 7, Domain: 1, Source: 3}}) ||
		!footprintsOverlap(nil, []Scope{{Library: 7, Domain: 1, Source: 2}}) {
		t.Fatal("scope overlap")
	}
}
