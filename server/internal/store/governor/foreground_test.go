package governor

import (
	"context"
	"testing"
	"time"
)

func foregroundSetup(t *testing.T) (Governor, *Authority, *testClock) {
	t.Helper()
	_, _, clock := setup(t)
	c := Config{MaxGrantsPerClass: 64, Now: clock.now, Foreground: &ForegroundProfile{
		AssignedBytes: 2_000_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1360 << 20,
		MaxWaiters: 2, MinimumLive: 8, BookkeepingBytes: 4096, MaxWait: 40 * time.Millisecond}}
	c.Partitions[Foreground] = Amounts{Reader: 8, MemoryBytes: 224 << 20, QueueBytes: 1 << 20}
	g, a, err := newGovernor(c, 0)
	if err != nil {
		t.Fatal(err)
	}
	return g, a, clock
}
func foregroundQ(clock *testClock) ForegroundRequest {
	return ForegroundRequest{Key: JobKey{Domain: 4, ID: "browse"}, Owner: 1, ReadyDeadline: clock.now().Add(60 * time.Millisecond), ProtectedDeadline: clock.now().Add(time.Second)}
}
func admitLive(t *testing.T, a *Authority, q ForegroundRequest) *Grant {
	t.Helper()
	d := a.AdmitForeground(t.Context(), q)
	if d.Grant == nil || d.Refusal != nil {
		t.Fatal("live admission", d)
	}
	return d.Grant
}
func fillLive(t *testing.T, a *Authority, q ForegroundRequest) []*Grant {
	t.Helper()
	var all []*Grant
	for i := 0; i < 8; i++ {
		all = append(all, admitLive(t, a, q))
	}
	return all
}
func retireLive(t *testing.T, a *Authority, all []*Grant) {
	t.Helper()
	for _, p := range all {
		retire(t, a, p)
	}
}

func TestForegroundCapacityServerBudgetAndProfileConflicts(t *testing.T) {
	for _, tc := range []struct {
		m, b int64
		want int
	}{
		{2_000_000_000, 1360 << 20, 8}, {4_000_000_000, 2720 << 20, 16}, {8_000_000_000, 5440 << 20, 33},
	} {
		p := ForegroundProfile{AssignedBytes: tc.m, EffectiveBytes: tc.m, NonBrowseBytes: tc.b, MinimumLive: 8}
		n, e := p.Capacity()
		if e != nil || n != tc.want {
			t.Fatal(tc, n, e)
		}
	}
	p := ForegroundProfile{AssignedBytes: 8_000_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1360 << 20, MinimumLive: 8}
	if n, e := p.Capacity(); e != nil || n != 8 {
		t.Fatal("effective ceiling ignored", n, e)
	}
	p.AssignedBytes = 2_000_000_000
	p.EffectiveBytes = 8_000_000_000
	if n, e := p.Capacity(); e != nil || n != 8 {
		t.Fatal("host increase invented slots", n, e)
	}
	for _, b := range []int64{0, 1500 << 20, 2_000_000_000} {
		p.NonBrowseBytes = b
		if _, e := p.Capacity(); e == nil {
			t.Fatal("unsafe floor/unknown budget", b)
		}
	}
	p = ForegroundProfile{AssignedBytes: 1_600_000_000, EffectiveBytes: 1_600_000_000, NonBrowseBytes: 1360 << 20, MinimumLive: 8}
	if _, e := p.Capacity(); e == nil {
		t.Fatal("unfunded 1.6GB profile silently admitted baseline")
	}
	g, a, clock := foregroundSetup(t)
	cfg := g.(*ledger).config
	cfg.Foreground = &ForegroundProfile{AssignedBytes: 2_000_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1360 << 20, BookkeepingBytes: 4096, MinimumLive: 8, MaxWaiters: 2, MaxWait: 40 * time.Millisecond}
	for _, change := range []func(*Config){
		func(c *Config) { c.MaxGrantsPerClass = 8 },
		func(c *Config) { c.Partitions[Foreground][MemoryBytes] = 160 << 20 },
		func(c *Config) { c.Partitions[Foreground][QueueBytes] = 0 },
		func(c *Config) { c.Foreground.BookkeepingBytes = 1 },
		func(c *Config) { c.Foreground.MaxWaiters = -1 },
		func(c *Config) { c.Foreground.MaxWait = time.Second },
	} {
		c := cfg
		p := *cfg.Foreground
		c.Foreground = &p
		change(&c)
		if _, _, e := newGovernor(c, 0); e == nil {
			t.Fatal("unfunded/unbounded configuration accepted")
		}
	}
	cfg.Foreground.MaxWaiters = 100 // caller-owned pointer never retained by the existing ledger
	p0 := admitLive(t, a, foregroundQ(clock))
	retire(t, a, p0)
	if a.ForegroundUsage().Capacity != 8 {
		t.Fatal("profile mutated")
	}
}

