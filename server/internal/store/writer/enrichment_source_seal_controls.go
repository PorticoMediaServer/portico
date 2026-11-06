package writer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const enrichmentRetainedControlBytes uint64 = 64 << 10

// A CLOSED accounting projection of actual retained native rows, not generic
// persistence, a request codec, a caller-selected quota or verified provenance.
// Values follow the literal native column layout below; NULL carries no text.
// W's complete bounded snapshot supplies every actual row/occurrence and proves
// no-next/closure membership. This pure EN preflight cannot prove completeness.
// Repeated values in DISTINCT stored occurrences are counted again. The reader
// supplies each physical row once within this closure; sharing ACROSS pinners
// never discounts the full per-pinner charge.
type enrichmentControlRecord struct {
	table string
	text  []EvidenceText
}
type enrichmentControlCapture struct{ records []enrichmentControlRecord }

type EnrichmentControlSizeError struct{ LimitBytes, ObservedBytes uint64 }

func (e *EnrichmentControlSizeError) Error() string {
	return fmt.Sprintf("enrichment retained control bytes %d exceed %d", e.ObservedBytes, e.LimitBytes)
}
func (*EnrichmentControlSizeError) Unwrap() error { return ErrCap }

// Preflight BEFORE derived allocation, cloning or calling W's codec. Content
// strings/source tuple payloads use their SEPARATE adopted limits. Native key
// occurrences within content are included here too. Secondary copies and fixed
// generated-kind/BLOB/integer/record cells remain in the complete W forecast.
func preflightEnrichmentControls(c enrichmentControlCapture) (uint64, error) {
	var total uint64
	counts := make(map[string]int, 16)
	for _, row := range c.records {
		bound, layout := enrichmentControlLayout(row.table)
		if bound == 0 {
			return 0, invalid("control_table")
		}
		counts[row.table]++
		if counts[row.table] > bound {
			return 0, invalid("control_closure_cardinality")
		}
		columns := strings.Fields(layout)
		if len(columns) != len(row.text) {
			return 0, invalid("control_native_columns")
		}
		for i, v := range row.text {
			nullable := strings.HasSuffix(columns[i], "?")
			if !v.Present {
				if !nullable || v.Value != "" {
					return 0, invalid("control_null_shape")
				}
				continue
			}
			n := uint64(len(v.Value))
			if n > enrichmentRetainedControlBytes-total {
				return 0, &EnrichmentControlSizeError{enrichmentRetainedControlBytes, total + n}
			}
			if !utf8.ValidString(v.Value) || strings.IndexByte(v.Value, 0) >= 0 {
				return 0, invalid("control_text")
			}
			total += n
		}
	}
	return total, nil
}

// Exact adopted009 stored TEXT projection. '?' denotes native nullable TEXT.
// BODY content and ten explicitly bounded source-tuple payload fields are the
// named exclusions, not arbitrary prefix/JSON filtering. Generated columns are
// fixed typed projections, not another mutable retained string. New fields or
// tables require a new explicit disposition and whole native envelope audit.
func enrichmentControlLayout(table string) (int, string) {
	switch table {
	case "metadata_provider_operations":
		return 2, "set_subject_kind? set_provider_id? id provider phase status lease lease_until algorithm refresh_mode query_title query_artist query_digest input_digest result_digest selected_id hint_id hint_problem qualification episode_order set_id? after_item created_at finished_at reason evidence_build_id? source_publication_id? source_evidence_id? source_parser_version? source_algorithm? proof_cleanup_phase source_evidence_owner?"
	case "metadata_operation_subjects":
		return 2, "provider evidence_kind? operation_id role accepted_evidence_id?"
	case "metadata_provider_operation_sources":
		return 2, "operation_id path source_id root_incarnation object_revision"
	case "metadata_candidates":
		return 2, "id operation_id provider subject_kind provider_id evidence_id? title artist edition decision query_digest input_digest source_kind observed_at language region"
	case "metadata_provider_subjects":
		return 1, "provider subject_kind provider_id language region request_shape accepted_evidence_id?"
	case "metadata_evidence":
		return 1, "id parser_version provider subject_kind provider_id language region request_shape digest seal_proof_owner?"
	case "metadata_evidence_credits":
		return 150, "provider evidence_kind credit_class evidence_id scope"
	case "metadata_evidence_builds":
		return 1, "id evidence_id owner_operation parent_build? state phase expected_shape_digest materialized_digest expires_at"
	case "metadata_evidence_accounting":
		return 1, "evidence_id observed_at"
	case "credit_source_publications":
		return 1, "publication_id provider accepted_record_reference observed_at seal_proof_owner?"
	case "credit_source_publication_members":
		return 150, "publication_id provider source_row_id reference_status"
	case "credit_source_rows":
		return 150, "source_row_id provider tuple_digest original_credit_class"
	case "enrichment_command_receipts":
		return 6, "provider_operation_id command_kind after_item publication_id? seal_evidence_id? seal_parser_version? seal_provider? seal_subject_kind? seal_provider_id? seal_language? seal_region? seal_request_shape? seal_observation_digest? seal_build_id? seal_algorithm? upstream_evidence_owner? source_dependency_owner? source_dependency_publication_id? source_dependency_provider?"
	case "catalog_assets":
		return 2, "token path container video_codec audio_codec info_owner_object? info_owner_revision? music_technical_source_revision? download_hash_sha256?"
	case "catalog_subjects":
		return 2, ""
	case "catalog_credit_images":
		return 1, "source_proof_owner? source_proof_publication_id? source_proof_provider?"
	default:
		return 0, ""
	}
}
