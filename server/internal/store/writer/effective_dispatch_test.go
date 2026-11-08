package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Protocol fixtures only: virtual time and deliberately synthetic complete
// forecasts. This is NOT production source/seal proof or a latency profile.
func effectiveFacadeFixture(t *testing.T, clock ...*time.Time) (*Writer, *EnrichmentDisclosure, *effectiveStageBeginCommand, *executor, *governor.Authority) {
	t.Helper()
	executor, _, input, identity := effectiveBeginWholeFixture(t)
	if len(clock) > 1 || (len(clock) == 1 && clock[0] == nil) {
		t.Fatal("invalid fixture clock")
	}
	if len(clock) == 1 {
		*clock[0] = executor.now()
		executor.now = func() time.Time { return *clock[0] }
	}
	command, err := newEffectiveStageBeginCommand(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	config := executor.config
	config.Synchronous = sqliteconn.Normal // preparatory component turn completed
	return bindEffectiveFixtureRuntime(t, executor, command, config)
}

func bindEffectiveFixtureRuntime(t *testing.T, executor *executor, command *effectiveStageBeginCommand, config sqliteconn.Config) (*Writer, *EnrichmentDisclosure, *effectiveStageBeginCommand, *executor, *governor.Authority) {
	t.Helper()
	input := command.request
	var sequence int64
	var name, path string
	if err := executor.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	plan := effectiveFixturePlan()
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	conn, err := executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	golden := captureEffectiveFixture(t, conn, config, defs)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	plan.registration = golden.Receipt()
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = plan.need
	}
	// Capture the clock function: later cancellation instrumentation may read
	// Authority.Usage, which must never run recursively under GV's mutex.
	g, authority, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: executor.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := newEffectiveRuntime(executor, path, g, authority, wal, 1, plan)
	if err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: runtime}
	cap := &EnrichmentDisclosure{issuer: w, scope: &enrichmentScope{ownerID: input.ownerID, libraryKey: "library-native", libraryID: input.entity.libraryID, entityID: input.entity.id, lifetime: t.Context()}}
	return w, cap, command, executor, authority
}

// Native qualification is setup work on the actual owned fixture, outside
// measured command custody. Resource forecasts and source/seal authority remain
// synthetic/test-only; an EXPLAIN receipt does not certify either of them.
func captureEffectiveFixture(t *testing.T, conn *sql.Conn, config sqliteconn.Config, defs []stmt.Definition, additionalSources ...string) plan.Golden {
	t.Helper()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	sources := append([]string{"effective_dispatch_test.go", "enrichment_effective_begin_handler_test.go", "enrichment_effective_begin_facts_test.go"}, additionalSources...)
	for _, name := range sources {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(data)
	}
	golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{
		ConnectionPolicy: config, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed,
		Implementations: "owned writer component fixture setup; actual native qualification, synthetic resource forecasts and test-only source permission",
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Manifest) != len(defs) || golden.Receipt() == nil {
		t.Fatal("incomplete native registration proof", len(golden.Manifest), len(defs))
	}
	return golden
}

