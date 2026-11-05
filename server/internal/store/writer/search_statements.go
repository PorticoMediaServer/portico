package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Closed IDs are composed by the ONE shared writer registry. These definitions
// confer no execution/receipt authority and contain no bank/table interpolation.
const (
	searchInputHead     stmt.ID = "search.publish.owner.revision_head"
	searchInputBank     stmt.ID = "search.publish.owner.input_bank"
	searchInputEntity   stmt.ID = "search.publish.owner.entity_input"
	searchInputExisting stmt.ID = "search.publish.owner.entity_intent"
	searchInputInsert   stmt.ID = "search.publish.cas.new_entity_intent"
	searchInputPending  stmt.ID = "search.publish.cas.generation_pending"
	searchInputSequence stmt.ID = "search.publish.cas.input_sequence"
)

func searchInputIntentDefinitions() []stmt.Definition {
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	definition := func(id stmt.ID, sql string, kind stmt.Kind, params []stmt.Param, cohorts []stmt.Cohort) stmt.Definition {
		return stmt.Definition{
			ID: id, SQL: sql, Owner: "writer.search",
			CallSites: []string{"writer/search_commands.go:applySearchInputIntent"},
			Kind:      kind, Params: params, Cohorts: cohorts, Plan: stmt.Plan{Hot: true},
		}
	}
	return []stmt.Definition{
		definition(searchInputHead, `SELECT revision,active_generation_id,building_generation_id,input_sequence
FROM search_revision WHERE singleton=1`, stmt.Read, nil,
			[]stmt.Cohort{{Name: "singleton", Bindings: []any{}}}),
		definition(searchInputBank, `SELECT bank_key,recipe_version,engine_version,unicode_fingerprint,state,pending_subjects,job_revision
FROM search_generations WHERE id=?`, stmt.Read, []stmt.Param{integer("bank_id")},
			[]stmt.Cohort{{Name: "active", Bindings: []any{int64(1)}}, {Name: "missing", Bindings: []any{int64(999)}}}),
		definition(searchInputEntity, `SELECT e.metadata_incarnation,e.revision
FROM catalog_entities e JOIN catalog_subjects s ON s.id=e.id
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0
AND s.library_id=e.library_id AND s.kind=e.kind`, stmt.Read,
			[]stmt.Param{integer("subject_id"), integer("library_id"), integer("kind")},
			[]stmt.Cohort{{Name: "new_movie", Bindings: []any{int64(1), int64(1), int64(1)}},
				{Name: "wrong_library", Bindings: []any{int64(1), int64(2), int64(1)}}}),
		definition(searchInputExisting, `SELECT id FROM search_publish_jobs INDEXED BY sqlite_autoindex_search_publish_jobs_1
WHERE entity_id=? LIMIT 1`, stmt.Read, []stmt.Param{integer("subject_id")},
			[]stmt.Cohort{{Name: "new_subject", Bindings: []any{int64(1)}}}),
		definition(searchInputInsert, `INSERT INTO search_publish_jobs(
entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_next_attempt_ms)
VALUES(?,?,1,?,?,?,'capture',?)`, stmt.Write,
			[]stmt.Param{integer("subject_id"), integer("bank_id"), {Name: "source_incarnation", Type: stmt.Blob},
				integer("source_revision"), text("recipe_version"), integer("accepted_at_ms")},
			[]stmt.Cohort{{Name: "first_intent", Bindings: []any{int64(1), int64(1), make([]byte, 16), int64(1), "A-CJK-uni-bi-v1", int64(1000)}}}),
		definition(searchInputPending, `UPDATE search_generations
SET pending_subjects=pending_subjects+?,job_revision=job_revision+1
WHERE id=? AND state=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=?
AND pending_subjects=? AND job_revision=? AND pending_subjects<=9223372036854775807-?
AND job_revision<9223372036854775807`, stmt.Write,
			[]stmt.Param{integer("new_subjects"), integer("bank_id"), text("bank_state"), text("recipe_version"), text("engine_version"),
				text("unicode_fingerprint"), integer("expected_pending"), integer("expected_revision"), integer("pending_increment")},
			[]stmt.Cohort{{Name: "first_pending", Bindings: []any{int64(1), int64(1), "active", "A-CJK-uni-bi-v1", "engine", "unicode", int64(0), int64(1), int64(1)}}}),
		definition(searchInputSequence, `UPDATE search_revision SET input_sequence=input_sequence+1
WHERE singleton=1 AND revision=? AND active_generation_id=? AND building_generation_id IS ?
AND input_sequence=? AND input_sequence<9223372036854775807`, stmt.Write,
			[]stmt.Param{integer("expected_revision"), integer("active_bank"), {Name: "building_bank", Type: stmt.Integer, Nullable: true}, integer("expected_input_sequence")},
			[]stmt.Cohort{{Name: "active_only", Bindings: []any{int64(1), int64(1), nil, int64(0)}},
				{Name: "active_building", Bindings: []any{int64(1), int64(1), int64(2), int64(0)}}}),
	}
}
