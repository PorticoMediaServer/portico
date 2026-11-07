package writer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func nativeSealProjectionAnswer(t *testing.T, a sealResultAnswer) (sealOwnerScope, sealMutationFacts, sealReceiptContent) {
	t.Helper()
	i, q, _ := sealResultInputs(t, a)
	f := a.Facts
	scope := sealOwnerScope{ownerID: f.ProviderOperationID, provider: f.Provider, selectedID: f.ProviderID,
		identity: i, request: q, generation: f.OwnerGeneration, last: i.Sequence - 1, ack: i.Sequence - 1,
		entityID: f.EntityID, entityRevision: f.ObservedCatalogRevision, afterItem: f.AfterItem,
		lease: "native-lease", leaseUntil: "2100-11-05T12:00:00Z", sourceRevision: f.ContentRevision - 1,
		selection: effectiveBeginSelection{Revision: 7, Role: "primary", Kind: "movie", Status: "selected", ID: f.ProviderID}}
	h := EvidenceHeaderInput{ID: f.EvidenceID, SubjectID: f.SubjectID, ParserVersion: f.ParserVersion, Provider: f.Provider,
		SubjectKind: f.SubjectKind, ProviderID: f.ProviderID, Language: f.Language, Region: f.Region, RequestShape: f.RequestShape}
	observation, err := hex.DecodeString(f.ObservationDigest)
	if err != nil || len(observation) != 32 {
		t.Fatal(err)
	}
	copy(h.ObservationDigest[:], observation)
	content := sealReceiptContent{h, normalizedSealManifest{f.Algorithm, f.SealVersion, f.VerifiedRows, f.CanonicalBytes, normalizedSealCommitment(f.Commitment)}, f.RetainedBytes}
	return scope, sealMutationFacts{f.ContentRevision, f.NextOrdinal, f.ObservedCatalogRevision, f.AppliedMS, f.AfterItem}, content
}

func TestSealNativeProjectionMatchesIndependentResultAnswers(t *testing.T) {
	for _, a := range sealResultAnswers(t) {
		t.Run(a.Kind, func(t *testing.T) {
			scope, actual, content := nativeSealProjectionAnswer(t, a)
			f := a.Facts
			var got sealResultFacts
			var digest ResultDigest
			var err error
			if a.Kind == "evidence_seal" {
				root := evidenceSealRoot{f.BuildID.Value, f.EvidenceID, f.ParserVersion, f.BuildGeneration.Value, f.ContentRevision - 1, f.SubjectID, f.VerifiedRows}
				var result evidenceSealResult
				result, digest, err = projectEvidenceSealResult(scope, root, actual, content)
				got = result.facts
			} else {
				root := sourceSealRoot{publicationID: f.PublicationID.Value, evidenceID: f.EvidenceID, parser: f.ParserVersion,
					subjectID: f.SubjectID, next: f.NextOrdinal, expected: f.VerifiedRows, upstreamOwner: f.UpstreamOwner.Value,
					upstreamIncarnation: f.UpstreamIncarnation.Value, upstreamSequence: f.UpstreamSequence.Value, manifest: content.manifest}
				var result creditSourceSealResult
				result, digest, err = projectSourceSealResult(scope, root, actual, content)
				got = result.facts
			}
			if err != nil || !reflect.DeepEqual(got, f) || hex.EncodeToString(digest[:]) != a.SHA256 {
				t.Fatal("native projection changed a saved result field", got, f, digest, err)
			}
		})
	}
}