func effectiveFixturePlan() effectivePlan {
	plan := effectivePlan{recipe: turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, 8 * time.Millisecond}}, headerSync: Work{WALBytes: 32, Hold: 5 * time.Millisecond}}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	for _, def := range defs {
		plan.steps = append(plan.steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	plan.need[governor.MemoryBytes] = 128 << 20
	plan.need[governor.CPUMilli] = 100
	plan.need[governor.WALBytes] = int64(plan.recipe.limit.WALBytes)
	plan.need[governor.IOBytes] = 64 << 20
	plan.io = []governor.DeviceIO{{DeviceID: "protocol-fixture", Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
	return plan
}

func effectiveReference(input *effectiveStageBeginCommand) EnrichmentReceiptRequest {
	return EnrichmentReceiptRequest{ProviderOperationID: input.request.ownerID, LibraryID: input.request.entity.libraryID, EntityID: input.request.entity.id, Receipt: ReceiptReference{input.identity, input.digest}}
}

func TestEffectiveFacadeProductionRefusesUnprovedSource(t *testing.T) {
	w, cap, command, executor, authority := effectiveFacadeFixture(t)
	request := effectiveReference(command)
	var originalReceipts int64
	if err := executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM enrichment_command_receipts").Scan(&originalReceipts); err != nil {
		t.Fatal(err)
	}
	result, err := w.StageEffective(t.Context(), cap, command)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(result, EffectiveStageResult{}) {
		t.Fatal("unproved source reached production BEGIN", result, err)
	}
	var images, receipts, sequence int64
	if err := executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&images); err != nil {
		t.Fatal(err)
	}
	if err := executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM enrichment_command_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := executor.db.QueryRowContext(t.Context(), "SELECT last_command_sequence FROM metadata_provider_operations WHERE id=?", request.ProviderOperationID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if images != 0 || receipts != originalReceipts || sequence != command.request.owner.lastSequence || authority.Usage()[governor.Background] != (governor.Amounts{}) || executor.db.Stats().InUse != 0 {
		t.Fatal("refusal changed durable effects or stranded custody", images, receipts, sequence)
	}
	resolved, err := w.ResolveEffective(t.Context(), cap, request)
	if err != nil || resolved.Outcome.State != Pending || resolved.Value.ImageID != 0 {
		t.Fatal(resolved, err)
	}
	if _, err := w.AckEffective(t.Context(), cap, request); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("ACK invented missing result", err)
	}
}

func TestEffectiveFacadeHistoricalReplayAckAndFloor(t *testing.T) {
	w, cap, command, executor, authority := effectiveFacadeFixture(t)
	// Seed an independently committed component receipt through the MERGED
	// private handler. No fixture capability is minted by this implementation.
	turn, _ := effectiveBeginWholeTurn(t, executor)
	saved, err := applyEffectiveStageBegin(t.Context(), turn, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Historical disclosure never borrows today's lease/source/policy eligibility.
	if _, err := executor.db.ExecContext(t.Context(), "UPDATE metadata_provider_operations SET lease_until='1900-01-01T00:00:00Z',source_revision=source_revision+1 WHERE id=?", command.request.ownerID); err != nil {
		t.Fatal(err)
	}
	request := effectiveReference(command)
	for _, invoke := range []func() (EffectiveStageResult, error){
		func() (EffectiveStageResult, error) { return w.ResolveEffective(t.Context(), cap, request) },
		func() (EffectiveStageResult, error) { return w.StageEffective(t.Context(), cap, command) },
	} {
		result, err := invoke()
		if err != nil || result.Outcome.State != Applied || !result.Outcome.Replayed || result.Value != saved || result.Outcome.Reference != request.Receipt {
			t.Fatal(result, err)
		}
		if e := result.Outcome.Evidence; e == nil || e.Commit != CommitSucceeded || e.Policy == nil || e.PhysicalReturnedAt == nil || e.WALAfter == nil {
			t.Fatal("replay lost THIS call evidence", e)
		}
	}
	for _, mutate := range []func(*EnrichmentReceiptRequest){
		func(r *EnrichmentReceiptRequest) { r.Receipt.RequestDigest[0]++ },
		func(r *EnrichmentReceiptRequest) { r.Receipt.Identity.OperationID[0]++ },
	} {
		wrong := request
		mutate(&wrong)
		if _, err := w.ResolveEffective(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong identity disclosed result", err)
		}
		if _, err := w.AckEffective(t.Context(), cap, wrong); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong identity ACKed result", err)
		}
	}
	ack, err := w.AckEffective(t.Context(), cap, request)
	if err != nil || ack.AcknowledgedThrough != command.identity.Sequence || ack.Evidence == nil || ack.Evidence.Commit != CommitSucceeded {
		t.Fatal(ack, err)
	}
	if _, err := executor.db.ExecContext(t.Context(), "DELETE FROM enrichment_command_receipts WHERE provider_operation_id=?", request.ProviderOperationID); err != nil {
		t.Fatal(err)
	}
	retired, err := w.ResolveEffective(t.Context(), cap, request)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (EffectiveStageBeginResult{}) {
		t.Fatal("drained receipt reconstructed Applied", retired, err)
	}
	ack, err = w.AckEffective(t.Context(), cap, request)
	if err != nil || ack.AcknowledgedThrough != command.identity.Sequence {
		t.Fatal("retained floor not idempotent", ack, err)
	}
	if authority.Usage()[governor.Background] != (governor.Amounts{}) || executor.db.Stats().InUse != 0 {
		t.Fatal("replay/ACK leaked custody")
	}
}

type wrappedEffectiveCommand struct{ EffectiveStageBeginCommand }

type foreignEffectiveCommand struct{}

func (foreignEffectiveCommand) isEffectiveStageBeginCommand() { panic("marker invoked") }

func TestEffectiveFacadeDisclosureAndClosedDispatch(t *testing.T) {
	w, cap, command, executor, _ := effectiveFacadeFixture(t)
	request := effectiveReference(command)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, wrong := range []*EnrichmentDisclosure{nil, {}, {issuer: &Writer{}, scope: cap.scope},
		{issuer: w, scope: &enrichmentScope{ownerID: cap.scope.ownerID, libraryKey: cap.scope.libraryKey, libraryID: cap.scope.libraryID, entityID: cap.scope.entityID, lifetime: cancelled}},
	} {
		if _, err := w.ResolveEffective(t.Context(), wrong, request); !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("invalid capability disclosed receipt", err)
		}
	}
	for _, invalid := range []EffectiveStageBeginCommand{nil, (*effectiveStageBeginCommand)(nil), wrappedEffectiveCommand{command}, &wrappedEffectiveCommand{command}, foreignEffectiveCommand{}} {
		if _, err := w.StageEffective(t.Context(), cap, invalid); !errors.Is(err, ErrInvalid) {
			t.Fatal("non-concrete command escaped closed switch", err)
		}
	}
	for _, mutate := range []func(*EnrichmentReceiptRequest){
		func(r *EnrichmentReceiptRequest) { r.EntityID++ }, func(r *EnrichmentReceiptRequest) { r.LibraryID++ }, func(r *EnrichmentReceiptRequest) { r.ProviderOperationID += "foreign" },
	} {
		wrong := request
		mutate(&wrong)
		if _, err := w.ResolveEffective(t.Context(), cap, wrong); !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("scope mismatch admitted", err)
		}
	}
	// Capability scope is rechecked against actual native library identity too.
	cap.scope.libraryKey = "another-library"
	if _, err := w.ResolveEffective(t.Context(), cap, request); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("native library mismatch disclosed receipt", err)
	}
	cap.scope.libraryKey = "library-native"
	cap.scope.revoked.Store(true)
	if _, err := w.ResolveEffective(t.Context(), cap, request); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("revoked capability usable", err)
	}
	if executor.db.Stats().InUse != 0 {
		t.Fatal("denial retained connection")
	}
}

type revokingDisclosureContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
}

func (c *revokingDisclosureContext) Err() error {
	c.calls++
	if c.calls == 3 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestEffectiveFacadeRevokedDuringHistoryDisclosesNoResult(t *testing.T) {
	w, cap, command, executor, authority := effectiveFacadeFixture(t)
	seedEffectiveReceipt(t, executor, command)
	request := effectiveReference(command)
	for _, entry := range []string{"stage_replay", "resolve", "ack"} {
		t.Run(entry, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(t.Context())
			defer cancel()
			revoking := &revokingDisclosureContext{Context: lifetime, cancel: cancel}
			current := &EnrichmentDisclosure{issuer: w, scope: &enrichmentScope{ownerID: cap.scope.ownerID, libraryKey: cap.scope.libraryKey, libraryID: cap.scope.libraryID, entityID: cap.scope.entityID, lifetime: revoking}}
			var result EffectiveStageResult
			var ack AckResult
			var err error
			switch entry {
			case "stage_replay":
				result, err = w.StageEffective(t.Context(), current, command)
			case "resolve":
				result, err = w.ResolveEffective(t.Context(), current, request)
			case "ack":
				ack, err = w.AckEffective(t.Context(), current, request)
			}
			if !errors.Is(err, ErrDisclosureDenied) || result.Value != (EffectiveStageBeginResult{}) || result.Outcome.State != 0 || result.Outcome.Reference != (ReceiptReference{}) || ack.AcknowledgedThrough != 0 {
				t.Fatal("revoked disclosure leaked saved proof or ACK", result, ack, err)
			}
			var floor int64
			if err := executor.db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id=?", request.ProviderOperationID).Scan(&floor); err != nil {
				t.Fatal(err)
			}
			if floor != command.request.owner.acknowledgedSequence || authority.Usage()[governor.Background] != (governor.Amounts{}) || executor.db.Stats().InUse != 0 {
				t.Fatal("revocation committed ACK or stranded custody", floor)
			}
		})
	}
}

