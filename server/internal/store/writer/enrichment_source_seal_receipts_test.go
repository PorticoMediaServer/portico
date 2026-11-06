package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func sourceSealFixture(t *testing.T) (*executor, sealOwnerScope, sourceSealRoot, sealReceiptContent) {
	t.Helper()
	w, owner, identity := draftSourceStageFixture(t)
	var entityInc [16]byte
	for i := range entityInc {
		entityInc[i] = byte(48 + i)
	}
	scope := sealOwnerScope{ownerID: owner.operationID, provider: owner.provider, lease: owner.lease, leaseUntil: owner.leaseUntil, afterItem: "opaque-page:61", selectedID: "opaque:selection", identity: identity,
		generation: 31, last: 11, ack: 11, entityID: 101, entityRevision: 203, page: 59, sourceRevision: 47, entityIncarnation: entityInc,
		selection: effectiveBeginSelection{Incarnation: [16]byte{71}, Revision: 73, Role: "primary", Kind: "movie", ID: "opaque:selection", Status: "selected"}, selectionObserved: "2026-11-06T12:00:00Z"}
	for _, q := range []string{
		`DELETE FROM credit_source_publication_members`,
		`UPDATE enrichment_command_receipts SET proof_pin_count=2 WHERE command_kind='evidence_seal'`,
		`UPDATE credit_source_publications SET member_count=0`,
		`UPDATE metadata_provider_operations SET source_next_position=0,source_expected_members=0,source_expected_bytes=917,source_algorithm='portico.normalized-credit-source.sha256.v1'`,
		`UPDATE metadata_evidence SET sealed=1,seal_proof_owner='operation-one',seal_proof_incarnation=(SELECT command_incarnation FROM metadata_provider_operations WHERE id='operation-one'),seal_proof_sequence=7 WHERE id='evidence-original:197'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	root := sourceSealRoot{publicationID: owner.publicationID, evidenceID: "evidence-original:197", parser: "fixture-parser", subjectID: 193, upstreamOwner: owner.operationID, upstreamIncarnation: identity.Incarnation, upstreamSequence: 7, manifest: normalizedSealManifest{normalizedSourceAlgorithm, 1, 0, 917, normalizedSealCommitment{}}}
	h := EvidenceHeaderInput{ID: root.evidenceID, SubjectID: 193, ParserVersion: root.parser, Provider: scope.provider, SubjectKind: "movie", ProviderID: scope.selectedID, Language: "en-CA", Region: "CA", RequestShape: "detail-v1"}
	for i := range h.ObservationDigest {
		h.ObservationDigest[i] = 170
	}
	return w, scope, root, sealReceiptContent{h, root.manifest, 8876032}
}
func sourceSealDefinitions() []stmt.Definition {
	return append(append([]stmt.Definition{}, enrichmentSealAdvanceDefinitions()...), append(enrichmentSealReceiptDefinitions(), enrichmentSourceAllocationDefinitions()...)...)
}
func TestSourceSealReceiptRetainsUpstreamAndNativeCursor(t *testing.T) {
	w, scope, root, content := sourceSealFixture(t)
	turn := sealEffectTurn(t, w, sourceSealDefinitions())
	facts, err := advanceSourceSeal(t.Context(), turn, scope, root)
	if err != nil {
		t.Fatal(err)
	}
	if facts.revision != 48 || facts.next != 0 || facts.observedCatalogRevision != 203 || facts.afterItem != scope.afterItem {
		t.Fatal("source native facts aliased to evidence cursor", facts)
	}
	if err := writeSourceSealReceipt(t.Context(), turn, scope, root, facts, content, ResultDigest{197}, 1, 2); err != nil {
		t.Fatal(err)
	}
	var sealed, seq, pins, revision, next, resultCount int64
	var owner, kind, upstream, pub string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT p.sealed,p.seal_proof_owner,p.seal_proof_sequence,r.command_kind,r.upstream_evidence_owner,r.publication_id,r.after_ordinal,r.result_count,e.proof_pin_count,e.proof_revision
 FROM credit_source_publications p JOIN enrichment_command_receipts r ON r.provider_operation_id=p.seal_proof_owner AND r.command_incarnation=p.seal_proof_incarnation AND r.command_sequence=p.seal_proof_sequence
 JOIN enrichment_command_receipts e ON e.provider_operation_id=r.upstream_evidence_owner AND e.command_incarnation=r.upstream_evidence_incarnation AND e.command_sequence=r.upstream_evidence_sequence WHERE p.publication_id=?`, root.publicationID).Scan(&sealed, &owner, &seq, &kind, &upstream, &pub, &next, &resultCount, &pins, &revision); err != nil {
		t.Fatal(err)
	}
	if sealed != 1 || owner != scope.ownerID || seq != 12 || kind != "credit_source_seal" || upstream != root.upstreamOwner || pub != root.publicationID || next != 0 || resultCount != 0 || pins != 3 || revision != 2 {
		t.Fatal("native proof dependency/link", sealed, owner, seq, kind, upstream, pub, next, resultCount, pins, revision)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT sealed FROM credit_source_publications WHERE publication_id=?`, root.publicationID).Scan(&sealed); err != nil || sealed != 0 {
		t.Fatal("source seal escaped rollback", sealed, err)
	}
}
func TestSourceSealStaleUpstreamPinRollsBackReceiptAndCAS(t *testing.T) {
	w, scope, root, content := sourceSealFixture(t)
	turn := sealEffectTurn(t, w, sourceSealDefinitions())
	facts, err := advanceSourceSeal(t.Context(), turn, scope, root)
	if err != nil {
		t.Fatal(err)
	}
	err = writeSourceSealReceipt(t.Context(), turn, scope, root, facts, content, ResultDigest{197}, 2, 2)
	if !errors.Is(err, ErrConflict) {
		t.Fatal("stale upstream proof revision accepted", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var receipts, revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM enrichment_command_receipts WHERE command_kind='credit_source_seal'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("failed receipt survived", receipts, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT source_content_revision FROM metadata_provider_operations WHERE id='operation-one'`).Scan(&revision); err != nil || revision != 47 {
		t.Fatal("failed source revision survived", revision, err)
	}
}

