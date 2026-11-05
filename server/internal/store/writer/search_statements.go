package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This fixed, well-formed stale tuple is a binding-population example only,
// never a compiled profile or a bank-initialization value. Keeping it fixed
// makes native plan cohorts independent of the compiling Unicode version.
const searchInputExampleFingerprint = searchpolicy.ProfileAlgorithm + ":0000000000000000000000000000000000000000000000000000000000000000"

// Closed IDs are composed by the ONE shared writer registry. These definitions
// confer no execution/receipt authority and contain no bank/table interpolation.
const (
	searchInputHead     stmt.ID = "search.publish.owner.revision_head"
	searchInputBank     stmt.ID = "search.publish.owner.input_bank"
	searchInputEntity   stmt.ID = "search.publish.owner.entity_input"
	searchInputExisting stmt.ID = "search.publish.owner.entity_intent"
	searchInputInsert   stmt.ID = "search.publish.cas.new_entity_intent"
	searchInputPending  stmt.ID = "search.publish.cas.generation_pending"
	searchInputSubject  stmt.ID = "search.publish.owner.subject_input"
	searchInputSequence stmt.ID = "search.publish.cas.input_sequence_active"
	searchInputBuilding stmt.ID = "search.publish.cas.input_sequence_building"
)

func searchInputIntentDefinitions() []stmt.Definition {
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	definition := func(id stmt.ID, sql string, kind stmt.Kind, params []stmt.Param, cohorts []stmt.Cohort) stmt.Definition {
		plan := stmt.Plan{Hot: true}
		switch id {
		case searchInputHead, searchInputSequence, searchInputBuilding:
			plan.PrimaryKeys, plan.Seeks = []string{"search_revision"}, []string{"rowid=?"}
		case searchInputBank, searchInputPending:
			plan.PrimaryKeys, plan.Seeks = []string{"search_generations"}, []string{"rowid=?"}
		case searchInputEntity:
			plan.PrimaryKeys, plan.Seeks = []string{"catalog_entities"}, []string{"rowid=?"}
		case searchInputSubject:
			plan.PrimaryKeys, plan.Seeks = []string{"catalog_subjects"}, []string{"rowid=?"}
		case searchInputExisting:
			// A generated UNIQUE index name is an asserted expected plan, not
			// an INDEXED BY hint. HN's native guard checks the actual path.
			plan.Indexes, plan.Seeks = []string{"sqlite_autoindex_search_publish_jobs_1"}, []string{"entity_id=?"}
		case searchInputInsert:
			// INSERT has no destination SEARCH. Full VM, destination effects
			// and FK qualification are required; no fake PK seek or Hot=false.
		}
		return stmt.Definition{
			ID: id, SQL: sql, Owner: "writer.search",
			CallSites: []string{"writer/search_commands.go:applySearchInputIntent"},
			Kind:      kind, Params: params, Cohorts: cohorts, Plan: plan,
		}
	}
	return []stmt.Definition{
		definition(searchInputHead, `SELECT revision,active_generation_id,building_generation_id,input_sequence
FROM search_revision WHERE singleton=1`, stmt.Read, nil,
			[]stmt.Cohort{{Name: "singleton", Bindings: []any{}}}),
		definition(searchInputBank, `SELECT bank_key,recipe_version,engine_version,unicode_fingerprint,state,pending_subjects,job_revision
FROM search_generations WHERE id=?`, stmt.Read, []stmt.Param{integer("bank_id")},
			[]stmt.Cohort{{Name: "active", Bindings: []any{int64(1)}}, {Name: "missing", Bindings: []any{int64(999)}}}),
		definition(searchInputEntity, `SELECT metadata_incarnation,revision FROM catalog_entities
WHERE id=? AND library_id=? AND kind=? AND retired=0`, stmt.Read,
			[]stmt.Param{integer("subject_id"), integer("library_id"), integer("kind")},
			[]stmt.Cohort{{Name: "new_movie", Bindings: []any{int64(1), int64(1), int64(1)}},
				{Name: "wrong_library", Bindings: []any{int64(1), int64(2), int64(1)}}}),
		definition(searchInputSubject, `SELECT id FROM catalog_subjects
WHERE id=? AND library_id=? AND kind=?`, stmt.Read,
			[]stmt.Param{integer("subject_id"), integer("library_id"), integer("kind")},
			[]stmt.Cohort{{Name: "retained_movie", Bindings: []any{int64(1), int64(1), int64(1)}},
				{Name: "wrong_library", Bindings: []any{int64(1), int64(2), int64(1)}}}),
		definition(searchInputExisting, `SELECT id FROM search_publish_jobs
WHERE entity_id=? LIMIT 1`, stmt.Read, []stmt.Param{integer("subject_id")},
			[]stmt.Cohort{{Name: "new_subject", Bindings: []any{int64(1)}}}),
		definition(searchInputInsert, `INSERT INTO search_publish_jobs(
entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_next_attempt_ms)
VALUES(?,?,?,?,?,?,?,?)`, stmt.Write,
			[]stmt.Param{integer("subject_id"), integer("bank_id"), integer("input_generation"), {Name: "source_incarnation", Type: stmt.Blob},
				integer("source_revision"), text("recipe_version"), text("phase"), integer("accepted_at_ms")},
			[]stmt.Cohort{{Name: "first_intent", Bindings: []any{int64(1), int64(1), firstSearchInputGeneration, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), searchpolicy.RecipeV1, firstSearchInputPhase, int64(1000)}}}),
		definition(searchInputPending, `UPDATE search_generations
SET pending_subjects=?,job_revision=?
WHERE id=? AND state=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=?
AND pending_subjects=? AND job_revision=? AND pending_subjects<9223372036854775807
AND job_revision<9223372036854775807`, stmt.Write,
			[]stmt.Param{integer("next_pending"), integer("next_revision"), integer("bank_id"), text("bank_state"), text("recipe_version"), text("engine_version"),
				text("unicode_fingerprint"), integer("expected_pending"), integer("expected_revision")},
			[]stmt.Cohort{{Name: "first_pending", Bindings: []any{int64(1), int64(2), int64(1), "active", searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint, int64(0), int64(1)}}}),
		definition(searchInputSequence, `UPDATE search_revision SET input_sequence=?
WHERE singleton=1 AND revision=? AND active_generation_id=? AND building_generation_id IS NULL
AND input_sequence=? AND input_sequence<9223372036854775807`, stmt.Write,
			[]stmt.Param{integer("next_input_sequence"), integer("expected_revision"), integer("active_bank"), integer("expected_input_sequence")},
			[]stmt.Cohort{{Name: "active_only", Bindings: []any{int64(1), int64(1), int64(1), int64(0)}}}),
		definition(searchInputBuilding, `UPDATE search_revision SET input_sequence=?
WHERE singleton=1 AND revision=? AND active_generation_id=? AND building_generation_id=?
AND input_sequence=? AND input_sequence<9223372036854775807`, stmt.Write,
			[]stmt.Param{integer("next_input_sequence"), integer("expected_revision"), integer("active_bank"), integer("building_bank"), integer("expected_input_sequence")},
			[]stmt.Cohort{{Name: "active_building", Bindings: []any{int64(1), int64(1), int64(1), int64(2), int64(0)}}}),
	}
}
