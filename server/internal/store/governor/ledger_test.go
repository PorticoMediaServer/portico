package governor

import (
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct{ ms atomic.Int64 }

func (c *testClock) now() time.Time { return time.UnixMilli(c.ms.Load()) }

func setup(t *testing.T) (Governor, *Authority, *testClock) {
	t.Helper()
	c := &testClock{}
	config := Config{Now: c.now, MaxGrantsPerClass: 32}
	for cl := range config.Partitions {
		config.Partitions[cl] = Amounts{2, 2, 128, 500, 64, 128, 128, 8}
	}
	g, a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return g, a, c
}

func request(c *testClock, cl Class) Request {
	return Request{Key: JobKey{Domain: 1, ID: "opaque"}, Owner: 1, Class: cl, Need: Amounts{Reader: 1}, Deadline: c.now().Add(time.Second)}
}

func grant(t *testing.T, g Governor, r Request) *Grant {
	t.Helper()
	d := g.Try(r)
	if d.Grant == nil || d.Refusal != nil {
		t.Fatalf("expected grant, got %+v", d)
	}
	return d.Grant
}

func refused(t *testing.T, g Governor, r Request, want RefusalCode) {
	t.Helper()
	d := g.Try(r)
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != want || d.Refusal.RetryAt.IsZero() {
		t.Fatalf("want refusal %d, got %+v", want, d)
	}
}

func retire(t *testing.T, a *Authority, p *Grant) {
	t.Helper()
	if err := a.Retired(p); err != nil {
		t.Fatal(err)
	}
}

func TestCancelExpiryKeepPhysicalDebt(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, Background)
	r.Need[Reader] = 2
	p := grant(t, g, r)
	g.Cancel(p)
	if a.Check(p, r) != ErrGrant {
		t.Fatal("cancel still authorized")
	}
	refused(t, g, r, Capacity)
	if a.Usage()[Background][Reader] != 2 {
		t.Fatal("cancel refunded physical debt")
	}
	retire(t, a, p)
	p = grant(t, g, r)
	c.ms.Store(1000)
	if a.Check(p, r) != ErrGrant {
		t.Fatal("deadline still authorized")
	}
	r.Deadline = c.now().Add(time.Second)
	refused(t, g, r, Capacity)
	retire(t, a, p)
	if a.Retired(p) != ErrRetirement {
		t.Fatal("duplicate retirement accepted")
	}
	retire(t, a, grant(t, g, r))
}

func TestPartitionsAndAllOrNothing(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, Background)
	r.Need[Reader] = 3
	refused(t, g, r, Capacity) // unused foreground/recovery/scan floors are not loans
	if a.Usage() != [4]Amounts{} {
		t.Fatal("partial reservation on refusal")
	}
	var ps []*Grant
	for cl := Recovery; cl <= Background; cl++ {
		r = request(c, cl)
		r.Need[Reader] = 2
		ps = append(ps, grant(t, g, r))
	}
	for _, p := range ps {
		retire(t, a, p)
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("physical resource leak")
	}
}

func TestExactAuthorityAndOwnedMetadata(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, ScanMinimum)
	r.IO = []DeviceIO{{DeviceID: "spindle-1", Class: Seeking, Outstanding: 1, Bytes: 32}}
	r.Need[IOBytes] = 32
	r.Footprint = []Scope{{Library: 3, Domain: 1, Source: 7}}
	p := grant(t, g, r)
	expected := r
	expected.IO = slices.Clone(r.IO)
	expected.Footprint = slices.Clone(r.Footprint)
	r.IO[0].DeviceID = "changed"
	r.Footprint[0].Library = 9
	if err := a.Check(p, expected); err != nil {
		t.Fatal(err)
	}
	changes := []func(*Request){
		func(x *Request) { x.Owner++ }, func(x *Request) { x.Class = Foreground },
		func(x *Request) { x.Need[Reader]++ }, func(x *Request) { x.Writer = true },
		func(x *Request) { x.Key.ID = "other" }, func(x *Request) { x.Budget.Pages = 1 },
		func(x *Request) { x.MaxHold = time.Millisecond }, func(x *Request) { x.Deadline = x.Deadline.Add(time.Millisecond) },
		func(x *Request) { x.IO = nil }, func(x *Request) { x.Footprint = nil },
	}
	for i, change := range changes {
		x := expected
		change(&x)
		if a.Check(p, x) != ErrGrant {
			t.Fatalf("authority change %d accepted", i)
		}
	}
	other, otherA, _ := setup(t)
	foreign := grant(t, other, request(c, Foreground))
	if a.Check(foreign, expected) != ErrGrant || a.Retired(foreign) != ErrRetirement {
		t.Fatal("cross-governor identity accepted")
	}
	forged := &Grant{id: p.id, epoch: p.epoch, issuer: p.issuer}
	if a.Check(forged, expected) != ErrGrant || a.Retired(forged) != ErrRetirement || a.IOCompleted(forged, "spindle-1") != ErrIO {
		t.Fatal("forged identity accepted")
	}
	if a.Check(&Grant{}, expected) != ErrGrant || a.Check(nil, expected) != ErrGrant {
		t.Fatal("zero identity accepted")
	}
	retire(t, a, p)
	retire(t, otherA, foreign)
}

