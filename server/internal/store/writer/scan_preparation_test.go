package writer

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/stateownership"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Reproduce cancellation specifically BETWEEN constructor release checks.
// The native grant remains issuer-owned on failure, while every constructor
// borrow must be released even when the last check discards its only handle.
type preparationFinishCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	finishes atomic.Int32
}

func (c *preparationFinishCancelContext) Err() error {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, ".finishCommand[") {
			if c.finishes.Add(1) == 2 {
				c.cancel()
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestScanPreparationConstructorFinalFinishCancellation(t *testing.T) {
	opening, owner, r, request, grant := scanPreparationFundingFixture(t, time.Now)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &preparationFinishCancelContext{Context: base, cancel: cancel}
	p, err := newScanPreparation(ctx, opening, owner, r, nativeinventory.NewObserver(&scanObservationFaultTransport{}), request, grant,
		nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), Limit: 1})
	if p != nil {
		_ = p.Close(context.Background())
		t.Fatalf("cancellation missed final release: calls=%d err=%v", ctx.finishes.Load(), err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("constructor refunded issuer-owned grant")
	}
	if err := r.authority.Retired(grant); err != nil {
		t.Fatal(err)
	}
	if err := opening.close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("discarded capability retained native borrow: %v (finish checks=%d)", err, ctx.finishes.Load())
	}
}

func TestScanPreparationCanceledBusyAdmissionCannotRetry(t *testing.T) {
	var armed atomic.Bool
	entered, release, contended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var clockCalls atomic.Int32
	var releaseOnce sync.Once
	releaseClock := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseClock()
	now := func() time.Time {
		if armed.Load() {
			switch clockCalls.Add(1) {
			case 1:
				close(entered)
				<-release
			case 2:
				close(contended) // bounded-wait deadline read, outside the held mutex
			}
		}
		return time.Now()
	}
	opening, owner, r, request, grant := scanPreparationFundingFixture(t, now)
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.NewObserver(&scanObservationFaultTransport{}), request, grant,
		nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), Limit: 1})
	if err != nil {
		_ = r.authority.Retired(grant)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	armed.Store(true)
	done := make(chan error, 1)
	go func() { done <- r.authority.Check(grant, request) }()
	<-entered // the actual ledger mutex is held by Authority.Check's clock
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	type observation struct {
		value nativeinventory.Page
		err   error
	}
	observed := make(chan observation, 1)
	go func() { value, err := p.ObserveInventory(ctx); observed <- observation{value, err} }()
	<-contended
	armed.Store(false)
	releaseClock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	firstResult := <-observed
	value, first := firstResult.value, firstResult.err
	if !errors.Is(first, context.Canceled) || errors.Is(first, governor.ErrBusy) || value.Version != 0 {
		t.Fatal("missed canceled/contended interleaving", value, first)
	}
	value, err = p.ObserveInventory(t.Context())
	if err == nil || value.Version != 0 {
		t.Fatalf("canceled preparation revived: version=%d err=%v failed=%v", value.Version, err, p.failed)
	}
}

// Deliberate transport fault fixture only; never a production observer/source
// or resource proof. Scope/opening/governor ownership are the real W objects.
type scanObservationFaultTransport struct {
	page string // optional controlled native wire fixture; empty preserves existing controls

	preflight     chan struct{}
	release       chan struct{}
	panicExchange bool
	once          sync.Once
	monitor       nativeinventory.Monitor
}

func (f *scanObservationFaultTransport) NativeInventoryPreflight(r nativeinventory.Request) ([]byte, error) {
	if f.preflight != nil {
		f.once.Do(func() { close(f.preflight) })
		<-f.release
	}
	return r.Root, nil
}
func (f *scanObservationFaultTransport) NativeInventorySettings() nativeinventory.Settings {
	return nativeinventory.Settings{Timeout: time.Second, Monitor: &f.monitor}
}
func (f *scanObservationFaultTransport) NativeInventoryExchange(ctx context.Context, e *nativeinventory.Exchange) error {
	// The fault path still retires its actual Exchange; the test must not turn
	// a transport panic into leaked fictional debt or an automatic refund.
	defer e.Retire()
	if f.panicExchange {
		panic("controlled observer panic")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := `{"Version":1,"RootIdentity":"root","DirectoryIdentity":"directory","DirectoryRevision":"revision","Complete":true,"Entries":[{"Path":"L21lZGlhL2EubWt2","Name":"YS5ta3Y=","ObjectIdentity":"native-object","ChangeToken":"token","Size":1,"ModifiedNS":1}],"Issues":[{"Path":"L21lZGlhL2lzc3Vl","Reason":"dW5yZWFkYWJsZQ=="}]}`
	if f.page != "" {
		payload = f.page
	}
	return e.Consume(strings.NewReader(payload))
}

func scanPreparationFundingFixture(t *testing.T, now func() time.Time, memory ...int64) (*scanOpening, *stateownership.Lock, *effectiveRuntime, governor.Request, *governor.Grant) {
	t.Helper()
	path, owner := scanOpeningFixture(t)
	opening := openScanFixture(t, path, owner)
	profile := governedFixtureProfile{testRecipe(), 65536}
	if len(memory) > 1 {
		t.Fatal("multiple synthetic funding profiles")
	}
	if len(memory) == 1 {
		profile.memoryBytes = memory[0]
	}
	g, a, wal, request := governedFixture(t, now, profile)
	request.Writer = false
	request.Key = governor.JobKey{Domain: uint16(InventoryCreate), ID: "prepared-original"}
	request.Deadline = time.Now().Add(10 * time.Second)
	request.Need[governor.WALBytes] = 0
	request.Footprint = []governor.Scope{{Library: 7, Domain: uint16(InventoryCreate)}}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	r := &effectiveRuntime{executor: opening.executor, path: path, owner: request.Owner, governor: g, authority: a, wal: wal}
	return opening, owner, r, request, decision.Grant
}

func TestScanPreparationInputCoverageBeforeConstruction(t *testing.T) {
	opening, owner, r, request, grant := scanPreparationFundingFixture(t, time.Now, 1)
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.NewObserver(&scanObservationFaultTransport{}), request, grant,
		nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), Limit: 1})
	var refusal *Refusal
	if p != nil || !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
		t.Fatal("unfunded input allocated a preparation", p, err)
	}
	if r.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("constructor refunded unconsumed caller grant")
	}
	if err := r.authority.Retired(grant); err != nil {
		t.Fatal(err)
	}
}

