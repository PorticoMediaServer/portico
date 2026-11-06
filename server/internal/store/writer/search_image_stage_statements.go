package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	searchImageSource            stmt.ID = "search.publish.owner.image_source"
	searchImageSubject           stmt.ID = "search.publish.owner.image_subject"
	searchImageBank              stmt.ID = "search.publish.owner.image_bank"
	searchImageHeads             stmt.ID = "search.publish.owner.image_heads"
	searchImageLibrary           stmt.ID = "search.publish.owner.image_library"
	searchImageJob               stmt.ID = "search.publish.owner.image_job"
	searchImageInsert            stmt.ID = "search.publish.image_stage.image"
	searchImageSealInsert        stmt.ID = "search.publish.image_stage.seal"
	searchImageReclaimInsert     stmt.ID = "search.publish.image_stage.reclaim_custody"
	searchImageAttachNoHeadNoDue stmt.ID = "search.publish.cas.image_no_head_no_due"
	searchImageAttachNoHeadDue   stmt.ID = "search.publish.cas.image_no_head_due"
	searchImageAttachHeadNoDue   stmt.ID = "search.publish.cas.image_head_no_due"
	searchImageAttachHeadDue     stmt.ID = "search.publish.cas.image_head_due"
)

// A finite, compiled v1/entity-own-title definition set. Person names are NOT
// copied into child works. The global input/pending/G-driver fields are neither
// written nor used as unrelated cursor fences. W alone composes the registry.
func searchImageBeginDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	inc := [16]byte{23}
	defs := []stmt.Definition{
		{ID: searchImageSource, Kind: stmt.Read, SQL: `SELECT id,library_id,kind,revision,metadata_incarnation
FROM catalog_entities WHERE id=? AND library_id=? AND kind=? AND retired=0`,
			Params: []stmt.Param{i("entity_id"), i("library_id"), i("kind")}, Cohorts: []stmt.Cohort{
				{Name: "captured_own_title_source", Bindings: []any{int64(41), int64(3), int64(1)}},
				{Name: "foreign_library", Bindings: []any{int64(41), int64(99), int64(1)}},
				{Name: "missing_source", Bindings: []any{int64(999), int64(3), int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageSubject, Kind: stmt.Read, SQL: `SELECT id FROM catalog_subjects WHERE id=? AND library_id=? AND kind=?`,
			Params: []stmt.Param{i("entity_id"), i("library_id"), i("kind")}, Cohorts: []stmt.Cohort{{Name: "retained_subject", Bindings: []any{int64(41), int64(3), int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_subjects"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageBank, Kind: stmt.Read, SQL: `SELECT id,bank_key,state,recipe_version,engine_version,unicode_fingerprint FROM search_generations WHERE id=? AND bank_key=? AND state=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=?`,
			Params: []stmt.Param{i("bank_id"), t("bank_key"), t("state"), t("recipe"), t("engine"), t("unicode")}, Cohorts: []stmt.Cohort{{Name: "captured_bank", Bindings: []any{int64(7), "v1", "active", searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_generations"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageHeads, Kind: stmt.Read, SQL: `SELECT active_generation_id,building_generation_id FROM search_revision WHERE singleton=?`,
			Params: []stmt.Param{i("singleton")}, Cohorts: []stmt.Cohort{{Name: "current_head_pair", Bindings: []any{int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_revision"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageLibrary, Kind: stmt.Read, SQL: `SELECT id FROM catalog_libraries WHERE id=? AND retired=0`,
			Params: []stmt.Param{i("library_id")}, Cohorts: []stmt.Cohort{{Name: "live_library", Bindings: []any{int64(3)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageJob, Kind: stmt.Read, SQL: `SELECT id,entity_id,search_generation_id,command_incarnation,job_revision,
input_generation,published_generation,published_document_id,document_id,source_incarnation,source_revision,
recipe_version,phase,after_field,after_ordinal,after_fragment_id,expected_fragments,staged_fragments,staged_bytes,
content_digest,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,
last_command_sequence,acknowledged_command_sequence FROM search_publish_jobs WHERE id=? AND person_id IS NULL AND recipe_version=? AND phase=? AND content_digest=?`,
			Params: []stmt.Param{i("job_id"), t("recipe"), t("phase"), t("content_digest")}, Cohorts: []stmt.Cohort{{Name: "native_publisher_job", Bindings: []any{int64(29), searchpolicy.RecipeV1, "capture", ""}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publish_jobs"}, Seeks: []string{"rowid=?"}}},
		{ID: searchImageInsert, Kind: stmt.Write, SQL: `INSERT INTO search_publications(
entity_id,search_generation_id,title,person_name_tokens,library_id,kind,generation,source_incarnation,
source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			Params:  []stmt.Param{i("entity_id"), i("bank_id"), t("own_primary_title"), t("person_name_tokens"), i("library_id"), i("kind"), i("input_generation"), b("source_incarnation"), i("source_revision"), t("recipe"), t("title_fold"), t("title_prefix_tokens"), t("initial_prefix_tokens")},
			Cohorts: []stmt.Cohort{{Name: "hidden_own_title_header", Bindings: []any{int64(41), int64(7), "Owner", "", int64(3), int64(1), int64(5), inc[:], int64(11), searchpolicy.RecipeV1, "owner", "00006f00007700006e000065000072", "00006f00007700006e000065000072"}}}, Plan: stmt.Plan{Hot: true}},
		{ID: searchImageSealInsert, Kind: stmt.Write, SQL: `INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,chain)
VALUES(?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("image_id"), t("algorithm"), b("seed"), b("expected_digest"), i("expected_fragments"), i("expected_bytes"), b("chain")},
			Cohorts: []stmt.Cohort{{Name: "unsealed_empty_cursor", Bindings: []any{int64(307), searchmanifest.Algorithm, make([]byte, 32), make([]byte, 32), int64(0), int64(0), make([]byte, 32)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: searchImageReclaimInsert, Kind: stmt.Write, SQL: `INSERT INTO search_reclaim_jobs(publication_id,live_publication_id,phase,command_incarnation) VALUES(?,?,?,?)`,
			Params:  []stmt.Param{i("image_id"), i("live_image_id"), t("phase"), b("reclaim_incarnation")},
			Cohorts: []stmt.Cohort{{Name: "held_native_custody", Bindings: []any{int64(307), int64(307), "held", make([]byte, 16)}}}, Plan: stmt.Plan{Hot: true}},
	}
	// Nullable fields have four CLOSED variants, not IS ?, OR or a sentinel.
	for ordinal, variant := range []struct {
		id        stmt.ID
		head, due bool
	}{
		{searchImageAttachNoHeadNoDue, false, false}, {searchImageAttachNoHeadDue, false, true},
		{searchImageAttachHeadNoDue, true, false}, {searchImageAttachHeadDue, true, true},
	} {
		query := `UPDATE search_publish_jobs SET document_id=?,phase=?,expected_fragments=?,staged_fragments=?,staged_bytes=?,
after_field=?,after_ordinal=?,after_fragment_id=?,job_revision=?,last_command_sequence=?
WHERE id=? AND entity_id=? AND person_id IS NULL AND search_generation_id=? AND command_incarnation=?
AND job_revision=? AND input_generation=? AND source_incarnation=? AND source_revision=? AND recipe_version=?
AND phase=? AND document_id IS NULL AND published_generation=?`
		params := []stmt.Param{i("new_image"), t("new_phase"), i("new_expected_fragments"), i("new_staged_fragments"), i("new_staged_bytes"), i("new_after_field"), i("new_after_ordinal"), i("new_after_fragment"), i("new_revision"), i("new_sequence"),
			i("job_id"), i("entity_id"), i("bank_id"), b("command_incarnation"), i("expected_revision"), i("input_generation"), b("source_incarnation"), i("source_revision"), t("recipe"), t("expected_phase"), i("published_generation")}
		identity := [16]byte{17}
		job, entity := int64(35+ordinal), int64(47+ordinal)
		if ordinal == 3 {
			job, entity = 29, 41
		}
		args := []any{int64(307), "stage", int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(20), int64(8), job, entity, int64(7), identity[:], int64(19), int64(5), inc[:], int64(11), searchpolicy.RecipeV1, "capture", int64(0)}
		if variant.head {
			query += ` AND published_document_id=?`
			params = append(params, i("published_image"))
			args[len(args)-1] = int64(3)
			head := int64(14)
			if ordinal == 3 {
				head = 13
			}
			args = append(args, head)
		} else {
			query += ` AND published_document_id IS NULL`
		}
		query += ` AND after_field=? AND after_ordinal=? AND after_fragment_id=? AND expected_fragments=?
AND staged_fragments=? AND staged_bytes=? AND content_digest=? AND job_lease_owner=? AND job_lease_generation=?
AND job_lease_expires_ms=? AND job_attempts=?`
		params = append(params, i("expected_field"), i("expected_ordinal"), i("expected_fragment"), i("expected_fragments"), i("expected_staged_fragments"), i("expected_staged_bytes"), t("captured_content_digest"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), i("attempts"))
		args = append(args, int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), "", int64(1), int64(2), int64(2100000000000), int64(0))
		if variant.due {
			query += ` AND job_next_attempt_ms=?`
			params = append(params, i("next_attempt_ms"))
			args = append(args, int64(1000))
		} else {
			query += ` AND job_next_attempt_ms IS NULL`
		}
		query += ` AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING job_revision`
		params = append(params, i("expected_last_sequence"), i("expected_ack_sequence"))
		args = append(args, int64(7), int64(7))
		defs = append(defs, stmt.Definition{ID: variant.id, Kind: stmt.Write, SQL: query, Params: params, Cohorts: []stmt.Cohort{{Name: "post_allocation_exact_cas", Bindings: args}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publish_jobs"}, Seeks: []string{"rowid=?"}}})
	}
	for n := range defs {
		defs[n].Owner = "writer.search.publisher"
		defs[n].CallSites = []string{"writer/search_image_stage.go"}
	}
	return defs
}
