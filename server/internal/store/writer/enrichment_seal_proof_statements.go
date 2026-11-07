package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const sealReceiptLengths stmt.ID = "enrichment.seal.receipt_lengths"
const sealEvidenceHeaderLengths stmt.ID = "enrichment.seal.evidence_header_lengths"

// Native scalar byte counts only. HN must certify these exact named points;
// generic function lint is unchanged. Payload copy follows in the SAME snapshot
// with per-column byte equality; these data declarations supply no authority.
func enrichmentSealProofLengthDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: sealReceiptLengths, Owner: "writer.enrichment", Kind: stmt.Read,
			SQL:       `SELECT octet_length(provider_operation_id),octet_length(command_incarnation),octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(after_item),octet_length(publication_id),octet_length(result_digest),octet_length(seal_evidence_id),octet_length(seal_parser_version),octet_length(seal_provider),octet_length(seal_subject_kind),octet_length(seal_provider_id),octet_length(seal_language),octet_length(seal_region),octet_length(seal_request_shape),octet_length(seal_observation_digest),octet_length(seal_build_id),octet_length(seal_algorithm),octet_length(seal_commitment),octet_length(upstream_evidence_owner),octet_length(upstream_evidence_incarnation),octet_length(source_dependency_owner),octet_length(source_dependency_incarnation),octet_length(source_dependency_publication_id),octet_length(source_dependency_provider) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:    []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}},
			CallSites: []string{"writer/enrichment_seal_proof.go"},
			Cohorts:   []stmt.Cohort{{Name: "missing_receipt", Bindings: []any{"missing", make([]byte, 16), int64(1)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}},
		},
		{ID: sealEvidenceHeaderLengths, Owner: "writer.enrichment", Kind: stmt.Read,
			SQL:       `SELECT octet_length(id),octet_length(parser_version),octet_length(provider),octet_length(subject_kind),octet_length(provider_id),octet_length(language),octet_length(region),octet_length(request_shape),octet_length(digest),octet_length(observed_at),octet_length(source_url),octet_length(etag),octet_length(last_modified),octet_length(title),octet_length(original_title),octet_length(overview),octet_length(tagline),octet_length(content_rating),octet_length(studio),octet_length(network),octet_length(country),octet_length(release_date),octet_length(artist),octet_length(artist_type),octet_length(wikidata_id),octet_length(biography),octet_length(biography_source),octet_length(biography_source_url),octet_length(disambiguation),octet_length(release_group_id),octet_length(group_title),octet_length(primary_type),octet_length(barcode),octet_length(seal_proof_owner),octet_length(seal_proof_incarnation) FROM metadata_evidence INDEXED BY sqlite_autoindex_metadata_evidence_1 WHERE id=? AND subject_id=? AND parser_version=?`,
			Params:    []stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}},
			CallSites: []string{"writer/enrichment_seal_proof.go"},
			Cohorts:   []stmt.Cohort{{Name: "missing_header", Bindings: []any{"missing", int64(1), "tmdb.movie.header-selected-credits.v1"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "metadata_evidence", Name: "sqlite_autoindex_metadata_evidence_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}},
		},
	}
}
