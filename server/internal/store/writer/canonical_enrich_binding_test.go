package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"os"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var errCanonicalEnrichSustained = errors.New("canonical staged-enrich composition unavailable: funded same-DB keeper or its pressure-recovery hook not attached")

// The canonical staged-enrich class measures the actual final EnrichAccept
// command with prepared inputs. One immutable evidence lineage is shared; every
// original gets its own operation-owned source publication, source seal, hidden
// zero-member image and BEGIN receipt, then flips the accepted head. Preparation
// is outside the measured hold. The shared keeper recovers pressure before
// admission, as the WC and publisher targets do.
type canonicalEnrichComponent struct {
	writer       *Writer
	opening      *scanOpening
	maintenance  *canonicalN1MaintenanceOwner
	trace        *n1Trace
	stream       *n1Stream
	prep         *canonicalEnrichPrep
	opTemplate   canonicalSQLRow
	srcTemplate  canonicalSQLRow
	sealTemplate canonicalSQLRow
	evidenceRef  effectiveBeginSealReference
	evidenceID   string
	objectID     string
	header       effectiveBeginHeader
	entityID     int64
	provider     string
	libraryKey   string
	reserved     int64
	first        *enrichAcceptCommand
	firstQ       EnrichmentReceiptRequest
	measured     bool
	fresh        int
	acked        int
	calls        int
	physical     int
}

type canonicalEnrichPrep struct {
	e        *executor
	registry *stmt.Registry
	steps    []statementStep
}