func TestSealNativeProjectionRejectsContextAndReturnedDrift(t *testing.T) {
	a := sealResultAnswers(t)[0]
	f := a.Facts
	scope, actual, content := nativeSealProjectionAnswer(t, a)
	root := evidenceSealRoot{f.BuildID.Value, f.EvidenceID, f.ParserVersion, f.BuildGeneration.Value, f.ContentRevision - 1, f.SubjectID, f.VerifiedRows}
	for name, mutate := range map[string]func(*sealOwnerScope, *evidenceSealRoot, *sealMutationFacts, *sealReceiptContent){
		"native_revision": func(_ *sealOwnerScope, _ *evidenceSealRoot, f *sealMutationFacts, _ *sealReceiptContent) {
			f.revision++
		},
		"catalog_revision": func(_ *sealOwnerScope, _ *evidenceSealRoot, f *sealMutationFacts, _ *sealReceiptContent) {
			f.observedCatalogRevision++
		},
		"opaque_after_item": func(_ *sealOwnerScope, _ *evidenceSealRoot, f *sealMutationFacts, _ *sealReceiptContent) {
			f.afterItem += "different"
		},
		"evidence_subject": func(_ *sealOwnerScope, r *evidenceSealRoot, _ *sealMutationFacts, _ *sealReceiptContent) {
			r.subjectID++
		},
		"provider": func(_ *sealOwnerScope, _ *evidenceSealRoot, _ *sealMutationFacts, c *sealReceiptContent) {
			c.header.Provider = "other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, r, f, c := scope, root, actual, content
			mutate(&s, &r, &f, &c)
			v, d, err := projectEvidenceSealResult(s, r, f, c)
			if !errors.Is(err, ErrConflict) || v != (evidenceSealResult{}) || d != (ResultDigest{}) {
				t.Fatal("drift emitted partial result", v, d, err)
			}
		})
	}
}

func TestSealNativeProjectionBindsActualReceiptMutation(t *testing.T) {
	// Canonical native component fixture ONLY: no disclosure/proof/physical
	// recipe is issued. Actual SQL RETURNING and receipt are connected here.
	w, scope, payload := evidenceProducerFixture(t)
	m, err := prepareEvidenceSealManifest(t.Context(), payload, enrichmentControlCapture{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := evidenceStagePrefix{root: evidenceSealRoot{buildID: "build-projection-original", evidenceID: payload.header.ID(), parser: payload.header.input.ParserVersion,
		buildGeneration: 89, subjectID: payload.header.input.SubjectID, rows: int64(1 + payload.CreditCount())}, manifest: m, expires: "2100-11-05T13:00:00Z"}
	turn := sealEffectTurnPurpose(t, w, evidenceProducerDefinitions(), EvidenceStage)
	prefix, err = createEvidenceBuild(t.Context(), turn, scope, prefix, payload)
	if err == nil {
		prefix, err = stageEvidenceCredits(t.Context(), turn, scope, prefix, payload.Credits())
	}
	if err != nil {
		t.Fatal(err)
	}
	actual, err := advanceEvidenceSeal(t.Context(), turn, scope, prefix.root)
	if err != nil {
		t.Fatal(err)
	}
	content := sealReceiptContent{payload.header.input, m, 8876032}
	result, digest, err := projectEvidenceSealResult(scope, prefix.root, actual, content)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeEvidenceSealReceipt(t.Context(), turn, scope, prefix.root, actual, content, digest); err != nil {
		t.Fatal(err)
	}
	var revision, catalog, applied, next int64
	var after string
	var savedDigest []byte
	if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT seal_content_revision,accepted_revision,applied_ms,after_ordinal,after_item,result_digest FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`, scope.ownerID, scope.identity.Incarnation[:], scope.identity.Sequence).Scan(&revision, &catalog, &applied, &next, &after, &savedDigest); err != nil {
		t.Fatal(err)
	}
	f := result.facts
	if revision != actual.revision || catalog != actual.observedCatalogRevision || applied != actual.appliedMS || next != actual.next || after != actual.afterItem ||
		f.ContentRevision != revision || f.ObservedCatalogRevision != catalog || f.AppliedMS != applied || f.NextOrdinal != next || f.AfterItem != after || !bytes.Equal(savedDigest, digest[:]) {
		t.Fatal("actual native result was guessed, relabelled or swapped")
	}
}