func TestEffectivePlanOwnsFiniteRecipe(t *testing.T) {
	w, _, _, _, _ := effectiveFacadeFixture(t)
	r := w.effective
	for _, mutate := range []func(*effectivePlan){
		func(p *effectivePlan) { p.registration = nil },
		func(p *effectivePlan) { p.steps = p.steps[:len(p.steps)-1] },
		func(p *effectivePlan) { p.steps[0].id = stmt.ID("foreign") },
		func(p *effectivePlan) { p.steps[0].work.Hold = 25 * time.Millisecond },
		func(p *effectivePlan) { p.need[governor.WALBytes]-- },
		func(p *effectivePlan) { p.recipe.limit.Rows = 1 << 32 },
		func(p *effectivePlan) { p.headerSync = Work{} },
	} {
		copyPlan := r.plan
		copyPlan.steps = append([]statementStep(nil), r.plan.steps...)
		mutate(&copyPlan)
		if candidate, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, copyPlan); err == nil || candidate != nil {
			t.Fatal("invalid/incomplete recipe accepted")
		}
	}
	copyPlan := r.plan
	copyPlan.steps = append([]statementStep(nil), r.plan.steps...)
	copyPlan.io = append([]governor.DeviceIO(nil), r.plan.io...)
	newRuntime, err := newEffectiveRuntime(r.executor, r.path, r.governor, r.authority, r.wal, r.owner, copyPlan)
	if err != nil {
		t.Fatal(err)
	}
	copyPlan.steps[0].work.Rows++
	copyPlan.io[0].DeviceID = "mutated"
	if reflect.DeepEqual(copyPlan.steps, newRuntime.plan.steps) || newRuntime.plan.io[0].DeviceID == "mutated" {
		t.Fatal("recipe aliases constructor input")
	}
}

func seedEffectiveReceipt(t *testing.T, executor *executor, command *effectiveStageBeginCommand) EffectiveStageBeginResult {
	t.Helper()
	turn, _ := effectiveBeginWholeTurn(t, executor)
	saved, err := applyEffectiveStageBegin(t.Context(), turn, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return saved
}

func primeEffectiveFixturePool(t *testing.T, executor *executor) {
	t.Helper()
	// Startup custody establishes zero-frame WAL/index via read-only schema
	// access. Missing sidecars must remain a serving admission failure.
	conn, err := executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, executor.config); err != nil {
		t.Fatal(err)
	}
	var tables int64
	if err := conn.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&tables); err != nil || tables == 0 {
		t.Fatal("fixture startup read failed", tables, err)
	}
}

func TestEffectiveFacadeMissingUnackedProofFailsClosed(t *testing.T) {
	w, cap, command, executor, _ := effectiveFacadeFixture(t)
	seedEffectiveReceipt(t, executor, command)
	if _, err := executor.db.ExecContext(t.Context(), "DELETE FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_sequence=?", command.request.ownerID, command.identity.Sequence); err != nil {
		t.Fatal(err)
	}
	request := effectiveReference(command)
	for _, invoke := range []func() (EffectiveStageResult, error){
		func() (EffectiveStageResult, error) { return w.ResolveEffective(t.Context(), cap, request) },
		func() (EffectiveStageResult, error) { return w.StageEffective(t.Context(), cap, command) },
	} {
		result, err := invoke()
		if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(result, EffectiveStageResult{}) {
			t.Fatal("missing historical result became fresh/Applied", result, err)
		}
	}
	if _, err := w.AckEffective(t.Context(), cap, request); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("missing proof ACKed", err)
	}
}

func TestEffectiveFacadeCommittedAckOverrunKeepsFloor(t *testing.T) {
	var clock time.Time
	w, cap, command, executor, authority := effectiveFacadeFixture(t, &clock)
	seedEffectiveReceipt(t, executor, command)
	path := w.effective.path
	if err := executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, executor.config)
	if err != nil {
		t.Fatal(err)
	}
	baseNow := clock
	fault := &faultConnector{Connector: connector, afterNativeCommit: func() { clock = baseNow.Add(30 * time.Millisecond) }}
	installFaultPool(executor, fault)
	primeEffectiveFixturePool(t, executor)
	ack, err := w.AckEffective(t.Context(), cap, effectiveReference(command))
	if err != nil || ack.AcknowledgedThrough != command.identity.Sequence || ack.Evidence == nil ||
		!ack.Evidence.HoldTargetExceeded || ack.Evidence.HoldTarget <= 0 || ack.Evidence.HoldTarget > MaxCommandHold ||
		ack.Evidence.HoldSafetyCeiling != MaxCommandSafetyHold {
		t.Fatal("committed ACK above target became failure", ack, err)
	}
	var nativeFloor int64
	// Independently inspect actual native state with the shared policy opener;
	// a target miss must neither erase the floor nor quarantine the pool.
	db, _, openErr := sqliteconn.Open(t.Context(), path, executor.config)
	if openErr != nil {
		t.Fatal(openErr)
	}
	readErr := db.QueryRowContext(t.Context(), "SELECT acknowledged_command_sequence FROM metadata_provider_operations WHERE id=?", command.request.ownerID).Scan(&nativeFloor)
	closeErr := db.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	if nativeFloor != command.identity.Sequence {
		t.Fatal("committed floor was lost", nativeFloor)
	}
	if executor.fenced || w.effective.fenced || executor.holdTargetOverruns.Load() != 1 || authority.Usage()[governor.Background] != (governor.Amounts{}) {
		t.Fatal("over-target ACK fenced or lost counter/physical return")
	}
	retired, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command))
	if err != nil || retired.Outcome.State != ResultRetired {
		t.Fatal("next original resolution did not remain retired", retired, err)
	}
}

