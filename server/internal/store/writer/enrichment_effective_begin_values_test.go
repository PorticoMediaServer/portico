package writer

import (
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func effectiveBeginHeaderFixture() effectiveBeginHeader {
	h := effectiveBeginHeader{entityID: 101, sourceRevision: 13, identityRevision: 17, metadataRevision: 19,
		ownerRevision: 23, policyRevision: 37, consentRevision: 41, language: "en-CA", expectedCount: 150, expectedBytes: 12345}
	for i := range h.entityIncarnation {
		h.entityIncarnation[i], h.policyIncarnation[i] = byte(48+i), byte(80+i)
	}
	for i := range h.declaredInput {
		h.declaredInput[i], h.sourceManifest[i], h.expectedManifest[i] = byte(48+i), byte(80+i), byte(112+i)
	}
	return h
}

func TestEffectiveBeginDeclaredBuildSeedReference(t *testing.T) {
	// Independent Python hashlib/struct concatenation of INT's exact seed wire,
	// not a call to any Go request/result or materialized-member implementation.
	h := effectiveBeginHeaderFixture()
	seed := effectiveBeginBuildSeed(h)
	if hex.EncodeToString(seed[:]) != "ab934bd4e769ff1a96d27b8c1bc060c368ff3daa2eb0b744e8acf825049228db" {
		t.Fatal("declared build seed differs from independent 146-byte reference")
	}
	for _, mutate := range []func(*effectiveBeginHeader){
		func(h *effectiveBeginHeader) { h.entityID++ },
		func(h *effectiveBeginHeader) { h.declaredInput[0] ^= 1 },
		func(h *effectiveBeginHeader) { h.sourceManifest[31] ^= 1 },
		func(h *effectiveBeginHeader) { h.language += "x" },
		func(h *effectiveBeginHeader) { h.expectedCount++ },
		func(h *effectiveBeginHeader) { h.expectedBytes++ },
	} {
		other := h
		mutate(&other)
		if effectiveBeginBuildSeed(other) == seed {
			t.Fatal("seed omitted declared context")
		}
	}
	// Storage image identity and native authority are separate CAS/receipt facts;
	// the seed's coverage is exactly INT's algorithm, not an invented full proof.
}

func TestEffectiveBeginNativeHeaderAndCarrierRollback(t *testing.T) {
	w, _, _ := testExecutor(t)
	owner, _, entity := effectiveBeginNativePopulation(t, w)
	// This sets up the admitted-predecessor state only. Testing the real durable
	// common ACK/entry protocol belongs to the complete handler, not this carrier.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET acknowledged_command_sequence=11 WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	owner.acknowledgedSequence = 11
	turn, steps := effectiveBeginFactTurn(t, w)
	h := effectiveBeginHeaderFixture()
	seed := effectiveBeginBuildSeed(h)
	request := RequestDigest{11, 19, 31}
	nonce := [16]byte{99, 101, 103}
	image, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveInsert], h, request, nonce)
	if err != nil {
		t.Fatal("actual native RETURNING", image, err)
	}
	imageID, imageRevision := image.id, image.revision

	// Native column names, not the production INSERT order, key every expected
	// value. A future column is an explicit coverage failure rather than ignored.
	rows, err := turn.transaction.conn.QueryContext(t.Context(), "SELECT * FROM catalog_credit_images WHERE id=?", imageID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !rows.Next() {
		t.Fatal("header readback", err, rows.Err())
	}
	values, destinations := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"id": imageID, "revision": imageRevision, "entity_id": int64(101),
		"entity_incarnation": h.entityIncarnation[:], "source_revision": int64(13),
		"identity_revision": int64(17), "metadata_revision": int64(19), "owner_revision": int64(23),
		"policy_incarnation": h.policyIncarnation[:], "policy_revision": int64(37), "consent_revision": int64(41),
		"metadata_language": "en-CA", "source_manifest_digest": h.sourceManifest[:],
		"build_request_digest": request[:], "manifest_algorithm": "portico-credit-manifest-sha256-chain-v1",
		"build_expected_count": int64(150), "build_expected_bytes": int64(12345),
		"build_expected_digest": h.expectedManifest[:], "build_staged_count": int64(0), "build_staged_bytes": int64(0),
		"build_verified_count": int64(0), "build_verified_bytes": int64(0), "build_seed": seed[:], "build_chain": seed[:],
		"build_after_ord": int64(-1), "member_count": int64(0), "prepared_bytes": int64(0), "manifest_digest": nil,
		"current_input_digest": h.declaredInput[:], "sealed": int64(0), "remaining_rows": int64(0), "remaining_bytes": int64(0),
		"reclaim_state": "held", "reclaim_after_ord": int64(-1), "command_incarnation": nonce[:],
		"last_command_sequence": int64(0), "acknowledged_command_sequence": int64(0), "job_revision": int64(1),
		"job_lease_owner": int64(0), "job_lease_generation": int64(0), "job_lease_expires_ms": int64(0),
		"job_attempts": int64(0), "job_next_attempt_ms": nil, "job_due_ms": nil,
		"source_proof_owner": nil, "source_proof_incarnation": nil, "source_proof_sequence": nil,
		"source_proof_kind": nil, "source_proof_publication_id": nil, "source_proof_provider": nil,
	}
	if len(columns) != len(expected) {
		t.Fatal("header readback does not cover physical schema", len(columns), len(expected))
	}
	for i, name := range columns {
		want, ok := expected[name]
		if !ok || !reflect.DeepEqual(values[i], want) {
			t.Fatalf("header column %s differs: %v != %v", name, values[i], want)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	var operation [16]byte
	operation[0] = 239
	identity := CommandIdentity{Incarnation: owner.incarnation, Sequence: 12, OperationID: operation}
	for _, mutate := range []func(*effectiveBeginOwner){
		func(v *effectiveBeginOwner) { v.generation++ },
		func(v *effectiveBeginOwner) { v.phase += "-stale" },
		func(v *effectiveBeginOwner) { v.lease += "-stale" },
		func(v *effectiveBeginOwner) { v.page++ },
		func(v *effectiveBeginOwner) { v.afterItem += "-stale" },
	} {
		stale := owner
		mutate(&stale)
		if err := effectiveBeginAttachHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveAttach], "operation-one", stale, identity, image); !errors.Is(err, ErrConflict) {
			t.Fatal("stale native owner CAS did not refuse", err)
		}
	}
	if err := effectiveBeginAttachHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveAttach], "operation-one", owner, identity, image); err != nil {
		t.Fatal("exact operation pin CAS", err)
	}
	// Synthetic digest tests only the physical immutable carrier and NULL/int
	// bindings. No result validation or Applied is claimed before helper landing.
	syntheticDigest := [32]byte{73, 79}
	if _, err := turn.exec(t.Context(), steps[enrichmentEffectiveRecord], "operation-one", owner.incarnation[:],
		int64(12), operation[:], request[:], "effective_stage", entity.revision, int64(1793861096027), entity.id,
		owner.afterItem, int64(0), nil, imageID, imageRevision, int64(0), syntheticDigest[:]); err != nil {
		t.Fatal(err)
	}
	got, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], "operation-one")
	if err != nil || got.afterItem != owner.afterItem || got.page != owner.page || got.phase != owner.phase ||
		got.status != owner.status || !got.imageID.Valid || got.imageID.Int64 != imageID || got.lastSequence != 12 {
		t.Fatal("BEGIN changed unrelated progress or lost pin", got, err)
	}
	current, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], entity.id)
	if err != nil || !reflect.DeepEqual(current, entity) {
		t.Fatal("hidden stage changed serving entity", current, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var headers, receipts, last int64
	var pin any
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), "SELECT count(*) FROM enrichment_command_receipts WHERE command_sequence=12").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), "SELECT credit_image_id,last_command_sequence FROM metadata_provider_operations WHERE id='operation-one'").Scan(&pin, &last); err != nil {
		t.Fatal(err)
	}
	if headers != 0 || receipts != 0 || pin != nil || last != 11 {
		t.Fatal("rollback left hidden/header/pin/receipt prefix", headers, receipts, pin, last)
	}
}