func TestSpindleRetirementSeam(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, ScanMinimum)
	r.Need = Amounts{Helper: 1, MemoryBytes: 64, IOBytes: 32}
	r.IO = []DeviceIO{{DeviceID: "physical-disk", Class: Seeking, Outstanding: 1, Bytes: 32}}
	p := grant(t, g, r)
	refused(t, g, r, Capacity)
	g.Cancel(p)
	refused(t, g, r, Capacity) // cancellation/pipe EOF cannot prove helper exit
	if err := a.IOCompleted(p, "physical-disk"); err != nil {
		t.Fatal(err)
	}
	if a.IOCompleted(p, "physical-disk") != ErrIO {
		t.Fatal("duplicate completion accepted")
	}
	u := a.Usage()[ScanMinimum]
	if u[IOBytes] != 0 || u[Helper] != 1 || u[MemoryBytes] != 64 {
		t.Fatalf("incorrect IO-only retirement: %v", u)
	}
	q := grant(t, g, r) // another probe may use spindle while first helper remains
	refused(t, g, r, Capacity)
	retire(t, a, q)
	retire(t, a, p)
	if a.Usage() != [4]Amounts{} {
		t.Fatal("double IO credit")
	}
	// A live IO grant can continue to hold helper/bytes while CPU uses its own
	// independent stage; expected authority reflects only remaining custody.
	p = grant(t, g, r)
	if err := a.IOCompleted(p, "physical-disk"); err != nil {
		t.Fatal(err)
	}
	r.IO = nil
	r.Need[IOBytes] = 0
	if err := a.Check(p, r); err != nil {
		t.Fatal(err)
	}
	cpu := request(c, ScanMinimum)
	cpu.Need = Amounts{CPUMilli: 500}
	q = grant(t, g, cpu)
	retire(t, a, q)
	retire(t, a, p)
}

func TestQualifiedSingleWriter(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, Background)
	r.Writer = true
	r.MaxHold = 5 * time.Millisecond
	r.Budget = UnitBudget{RawRows: 1, VMOps: 100, Pages: 8}
	r.Need[WALBytes] = 32
	p := grant(t, g, r)
	fg := r
	fg.Class = Foreground
	refused(t, g, fg, Capacity)
	g.Cancel(p)
	refused(t, g, fg, Capacity)
	retire(t, a, p)
	retire(t, a, grant(t, g, fg))
	for _, change := range []func(*Request){
		func(x *Request) { x.MaxHold = 26 * time.Millisecond }, func(x *Request) { x.MaxHold = 0 },
		func(x *Request) { x.Budget.RawRows = 0 }, func(x *Request) { x.Budget.VMOps = 0 },
		func(x *Request) { x.Budget.Pages = 0 }, func(x *Request) { x.Need[WALBytes] = 0 },
	} {
		x := r
		change(&x)
		refused(t, g, x, Unqualified)
	}
}

func TestBoundedValidationAndFailFast(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, Background)
	for _, change := range []func(*Request){
		func(x *Request) { x.Class = 255 }, func(x *Request) { x.Owner = 0 }, func(x *Request) { x.Need[Reader] = -1 },
		func(x *Request) { x.IO = make([]DeviceIO, 9) }, func(x *Request) { x.Footprint = make([]Scope, 17) },
		func(x *Request) { x.Key.ID = string(make([]byte, 1025)) },
		func(x *Request) {
			x.Need[IOBytes] = math.MaxInt64
			x.IO = []DeviceIO{{"d", Seeking, 1, math.MaxInt64}, {"e", Seeking, 1, 1}}
		},
		func(x *Request) {
			x.Need[IOBytes] = 64
			x.IO = []DeviceIO{{"d", Seeking, 1, 32}, {"d", Seeking, 1, 32}}
		},
		func(x *Request) { x.IO = []DeviceIO{{"d", Seeking, 2, 1}} },
	} {
		x := r
		change(&x)
		refused(t, g, x, Invalid)
	}
	x := r
	x.Deadline = c.now()
	refused(t, g, x, Deadline)
	p := grant(t, g, r)
	l := g.(*ledger)
	entered := make(chan time.Duration, 1)
	expiry := make(chan time.Time, 1)
	l.mu.timer = func(remaining time.Duration) (<-chan time.Time, func()) {
		entered <- remaining
		return expiry, func() {}
	}
	l.mu.Lock()
	refused(t, g, r, Capacity) // ordinary Try remains nonwaiting
	if a.Check(nil, r) != ErrGrant {
		l.mu.Unlock()
		t.Fatal("nil authority must still refuse immediately")
	}
	done := make(chan error, 1)
	go func() { done <- a.Check(p, r) }()
	select {
	case remaining := <-entered:
		if remaining != p.deadline.Sub(c.now()) {
			t.Error("check did not use original issuer deadline", remaining)
		}
	case err := <-done:
		l.mu.Unlock()
		t.Fatal("admitted validation must wait through contention", err)
	}
	select {
	case err := <-done:
		l.mu.Unlock()
		t.Fatal("check returned before its original deadline", err)
	default:
	}
	c.ms.Store(1000) // deliver the ORIGINAL deadline without a wall-clock sleep
	expiry <- c.now()
	if err := <-done; err != ErrGrant {
		l.mu.Unlock()
		t.Fatal("expired check must return typed grant refusal", err)
	}
	if l.find(p) == nil || l.used[r.Class] != r.Need {
		l.mu.Unlock()
		t.Fatal("validation expiry refunded physical custody")
	}
	l.mu.Unlock()
	retire(t, a, p)
	if a.Usage() != [4]Amounts{} {
		t.Fatal("invalid request mutated capacity")
	}
}