func scanPreparationFixture(t *testing.T, transport *scanObservationFaultTransport) (*scanPreparation, *governor.Authority) {
	t.Helper()
	opening, owner, r, request, grant := scanPreparationFundingFixture(t, time.Now)
	a := r.authority
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.NewObserver(transport), request, grant,
		nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), RootIdentity: "root", DirectoryRevision: "revision", Limit: 1})
	if err != nil {
		_ = a.Retired(grant)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return p, a
}

func TestScanPreparationConstructorLateCancellationReturnsNoCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	armed, calls := false, 0
	now := func() time.Time {
		if armed {
			calls++
			if calls == 2 {
				cancel()
			}
		}
		return time.Now()
	}
	opening, owner, r, request, grant := scanPreparationFundingFixture(t, now)
	armed = true
	p, err := newScanPreparation(ctx, opening, owner, r, nativeinventory.NewObserver(&scanObservationFaultTransport{}), request, grant,
		nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), RootIdentity: "root", Limit: 1})
	if calls != 2 || p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late constructor cancellation leaked capability", calls, p, err)
	}
	if r.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("failed constructor refunded caller-owned grant")
	}
	if err := r.authority.Retired(grant); err != nil {
		t.Fatal(err)
	}
	if err := opening.close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal("constructor leaked physical borrow", err)
	}
}

func TestScanPreparationObserveRetainsOriginalFundingUntilClose(t *testing.T) {
	p, a := scanPreparationFixture(t, &scanObservationFaultTransport{})
	before := a.Usage()
	value, err := p.ObserveInventory(t.Context())
	if err != nil || value.DirectoryIdentity != "directory" || !value.Complete {
		t.Fatal(value, err)
	}
	if a.Usage() != before || p.scope.Active() != 0 {
		t.Fatal("helper return refunded original prepared lifetime")
	}
	value.Entries[0].Name[0] = '!'
	value.Issues[0].Reason[0] = '!'
	if string(p.observed.Entries[0].Name) != "a.mkv" || string(p.observed.Issues[0].Reason) != "unreadable" {
		t.Fatal("producer mutation changed private native observation")
	}
	if next, err := p.ObserveInventory(t.Context()); err == nil || next.Version != 0 {
		t.Fatal("same preparation observed a second original", next, err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("joined preparation retained its grant")
	}
	if value, err := p.ObserveInventory(t.Context()); err == nil || value.Version != 0 {
		t.Fatal("closed preparation revived", value, err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal("duplicate close", err)
	}
}

func TestScanPreparationCloseRetainsPreflightInvocation(t *testing.T) {
	fault := &scanObservationFaultTransport{preflight: make(chan struct{}), release: make(chan struct{})}
	p, a := scanPreparationFixture(t, fault)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(fault.release) }) }
	defer release()
	done := make(chan error, 1)
	go func() {
		value, err := p.ObserveInventory(t.Context())
		if value.Version != 0 {
			done <- errors.New("canceled preflight disclosed a page")
			return
		}
		done <- err
	}()
	<-fault.preflight
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.Close(ctx); !errors.Is(err, nativeinventory.ErrHelperRetirementPending) {
		t.Fatal("preflight was not held", err)
	}
	if p.scope.Active() != 1 || a.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("canceled preflight lost physical/funded custody")
	}
	// Release the controlled preflight once; its later observer Acquire must
	// fail on the SAME closed Scope rather than starting an unowned helper.
	release()
	if err := <-done; err == nil {
		t.Fatal("closed preflight returned success")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("retired preflight remained charged")
	}
}

func TestScanPreparationPanicIsStickyAndDoesNotRefund(t *testing.T) {
	p, a := scanPreparationFixture(t, &scanObservationFaultTransport{panicExchange: true})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("missing controlled panic")
			}
		}()
		_, _ = p.ObserveInventory(t.Context())
	}()
	if !p.failed || p.scope.Active() != 0 || a.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("panic revived or refunded preparation")
	}
	if value, err := p.ObserveInventory(t.Context()); err == nil || value.Version != 0 {
		t.Fatal("panic allowed retry", value, err)
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