func TestEffectiveFacadeAmbiguousAckFencesAfterPhysicalReturn(t *testing.T) {
	w, cap, command, executor, authority := effectiveFacadeFixture(t)
	seedEffectiveReceipt(t, executor, command)
	path := w.effective.path
	if err := executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, executor.config)
	if err != nil {
		t.Fatal(err)
	}
	fault := &faultConnector{Connector: connector, replyError: true, afterCommit: true}
	installFaultPool(executor, fault)
	primeEffectiveFixturePool(t, executor)
	ack, err := w.AckEffective(t.Context(), cap, effectiveReference(command))
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(ack, AckResult{}) {
		t.Fatal("ambiguous ACK invented acknowledgement", ack, err)
	}
	if !executor.ambiguous || !w.effective.fenced || authority.Usage()[governor.Background] != (governor.Amounts{}) || !fault.nativeClosed.Load() {
		t.Fatal("ambiguity escaped quarantine/physical return")
	}
	if _, err := w.StageEffective(t.Context(), cap, command); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("ambiguous facade admitted a new turn", err)
	}
}

func TestEffectiveDispatchNativeCohortsAndPlans(t *testing.T) {
	w, _, command, executor, _ := effectiveFacadeFixture(t)
	turn, _ := effectiveBeginWholeTurn(t, executor)
	if _, err := applyEffectiveStageBegin(t.Context(), turn, command); err != nil {
		t.Fatal(err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	defs := effectiveDispatchDefinitions()
	for _, def := range defs {
		if issues := plan.LintSQL(def.SQL); len(issues) != 0 {
			t.Fatal(def.ID, issues)
		}
	}
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	policyBytes, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(policyBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtureHash := sha256.New()
	for _, source := range []string{"effective_dispatch_test.go", "enrichment_effective_begin_handler_test.go", "enrichment_effective_begin_facts_test.go"} {
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		fixtureHash.Write(body)
	}
	conn, err := executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := executor.config
	config.Synchronous = sqliteconn.Normal
	golden, err := cachedCapture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: config, SHA256: hex.EncodeToString(fixtureHash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "closed-effective-disclosure-ack-components"}, policy)
	closeErr := conn.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if len(golden.Plans) != 6 || len(golden.Manifest) != 3 {
		t.Fatal("incomplete exact-cohort plans", len(golden.Plans), len(golden.Manifest))
	}
	registry, err = stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	// Assert cohort populations, not names alone: two actual hits/misses and
	// stale/pending scalar CAS in one rolled-back private component turn.
	tx, err := executor.begin(t.Context(), EffectiveStage, w.effective.plan.recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	steps := make([]statementStep, 0, len(defs))
	for _, def := range defs {
		steps = append(steps, statementStep{def.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	cohortTurn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range defs {
		for _, cohort := range def.Cohorts {
			if def.Kind == stmt.Read {
				rows, err := cohortTurn.query(t.Context(), cohortTurn.steps[def.ID].statementStep, cohort.Bindings...)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						t.Fatal(err)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				want := 0
				if cohort.Name == "library_present" || cohort.Name == "subject_present" {
					want = 1
				}
				if count != want {
					t.Fatal("false native population", cohort.Name, count, want)
				}
			}
		}
	}
	ack := defs[2]
	for _, index := range []int{1, 0} {
		cohort := ack.Cohorts[index]
		result, err := cohortTurn.exec(t.Context(), cohortTurn.steps[ack.ID].statementStep, cohort.Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		want := int64(1 - index)
		if err != nil || n != want {
			t.Fatal("false scalar CAS population", cohort.Name, n, want, err)
		}
	}
	if err := cohortTurn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
