//go:build linux || darwin

package ownedcapture

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// ticketPolicy is the package's actual policy fixture plus a resolver-scoped
// preflight. It is test admission only: no production source, provider or
// helper authority. Its Admit rewrites the fixture acquisition to the exact
// resolved scalars so the real service's ticket match can be exercised.
type ticketPolicy struct {
	*policyFixture
	mu              sync.Mutex
	source          PlaybackCaptureSource
	resolveErr      error
	resolveCalls    int
	admits          int
	lastRequest     Request
	resolvedRequest Request
	bindingChange   func(*Binding)
}

func (p *ticketPolicy) ResolvePlaybackCaptureTx(_ context.Context, _ *sql.Tx, captureID string, request Request) (PlaybackCaptureSource, error) {
	p.mu.Lock()
	p.resolveCalls++
	p.resolvedRequest = request
	source, resolveErr := p.source, p.resolveErr
	p.mu.Unlock()
	if resolveErr != nil {
		return PlaybackCaptureSource{}, resolveErr
	}
	source.AssetID = request.AssetID
	source.ReadAuthorityWitness = captureID
	return source, nil
}

func (p *ticketPolicy) Admit(ctx context.Context, id string, request Request, destination Destination) (Acquisition, error) {
	p.mu.Lock()
	p.admits++
	p.lastRequest = request
	source := p.source
	change := p.bindingChange
	p.mu.Unlock()
	acquisition, e := p.policyFixture.Admit(ctx, id, request, destination)
	if e != nil {
		return nil, e
	}
	controlled, ok := acquisition.(*fakeAcquisition)
	if !ok {
		acquisition.Close()
		return nil, ErrInput
	}
	binding := controlled.b
	binding.SourceID, binding.ConfigGeneration = source.RemoteSourceID, source.EffectiveConfiguration
	binding.ObjectID, binding.ObjectRevision = source.InventoryObjectID, source.InventoryRevision
	binding.AssetID, binding.PolicyWitness, binding.AuthorityWitness = source.AssetID, source.ReadPolicyWitness, id
	if change != nil {
		change(&binding)
	}
	controlled.fakeLease.b = binding
	p.policyFixture.records[binding.ReservationID].b = binding
	return controlled, nil
}

func ticketSource(size int64) PlaybackCaptureSource {
	return PlaybackCaptureSource{
		LibraryID: "library-1", LibrarySourceID: "library-source-1", AssetID: "physical-asset",
		InventoryObjectID: "physical-object", InventoryRevision: "observation-1", RootIdentity: "/remote/root",
		Incarnation: "1", RemoteSourceID: "source", EffectiveConfiguration: "config-1",
		ReadPolicyWitness: "saved-policy-1", ReadAuthorityWitness: "unresolved",
		LibraryGeneration: 1, RawConfigurationGeneration: 1, ProfileRevision: 0, Size: size, ModifiedNS: 0,
	}
}

func ticketFixture(t *testing.T) (*Service, *ticketPolicy, *policyFixture) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.OpenFresh(filepath.Join(root, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	base := &policyFixture{data: []byte("one complete finite owned ISO byte fixture"), records: map[string]*policyRecord{}}
	policy := &ticketPolicy{policyFixture: base, source: ticketSource(int64(len(base.data)))}
	service, e := New(Options{DB: db, Directory: filepath.Join(root, "captures"), Producer: policy})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { service.Close() })
	return service, policy, base
}

func resolveTicket(t *testing.T, s *Service, ticket *PlayCaptureTicket) PlaybackCaptureSource {
	t.Helper()
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { gated.Rollback() })
	defer gated.Rollback()
	source, e := s.ResolvePlayCaptureTx(gated.Context(), gated.Tx(), ticket)
	if e != nil {
		t.Fatal(e)
	}
	return source
}

func ticketOriginal() PlayCaptureCheck { return func(context.Context, *sql.Tx) error { return nil } }