func TestEffectiveBeginMalformedIntentHasNoBodyEffects(t *testing.T) {
	w, _, _ := testExecutor(t)
	effectiveBeginNativePopulation(t, w)
	turn, steps := effectiveBeginFactTurn(t, w)
	for _, mutate := range []func(*effectiveBeginHeader){
		func(v *effectiveBeginHeader) { v.entityID = 0 },
		func(v *effectiveBeginHeader) { v.sourceRevision = 0 },
		func(v *effectiveBeginHeader) { v.ownerRevision = -1 },
		func(v *effectiveBeginHeader) { v.policyRevision = -1 },
		func(v *effectiveBeginHeader) { v.expectedCount = -1 },
		func(v *effectiveBeginHeader) { v.expectedBytes = -1 },
		func(v *effectiveBeginHeader) { v.expectedCount = 0 },
		func(v *effectiveBeginHeader) { v.expectedBytes = 0 },
		func(v *effectiveBeginHeader) { v.language = "en\x00CA" },
		func(v *effectiveBeginHeader) { v.language = "\xff" },
		func(v *effectiveBeginHeader) { v.expectedCount, v.expectedBytes = 0, 0 },
	} {
		input := effectiveBeginHeaderFixture()
		mutate(&input)
		before := turn.transaction.quantum.Remaining()
		if _, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveInsert], input, RequestDigest{}, [16]byte{}); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed declared intent accepted", err)
		}
		if turn.transaction.quantum.Remaining() != before {
			t.Fatal("malformed intent entered a statement step")
		}
	}
	var count int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_images").Scan(&count); err != nil || count != 0 {
		t.Fatal("malformed intent created hidden rows", count, err)
	}
	input := effectiveBeginHeaderFixture()
	input.expectedCount, input.expectedBytes = 0, 0
	input.expectedManifest = effectiveBeginBuildSeed(input)
	image, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveInsert], input, RequestDigest{}, [16]byte{})
	if err != nil {
		t.Fatal("well-formed empty intent", err)
	}
	var sealed, members int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT sealed,member_count FROM catalog_credit_images WHERE id=?", image.id).Scan(&sealed, &members); err != nil || sealed != 0 || members != 0 {
		t.Fatal("empty BEGIN claimed seal/completion", sealed, members, err)
	}
}
