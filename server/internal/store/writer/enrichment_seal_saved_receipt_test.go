package writer

import (
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

// An independent explicit native projection; never fill omissions from schema
// defaults/current rows. The original data-only answers provide expected facts.
func savedSealAnswerRow(t *testing.T, answer sealResultAnswer) []any {
	t.Helper()
	i, q, _ := sealResultInputs(t, answer)
	f := answer.Facts
	digest, err := hex.DecodeString(answer.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	text := func(v EvidenceText) any {
		if v.Present {
			return v.Value
		}
		return nil
	}
	integer := func(v EvidenceInteger) any {
		if v.Present {
			return v.Value
		}
		return nil
	}
	var incarnation any
	if f.UpstreamIncarnation.Present {
		incarnation = f.UpstreamIncarnation.Value[:]
	}
	return []any{
		f.ProviderOperationID, i.Incarnation[:], i.Sequence, i.OperationID[:], q[:], answer.Kind,
		f.ObservedCatalogRevision, f.AppliedMS, f.EntityID, f.AfterItem, f.NextOrdinal,
		text(f.PublicationID), integer(f.ImageID), integer(f.ImageRevision), f.ResultCount, digest,
		f.SealVersion, f.EvidenceID, f.SubjectID, f.ParserVersion, f.Provider, f.SubjectKind,
		f.ProviderID, f.Language, f.Region, f.RequestShape, f.ObservationDigest, f.OwnerGeneration,
		text(f.BuildID), integer(f.BuildGeneration), f.ContentRevision, f.Algorithm, f.Commitment[:],
		f.VerifiedRows, f.CanonicalBytes, f.RetainedBytes, text(f.UpstreamOwner), incarnation,
		integer(f.UpstreamSequence), nil, nil, nil, nil, nil,
	}
}

func TestSavedSealReceiptNativeProjection(t *testing.T) {
	for _, answer := range sealResultAnswers(t) {
		t.Run(answer.Kind, func(t *testing.T) {
			row := savedSealAnswerRow(t, answer)
			got, err := decodeSavedSealReceipt(row)
			identity, request, _ := sealResultInputs(t, answer)
			wantKind := EvidenceSeal
			if answer.Kind == "credit_source_seal" {
				wantKind = CreditSourceSeal
			}
			if err != nil || got.identity != (receiptIdentity{identity, request}) || got.kind != wantKind ||
				got.facts != answer.Facts || hex.EncodeToString(got.resultDigest[:]) != answer.SHA256 {
				t.Fatal("saved native result differs from independent answer", got, err)
			}
			// BLOB input backing is not retained in copied fixed-width identities.
			row[1].([]byte)[0] ^= 255
			if got.identity.command.Incarnation != identity.Incarnation {
				t.Fatal("borrowed identity backing escaped")
			}
		})
	}
}

func TestSavedSealReceiptRefusesNativeDrift(t *testing.T) {
	for _, answer := range sealResultAnswers(t) {
		for name, mutate := range map[string]func([]any) []any{
			"missing_column":       func(row []any) []any { return row[:len(row)-1] },
			"foreign_kind":         func(row []any) []any { row[5] = "effective_stage"; return row },
			"integer_text":         func(row []any) []any { row[6] = "109"; return row },
			"revision_entity_swap": func(row []any) []any { row[6], row[8] = row[8], row[6]; return row },
			"language_region_swap": func(row []any) []any { row[23], row[24] = row[24], row[23]; return row },
			"empty_not_null":       func(row []any) []any { row[12] = ""; return row },
			"partial_dependency":   func(row []any) []any { row[41] = int64(47); return row },
			"digest_drift":         func(row []any) []any { row[15].([]byte)[0] ^= 1; return row },
		} {
			t.Run(answer.Kind+"/"+name, func(t *testing.T) {
				got, err := decodeSavedSealReceipt(mutate(savedSealAnswerRow(t, answer)))
				if !errors.Is(err, ErrResolveRequired) || got != (savedSealReceipt{}) {
					t.Fatal("bad history exposed data or lost typed error", got, err)
				}
			})
		}
	}
}

func TestSavedSealReceiptSchemaCoverage(t *testing.T) {
	// Inventory independently names every adopted native column; no generated
	// column or mutable proof lifecycle field silently enters result authority.
	inputs := []string{
		"provider_operation_id", "command_incarnation", "command_sequence", "operation_id", "request_digest", "command_kind",
		"accepted_revision", "applied_ms", "entity_id", "after_item", "after_ordinal", "publication_id", "effective_image_id", "effective_image_revision", "result_count", "result_digest",
		"seal_version", "seal_evidence_id", "seal_subject_id", "seal_parser_version", "seal_provider", "seal_subject_kind", "seal_provider_id", "seal_language", "seal_region", "seal_request_shape", "seal_observation_digest", "seal_owner_generation", "seal_build_id", "seal_build_generation", "seal_content_revision", "seal_algorithm", "seal_commitment", "seal_verified_rows", "seal_canonical_bytes", "seal_retained_bytes", "upstream_evidence_owner", "upstream_evidence_incarnation", "upstream_evidence_sequence", "source_dependency_owner", "source_dependency_incarnation", "source_dependency_sequence", "source_dependency_publication_id", "source_dependency_provider",
	}
	if len(inputs) != savedSealColumnCount {
		t.Fatal("explicit input inventory differs", len(inputs), savedSealColumnCount)
	}
	classified := map[string]int{}
	for _, name := range inputs {
		classified[name] = 0
	}
	for _, name := range []string{"proof_revision", "proof_pin_count", "proof_reclaim_due_ms", "proof_cleanup_after_sequence"} {
		classified[name] = 0 // mutable lifecycle, not saved immutable result
	}
	classified["upstream_evidence_kind"], classified["source_dependency_kind"] = 2, 2 // generated VIRTUAL
	w, _, _ := testExecutor(t)
	rows, err := w.db.QueryContext(t.Context(), "PRAGMA table_xinfo(enrichment_command_receipts)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]int{}
	for rows.Next() {
		var cid, notnull, pk, hidden int
		var name, typ string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk, &hidden); err != nil {
			t.Fatal(err)
		}
		seen[name] = hidden
	}
	if rows.Err() != nil || !reflect.DeepEqual(seen, classified) {
		t.Fatal("native column lacks exact input/non-input classification", seen, classified, rows.Err())
	}
}