// Issuing a ticket resolves nothing, admits nothing, reads no bytes and writes
// no rows; it only fixes its eventual identity.
func TestNewPlayCaptureTicketHasNoSideEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ticket, e := s.NewPlayCaptureTicket(playCaptureContext(t), Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	if !captureID.MatchString(ticket.ID()) {
		t.Fatalf("ticket identity %q is not a private capture ID", ticket.ID())
	}
	if policy.resolveCalls != 0 || policy.admits != 0 || len(base.records) != 0 || base.reads != 0 || base.persisted != 0 {
		t.Fatal("issuing a ticket resolved, admitted, read or persisted something")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 || readyLifecycleRows(t, s) != 0 {
		t.Fatal("issuing a ticket published rows")
	}
}

// The preflight must be exact: the original asset and this ticket's own ID, and
// the resolved scalars survive a later local request mutation.
func TestResolvePlayCaptureTxPinsExactSourceAndOriginalRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, _ := ticketFixture(t)
	request := Request{AssetID: "physical-asset", Principal: identity.Principal{
		Viewer: identity.Viewer{AccountID: "account-1", ProfileID: "profile-1", ServerID: "server-1", Authority: "local", Role: "member"},
		Hash:   "hash-1",
	}}
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	request.AssetID = "mutated-asset"
	request.Principal.Hash = "mutated-hash"
	request.Principal.AccountID = "mutated-account"
	source := resolveTicket(t, s, ticket)
	if source.AssetID != "physical-asset" || source.ReadAuthorityWitness != ticket.ID() {
		t.Fatalf("preflight pinned %q with authority %q", source.AssetID, source.ReadAuthorityWitness)
	}
	if policy.resolvedRequest.AssetID != "physical-asset" || policy.resolvedRequest.Principal.Hash != "hash-1" || policy.resolvedRequest.Principal.AccountID != "account-1" {
		t.Fatal("the resolver received a mutated request or principal")
	}
	receipt, e := s.CapturePlayTicket(ctx, ticket, ticketOriginal())
	if e != nil {
		t.Fatal(e)
	}
	if receipt.ID != ticket.ID() || receipt.Binding.AssetID != "physical-asset" {
		t.Fatalf("capture %q bound asset %q", receipt.ID, receipt.Binding.AssetID)
	}
	if policy.lastRequest.AssetID != "physical-asset" || policy.lastRequest.Principal.Hash != "hash-1" || policy.lastRequest.Principal.AccountID != "account-1" {
		t.Fatal("admission received a mutated request or principal")
	}
}

// An unresolved, zero, missing-resolver or cross-service ticket is refused
// before admission, bytes or rows.
func TestCapturePlayTicketRefusesUnresolvedZeroAndForeignTickets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CapturePlayTicket(ctx, ticket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("unresolved ticket returned %v", e)
	}
	if _, e = s.CapturePlayTicket(ctx, &PlayCaptureTicket{}, ticketOriginal()); !errors.Is(e, ErrInput) {
		t.Fatalf("zero ticket returned %v", e)
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { gated.Rollback() })
	if _, e = s.ResolvePlayCaptureTx(gated.Context(), gated.Tx(), &PlayCaptureTicket{}); !errors.Is(e, ErrInput) {
		gated.Rollback()
		t.Fatalf("resolving a zero ticket returned %v", e)
	}
	gated.Rollback()
	other, otherPolicy, _ := ticketFixture(t)
	if _, e = other.ResolvePlayCaptureTx(context.Background(), nil, ticket); !errors.Is(e, ErrInput) {
		t.Fatalf("resolving with a nil transaction returned %v", e)
	}
	foreignGate, e := dbwork.Begin(context.Background(), other.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { foreignGate.Rollback() })
	if _, e = other.ResolvePlayCaptureTx(foreignGate.Context(), foreignGate.Tx(), ticket); !errors.Is(e, ErrInput) {
		t.Fatalf("cross-service ticket with a real transaction returned %v", e)
	}
	foreignGate.Rollback()
	otherCtx := playCaptureContext(t)
	foreign, e := other.NewPlayCaptureTicket(otherCtx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CapturePlayTicket(otherCtx, foreign, ticketOriginal()); !errors.Is(e, ErrInput) {
		t.Fatalf("cross-service ticket returned %v", e)
	}
	if policy.admits != 0 || otherPolicy.admits != 0 || len(base.records) != 0 || base.reads != 0 {
		t.Fatal("a refused ticket reached admission or bytes")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 {
		t.Fatal("a refused ticket published rows")
	}
}

// Without a resolver the producer cannot issue a preflight, and the ticket is
// terminally refused rather than silently admitted.
func TestCapturePlayTicketRefusesMissingResolver(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, base := fixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { gated.Rollback() })
	_, e = s.ResolvePlayCaptureTx(gated.Context(), gated.Tx(), ticket)
	gated.Rollback()
	if !errors.Is(e, ErrCurrent) {
		t.Fatalf("missing resolver returned %v", e)
	}
	if _, e = s.CapturePlayTicket(ctx, ticket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("refused ticket returned %v", e)
	}
	if len(base.records) != 0 || base.reads != 0 {
		t.Fatal("a missing resolver still reached admission or bytes")
	}
}