func TestForegroundLiveOutlastsWindowsCancelExpiryAndOutputOnly(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	defer retireLive(t, a, all)
	if a.ForegroundUsage().Live != 8 {
		t.Fatal(a.ForegroundUsage())
	}
	// These are separate actual read/output resource holdings. Returning the
	// read leaves output/live custody; returning output still cannot attest that
	// the external consumer has relinquished its page.
	r := request(clock, Foreground)
	r.Need = Amounts{Reader: 1, MemoryBytes: ForegroundReadBytes}
	read := grant(t, g, r)
	r.Need = Amounts{MemoryBytes: 8 << 20}
	output := grant(t, g, r)
	retire(t, a, read)
	g.Cancel(all[0])
	clock.ms.Store(1001)
	if a.ForegroundUsage().Live != 8 {
		t.Fatal("native close/cancel/expiry released LIVE")
	}
	retire(t, a, output)
	if a.ForegroundUsage().Live != 8 {
		t.Fatal("output grant alone released consumer")
	}
	if a.CheckForeground(all[0], q) != ErrGrant {
		t.Fatal("expired/cancelled live grant usable")
	}
	// Immediate canceled admission cannot add payload or a ninth live request.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := a.AdmitForeground(ctx, foregroundQ(clock))
	if d.Grant != nil || d.Refusal == nil || a.ForegroundUsage().Live != 8 {
		t.Fatal(d, a.ForegroundUsage())
	}
	if a.Usage()[Foreground][Reader] != 0 || a.Usage()[Foreground][MemoryBytes] != 8*4096 {
		t.Fatal("unowned read/output debt", a.Usage())
	}
}

func TestForegroundFiniteChargedFIFOAndPhysicalRelease(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	entered := make(chan struct{}, 2)
	g.(*ledger).foregroundWait = func(ctx context.Context, _ time.Time, wake <-chan struct{}) {
		entered <- struct{}{}
		if a.ForegroundUsage().Waiters > 2 {
			t.Error("unbounded waiter admitted")
			return
		}
		select {
		case <-wake:
		case <-ctx.Done():
		}
	}
	one, two := make(chan Decision, 1), make(chan Decision, 1)
	go func() { one <- a.AdmitForeground(t.Context(), q) }()
	<-entered
	go func() { two <- a.AdmitForeground(t.Context(), q) }()
	<-entered
	before := a.Usage()
	d := a.AdmitForeground(t.Context(), q)
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Capacity || d.Refusal.RetryAt.After(q.ReadyDeadline) || a.Usage() != before {
		t.Fatal("unbounded waiter", d, a.Usage())
	}
	if s := a.ForegroundUsage(); s.Live != 8 || s.Waiters != 2 || s.BookkeepingBytes != 10*4096 || a.Usage()[Foreground][Reader] != 0 {
		t.Fatal("uncharged waiter or page preallocation", s, a.Usage())
	}
	g.Cancel(all[0])
	select {
	case <-one:
		t.Fatal("cancel refunded live")
	default:
	}
	retire(t, a, all[0])
	d = <-one
	if d.Grant == nil || d.Refusal != nil {
		t.Fatal("oldest waiter lost", d)
	}
	select {
	case <-two:
		t.Fatal("second queue jumped FIFO")
	default:
	}
	if a.CheckForeground(d.Grant, q) != nil {
		t.Fatal("promoted descriptor changed")
	}
	retire(t, a, d.Grant)
	d = <-two
	if d.Grant == nil {
		t.Fatal(d)
	}
	retire(t, a, d.Grant)
	retireLive(t, a, all[1:])
	if a.Usage() != [4]Amounts{} || a.ForegroundUsage().Live != 0 || a.ForegroundUsage().Waiters != 0 {
		t.Fatal("leaked live/wait custody")
	}
}

