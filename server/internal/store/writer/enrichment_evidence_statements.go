package writer

import (
	"encoding/hex"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	enrichmentEvidenceInsert stmt.ID = "enrichment.evidence_stage.insert_header"
	enrichmentBuildInsert    stmt.ID = "enrichment.evidence_stage.insert_build"
	enrichmentOwnerFacts     stmt.ID = "enrichment.evidence_stage.owner"
	enrichmentReceiptFacts   stmt.ID = "enrichment.evidence_stage.receipt"
)

// W composes this finite definition slice into its one registry. This function
// neither creates a registry nor opens/adopts a connection. Owner/receipt/CAS
// statements join this slice when wired to W's common receipt fact types.
func enrichmentEvidenceDefinitions() []stmt.Definition {
	cohort := EvidenceHeaderInput{
		ID: "evidence-one", SubjectID: 1, ParserVersion: "parser-v1",
		Provider: "fixture", SubjectKind: "movie", ProviderID: "opaque:001",
		Language: "en", Region: "CA", RequestShape: "detail-v1",
		ObservedAt: "2026-11-05T00:00:00Z",
	}
	return []stmt.Definition{
		{
			ID: enrichmentOwnerFacts, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_evidence_statements.go:enrichmentEvidenceDefinitions"},
			SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,
 entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,
 source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,
 descriptive_revision,policy_incarnation,policy_revision,consent_revision,input_digest
 FROM metadata_provider_operations WHERE id=?`,
			Params:  []stmt.Param{{Name: "provider_operation_id", Type: stmt.Text}},
			Cohorts: []stmt.Cohort{{Name: "current_owner", Bindings: []any{"operation-one"}}, {Name: "missing_owner", Bindings: []any{"operation-missing"}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: enrichmentReceiptFacts, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_evidence_statements.go:enrichmentEvidenceDefinitions"},
			SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,entity_id,after_item,after_ordinal,publication_id,
 effective_image_id,effective_image_revision,result_count,result_digest
 FROM enrichment_command_receipts
 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{
				{Name: "provider_operation_id", Type: stmt.Text},
				{Name: "command_incarnation", Type: stmt.Blob},
				{Name: "command_sequence", Type: stmt.Integer},
			},
			Cohorts: []stmt.Cohort{
				{Name: "first_sequence", Bindings: []any{"operation-one", make([]byte, 16), int64(1)}},
				{Name: "historical_sequence", Bindings: []any{"operation-one", make([]byte, 16), int64(2)}},
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: enrichmentEvidenceInsert, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_evidence_statements.go:evidenceHeaderValues"},
			SQL: `INSERT INTO metadata_evidence(
 id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,
 observed_at,source_url,etag,last_modified,title,original_title,overview,tagline,content_rating,studio,
 network,country,release_date,year,artist,length_millis,artist_type,begin_year,end_year,wikidata_id,
 biography,biography_source,biography_source_url,disambiguation,release_group_id,group_title,primary_type,barcode,sealed
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
			Params: []stmt.Param{
				{Name: "id", Type: stmt.Text}, {Name: "subject_id", Type: stmt.Integer},
				{Name: "parser_version", Type: stmt.Text}, {Name: "provider", Type: stmt.Text},
				{Name: "subject_kind", Type: stmt.Text}, {Name: "provider_id", Type: stmt.Text},
				{Name: "language", Type: stmt.Text}, {Name: "region", Type: stmt.Text},
				{Name: "request_shape", Type: stmt.Text}, {Name: "digest", Type: stmt.Text},
				{Name: "observed_at", Type: stmt.Text}, {Name: "source_url", Type: stmt.Text},
				{Name: "etag", Type: stmt.Text}, {Name: "last_modified", Type: stmt.Text},
				{Name: "title", Type: stmt.Text}, {Name: "original_title", Type: stmt.Text},
				{Name: "overview", Type: stmt.Text}, {Name: "tagline", Type: stmt.Text},
				{Name: "content_rating", Type: stmt.Text}, {Name: "studio", Type: stmt.Text},
				{Name: "network", Type: stmt.Text}, {Name: "country", Type: stmt.Text},
				{Name: "release_date", Type: stmt.Text}, {Name: "year", Type: stmt.Integer},
				{Name: "artist", Type: stmt.Text}, {Name: "length_millis", Type: stmt.Integer, Nullable: true},
				{Name: "artist_type", Type: stmt.Text, Nullable: true}, {Name: "begin_year", Type: stmt.Integer, Nullable: true},
				{Name: "end_year", Type: stmt.Integer, Nullable: true}, {Name: "wikidata_id", Type: stmt.Text},
				{Name: "biography", Type: stmt.Text}, {Name: "biography_source", Type: stmt.Text},
				{Name: "biography_source_url", Type: stmt.Text}, {Name: "disambiguation", Type: stmt.Text},
				{Name: "release_group_id", Type: stmt.Text}, {Name: "group_title", Type: stmt.Text},
				{Name: "primary_type", Type: stmt.Text}, {Name: "barcode", Type: stmt.Text},
			},
			Cohorts: []stmt.Cohort{
				{Name: "header_absent_optionals", Bindings: evidenceHeaderValues(cohort)},
				{Name: "header_present_zero", Bindings: evidenceHeaderPresentZeroCohort(cohort)},
			},
		},
		{
			ID: enrichmentBuildInsert, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_evidence_statements.go:enrichmentEvidenceDefinitions"},
			SQL: `INSERT INTO metadata_evidence_builds(
 id,evidence_id,generation,owner_operation,parent_build,state,phase,cursor,
 expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at
) VALUES(?,?,?,?,?,'building','children',1,?,1,?,?,?,?)`,
			Params: []stmt.Param{
				{Name: "id", Type: stmt.Text}, {Name: "evidence_id", Type: stmt.Text},
				{Name: "generation", Type: stmt.Integer}, {Name: "owner_operation", Type: stmt.Text},
				{Name: "parent_build", Type: stmt.Text, Nullable: true}, {Name: "expected_rows", Type: stmt.Integer},
				{Name: "expected_shape_digest", Type: stmt.Text}, {Name: "materialized_digest", Type: stmt.Text},
				{Name: "materialized_charge", Type: stmt.Integer}, {Name: "expires_at", Type: stmt.Text},
			},
			Cohorts: []stmt.Cohort{
				{Name: "single_header", Bindings: []any{"build-one", "evidence-one", int64(1), "operation-one", nil, int64(1), "expected", "header-chain", int64(512), "2026-11-05T00:01:00Z"}},
				{Name: "children_pending", Bindings: []any{"build-two", "evidence-two", int64(2), "operation-two", "build-one", int64(17), "expected", "header-chain", int64(1024), "2026-11-05T00:01:00Z"}},
			},
		},
	}
}

// Values are mapped explicitly to the static schema order. The observation is
// always inserted sealed=0; no worker parameter can make this a public seal.
func evidenceHeaderValues(v EvidenceHeaderInput) []any {
	return []any{
		v.ID, v.SubjectID, v.ParserVersion, v.Provider, v.SubjectKind, v.ProviderID,
		v.Language, v.Region, v.RequestShape, hex.EncodeToString(v.ObservationDigest[:]),
		v.ObservedAt, v.SourceURL, v.ETag, v.LastModified, v.Title, v.OriginalTitle,
		v.Overview, v.Tagline, v.ContentRating, v.Studio, v.Network, v.Country,
		v.ReleaseDate, v.Year, v.Artist, evidenceIntegerValue(v.LengthMillis), evidenceTextValue(v.ArtistType),
		evidenceIntegerValue(v.BeginYear), evidenceIntegerValue(v.EndYear), v.WikidataID,
		v.Biography, v.BiographySource, v.BiographySourceURL, v.Disambiguation,
		v.ReleaseGroupID, v.GroupTitle, v.PrimaryType, v.Barcode,
	}
}

func evidenceIntegerValue(v EvidenceInteger) any {
	if !v.Present {
		return nil
	}
	return v.Value
}

func evidenceTextValue(v EvidenceText) any {
	if !v.Present {
		return nil
	}
	return v.Value
}

func evidenceHeaderPresentZeroCohort(v EvidenceHeaderInput) []any {
	v.LengthMillis.Present = true
	v.BeginYear.Present = true
	v.EndYear.Present = true
	v.ArtistType.Present = true
	return evidenceHeaderValues(v)
}