func TestIndependentEpochFenceKeepsDebt(t *testing.T) {
	g, a, c := setup(t)
	r := request(c, Foreground)
	r.Need[Reader] = 2
	p := grant(t, g, r)
	g.(*ledger).mu.Lock()
	a.EnterStatic() // must not call into a wedged admission mutex
	g.(*ledger).mu.Unlock()
	if a.Check(p, r) != ErrGrant {
		t.Fatal("old epoch accepted")
	}
	refused(t, g, r, Capacity)
	refused(t, g, request(c, Background), FailSafe)
	if a.Recover() {
		t.Fatal("recovered with live custody")
	}
	for _, cl := range []Class{Recovery, ScanMinimum} {
		q := grant(t, g, request(c, cl))
		retire(t, a, q)
	}
	retire(t, a, p)
	if !a.Recover() {
		t.Fatal("empty ledger did not recover")
	}
	retire(t, a, grant(t, g, request(c, Background)))
}

func TestSeededPhysicalLifecycle(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		g, a, c := setup(t)
		rng := rand.New(rand.NewSource(seed))
		var live []*Grant
		var truth [4]Amounts
		for step := 0; step < 200; step++ {
			if len(live) > 0 && rng.Intn(3) == 0 {
				i := rng.Intn(len(live))
				p := live[i]
				if rng.Intn(2) == 0 {
					g.Cancel(p)
				} else {
					h := a.ledger.find(p)
					for k, n := range h.request.Need {
						truth[h.request.Class][k] -= n
					}
					retire(t, a, p)
					live = slices.Delete(live, i, i+1)
				}
			} else {
				r := request(c, Class(rng.Intn(4)))
				r.Need[MemoryBytes] = int64(rng.Intn(140))
				d := g.Try(r)
				if (d.Grant == nil) == (d.Refusal == nil) {
					t.Fatal("not grant XOR refusal")
				}
				if d.Grant != nil {
					live = append(live, d.Grant)
					for k, n := range r.Need {
						truth[r.Class][k] += n
					}
				}
			}
			c.ms.Add(10)
			if a.Usage() != truth {
				t.Fatalf("seed %d step %d physical debt mismatch", seed, step)
			}
		}
		for _, p := range live {
			retire(t, a, p)
		}
		if a.Usage() != [4]Amounts{} {
			t.Fatal("lifecycle leak")
		}
	}
}

func TestConcurrentAdmissionCancelFenceRetirement(t *testing.T) {
	g, a, c := setup(t)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				r := request(c, Foreground)
				d := g.Try(r)
				if d.Grant != nil {
					g.Cancel(d.Grant)
					retire(t, a, d.Grant)
				}
			}
		})
	}
	for i := 0; i < 100; i++ {
		a.EnterStatic()
	}
	wg.Wait()
	if a.Usage() != [4]Amounts{} {
		t.Fatal("concurrent lifecycle leak")
	}
}

func TestConfigAndMetadataCapacity(t *testing.T) {
	c := &testClock{}
	config := Config{MaxGrantsPerClass: 1, Now: c.now}
	config.Partitions[Foreground][Reader] = 2
	g, a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	r := request(c, Foreground)
	p := grant(t, g, r)
	refused(t, g, r, Capacity)
	retire(t, a, p)
	// Bounded descriptor storage has its own protected partitions, too.
	bg := request(c, Background)
	bg.Need = Amounts{}
	q := grant(t, g, bg)
	refused(t, g, bg, Capacity)
	retire(t, a, grant(t, g, r))
	retire(t, a, q)
	if _, exposesRetirement := g.(Retirer); exposesRetirement {
		t.Fatal("admission capability exposes retirement")
	}
	for _, bad := range []Config{{}, {Now: c.now, MaxGrantsPerClass: 65537}, {Now: c.now, MaxGrantsPerClass: 1, Partitions: [4]Amounts{{Reader: -1}}},
		{Now: c.now, MaxGrantsPerClass: 1, Partitions: [4]Amounts{{Reader: math.MaxInt64}, {Reader: 1}}},
	} {
		if _, _, err := New(bad); err == nil {
			t.Fatal("bad configuration accepted")
		}
	}
}
