package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Real schema/Capture/governor/frame and canonical receipts; synthetic source,
// clock and resource figures. No production issuer, birth/exclusion authority,
// legal-MAX or physical qualification is supplied by this component fixture.
func scanLifecycleExecutionFixture(t *testing.T) (*executor, *stmt.Registry, *inventoryCreateCommand) {
	t.Helper()
	w, _, _ := testExecutor(t)
	in := seedScanLifecycle(t, w)
	s := in.scope
	command, err := newInventoryCreateCommand(in.identity, inventoryCreateRequest{
		Scope: inventoryScope{runID: s.runID, libraryID: s.libraryID, libraryKey: s.libraryKey},
		Source: inventorySource{id: s.sourceID, incarnation: s.incarnation, rootIdentity: s.rootIdentity,
			resolvedRoot: []byte(s.resolvedRoot), generation: s.sourceGeneration, followSymlinks: s.followSymlinks == 1},
		PolicyRevision: s.policyRevision, ScanMode: s.scanMode, RootIdentity: in.root.identity, RootRevision: in.root.revision})
	if err != nil {
		t.Fatal(err)
	}
	defs := append(scanRunLifecycleDefinitions(), scanRunLifecycleHistoryDefinitions()...)
	defs = append(defs, scanLifecycleByteDefinitions()...)
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader:
			defs = append(defs, def)
		}
	}
	registry := nativeInventoryFixtureRegistry(t, w, defs, "scan_lifecycle_execution_test.go", "scan_run_lifecycle_test.go", "scan_receipts_test.go")
	return w, registry, command
}

func scanLifecycleExecutionTurn(t *testing.T, w *executor, registry *stmt.Registry, kind CommandID, budget commandMemoryBudget) *commandTurn {
	t.Helper()
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond},
		setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}
	g, a, wal, request := governedFixture(t, w.now, governedFixtureProfile{recipe, 8 << 20})
	request.Key.Domain = uint16(kind)
	if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, w.now()); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(kind, recipe.limit, g, a, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	custody.scanLifecycleMemory = budget
	// This lease-boundary fixture has an explicit original caller budget,
	// independent of both its 25ms forecast and the governor's 1s deadline.
	ctx, cancel := context.WithDeadline(t.Context(), w.now().Add(100*time.Millisecond))
	t.Cleanup(cancel)
	tx, err := w.begin(ctx, kind, recipe, custody)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			_ = custody.retired()
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range registry.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func TestScanLifecycleExecutionCanonicalCreateClaimAndAck(t *testing.T) {
	w, registry, create := scanLifecycleExecutionFixture(t)
	budget := commandMemoryBudget{4 << 20, 4 << 20}
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, budget)
	saved, err := applyInventoryCreate(t.Context(), turn, create)
	if err != nil || saved.AcceptedRunRevision != 1 || saved.LeaseOwner != 0 || saved.RootIdentity != create.request.RootIdentity {
		t.Fatal(saved, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	q := ScanReceiptRequest{RunID: saved.Original.RunID, LibraryID: saved.Original.LibraryID, LibraryKey: saved.Original.LibraryKey,
		Receipt: ReceiptReference{create.identity, create.digest}}
	turn = scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, budget)
	lookup, err := lookupScanRunLifecycle(t.Context(), turn, q, InventoryCreate)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(saved, lookup.result) {
		t.Fatal("canonical create replay", lookup, err)
	}
	if _, err := acknowledgeScanRunLifecycle(t.Context(), turn, q, InventoryCreate); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleExecutionTurn(t, w, registry, InventoryClaim, budget)
	before, err := readScanRunSnapshot(t.Context(), turn, q.RunID)
	if err != nil {
		t.Fatal(err)
	}
	r := before.run
	claim, err := newInventoryClaimCommand(CommandIdentity{create.identity.Incarnation, 2, [16]byte{19}}, inventoryClaimRequest{
		Original: saved.Original, Run: inventoryRun{revision: r.revision, policyRevision: r.scope.policyRevision,
			phase: r.phase, scanMode: r.scope.scanMode, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration,
			leaseExpiresMS: r.leaseExpiresMS, lastSequence: r.lastSequence, acknowledgedSequence: r.acknowledgedSequence},
		Root: scanRootWitness{Path: ".", Identity: before.root.identity, Revision: before.root.revision, Cursor: before.root.cursor,
			State: before.root.state, Pages: before.root.pages, Skipped: before.root.skipped},
		Discovered: r.discovered, Processed: before.job.processed, JobStatus: before.job.status, JobCreatedAt: before.job.createdAt,
		JobErrorText: before.job.errorText, Attempts: r.attempts,
		NextAttemptMS: scanOptionalMS{Present: r.nextAttemptMS.Valid, Value: r.nextAttemptMS.Int64}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := applyInventoryClaim(t.Context(), turn, claim, scanRunInitialClaim, 17, w.now().Add(time.Second).UnixMilli())
	if err != nil || claimed.LeaseGeneration != 1 || claimed.AcceptedRunRevision != 2 || claimed.AcknowledgedSequence != 1 {
		t.Fatal(claimed, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	q.Receipt = ReceiptReference{claim.identity, claim.digest}
	turn = scanLifecycleExecutionTurn(t, w, registry, InventoryClaim, budget)
	lookup, err = lookupScanRunLifecycle(t.Context(), turn, q, InventoryClaim)
	if err != nil || lookup.decision != receiptReplay || !reflect.DeepEqual(claimed, lookup.result) {
		t.Fatal("canonical claim replay", lookup, err)
	}
	if ack, err := acknowledgeScanRunLifecycle(t.Context(), turn, q, InventoryClaim); err != nil || ack != 2 {
		t.Fatal(ack, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func TestScanLifecycleExecutionRefusesBeforeEffects(t *testing.T) {
	w, registry, create := scanLifecycleExecutionFixture(t)
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, commandMemoryBudget{})
	got, err := applyInventoryCreate(t.Context(), turn, create)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(got, scanRunLifecycleResult{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	var count int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM jobs WHERE id=?`, create.request.Scope.runID).Scan(&count); err != nil || count != 0 {
		t.Fatal("unfunded preparation changed native state", count, err)
	}
}

func TestScanLifecycleResourcesClosedModeAndLifetime(t *testing.T) {
	w, registry, _ := scanLifecycleExecutionFixture(t)
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryClaim, commandMemoryBudget{4 << 20, 4 << 20})
	now := w.now().UnixMilli()
	for _, tc := range []struct {
		mode      scanRunClaimMode
		old, next int64
		pass      bool
	}{
		{scanRunInitialClaim, 0, now + 1000, true}, {scanRunExpiredReclaim, now, now + 1000, true},
		{scanRunLiveRenew, now + 500, now + 1000, true}, {scanRunLiveRenew, now + 1, now + 1000, false},
		{scanRunExpiredReclaim, now + 1, now + 1000, false}, {scanRunInitialClaim, now, now + 1000, false},
	} {
		got, err := turn.scanClaimTimeMS(t.Context(), tc.mode, tc.old, tc.next)
		if tc.pass && (err != nil || got != now) || !tc.pass && (err == nil || got != 0) {
			t.Fatal(tc, got, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := turn.scanClaimTimeMS(ctx, scanRunInitialClaim, 0, now+1000); got != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	if got, err := turn.scanCreateTimeMS(t.Context()); got != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatal("claim frame borrowed create purpose", got, err)
	}
}
