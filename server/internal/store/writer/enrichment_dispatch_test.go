package writer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func enrichmentDispatchFixture(t *testing.T) (*Writer, *EnrichmentDisclosure, EnrichmentReceiptRequest, *governor.Authority, *executor, effectivePlan) {
	t.Helper()
	e, q, _ := sealHistoryNativeReceipt(t)
	var seq int
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	base := effectiveFixturePlan()
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := e.config
	config.Synchronous = sqliteconn.Normal
	baseDefs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	baseDefs = append(baseDefs, effectiveDispatchDefinitions()...)
	base.registration = captureEffectiveFixture(t, conn, config, baseDefs).Receipt()
	defs := enrichmentSealHistoryCommandDefinitions()
	captured := captureEffectiveFixture(t, conn, config, defs, "enrichment_dispatch_test.go", "enrichment_seal_history_test.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	plan := base
	plan.registration = captured.Receipt()
	plan.steps = nil
	plan.need[governor.MemoryBytes] = 4 << 20
	plan.enrichmentMemory = commandMemoryBudget{2 << 20, 2 << 20}
	for _, d := range defs {
		plan.steps = append(plan.steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = base.need
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 4, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if err = composeEnrichmentSealHistory(t.Context(), r, plan, plan); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	cap := &EnrichmentDisclosure{issuer: w, scope: &enrichmentScope{ownerID: q.ProviderOperationID, libraryKey: "library-native", libraryID: q.LibraryID, entityID: q.EntityID, lifetime: t.Context()}}
	return w, cap, q, a, e, plan
}

func TestEnrichmentSharedDispatchReplayAckAndFloor(t *testing.T) {
	w, cap, q, a, e, _ := enrichmentDispatchFixture(t)
	in := &enrichmentInvocation{action: enrichmentResolve, purpose: EvidenceSeal, disclosure: cap, request: q}
	got, ack, err := w.effective.runEnrichment(t.Context(), w, in)
	if err != nil || got.Outcome.State != Applied || !got.Outcome.Replayed || got.Value.evidenceSeal == nil || got.Digest == (ResultDigest{}) || ack != (AckResult{}) {
		t.Fatal("same-frame saved replay", got, ack, err)
	}
	if v := got.Outcome.Evidence; v == nil || v.Purpose != EvidenceSeal || v.Class != governor.Background || v.Durability != Normal || v.Commit != CommitSucceeded || v.PhysicalReturnedAt == nil {
		t.Fatal("missing actual frame evidence", v)
	}
	in.action = enrichmentAcknowledge
	out, ack, err := w.effective.runEnrichment(t.Context(), w, in)
	if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || out != (enrichmentCommandResult{}) {
		t.Fatal("atomic domain ACK", out, ack, err)
	}
	var floor int64
	if err = e.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id=?", q.ProviderOperationID).Scan(&floor); err != nil || floor != q.Receipt.Identity.Sequence {
		t.Fatal(floor, err)
	}
	if _, err = e.db.ExecContext(t.Context(), "UPDATE enrichment_command_receipts SET result_digest=zeroblob(32) WHERE command_kind='evidence_seal'"); err != nil {
		t.Fatal(err)
	}
	in.action = enrichmentResolve
	got, _, err = w.effective.runEnrichment(t.Context(), w, in)
	if err != nil || got.Outcome.State != ResultRetired || got.Value != (enrichmentNativeResult{}) || got.Digest != (ResultDigest{}) {
		t.Fatal("public floor decoded retained proof", got, err)
	}
	if a.Usage()[governor.Background] != (governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("shared frame leaked custody", a.Usage(), e.db.Stats())
	}
}

func TestEnrichmentSharedDispatchRefusesUnprovedFresh(t *testing.T) {
	w, cap, q, a, e, _ := enrichmentDispatchFixture(t)
	ack := &enrichmentInvocation{action: enrichmentAcknowledge, purpose: EvidenceSeal, disclosure: cap, request: q}
	if _, _, err := w.effective.runEnrichment(t.Context(), w, ack); err != nil {
		t.Fatal(err)
	}
	q.Receipt.Identity.Sequence++
	q.Receipt.Identity.OperationID[0] ^= 1
	q.Receipt.RequestDigest[0] ^= 1
	p := &enrichmentFrozenPayload{scope: sealOwnerScope{ownerID: q.ProviderOperationID, entityID: q.EntityID, identity: q.Receipt.Identity, request: q.Receipt.RequestDigest}}
	in := &enrichmentInvocation{action: enrichmentApply, purpose: EvidenceSeal, disclosure: cap, request: q, payload: p}
	got, floor, err := w.effective.runEnrichment(t.Context(), w, in)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || got != (enrichmentCommandResult{}) || floor != (AckResult{}) {
		t.Fatal("fixture/data/digest authorized fresh proof", got, floor, err)
	}
	var last int64
	if err = e.db.QueryRowContext(t.Context(), "SELECT last_command_sequence FROM metadata_provider_operations WHERE id=?", q.ProviderOperationID).Scan(&last); err != nil || last != q.Receipt.Identity.Sequence-1 {
		t.Fatal("refused fresh advanced owner", last, err)
	}
	if a.Usage()[governor.Background] != (governor.Amounts{}) {
		t.Fatal("fresh refusal leaked", a.Usage())
	}
}

func TestEnrichmentSharedDispatchZeroErrorsAndProfileIsolation(t *testing.T) {
	w, cap, q, a, e, plan := enrichmentDispatchFixture(t)
	in := &enrichmentInvocation{action: enrichmentResolve, purpose: EvidenceSeal, disclosure: cap, request: q}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	out, ack, err := w.effective.runEnrichment(canceled, w, in)
	if !errors.Is(err, context.Canceled) || out != (enrichmentCommandResult{}) || ack != (AckResult{}) {
		t.Fatal("canceled caller exposed a result", out, ack, err)
	}
	cap.scope.revoked.Store(true)
	out, ack, err = w.effective.runEnrichment(t.Context(), w, in)
	if !errors.Is(err, ErrDisclosureDenied) || out != (enrichmentCommandResult{}) || ack != (AckResult{}) {
		t.Fatal("revoked disclosure exposed a result", out, ack, err)
	}
	cap.scope.revoked.Store(false)
	before := w.effective.registry
	if err = composeEnrichmentSealHistory(t.Context(), w.effective, plan, plan); !errors.Is(err, ErrBusy) || w.effective.registry != before {
		t.Fatal("profile replacement", err)
	}
	if _, err = freezeCommandPlan(e, plan, enrichmentSealHistoryCommandDefinitions(), EffectiveStage); err == nil {
		t.Fatal("foreign purpose accepted enrichment partition")
	}
	if _, _, err = w.effective.dispatchCommand(t.Context(), w, nil, EnrichmentReceiptRequest{}, nil, false, nil, in, &publisherInvocation{}); err == nil {
		t.Fatal("mixed union admitted")
	}
	if a.Usage()[governor.Background] != (governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("validation path leaked", a.Usage())
	}
	// Guards still cover every named result through the shared finish kernel.
	if !reflect.DeepEqual(out, enrichmentCommandResult{}) {
		t.Fatal(out)
	}
}

// The saved payload is deliberately too large for this explicit harness profile.
// A corrupt result cannot hide a missing pre-copy reservation: capacity must win
// before native TEXT copy and saved-result decoding.
func TestEnrichmentSharedDispatchBudgetsBeforePayloadCopy(t *testing.T) {
	w, cap, q, a, e, _ := enrichmentDispatchFixture(t)
	if _, err := e.db.ExecContext(t.Context(), "UPDATE enrichment_command_receipts SET after_item=? WHERE command_kind='evidence_seal'", strings.Repeat("x", 1<<20)); err != nil {
		t.Fatal(err)
	}
	in := &enrichmentInvocation{action: enrichmentResolve, purpose: EvidenceSeal, disclosure: cap, request: q}
	out, ack, err := w.effective.runEnrichment(t.Context(), w, in)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || out != (enrichmentCommandResult{}) || ack != (AckResult{}) {
		t.Fatal("saved payload copied before funded actual-byte capacity", out, ack, err)
	}
	if a.Usage()[governor.Background] != (governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("capacity refusal leaked", a.Usage(), e.db.Stats())
	}
}

func TestEnrichmentSharedRegistryKeepsEveryInstalledProfile(t *testing.T) {
	r := &effectiveRuntime{inventoryPlan: &effectivePlan{}, inventoryPreparationPlan: &effectivePlan{}, lifecyclePlans: [2]*effectivePlan{&effectivePlan{}, &effectivePlan{}}, issuerPlans: [2]*effectivePlan{&effectivePlan{}, &effectivePlan{}}, enrichmentPlans: [4]*effectivePlan{nil, nil, &effectivePlan{}, &effectivePlan{}}}
	// Nine installed profiles: one base, inventory+preparation, two lifecycle,
	// two issuer and two seals. Nil stage profiles contribute nothing.
	if got := len(r.commandRegistrations()); got != 9 {
		t.Fatal("installed profile receipt dropped", got)
	}
}