func TestSourceFirstAllocationPinsBeforeMembersAndRollsBack(t *testing.T) {
	w, scope, root, _ := sourceSealFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET source_publication_id=NULL,source_owner_generation=NULL,source_evidence_id=NULL,source_subject_id=NULL,source_parser_version=NULL,source_next_position=NULL,source_expected_members=NULL,source_expected_bytes=NULL,source_algorithm=NULL,source_version=NULL,source_expected_commitment=NULL,source_evidence_owner=NULL,source_evidence_incarnation=NULL,source_evidence_sequence=NULL WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE enrichment_command_receipts SET proof_pin_count=1 WHERE command_kind='evidence_seal'`); err != nil {
		t.Fatal(err)
	}
	root.publicationID = "33333333333333333333333333333333"
	header := normalizedSourceHeader{root.publicationID, scope.entityID, scope.provider, "opaque-ref:241", "2026-11-06T10:00:00Z", 0}
	turn := sealEffectTurn(t, w, sourceSealDefinitions())
	got, err := createSourceStage(t.Context(), turn, scope, header, root, 1, 1)
	if err != nil || got != (sourceStageProgress{0, 48}) {
		t.Fatal("first durable source custody", got, err)
	}
	var publication string
	var generation, pins, members int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT o.source_publication_id,o.source_owner_generation,r.proof_pin_count,(SELECT count(*) FROM credit_source_publication_members WHERE publication_id=o.source_publication_id)
 FROM metadata_provider_operations o JOIN enrichment_command_receipts r ON r.provider_operation_id=o.source_evidence_owner AND r.command_incarnation=o.source_evidence_incarnation AND r.command_sequence=o.source_evidence_sequence WHERE o.id='operation-one'`).Scan(&publication, &generation, &pins, &members); err != nil {
		t.Fatal(err)
	}
	if publication != root.publicationID || generation != 31 || pins != 2 || members != 0 {
		t.Fatal("publication not pinned at first allocation", publication, generation, pins, members)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM credit_source_publications WHERE publication_id=?`, root.publicationID).Scan(&n); err != nil || n != 0 {
		t.Fatal("first allocation escaped rollback", n, err)
	}
}