// A value copy shares one consume-once latch: the first capture takes the
// ticket identity exactly once and the copy cannot admit again.
func TestCapturePlayTicketCopiesConsumeOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	copyOfTicket := *ticket
	receipt, e := s.CapturePlayTicket(ctx, ticket, ticketOriginal())
	if e != nil {
		t.Fatal(e)
	}
	if receipt.ID != ticket.ID() {
		t.Fatalf("capture used %q instead of the ticket identity %q", receipt.ID, ticket.ID())
	}
	if _, e = s.CapturePlayTicket(ctx, &copyOfTicket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("copied ticket returned %v", e)
	}
	if policy.admits != 1 || len(base.records) != 1 {
		t.Fatalf("tickets admitted %d times over %d reservations", policy.admits, len(base.records))
	}
	if countRows(t, s, "owned_capture_receipts") != 1 || readyLifecycleRows(t, s) != 1 {
		t.Fatal("a copied ticket published a second receipt")
	}
}

// A same-ticket policy drift refuses and latches; restoring the original source
// cannot revive it.
func TestCapturePlayTicketRefusesDriftAndRestoration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	policy.mu.Lock()
	policy.source.ReadPolicyWitness = "changed-policy"
	policy.mu.Unlock()
	if _, e = s.CapturePlayTicket(ctx, ticket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("drifted source returned %v", e)
	}
	if policy.admits != 0 || len(base.records) != 0 || base.reads != 0 {
		t.Fatal("a drifted source reached admission or bytes")
	}
	policy.mu.Lock()
	policy.source = ticketSource(int64(len(base.data)))
	policy.mu.Unlock()
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { gated.Rollback() })
	_, e = s.ResolvePlayCaptureTx(gated.Context(), gated.Tx(), ticket)
	gated.Rollback()
	if !errors.Is(e, ErrCurrent) {
		t.Fatalf("restored source revived the ticket with %v", e)
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 {
		t.Fatal("a drifted ticket published rows")
	}
}

// An acquisition whose actual binding differs from the ticket's preflight is
// refused before any owned row or byte is produced.
func TestCapturePlayTicketRefusesWrongAcquisitionBindingBeforeWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	policy.mu.Lock()
	policy.bindingChange = func(b *Binding) { b.ObjectID = "other-object" }
	policy.mu.Unlock()
	if _, e = s.CapturePlayTicket(ctx, ticket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("wrong acquisition binding returned %v", e)
	}
	if policy.admits != 1 {
		t.Fatalf("the fixture admitted %d times", policy.admits)
	}
	if base.reads != 0 || base.persisted != 0 {
		t.Fatal("a mismatched acquisition read or persisted bytes")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 || countRows(t, s, "owned_capture_lifecycle") != 0 {
		t.Fatal("a mismatched acquisition published owned rows")
	}
}

