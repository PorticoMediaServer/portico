package writer

import (
	"encoding/hex"
	"testing"
	"testing/synctest"
)

// nativeEnrichmentSeals is a native component seed, not a production disclosure
// or fixture permit. Both immutable proof receipts and body pins are written by
// the actual stage/seal bodies and W's canonical result/normalized codecs.
// Empty selected credits are intentional: no synthetic person or source tuple
// can stand in for an independently verified source-row producer.
type nativeEnrichmentSeals struct {
	scope                          sealOwnerScope // next ORIGINAL command after the two acknowledged seals
	payload                        EvidenceStagePayload
	build                          evidenceSealRoot
	source                         sourceSealRoot
	evidenceReceipt, sourceReceipt ReceiptReference
	evidenceResult, sourceResult   ResultDigest
	evidenceFacts, sourceFacts     sealResultFacts
}

func nativeEnrichmentSealsFixture(t *testing.T) (*executor, nativeEnrichmentSeals) {
	t.Helper()
	w, scope, _ := evidenceProducerFixture(t)
	payload, err := PrepareEvidenceStagePayload(draftMovieStageHeader(t), []EvidenceCreditInput{})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := prepareEvidenceSealManifest(t.Context(), payload, enrichmentControlCapture{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := evidenceStagePrefix{root: evidenceSealRoot{buildID: "native-lineage-build", evidenceID: payload.header.ID(), parser: payload.header.input.ParserVersion, buildGeneration: 89, subjectID: 151, rows: 1}, manifest: manifest, expires: "2100-11-05T13:00:00Z"}
	scope.request, err = canonicalRequestDigest(EvidenceSeal, 1, scope.identity, struct {
		Scope  sealOwnerScope
		Prefix evidenceStagePrefix
		Header EvidenceHeaderInput
	}{scope, prefix, payload.header.input})
	if err != nil {
		t.Fatal(err)
	}
	turn := sealEffectTurnPurpose(t, w, evidenceProducerDefinitions(), EvidenceStage)
	prefix, err = createEvidenceBuild(t.Context(), turn, scope, prefix, payload)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := advanceEvidenceSeal(t.Context(), turn, scope, prefix.root)
	if err != nil {
		t.Fatal(err)
	}
	content := sealReceiptContent{payload.header.input, manifest, maxSealRetainedBytes}
	evidenceResult, evidenceDigest, err := projectEvidenceSealResult(scope, prefix.root, actual, content)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeEvidenceSealReceipt(t.Context(), turn, scope, prefix.root, actual, content, evidenceDigest); err != nil {
		t.Fatal(err)
	}
	if err = accountEvidenceSealReference(t.Context(), turn, scope, prefix.root, actual, manifest, prefix.charge, prefix.providerCharge, prefix.providerOther); err != nil {
		t.Fatal(err)
	}
	// Harness only: publish/ACK this completed native component command. No
	// production entry, common terminal, resource or authority claim is made.
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET last_command_sequence=?,acknowledged_command_sequence=? WHERE id=?`, scope.identity.Sequence, scope.identity.Sequence, scope.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	out := nativeEnrichmentSeals{payload: payload, build: prefix.root, evidenceReceipt: ReceiptReference{scope.identity, scope.request}, evidenceResult: evidenceDigest, evidenceFacts: evidenceResult.facts}
	out.build.revision = actual.revision
	e := evidenceResult.facts
	upstream := normalizedEvidenceReceipt{ownerID: scope.ownerID, commandKind: NormalizedEvidenceKind, identity: scope.identity, request: scope.request, entityID: scope.entityID, version: e.SealVersion, evidenceID: e.EvidenceID, subjectID: e.SubjectID, parser: e.ParserVersion, provider: e.Provider, kind: e.SubjectKind, providerID: e.ProviderID, language: e.Language, region: e.Region, shape: e.RequestShape, observation: e.ObservationDigest, ownerGeneration: e.OwnerGeneration, buildID: e.BuildID.Value, buildGeneration: e.BuildGeneration.Value, revision: e.ContentRevision, manifest: manifest}
	scope.last, scope.ack = scope.identity.Sequence, scope.identity.Sequence
	scope.identity.Sequence++
	scope.identity.OperationID = [16]byte{213}
	source := normalizedSourceContent{upstream: upstream, header: normalizedSourceHeader{id: "102132435465768798a9bacbdcedfe0f", entityID: scope.entityID, provider: scope.provider, record: "opaque-native-lineage-source", observed: payload.header.input.ObservedAt, count: 0}, members: []normalizedSourceMember{}, intent: NormalizedSourceIntent{ProviderOperationID: scope.ownerID, SourceOwnerGeneration: scope.generation, SourceContentRevision: scope.sourceRevision + 1, SourceEvidenceOwner: upstream.ownerID, SourceEvidenceIncarnation: upstream.identity.Incarnation, SourceEvidenceSequence: upstream.identity.Sequence, SourceEvidenceID: upstream.evidenceID, SourceSubjectID: upstream.subjectID, SourceParserVersion: upstream.parser, SourceNextPosition: 0, SourceExpectedMembers: 0, SourcePublicationID: "102132435465768798a9bacbdcedfe0f", EntityID: scope.entityID, Provider: scope.provider, SourceAlgorithm: NormalizedSourceAlgorithm, SourceVersion: 1}}
	source, sm, err := prepareSourceSealManifest(t.Context(), source, enrichmentControlCapture{})
	if err != nil {
		t.Fatal(err)
	}
	root := sourceSealRoot{publicationID: source.header.id, evidenceID: upstream.evidenceID, parser: upstream.parser, subjectID: upstream.subjectID, upstreamOwner: upstream.ownerID, upstreamIncarnation: upstream.identity.Incarnation, upstreamSequence: upstream.identity.Sequence, manifest: sm}
	scope.request, err = canonicalRequestDigest(CreditSourceSeal, 1, scope.identity, struct {
		Scope  sealOwnerScope
		Source normalizedSourceContent
		Root   sourceSealRoot
	}{scope, source, root})
	if err != nil {
		t.Fatal(err)
	}
	turn = sealEffectTurnPurpose(t, w, sourceSealDefinitions(), CreditStage)
	progress, err := createSourceStage(t.Context(), turn, scope, source.header, root, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	scope.sourceRevision = progress.revision
	actual, err = advanceSourceSeal(t.Context(), turn, scope, root)
	if err != nil {
		t.Fatal(err)
	}
	content = sealReceiptContent{payload.header.input, sm, maxSealRetainedBytes}
	sourceResult, sourceDigest, err := projectSourceSealResult(scope, root, actual, content)
	if err != nil {
		t.Fatal(err)
	}
	// Allocation retained one upstream dependency: revision/pins are now 2/2.
	if err = writeSourceSealReceipt(t.Context(), turn, scope, root, actual, content, sourceDigest, 2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET last_command_sequence=?,acknowledged_command_sequence=? WHERE id=?`, scope.identity.Sequence, scope.identity.Sequence, scope.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	out.source, out.sourceReceipt, out.sourceResult, out.sourceFacts = root, ReceiptReference{scope.identity, scope.request}, sourceDigest, sourceResult.facts
	scope.last, scope.ack, scope.sourceRevision = scope.identity.Sequence, scope.identity.Sequence, actual.revision
	scope.identity.Sequence++
	scope.identity.OperationID = [16]byte{229}
	out.scope = scope
	return w, out
}

func TestNativeEnrichmentSealsHaveActualSavedLineage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w, f := nativeEnrichmentSealsFixture(t)
		var evidenceID, publication, upstream, algorithm, commitment, build string
		var es, ss, epins, spins, buildRevision, sourceRevision int64
		err := w.db.QueryRowContext(t.Context(), `SELECT e.id,p.publication_id,s.upstream_evidence_owner,s.seal_algorithm,hex(s.seal_commitment),o.evidence_build_id,e.seal_proof_sequence,p.seal_proof_sequence,r.proof_pin_count,s.proof_pin_count,b.content_revision,o.source_content_revision FROM metadata_evidence e JOIN metadata_evidence_builds b ON b.evidence_id=e.id JOIN metadata_provider_operations o ON o.id=b.owner_operation JOIN credit_source_publications p ON p.publication_id=o.source_publication_id JOIN enrichment_command_receipts r ON r.provider_operation_id=e.seal_proof_owner AND r.command_incarnation=e.seal_proof_incarnation AND r.command_sequence=e.seal_proof_sequence JOIN enrichment_command_receipts s ON s.provider_operation_id=p.seal_proof_owner AND s.command_incarnation=p.seal_proof_incarnation AND s.command_sequence=p.seal_proof_sequence WHERE e.id=?`, f.payload.header.ID()).Scan(&evidenceID, &publication, &upstream, &algorithm, &commitment, &build, &es, &ss, &epins, &spins, &buildRevision, &sourceRevision)
		if err != nil {
			t.Fatal(err)
		}
		if evidenceID != f.build.evidenceID || publication != f.source.publicationID || upstream != f.scope.ownerID || algorithm != NormalizedSourceAlgorithm || commitment != hexUpper(f.source.manifest.commitment[:]) || build != f.build.buildID || es != f.evidenceReceipt.Identity.Sequence || ss != f.sourceReceipt.Identity.Sequence || epins != 3 || spins != 1 || buildRevision != f.build.revision || sourceRevision != f.scope.sourceRevision || f.evidenceResult == (ResultDigest{}) || f.sourceResult == (ResultDigest{}) {
			t.Fatal("native lineage mismatch", evidenceID, publication, upstream, algorithm, commitment, build, es, ss, epins, spins, buildRevision, sourceRevision)
		}
	})
}

func hexUpper(b []byte) string {
	const digits = "0123456789ABCDEF"
	dst := make([]byte, hex.EncodedLen(len(b)))
	for i, v := range b {
		dst[i*2] = digits[v>>4]
		dst[i*2+1] = digits[v&15]
	}
	return string(dst)
}
