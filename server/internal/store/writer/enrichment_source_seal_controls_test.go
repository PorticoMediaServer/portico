package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func controlAssetRecord(value string) enrichmentControlRecord {
	return enrichmentControlRecord{"catalog_assets", []EvidenceText{{value, true}, {"", true}, {"", true}, {"", true}, {"", true}, {}, {}, {}, {}}}
}
func TestSealControlPreflightCountsActualOccurrences(t *testing.T) {
	c := enrichmentControlCapture{[]enrichmentControlRecord{controlAssetRecord(strings.Repeat("x", 32768)), controlAssetRecord(strings.Repeat("x", 32768))}}
	if n, err := preflightEnrichmentControls(c); err != nil || n != 65536 {
		t.Fatal(n, err)
	}
	c.records[1].text[0].Value += "x"
	var size *PolicyError
	if _, err := preflightEnrichmentControls(c); !errors.As(err, &size) || size.Field != "seal_control_strings" || size.Limit != 65536 || !errors.Is(err, ErrCap) {
		t.Fatal("repeated physical occurrence discounted", err)
	}
	c.records[1].text[0].Value = "é"
	if n, err := preflightEnrichmentControls(c); err != nil || n != 32770 {
		t.Fatal("not actual UTF8 bytes", n, err)
	}
	c.records[1].text[0] = EvidenceText{Value: "ignored"}
	if _, err := preflightEnrichmentControls(c); !errors.Is(err, ErrInvalid) {
		t.Fatal("NULL/value alias", err)
	}
	c.records[1].text[0] = EvidenceText{Value: "\xff", Present: true}
	if _, err := preflightEnrichmentControls(c); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid UTF8", err)
	}
	c.records[1].text[0] = EvidenceText{Value: "a\x00b", Present: true}
	if _, err := preflightEnrichmentControls(c); !errors.Is(err, ErrInvalid) {
		t.Fatal("embedded NUL", err)
	}
}

func TestSealCodecBoundaryChecksControlsBeforePayload(t *testing.T) {
	c := enrichmentControlCapture{[]enrichmentControlRecord{controlAssetRecord(strings.Repeat("x", 65537))}}
	// The shared control error wins BEFORE payload projection/hashing/cloning.
	if got, err := prepareEvidenceSealManifest(t.Context(), EvidenceStagePayload{}, c); !errors.Is(err, ErrCap) || got != (normalizedSealManifest{}) {
		t.Fatal("control preflight did not run first", got, err)
	}
	p, err := PrepareEvidenceStagePayload(draftMovieStageHeader(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := prepareEvidenceSealManifest(ctx, p, enrichmentControlCapture{}); !errors.Is(err, context.Canceled) || got != (normalizedSealManifest{}) {
		t.Fatal("cancellation disclosed output", got, err)
	}
}

func TestSealControlProjectionEnumeratesNativeTextColumns(t *testing.T) {
	w, _, _ := testExecutor(t)
	projectionSlots := 0
	// Every absent mutable TEXT has a NAMED separately charged descriptor.
	// Future native TEXT is never silently ignored by reflecting over a DTO.
	excluded := map[string]string{
		// D person fields and IP proof ownership have no EN input path. Their
		// IP-domain byte registrations remain separate accepting-writer gates.
		"metadata_evidence":         "birth_date death_date birth_place ip_seal_owner observed_at source_url etag last_modified title original_title overview tagline content_rating studio network country release_date artist artist_type wikidata_id biography biography_source biography_source_url disambiguation release_group_id group_title primary_type barcode",
		"metadata_evidence_credits": "provider_person_id name artist_name sort_name disambiguation join_phrase role department credit_id",
		"credit_source_rows":        "original_record_reference original_credit_id original_provider_person_id original_person_identity_key original_person_token original_target_kind original_target_id original_credited_name original_role original_department",
		"catalog_credit_images":     "metadata_language manifest_algorithm reclaim_state",
	}
	generated := map[string]string{"metadata_provider_operations": "source_evidence_kind", "metadata_evidence": "seal_proof_kind ip_seal_kind", "metadata_evidence_credits": "track_id", "credit_source_publications": "seal_proof_kind", "enrichment_command_receipts": "upstream_evidence_kind source_dependency_kind", "catalog_credit_images": "source_proof_kind"}
	for _, table := range []string{"metadata_provider_operations", "metadata_operation_subjects", "metadata_provider_operation_sources", "metadata_candidates", "metadata_provider_subjects", "metadata_evidence", "metadata_evidence_credits", "metadata_evidence_builds", "metadata_evidence_accounting", "credit_source_publications", "credit_source_publication_members", "credit_source_rows", "enrichment_command_receipts", "catalog_assets", "catalog_subjects", "catalog_credit_images"} {
		bound, layout := enrichmentControlLayout(table)
		projectionSlots += bound * len(strings.Fields(layout))
		captured := map[string]bool{}
		for _, v := range strings.Fields(layout) {
			captured[strings.TrimSuffix(v, "?")] = false
		}
		excludedSet := map[string]bool{}
		for _, v := range strings.Fields(excluded[table]) {
			excludedSet[v] = false
		}
		generatedSet := map[string]bool{}
		for _, v := range strings.Fields(generated[table]) {
			generatedSet[v] = false
		}
		rows, err := w.db.QueryContext(t.Context(), "PRAGMA table_xinfo("+table+")")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var cid, notnull, pk, hidden int
			var name, typ string
			var dflt any
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
				t.Fatal(err)
			}
			if typ != "TEXT" {
				continue
			}
			if hidden != 0 {
				if _, ok := generatedSet[name]; !ok {
					t.Fatalf("%s.%s generated TEXT lacks explicit derived/fixed disposition", table, name)
				}
				generatedSet[name] = true
				continue
			}
			if _, ok := captured[name]; ok {
				captured[name] = true
			} else if _, ok := excludedSet[name]; ok {
				excludedSet[name] = true
			} else {
				t.Fatalf("%s.%s native TEXT lacks charge disposition", table, name)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for label, fields := range map[string]map[string]bool{"captured": captured, "excluded": excludedSet, "generated": generatedSet} {
			for field, seen := range fields {
				if !seen {
					t.Fatalf("%s.%s stale %s disposition", table, field, label)
				}
			}
		}
	}
	if projectionSlots > enrichmentControlProjectionSlots {
		t.Fatal("native closure exceeds fixed scratch capacity", projectionSlots)
	}
}