// A later operation cannot extend the ticket's original deadline.
func TestCapturePlayTicketCannotExtendDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, _ := ticketFixture(t)
	short, cancelShort := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShort()
	ticket, e := s.NewPlayCaptureTicket(short, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	if _, e = s.CapturePlayTicket(playCaptureContext(t), ticket, ticketOriginal()); !errors.Is(e, ErrInput) {
		t.Fatalf("extended deadline returned %v", e)
	}
	if policy.admits != 0 {
		t.Fatal("an extended deadline still admitted a source")
	}
}

// The mandatory original operation check still runs before admission, and a
// ticket that failed there is spent: it can never be retried.
func TestCapturePlayTicketKeepsMandatoryOriginalCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ctx := playCaptureContext(t)
	ticket, e := s.NewPlayCaptureTicket(ctx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	retired := errors.New("original operation retired")
	if _, e = s.CapturePlayTicket(ctx, ticket, func(context.Context, *sql.Tx) error { return retired }); !errors.Is(e, retired) {
		t.Fatalf("retired original returned %v", e)
	}
	if policy.admits != 0 || len(base.records) != 0 || base.reads != 0 {
		t.Fatal("a refused original still admitted or read")
	}
	if _, e = s.CapturePlayTicket(ctx, ticket, ticketOriginal()); !errors.Is(e, ErrCurrent) {
		t.Fatalf("spent ticket retried with %v", e)
	}
	if policy.admits != 0 {
		t.Fatal("a spent ticket admitted a second source")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 {
		t.Fatal("a spent ticket published a receipt")
	}
}

// Concurrent value copies race one atomic latch; exactly one wins the single
// admission and the other is refused without touching the fixture maps.
func TestCapturePlayTicketConcurrentCopiesAdmitOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, policy, base := ticketFixture(t)
	ticketCtx, cancelTicket := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancelTicket)
	ticket, e := s.NewPlayCaptureTicket(ticketCtx, Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	resolveTicket(t, s, ticket)
	copied := *ticket
	type outcome struct {
		receipt Receipt
		err     error
	}
	results := make(chan outcome, 2)
	done := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	start := make(chan struct{})
	var startOnce sync.Once
	unblock := func() { startOnce.Do(func() { close(start) }) }
	// Both workers are joined before the fixture service and database close.
	t.Cleanup(func() {
		cancelTicket()
		unblock()
		for i := range done {
			select {
			case <-done[i]:
			case <-time.After(3 * time.Second):
				t.Error("concurrent ticket capture did not join")
			}
		}
	})
	run := func(issued *PlayCaptureTicket, index int) {
		defer close(done[index])
		<-start
		receipt, err := s.CapturePlayTicket(ticketCtx, issued, ticketOriginal())
		results <- outcome{receipt, err}
	}
	go run(ticket, 0)
	go run(&copied, 1)
	unblock()
	won := 0
	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			if got.err == nil {
				won++
				if got.receipt.ID != ticket.ID() {
					t.Fatalf("winning capture used %q", got.receipt.ID)
				}
			} else if !errors.Is(got.err, ErrCurrent) {
				t.Fatalf("losing copy returned %v", got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent ticket captures did not finish")
		}
	}
	if won != 1 {
		t.Fatalf("%d concurrent copies won the single admission", won)
	}
	if policy.admits != 1 || len(base.records) != 1 {
		t.Fatalf("concurrent copies admitted %d times over %d reservations", policy.admits, len(base.records))
	}
	if countRows(t, s, "owned_capture_receipts") != 1 || readyLifecycleRows(t, s) != 1 {
		t.Fatal("concurrent copies published more than one receipt")
	}
}