func newCanonicalEnrichPrep(t *testing.T, e *executor) *canonicalEnrichPrep {
	t.Helper()
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := e.config
	golden := captureEffectiveFixture(t, conn, config, defs, "canonical_enrich_binding_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	return &canonicalEnrichPrep{e: e, registry: registry, steps: steps}
}

func (p *canonicalEnrichPrep) newTurn(t *testing.T) *commandTurn {
	t.Helper()
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := p.e.begin(t.Context(), EffectiveStage, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	turn, err := newCommandTurn(tx, p.registry, p.steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func newCanonicalEnrichComponent(t *testing.T, count int, stream *n1Stream, canonical, measured bool) *canonicalEnrichComponent {
	t.Helper()
	if count < 1 || count > canonicalHeldItems {
		t.Fatal("bounded canonical staged-enrich census required")
	}
	// The first original reuses the actual prepared fixture, which also creates
	// the immutable evidence lineage every title in this class shares.
	e0, f, begin0, image0, result0 := acceptBeginFixture(t)
	libraryKey := "library-native"
	if canonical {
		// The attached 1M population already owns catalog_libraries(7) and
		// catalog_kinds(1); the fixture's OR IGNORE keeps those rows. Point the
		// operation chain at that actual library namespace.
		libraryKey = "qualification-movies"
		for _, q := range []string{
			`UPDATE libraries SET metadata_language='en-CA' WHERE id='qualification-movies'`,
			`UPDATE library_sources SET library_id='qualification-movies' WHERE id='source-native'`,
		} {
			if _, err := e0.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := acceptRequestFixture(t, e0, f, begin0, image0, result0)
	firstQ := EnrichmentReceiptRequest{Receipt: ReceiptReference{first.identity, first.digest}, ProviderOperationID: begin0.request.ownerID, EntityID: begin0.request.entity.id, LibraryID: 7}
	opTemplate := canonicalReadRow(t, e0.db, "metadata_provider_operations", "id=?", begin0.request.ownerID)
	srcTemplate := canonicalReadRow(t, e0.db, "metadata_provider_operation_sources", "operation_id=?", begin0.request.ownerID)
	sealTemplate := canonicalReadAllRow(t, e0.db, "enrichment_command_receipts", "provider_operation_id=? AND command_sequence=? AND command_kind='credit_source_seal'", f.scope.ownerID, f.sourceReceipt.Identity.Sequence)
	var seq int
	var name, path string
	if err := e0.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := e0.db.Close(); err != nil {
		t.Fatal(err)
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	e := opening.executor
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	trace := newN1Trace(stream.origin, true)
	connector, err := sqliteconn.Connector(path, e.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	e.openPool(connector, trace.decorate)
	c := &canonicalEnrichComponent{
		opening:      opening,
		trace:        trace,
		stream:       stream,
		prep:         newCanonicalEnrichPrep(t, e),
		opTemplate:   opTemplate,
		srcTemplate:  srcTemplate,
		sealTemplate: sealTemplate,
		evidenceRef:  effectiveBeginSealReference{f.scope.ownerID, EvidenceSeal, 1, f.evidenceReceipt, f.evidenceResult},
		evidenceID:   begin0.request.evidenceID,
		objectID:     begin0.request.objectID,
		header:       begin0.request.header,
		entityID:     begin0.request.entity.id,
		provider:     begin0.request.owner.provider,
		libraryKey:   libraryKey,
		reserved:     f.evidenceFacts.RetainedBytes + f.sourceFacts.RetainedBytes,
		first:        first,
		firstQ:       firstQ,
		measured:     measured,
	}
	c.writer, c.maintenance = canonicalEnrichRuntime(t, e, opening, path)
	trace.useRegistry(c.writer.effective.registry)
	if err := c.stream.append("prepared", map[string]any{"recipe": canonicalEnrichRecipe, "prepared_inputs": count, "scope": "one immutable evidence lineage plus an operation-owned source seal, hidden image and BEGIN receipt per original; measured command is the final EnrichAccept only", "preparation_clock": nil, "product_enable": false, "canonical_population": canonical, "reserved_bytes": c.reserved, "qualification": false}); err != nil {
		t.Fatal(err)
	}
	c.maintenance.start(t.Context())
	return c
}

func canonicalEnrichRuntime(t *testing.T, e *executor, opening *scanOpening, path string) (*Writer, *canonicalN1MaintenanceOwner) {
	t.Helper()
	fixture := canonicalN1Maintenance()
	base := effectiveFixturePlan()
	baseDefs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	baseDefs = append(baseDefs, effectiveDispatchDefinitions()...)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := e.config
	base.registration = captureEffectiveFixture(t, conn, config, baseDefs, "canonical_enrich_binding_test.go").Receipt()
	historyDefs := enrichmentSealHistoryCommandDefinitions()
	history := effectiveFixturePlan()
	history.registration = captureEffectiveFixture(t, conn, config, historyDefs, "canonical_enrich_binding_test.go").Receipt()
	history.steps = nil
	history.need[governor.MemoryBytes] = 4 << 20
	history.enrichmentMemory = commandMemoryBudget{2 << 20, 2 << 20}
	for _, d := range historyDefs {
		history.steps = append(history.steps, statementStep{d.ID, Work{16, 2000, 16, 65536, time.Microsecond}})
	}
	acceptDefs := enrichmentAcceptCommandDefinitions()
	accept := effectiveFixturePlan()
	accept.registration = captureEffectiveFixture(t, conn, config, acceptDefs, "canonical_enrich_binding_test.go").Receipt()
	accept.steps = nil
	accept.recipe.limit = Work{65536, 100000000, 65536, 256 << 20, 25 * time.Millisecond}
	accept.need[governor.WALBytes] = 256 << 20
	accept.need[governor.MemoryBytes] = 128 << 20
	accept.effectiveSourceMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range acceptDefs {
		accept.steps = append(accept.steps, statementStep{d.ID, Work{64, 50000, 128, 65536, 10 * time.Microsecond}})
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	wal, err := governor.NewWALGate(fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = accept.need
	}
	partitions[governor.Recovery] = fixture.need
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 4, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := composeEnrichmentSealHistory(t.Context(), r, history, history, accept); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	m := newCanonicalCheckpointOwner(t, opening, r.governor, r.authority, r.wal, path, canonicalCheckpointShape{EnrichAccept, "canonical-enrich-cp", governor.Phase1aWALPolicy().HardBytes})
	w.effective.maintenance = &checkpointMaintenance{request: m.request("passive"), results: m.results}
	return w, m
}

func canonicalEnrichSustainedGate(c *canonicalEnrichComponent) error {
	if c == nil || c.writer == nil || c.writer.effective == nil || c.maintenance == nil || c.writer.effective.maintenance == nil ||
		c.opening == nil || c.opening.executor == nil || c.opening.executor.keeper.Load() == nil {
		return errCanonicalEnrichSustained
	}
	return c.opening.executor.keeper.Load().Check()
}

func (c *canonicalEnrichComponent) priorContext(t *testing.T) enrichAcceptPrior {
	t.Helper()
	var out enrichAcceptPrior
	var owner string
	var incarnation []byte
	var generation, image, units, reserved int64
	err := c.prep.e.db.QueryRowContext(t.Context(), `SELECT id,command_incarnation,generation,credit_image_id,proof_pin_units,proof_reserved_bytes FROM metadata_provider_operations WHERE entity_id=? AND provider=? AND proof_current_slot=1`, c.entityID, c.provider).Scan(&owner, &incarnation, &generation, &image, &units, &reserved)
	if errors.Is(err, sql.ErrNoRows) {
		return out
	}
	if err != nil || len(incarnation) != 16 {
		t.Fatal("canonical prior context", err)
	}
	out.exists = true
	out.owner, out.generation, out.image, out.pinUnits, out.reservedBytes = owner, generation, image, units, reserved
	copy(out.incarnation[:], incarnation)
	return out
}

// prepare builds the frozen final request for original n. Preparation writes
// only; the measured dispatching happens in action.
func (c *canonicalEnrichComponent) prepare(t *testing.T, n int) (*enrichAcceptCommand, EnrichmentReceiptRequest) {
	t.Helper()
	if n == 0 {
		return c.first, c.firstQ
	}
	opID := fmt.Sprintf("canonical-enrich-op-%d", n)
	incarnation := canonicalEnrichBlob('O', n)
	pubID := fmt.Sprintf("%032x", uint64(n)+1)
	var commitment [32]byte
	copy(commitment[:], fmt.Sprintf("canonical-enrich-commitment-%032d", n))
	contentRevision := int64(n + 2)
	generation := c.opTemplate.vals[c.opTemplate.index(t, "generation")].(int64)
	// Circular custody: operation -> publication -> seal receipt -> operation.
	op := c.opTemplate.with(t, map[string]any{
		"id":                            opID,
		"command_incarnation":           incarnation[:],
		"last_command_sequence":         int64(11),
		"acknowledged_command_sequence": int64(11),
		"credit_image_id":               nil,
		"evidence_build_id":             nil,
		"proof_stage_slot":              int64(0),
		"proof_current_slot":            nil,
		"proof_pin_units":               int64(2),
		"proof_reserved_bytes":          c.reserved,
		"status":                        "staged",
		"phase":                         "enrichment-v1",
		"result_digest":                 "",
		"finished_at":                   "",
		"reason":                        "",
		"source_publication_id":         nil,
		"source_owner_generation":       nil,
		"source_evidence_id":            nil,
		"source_subject_id":             nil,
		"source_parser_version":         nil,
		"source_next_position":          nil,
		"source_expected_members":       nil,
		"source_expected_bytes":         nil,
		"source_algorithm":              nil,
		"source_version":                nil,
		"source_expected_commitment":    nil,
		"source_evidence_owner":         nil,
		"source_evidence_incarnation":   nil,
		"source_evidence_sequence":      nil,
	})
	op.insert(t, c.prep.e.db, "metadata_provider_operations")
	sealIdentity := CommandIdentity{Incarnation: incarnation, Sequence: 11, OperationID: canonicalEnrichBlob('S', n)}
	requestBlob := canonicalEnrichBlob('R', n)
	var request [32]byte
	copy(request[:], requestBlob[:])
	sealRequest := RequestDigest(request)
	template, err := decodeSavedSealReceipt(canonicalSealSavedValues(t, c.sealTemplate))
	if err != nil {
		t.Fatal(err)
	}
	facts := template.facts
	facts.ProviderOperationID, facts.EntityID, facts.OwnerGeneration = opID, c.entityID, generation
	facts.PublicationID = EvidenceText{Value: pubID, Present: true}
	facts.ContentRevision = contentRevision
	facts.Commitment = commitment
	facts.VerifiedRows = 0
	facts.UpstreamOwner = EvidenceText{Value: c.evidenceRef.owner, Present: true}
	facts.UpstreamIncarnation = sealOptionalIncarnation{Value: c.evidenceRef.receipt.Identity.Incarnation, Present: true}
	facts.UpstreamSequence = EvidenceInteger{Value: c.evidenceRef.receipt.Identity.Sequence, Present: true}
	sealResult, err := canonicalResultDigest(sealIdentity, sealRequest, creditSourceSealResult{facts})
	if err != nil {
		t.Fatal(err)
	}
	operationBlob := canonicalEnrichBlob('S', n)
	seal := c.sealTemplate.with(t, map[string]any{
		"provider_operation_id":            opID,
		"command_incarnation":              incarnation[:],
		"command_sequence":                 int64(11),
		"operation_id":                     operationBlob[:],
		"request_digest":                   sealRequest[:],
		"entity_id":                        c.entityID,
		"publication_id":                   pubID,
		"result_digest":                    sealResult[:],
		"seal_owner_generation":            generation,
		"seal_content_revision":            contentRevision,
		"seal_commitment":                  commitment[:],
		"seal_verified_rows":               int64(0),
		"proof_revision":                   int64(1),
		"proof_pin_count":                  int64(1),
		"proof_reclaim_due_ms":             nil,
		"proof_cleanup_after_sequence":     int64(0),
		"upstream_evidence_owner":          c.evidenceRef.owner,
		"upstream_evidence_incarnation":    c.evidenceRef.receipt.Identity.Incarnation[:],
		"upstream_evidence_sequence":       c.evidenceRef.receipt.Identity.Sequence,
		"upstream_evidence_kind":           "evidence_seal",
		"source_dependency_owner":          nil,
		"source_dependency_incarnation":    nil,
		"source_dependency_sequence":       nil,
		"source_dependency_kind":           nil,
		"source_dependency_publication_id": nil,
		"source_dependency_provider":       nil,
	})
	seal.without(t, "upstream_evidence_kind", "source_dependency_kind").insert(t, c.prep.e.db, "enrichment_command_receipts")
	if _, err := c.prep.e.db.ExecContext(t.Context(), `INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed,seal_proof_owner,seal_proof_incarnation,seal_proof_sequence) VALUES(?,?,?,?,?,0,1,?,?,?)`, pubID, c.entityID, c.provider, "canonical-enrich-source", "2026-11-06T10:23:19.000000137+00:00", opID, incarnation[:], int64(11)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prep.e.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET source_publication_id=?,source_owner_generation=?,source_content_revision=?,source_evidence_id=?,source_subject_id=?,source_parser_version=?,source_next_position=0,source_expected_members=0,source_expected_bytes=?,source_algorithm=?,source_version=?,source_expected_commitment=?,source_evidence_owner=?,source_evidence_incarnation=?,source_evidence_sequence=? WHERE id=?`,
		pubID, generation, contentRevision, facts.EvidenceID, facts.SubjectID, facts.ParserVersion, facts.CanonicalBytes, facts.Algorithm, facts.SealVersion, commitment[:], c.evidenceRef.owner, c.evidenceRef.receipt.Identity.Incarnation[:], c.evidenceRef.receipt.Identity.Sequence, opID); err != nil {
		t.Fatal(err)
	}
	c.srcTemplate.with(t, map[string]any{"operation_id": opID}).insert(t, c.prep.e.db, "metadata_provider_operation_sources")

	h := c.header
	h.sourceManifest = commitment
	h.expectedManifest = effectiveBeginBuildSeed(h)
	turn := c.prep.newTurn(t)
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, turn.steps[enrichmentEffectiveOwner].statementStep, opID)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := effectiveBeginEntityFacts(t.Context(), turn, turn.steps[enrichmentEffectiveEntity].statementStep, c.entityID)
	if err != nil {
		t.Fatal(err)
	}
	producer, err := effectiveBeginReadProducer(t.Context(), turn, opID, entity, owner.provider, c.evidenceID, pubID, c.objectID)
	if err != nil {
		t.Fatal(err)
	}
	refs := effectiveBeginSourceReferences{evidence: c.evidenceRef, source: effectiveBeginSealReference{opID, CreditSourceSeal, 1, ReceiptReference{sealIdentity, sealRequest}, sealResult}}
	identity := CommandIdentity{Incarnation: incarnation, Sequence: 12, OperationID: canonicalEnrichBlob('B', n)}
	req := effectiveStageBeginRequest{variant: "begin", ownerID: opID, evidenceID: c.evidenceID, publicationID: pubID, objectID: c.objectID, owner: owner, entity: entity, producer: producer, header: h}
	begin, err := newEffectiveStageBeginFromSeals(identity, req, refs)
	if err != nil {
		t.Fatal(err)
	}
	image, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, turn.steps[enrichmentEffectiveInsert].statementStep, h, begin.digest, begin.retirementIncarnation)
	if err != nil {
		t.Fatal(err)
	}
	if err := effectiveBeginAttachHiddenHeader(t.Context(), turn, turn.steps[enrichmentEffectiveAttach].statementStep, opID, owner, identity, image); err != nil {
		t.Fatal(err)
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		t.Fatal(err)
	}
	result := EffectiveStageBeginResult{ProviderOperationID: opID, EntityID: entity.id, ObservedCatalogRevision: entity.revision, AppliedMS: now.UnixMilli(), AfterItem: owner.afterItem, ImageID: image.id, ImageRevision: image.revision}
	resultDigest, err := effectiveBeginResultDigest(identity, begin.digest, effectiveBeginResult{ProviderOperationID: result.ProviderOperationID, EntityID: result.EntityID, ObservedCatalogRevision: result.ObservedCatalogRevision, AppliedMS: result.AppliedMS, AfterItem: result.AfterItem, NextOrdinal: 0, ImageID: image.id, ImageRevision: image.revision, ResultCount: 0, MemberCohortLength: 0})
	if err != nil {
		t.Fatal(err)
	}
	result.Digest = resultDigest
	if _, err := turn.exec(t.Context(), turn.steps[enrichmentEffectiveRecord].statementStep, opID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], begin.digest[:], "effective_stage", entity.revision, result.AppliedMS, entity.id, owner.afterItem, int64(0), nil, image.id, image.revision, int64(0), resultDigest[:]); err != nil {
		t.Fatal(err)
	}
	inTurn := func(q string, args ...any) {
		t.Helper()
		if _, err := turn.transaction.conn.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	inTurn(`UPDATE enrichment_command_receipts SET source_dependency_owner=?,source_dependency_incarnation=?,source_dependency_sequence=?,source_dependency_publication_id=?,source_dependency_provider=? WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage'`, opID, incarnation[:], int64(11), pubID, c.provider, opID, incarnation[:], int64(11))
	inTurn(`UPDATE catalog_credit_images SET source_proof_owner=?,source_proof_incarnation=?,source_proof_sequence=?,source_proof_publication_id=?,source_proof_provider=? WHERE id=?`, opID, incarnation[:], int64(11), pubID, c.provider, image.id)
	inTurn(`UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count+2,proof_revision=proof_revision+1 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal'`, opID, incarnation[:], int64(11))
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prep.e.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET acknowledged_command_sequence=? WHERE id=?`, identity.Sequence, opID); err != nil {
		t.Fatal(err)
	}
	owner.imageID = sql.NullInt64{Int64: image.id, Valid: true}
	owner.lastSequence, owner.acknowledgedSequence = identity.Sequence, identity.Sequence
	acceptID := CommandIdentity{Incarnation: incarnation, Sequence: identity.Sequence + 1, OperationID: canonicalEnrichBlob('A', n)}
	seed := effectiveBeginBuildSeed(h)
	acceptReq := enrichAcceptRequest{
		variant:       "accept_provider_credits_v1",
		beginIdentity: begin.identity,
		beginRequest:  begin.digest,
		beginResult:   result,
		owner:         owner,
		entity:        entity,
		producer:      producer,
		image:         enrichAcceptImage{id: image.id, revision: image.revision, header: h, stagedCount: 0, stagedBytes: 0, seed: seed, chain: seed},
		prior:         c.priorContext(t),
		stageSlot:     0,
		reservedBytes: c.reserved,
	}
	cmd, err := newEnrichAcceptCommand(acceptID, begin, acceptReq)
	if err != nil {
		t.Fatal(err)
	}
	q := EnrichmentReceiptRequest{Receipt: ReceiptReference{cmd.identity, cmd.digest}, ProviderOperationID: opID, EntityID: c.entityID, LibraryID: 7}
	return cmd, q
}

func (c *canonicalEnrichComponent) action(t *testing.T, ctx context.Context, n int, population string) error {
	t.Helper()
	cmd, q := c.prepare(t, n)
	disclosure := &EnrichmentDisclosure{issuer: c.writer, scope: &enrichmentScope{ownerID: q.ProviderOperationID, libraryKey: c.libraryKey, libraryID: 7, entityID: c.entityID, lifetime: ctx}}
	var scope sealOwnerScope
	scope.identity, scope.request, scope.ownerID, scope.entityID = cmd.identity, cmd.digest, q.ProviderOperationID, q.EntityID
	in := &enrichmentInvocation{action: enrichmentApply, purpose: EnrichAccept, disclosure: disclosure, request: q,
		payload: &enrichmentFrozenPayload{scope: scope, variant: enrichmentAccept, accept: cmd}}

	c.trace.nextTurn()
	c.trace.armed = true
	before := c.trace.nativeBegins
	c.calls++
	if err := c.stream.append("attempt_start", map[string]any{"original_id": fmt.Sprintf("enrich/%d", n), "call_ordinal": c.calls, "turn_role": "ACCEPT", "original_population": population, "requested_reference": q, "qualification": false}); err != nil {
		return err
	}
	start := time.Now()
	value, _, err := c.writer.effective.runEnrichment(ctx, c.writer, in)
	returned := time.Now()
	began := c.trace.nativeBegins - before
	if began > 0 {
		c.physical += int(began)
	}
	var clock *n1Clock
	if c.measured {
		v := &n1Clock{InvocationStartedNS: n1NS(c.stream.origin, start), InvocationReturnedNS: n1NS(c.stream.origin, returned), InvocationNS: n1Duration(start, returned)}
		evidence := value.Outcome.Evidence
		if evidence != nil {
			v.HoldStartedNS = n1NS(c.stream.origin, evidence.HoldStartedAt)
			if evidence.PhysicalReturnedAt != nil {
				v.PhysicalReturnedNS = n1NS(c.stream.origin, *evidence.PhysicalReturnedAt)
				v.HoldNS = n1Duration(evidence.HoldStartedAt, *evidence.PhysicalReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil {
				v.CommitAttemptedNS = n1NS(c.stream.origin, *evidence.CommitAttemptedAt)
			}
			if evidence.CommitReturnedAt != nil {
				v.CommitReturnedNS = n1NS(c.stream.origin, *evidence.CommitReturnedAt)
			}
			if evidence.CommitAttemptedAt != nil && evidence.CommitReturnedAt != nil {
				v.CommitNS = n1Duration(*evidence.CommitAttemptedAt, *evidence.CommitReturnedAt)
			}
			if evidence.Purpose != EnrichAccept || evidence.Class != governor.Background || evidence.Durability != Normal || evidence.Policy == nil || evidence.Policy.Synchronous != 1 || evidence.Commit != CommitSucceeded || v.HoldNS == nil || *v.HoldNS <= 0 {
				err = errors.Join(err, errors.New("actual EnrichAccept physical class/policy/clock missing"))
			}
		} else {
			err = errors.Join(err, errors.New("EnrichAccept physical evidence missing"))
		}
		clock = v
	}
	var window *n1OwnerWindow
	if view, ok := c.opening.executor.lastOwnerWindow(); ok {
		window = n1BindOwnerWindowForPurpose(view, c.stream.origin, start, returned, EnrichAccept)
	}
	if err == nil && (began != 1 || c.trace.activeRows != 0 || c.trace.overflow || c.writer.effective.authority.Usage()[governor.Background] != (governor.Amounts{})) {
		err = errors.New("EnrichAccept physical turn/row/grant custody incomplete")
	}
	if err == nil {
		if value.Outcome.State != Applied || value.Outcome.Replayed || value.Value.accepted == nil {
			err = errors.New("EnrichAccept did not apply a fresh accepted result")
		} else {
			c.fresh++
		}
	}
	c.trace.armed = false
	err = errors.Join(err, c.stream.append("attempt", map[string]any{"original_id": fmt.Sprintf("enrich/%d", n), "call_ordinal": c.calls, "actual_physical_attempt_ordinal": c.physical, "native_begins_in_call": began, "turn_role": "ACCEPT", "original_population": population, "requested_reference": q, "owner_clock": clock, "owner_window": window, "raw": value, "native_units": c.trace.units[:c.trace.used], "error": checkpointError(err), "qualification": false}))
	if err == nil {
		ackIn := &enrichmentInvocation{action: enrichmentAcknowledge, purpose: EnrichAccept, disclosure: disclosure, request: q}
		_, ack, ackErr := c.writer.effective.runEnrichment(ctx, c.writer, ackIn)
		if ackErr != nil || ack.AcknowledgedThrough != cmd.identity.Sequence {
			err = errors.Join(errors.New("EnrichAccept ACK"), ackErr)
		} else {
			c.acked++
		}
	}
	// A retired predecessor keeps the accepted head instead of a live stage
	// slot; this harness reclaims the slot so the next original reuses it.
	if err == nil {
		prior := cmd.request.prior
		if prior.exists {
			if _, slotErr := c.prep.e.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET proof_stage_slot=NULL WHERE id=? AND proof_current_slot IS NULL`, prior.owner); slotErr != nil {
				err = slotErr
			}
		}
	}
	err = errors.Join(err, c.maintenance.drain(c.stream))
	return err
}

func runCanonicalHeldStagedEnrich(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest) (primary error) {
	if manifest.Facts.Residents != canonicalHeldItems || cfg.Attempts != 10000 {
		return errors.New("staged-enrich class requires the canonical 1M artifact and 10000 originals")
	}
	c := newCanonicalEnrichComponent(t, cfg.Attempts+canonicalEnrichWarmup, stream, true, true)
	if err := canonicalEnrichSustainedGate(c); err != nil {
		return err
	}
	defer func() {
		primary = errors.Join(primary, c.maintenance.close(), c.maintenance.drain(stream))
		usage := c.writer.effective.authority.Usage()
		if usage != ([4]governor.Amounts{}) {
			primary = errors.Join(primary, errors.New("staged-enrich shutdown retained custody"))
		}
		primary = errors.Join(primary, stream.append("final", map[string]any{"complete": primary == nil && c.fresh == cfg.Attempts+canonicalEnrichWarmup && c.acked == c.fresh, "fresh": c.fresh, "acked": c.acked, "calls": c.calls, "physical_turns": c.physical, "recipe": canonicalEnrichRecipe, "usage": usage, "error": checkpointError(primary), "qualification": false}))
	}()
	for n := 0; n < cfg.Attempts+canonicalEnrichWarmup; n++ {
		population := "measured"
		if n < canonicalEnrichWarmup {
			population = "warmup"
		}
		if err := c.action(t, ctx, n, population); err != nil {
			return err
		}
	}
	return nil
}

// Tiny functional control: the same prepared-image accept path without the
// canonical population or real-clock assertions.
func TestCanonicalStagedEnrichActionUsesExistingRuntime(t *testing.T) {
	path := t.TempDir() + "/enrich.ndjson"
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"qualification": false, "scope": "tiny functional control; no wall-clock hold assertions"}}
	c := newCanonicalEnrichComponent(t, 3, stream, false, false)
	if err := canonicalEnrichSustainedGate(c); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		if err := c.action(t, t.Context(), n, "control"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.maintenance.close(); err != nil {
		t.Fatal(err)
	}
	if err := c.maintenance.drain(stream); err != nil {
		t.Fatal(err)
	}
	if c.fresh != 3 || c.acked != 3 || c.calls != 3 || c.physical != 3 || c.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("incomplete staged-enrich Action", c)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// The start gate refuses if the funded keeper's pressure-recovery hook is not
// attached, and the canonical fixture uses the adopted phase 1a WAL policy.
func TestCanonicalEnrichKeeperHookAndWALPolicy(t *testing.T) {
	if canonicalN1Maintenance().policy != governor.Phase1aWALPolicy() {
		t.Fatal("canonical staged-enrich fixture does not use Phase1aWALPolicy")
	}
	path := t.TempDir() + "/gate.ndjson"
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stream := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"qualification": false}}
	c := newCanonicalEnrichComponent(t, 1, stream, false, false)
	if c.writer.effective.maintenance == nil || c.writer.effective.executor.keeper.Load() == nil {
		t.Fatal("canonical staged-enrich keeper/recovery hook not attached")
	}
	if err := canonicalEnrichSustainedGate(c); err != nil {
		t.Fatal("attached composition refused", err)
	}
	saved := c.writer.effective.maintenance
	c.writer.effective.maintenance = nil
	if err := canonicalEnrichSustainedGate(c); !errors.Is(err, errCanonicalEnrichSustained) {
		t.Fatal("start gate accepted a missing recovery hook", err)
	}
	c.writer.effective.maintenance = saved
	if err := c.maintenance.close(); err != nil {
		t.Fatal(err)
	}
	if err := c.maintenance.drain(stream); err != nil {
		t.Fatal(err)
	}
}