func TestForegroundOriginalDeadlinesAndCancelledWaiters(t *testing.T) {
	for _, which := range []string{"ready", "protected", "brief", "cancel"} {
		t.Run(which, func(t *testing.T) {
			g, a, clock := foregroundSetup(t)
			q := foregroundQ(clock)
			all := fillLive(t, a, q)
			defer retireLive(t, a, all)
			want := 40 * time.Millisecond
			if which == "ready" {
				q.ReadyDeadline = clock.now().Add(7 * time.Millisecond)
				want = 7 * time.Millisecond
			}
			if which == "protected" {
				q.ProtectedDeadline = clock.now().Add(9 * time.Millisecond)
				want = 9 * time.Millisecond
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			g.(*ledger).foregroundWait = func(_ context.Context, until time.Time, _ <-chan struct{}) {
				if until.Sub(clock.now()) != want {
					t.Fatal("original wait budget renewed", until, want)
				}
				if which == "cancel" {
					cancel()
				} else {
					clock.ms.Add(want.Milliseconds())
				}
			}
			d := a.AdmitForeground(ctx, q)
			if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Deadline || a.ForegroundUsage().Waiters != 0 || a.ForegroundUsage().Live != 8 || a.Usage()[Foreground][MemoryBytes] != 8*4096 {
				t.Fatal(d, a.ForegroundUsage(), a.Usage())
			}
		})
	}
}

func TestForegroundDescriptorNotPayloadAuthorityAndExactCheck(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	p := admitLive(t, a, q)
	defer retire(t, a, p)
	if a.CheckForeground(p, q) != nil {
		t.Fatal("live descriptor not valid")
	}
	for _, change := range []func(*ForegroundRequest){func(q *ForegroundRequest) { q.Owner++ }, func(q *ForegroundRequest) { q.Key.ID = "other" }, func(q *ForegroundRequest) { q.ReadyDeadline = q.ReadyDeadline.Add(time.Millisecond) }, func(q *ForegroundRequest) { q.ReadyDeadline = time.Time{} }, func(q *ForegroundRequest) { q.ProtectedDeadline = q.ProtectedDeadline.Add(time.Millisecond) }} {
		other := q
		change(&other)
		if a.CheckForeground(p, other) == nil {
			t.Fatal("foreign/modified exact descriptor accepted")
		}
	}
	// A matching ordinary metadata grant cannot impersonate live admission.
	ordinary := grant(t, g, g.(*ledger).foregroundRequest(q))
	defer retire(t, a, ordinary)
	if a.CheckForeground(ordinary, q) == nil {
		t.Fatal("ordinary grant forged LIVE")
	}
	payload := g.(*ledger).foregroundRequest(q)
	payload.Need[MemoryBytes] = 8 << 20
	if a.Check(p, payload) != ErrGrant {
		t.Fatal("descriptor spent as output payload")
	}
	_, other, _ := foregroundSetup(t)
	if other.Retired(p) != ErrRetirement || other.CheckForeground(p, q) == nil {
		t.Fatal("foreign ledger released/accepted LIVE")
	}
}

// Actual waiter cancellation is a control event, not a wall-clock-speed test.
func TestForegroundActualWaitCancellationAndNoRenewedExecutionBudget(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	entered := make(chan struct{})
	g.(*ledger).foregroundWait = func(ctx context.Context, until time.Time, wake <-chan struct{}) {
		close(entered)
		g.(*ledger).waitForeground(ctx, until, wake)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan Decision, 1)
	go func() { done <- a.AdmitForeground(ctx, q) }()
	<-entered
	cancel()
	d := <-done
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Deadline || a.ForegroundUsage().Waiters != 0 {
		t.Fatal(d, a.ForegroundUsage())
	}
	// Queued work receives the ORIGINAL ready/protected descriptor after
	// physical release, with only the residual 30ms ready budget remaining.
	entered = make(chan struct{})
	g.(*ledger).foregroundWait = func(_ context.Context, _ time.Time, wake <-chan struct{}) { close(entered); <-wake }
	go func() { done <- a.AdmitForeground(t.Context(), q) }()
	<-entered
	clock.ms.Add(30)
	retire(t, a, all[0])
	d = <-done
	if d.Grant == nil || a.CheckForeground(d.Grant, q) != nil {
		t.Fatal(d)
	}
	changed := q
	changed.ReadyDeadline = clock.now().Add(60 * time.Millisecond)
	if a.CheckForeground(d.Grant, changed) == nil {
		t.Fatal("wait renewed execution descriptor")
	}
	if q.ReadyDeadline.Sub(clock.now()) != 30*time.Millisecond {
		t.Fatal("lost remaining original ready budget")
	}
	retire(t, a, d.Grant)
	retireLive(t, a, all[1:])
}

func TestForegroundEpochFenceForgeryAndDuplicateReturn(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	a.EnterStatic()
	if a.CheckForeground(all[0], q) != ErrGrant || a.ForegroundUsage().Live != 8 {
		t.Fatal("epoch fence refunded LIVE")
	}
	fake := &Grant{id: all[0].id, epoch: all[0].epoch, issuer: g.(*ledger)}
	if a.Retired(fake) != ErrRetirement || a.ForegroundUsage().Live != 8 {
		t.Fatal("forgery released LIVE")
	}
	retireLive(t, a, all)
	if a.Retired(all[0]) != ErrRetirement || a.Usage() != [4]Amounts{} || a.ForegroundUsage().Live != 0 {
		t.Fatal("duplicate release or metadata leak")
	}
	plain, plainA, c := setup(t)
	if plainA.AdmitForeground(t.Context(), foregroundQ(c)).Refusal == nil {
		t.Fatal("absent profile admitted LIVE")
	}
	if plain == nil {
		t.Fatal("missing pure-ledger fixture")
	}
}

func TestForegroundCeilingReserveScaledAdmissionAndImmutableProfile(t *testing.T) {
	m := int64(2_000_000_001)
	p := ForegroundProfile{AssignedBytes: m, EffectiveBytes: m, NonBrowseBytes: m - m/5 - 8*ForegroundRequestBytes, MinimumLive: 8}
	if _, e := p.Capacity(); e == nil {
		t.Fatal("floor(.2M) spent protected byte")
	}
	g, _, clock := foregroundSetup(t)
	cfg := g.(*ledger).config
	p = ForegroundProfile{AssignedBytes: 4_000_000_000, EffectiveBytes: 4_000_000_000, NonBrowseBytes: 2720 << 20, BookkeepingBytes: 4096, MinimumLive: 8, MaxWaiters: 0, MaxWait: 40 * time.Millisecond}
	cfg.Foreground = &p
	cfg.Partitions[Foreground][MemoryBytes] = 512 << 20
	_, a, e := newGovernor(cfg, 0)
	if e != nil {
		t.Fatal(e)
	}
	p.AssignedBytes = 0
	p.NonBrowseBytes = 0 // actual caller-owned object passed to New
	if a.ForegroundUsage().Capacity != 16 {
		t.Fatal("global8 or mutable profile", a.ForegroundUsage())
	}
	var all []*Grant
	for i := 0; i < 16; i++ {
		all = append(all, admitLive(t, a, foregroundQ(clock)))
	}
	d := a.AdmitForeground(t.Context(), foregroundQ(clock))
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Capacity {
		t.Fatal("scaled cap not enforced", d)
	}
	retireLive(t, a, all)
}

func TestForegroundExpiredAtHandoffDoesNotLeakPromotedSlot(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	entered := make(chan struct{})
	done := make(chan Decision, 1)
	g.(*ledger).foregroundWait = func(_ context.Context, _ time.Time, wake <-chan struct{}) { close(entered); <-wake; clock.ms.Add(41) }
	go func() { done <- a.AdmitForeground(t.Context(), q) }()
	<-entered
	retire(t, a, all[0])
	d := <-done
	if d.Grant != nil || d.Refusal == nil || a.ForegroundUsage().Live != 7 || a.ForegroundUsage().Waiters != 0 {
		t.Fatal("late handoff leaked LIVE", d, a.ForegroundUsage())
	}
	retireLive(t, a, all[1:])
	if a.Usage() != [4]Amounts{} {
		t.Fatal("late handoff lost metadata", a.Usage())
	}
}

func TestForegroundPhysical2GBProfileContingencyNoFifth(t *testing.T) {
	g, _, clock := foregroundSetup(t)
	cfg := g.(*ledger).config
	p := ForegroundProfile{AssignedBytes: 1_600_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1140 << 20, MinimumLive: 4, BookkeepingBytes: 4096, MaxWaiters: 0, MaxWait: 40 * time.Millisecond}
	cfg.Foreground = &p
	_, a, e := newGovernor(cfg, 0)
	if e != nil {
		t.Fatal(e)
	}
	if a.ForegroundUsage().Capacity != 4 {
		t.Fatal("profile contingency spent", a.ForegroundUsage())
	}
	var all []*Grant
	for i := 0; i < 4; i++ {
		all = append(all, admitLive(t, a, foregroundQ(clock)))
	}
	d := a.AdmitForeground(t.Context(), foregroundQ(clock))
	if d.Grant != nil || d.Refusal == nil || d.Refusal.Code != Capacity {
		t.Fatal("fifth request admitted", d)
	}
	retireLive(t, a, all)
	// Provider fallback changes no frozen profile input and creates no slots.
	if a.ForegroundUsage().Capacity != 4 {
		t.Fatal("capacity changed without profile")
	}
}

type foregroundDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c foregroundDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func TestForegroundParentDeadlineAndImmediateExpiry(t *testing.T) {
	g, a, clock := foregroundSetup(t)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	defer retireLive(t, a, all)
	ctx := foregroundDeadlineContext{t.Context(), clock.now().Add(3 * time.Millisecond)}
	g.(*ledger).foregroundWait = func(_ context.Context, until time.Time, _ <-chan struct{}) {
		if !until.Equal(ctx.deadline) {
			t.Fatal("parent deadline ignored", until)
		}
		clock.ms.Add(3)
	}
	d := a.AdmitForeground(ctx, q)
	if d.Grant != nil || d.Refusal == nil || a.ForegroundUsage().Waiters != 0 {
		t.Fatal(d)
	}
	g.(*ledger).waitForeground(t.Context(), clock.now().Add(-time.Second), make(chan struct{}))
}
