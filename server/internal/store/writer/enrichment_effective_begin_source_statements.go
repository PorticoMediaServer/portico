package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	enrichmentEffectiveLibrary     stmt.ID = "enrichment.effective_stage.library"
	enrichmentEffectiveLanguage    stmt.ID = "enrichment.effective_stage.language"
	enrichmentEffectivePolicy      stmt.ID = "enrichment.effective_stage.policy"
	enrichmentEffectiveProvider    stmt.ID = "enrichment.effective_stage.provider"
	enrichmentEffectiveConsent     stmt.ID = "enrichment.effective_stage.consent"
	enrichmentEffectiveSelection   stmt.ID = "enrichment.effective_stage.selection"
	enrichmentEffectiveEvidence    stmt.ID = "enrichment.effective_stage.evidence"
	enrichmentEffectiveSubject     stmt.ID = "enrichment.effective_stage.subject"
	enrichmentEffectivePublication stmt.ID = "enrichment.effective_stage.source_publication"
	enrichmentEffectiveCreditHead  stmt.ID = "enrichment.effective_stage.credit_head"
	enrichmentEffectiveCapture     stmt.ID = "enrichment.effective_stage.source_capture"
	enrichmentEffectiveAssociation stmt.ID = "enrichment.effective_stage.association"
	enrichmentEffectiveAsset       stmt.ID = "enrichment.effective_stage.asset"
	enrichmentEffectiveObject      stmt.ID = "enrichment.effective_stage.object"
	enrichmentEffectiveSource      stmt.ID = "enrichment.effective_stage.source"
)

// Only compiled point/cardinality probes. Role and LIMIT arguments are handler-owned
// constants; the request cannot select another authority family or scan bound.
func enrichmentEffectiveProducerDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{
			ID: enrichmentEffectiveLibrary, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT id,library_id,root,retired FROM catalog_libraries WHERE id=?`,
			Params:    []stmt.Param{{Name: "library_id", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_library_shape", Bindings: []any{int64(7)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: enrichmentEffectiveLanguage, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT kind,metadata_language FROM libraries WHERE id=?`,
			Params:    []stmt.Param{{Name: "library_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_language_shape", Bindings: []any{"library-native"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, Seeks: []string{"id=?"}},
		},
		{
			ID: enrichmentEffectivePolicy, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT incarnation,revision,enabled,region,algorithm,refresh_mode,credit_enabled FROM metadata_provider_policy WHERE library_id=? AND provider=?`,
			Params:    []stmt.Param{{Name: "library_id", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_policy_shape", Bindings: []any{int64(7), "fixture"}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_policy"}, Seeks: []string{"library_id=? AND provider=?"}},
		},
		{
			ID: enrichmentEffectiveProvider, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT adapter,enabled FROM metadata_providers WHERE code=?`,
			Params:    []stmt.Param{{Name: "provider", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_provider_shape", Bindings: []any{"fixture"}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_providers"}, Seeks: []string{"code=?"}},
		},
		{
			ID: enrichmentEffectiveConsent, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT revision,confirmed FROM screen_metadata_consent WHERE singleton=?`,
			Params:    []stmt.Param{{Name: "singleton", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_consent_shape", Bindings: []any{int64(1)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"screen_metadata_consent"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: enrichmentEffectiveSelection, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT incarnation,revision,role,subject_kind,provider_id,status,accepted_evidence_id,accepted_set_id,accepted_publication_id,accepted_track_id FROM metadata_subject_providers WHERE entity_id=? AND provider=? AND role=?`,
			Params:    []stmt.Param{{Name: "entity_id", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}, {Name: "role", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_selection_shape", Bindings: []any{int64(101), "fixture", "primary"}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_subject_providers"}, Seeks: []string{"entity_id=? AND provider=? AND role=?"}},
		},
		{
			ID: enrichmentEffectiveEvidence, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT subject_id,provider,subject_kind,provider_id,language,region,request_shape,digest,sealed FROM metadata_evidence WHERE id=?`,
			Params:    []stmt.Param{{Name: "evidence_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_evidence_shape", Bindings: []any{"evidence-source"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_1"}, Seeks: []string{"id=?"}},
		},
		{
			ID: enrichmentEffectiveSubject, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT incarnation,revision,provider,subject_kind,provider_id,language,region,request_shape FROM metadata_provider_subjects WHERE id=?`,
			Params:    []stmt.Param{{Name: "subject_id", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_subject_shape", Bindings: []any{int64(601)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_subjects"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: enrichmentEffectivePublication, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT entity_id,provider,accepted_record_reference,observed_at,member_count,sealed FROM credit_source_publications WHERE publication_id=?`,
			Params:    []stmt.Param{{Name: "publication_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_source_publication_shape", Bindings: []any{"aabbccddeeff00112233445566778899aa"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_credit_source_publications_1"}, Seeks: []string{"publication_id=?"}},
		},
		{
			ID: enrichmentEffectiveCreditHead, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT publication_id,revision FROM credit_source_heads WHERE entity_id=? AND provider=?`,
			Params:    []stmt.Param{{Name: "entity_id", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_credit_head_shape", Bindings: []any{int64(101), "fixture"}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"credit_source_heads"}, Seeks: []string{"entity_id=? AND provider=?"}},
		},
		{
			ID: enrichmentEffectiveCapture, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT ordinal,asset_id,asset_incarnation,part_index,start_seconds,end_seconds,path,size,modified_ns,available,source_id,source_generation,root_incarnation,object_revision FROM metadata_provider_operation_sources WHERE operation_id=? ORDER BY ordinal LIMIT ?`,
			Params:    []stmt.Param{{Name: "provider_operation_id", Type: stmt.Text}, {Name: "limit", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_source_capture_shape", Bindings: []any{"operation-one", int64(2)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operation_sources"}, Seeks: []string{"operation_id=?"}},
		},
		{
			ID: enrichmentEffectiveAssociation, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT asset_id,library_id,part_index,available,start_seconds,end_seconds,cue_ordinal FROM catalog_asset_links WHERE entity_id=? ORDER BY asset_id LIMIT ?`,
			Params:    []stmt.Param{{Name: "entity_id", Type: stmt.Integer}, {Name: "limit", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_association_shape", Bindings: []any{int64(101), int64(2)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=?"}},
		},
		{
			ID: enrichmentEffectiveAsset, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT token,path,size,modified_ns,available,library_id,playback_origin_incarnation,info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`,
			Params:    []stmt.Param{{Name: "asset_id", Type: stmt.Integer}},
			Cohorts:   []stmt.Cohort{{Name: "native_asset_shape", Bindings: []any{int64(701)}}},
			Plan:      stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: enrichmentEffectiveObject, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT source_id,asset_id,root_incarnation,revision,size,modified_ns,state,retired,unsupported_reason FROM inventory_objects WHERE id=?`,
			Params:    []stmt.Param{{Name: "object_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_object_shape", Bindings: []any{"object-native"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, Seeks: []string{"id=?"}},
		},
		{
			ID: enrichmentEffectiveSource, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_source.go:effectiveBeginReadProducer"},
			SQL:       `SELECT library_id,generation,incarnation,root,root_identity,identity_confirmed,enabled,health FROM library_sources WHERE id=?`,
			Params:    []stmt.Param{{Name: "source_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "native_source_shape", Bindings: []any{"source-native"}}},
			Plan:      stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}},
		},
	}
}
