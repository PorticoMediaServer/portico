package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	searchHeadOwnRead      stmt.ID = "search.publish.head.own_tokens"
	searchHeadClockRead    stmt.ID = "search.publish.head.serving_revision"
	searchHeadPendingRead  stmt.ID = "search.publish.head.pending"
	searchHeadNoNext       stmt.ID = "search.publish.head.no_next_fragment"
	searchHeadOldCustody   stmt.ID = "search.publish.head.old_custody"
	searchHeadWakeNoDue    stmt.ID = "search.publish.head.wake_no_due"
	searchHeadWakeDue      stmt.ID = "search.publish.head.wake_due"
	searchHeadOwnInsert    stmt.ID = "search.publish.head.own_insert"
	searchHeadOwnDelete    stmt.ID = "search.publish.head.own_delete"
	searchHeadPendingCAS   stmt.ID = "search.publish.cas.head_pending"
	searchHeadClockOnly    stmt.ID = "search.publish.cas.head_clock_only"
	searchHeadClockDual    stmt.ID = "search.publish.cas.head_clock_dual"
	searchHeadReceiptPut   stmt.ID = "search.publish.receipt.head_insert"
	searchHeadReceiptRead  stmt.ID = "search.publish.receipt.head_result"
	searchHeadNoHeadNoDue  stmt.ID = "search.publish.cas.head_no_head_no_due"
	searchHeadNoHeadDue    stmt.ID = "search.publish.cas.head_no_head_due"
	searchHeadHasHeadNoDue stmt.ID = "search.publish.cas.head_has_head_no_due"
	searchHeadHasHeadDue   stmt.ID = "search.publish.cas.head_has_head_due"
)

const searchHeadReceiptKind = "head_publish"

// Finite head family, including shared source/history/custody reads. This is
// inert definition composition, not registration, admission or a Work recipe.
func searchHeadDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: searchHeadOwnRead, Kind: stmt.Read, SQL: `SELECT id,title_prefix_tokens,initial_prefix_tokens FROM search_publications
WHERE id=? AND entity_id=? AND person_id IS NULL AND library_id=? AND kind=? AND search_generation_id=? AND generation=? AND recipe_version=? AND person_name_tokens=?`,
			Params:  []stmt.Param{i("image"), i("entity"), i("library"), i("kind"), i("bank"), i("generation"), t("recipe"), t("owner_names")},
			Cohorts: []stmt.Cohort{{Name: "own_header_only", Bindings: []any{int64(307), int64(41), int64(3), int64(1), int64(7), int64(5), searchpolicy.RecipeV1, ""}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publications"}, Seeks: []string{"rowid=?"}}},
		{ID: searchHeadClockRead, Kind: stmt.Read, SQL: `SELECT revision FROM search_revision WHERE singleton=?`, Params: []stmt.Param{i("singleton")}, Cohorts: []stmt.Cohort{{Name: "current_serving_clock", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_revision"}, Seeks: []string{"rowid=?"}}},
		{ID: searchHeadPendingRead, Kind: stmt.Read, SQL: `SELECT pending_subjects FROM search_generations WHERE id=? AND bank_key=? AND state=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=?`,
			Params: []stmt.Param{i("bank"), t("key"), t("role"), t("recipe"), t("engine"), t("unicode")}, Cohorts: []stmt.Cohort{{Name: "exact_pending_bank", Bindings: []any{int64(7), "v1", "active", searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_generations"}, Seeks: []string{"rowid=?"}}},
		{ID: searchHeadNoNext, Kind: stmt.Read, SQL: `SELECT id FROM search_fragments INDEXED BY search_fragment_reclaim WHERE publication_id=? AND id>? ORDER BY id LIMIT ?`, Params: []stmt.Param{i("image"), i("after_fragment"), i("limit")}, Cohorts: []stmt.Cohort{{Name: "indexed_final_tail", Bindings: []any{int64(307), int64(503), int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"search_fragment_reclaim"}, Seeks: []string{"publication_id=? AND id>?"}}},
		{ID: searchHeadOldCustody, Kind: stmt.Read, SQL: `SELECT id,publication_id,live_publication_id,phase,command_incarnation,job_revision,after_fragment_id,last_command_sequence,acknowledged_command_sequence,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms FROM search_reclaim_jobs WHERE publication_id=?`, Params: []stmt.Param{i("image")}, Cohorts: []stmt.Cohort{{Name: "old_head_held", Bindings: []any{int64(13)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_search_reclaim_jobs_1"}, Seeks: []string{"publication_id=?"}}},
		{ID: searchHeadPendingCAS, Kind: stmt.Write, SQL: `UPDATE search_generations SET pending_subjects=? WHERE id=? AND bank_key=? AND state=? AND recipe_version=? AND engine_version=? AND unicode_fingerprint=? AND pending_subjects=?`, Params: []stmt.Param{i("next_pending"), i("bank"), t("key"), t("role"), t("recipe"), t("engine"), t("unicode"), i("pending")}, Cohorts: []stmt.Cohort{{Name: "one_caught_up_subject", Bindings: []any{int64(2), int64(7), "v1", "active", searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint, int64(3)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_generations"}, Seeks: []string{"rowid=?"}}},
	}
	for _, v := range []struct {
		id  stmt.ID
		due bool
	}{{searchHeadWakeNoDue, false}, {searchHeadWakeDue, true}} {
		q := `UPDATE search_reclaim_jobs SET phase=?,job_revision=?,job_next_attempt_ms=? WHERE id=? AND publication_id=? AND live_publication_id=? AND phase=? AND command_incarnation=? AND job_revision=? AND after_fragment_id=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=?`
		params := []stmt.Param{t("ready"), i("next_revision"), i("next_due"), i("id"), i("publication"), i("live_publication"), t("held"), b("incarnation"), i("revision"), i("after_fragment"), i("last_sequence"), i("ack_sequence"), i("lease_owner"), i("lease_generation"), i("lease_expires"), i("attempts")}
		args := []any{"ready", int64(2), int64(1000), int64(1), int64(13), int64(13), "held", make([]byte, 16), int64(1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)}
		if v.due {
			q += ` AND job_next_attempt_ms=?`
			params = append(params, i("due"))
			args = append(args, int64(500))
		} else {
			q += ` AND job_next_attempt_ms IS NULL`
		}
		defs = append(defs, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: q, Params: params, Cohorts: []stmt.Cohort{{Name: "eligibility_only", Bindings: args}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_reclaim_jobs"}, Seeks: []string{"rowid=?"}}})
	}
	for _, v := range []struct {
		id   stmt.ID
		dual bool
	}{{searchHeadClockOnly, false}, {searchHeadClockDual, true}} {
		q := `UPDATE search_revision SET revision=? WHERE singleton=? AND revision=? AND active_generation_id=?`
		params := []stmt.Param{i("next_revision"), i("singleton"), i("revision"), i("active")}
		args := []any{int64(20), int64(1), int64(19), int64(7)}
		if v.dual {
			q += ` AND building_generation_id=?`
			params = append(params, i("building"))
			args = append(args, int64(8))
		} else {
			q += ` AND building_generation_id IS NULL`
		}
		defs = append(defs, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: q, Params: params, Cohorts: []stmt.Cohort{{Name: "serving_head_only", Bindings: args}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_revision"}, Seeks: []string{"rowid=?"}}})
	}
	for _, v := range []struct {
		id   stmt.ID
		slot stmt.FTSRetainedSlot
	}{{searchHeadOwnInsert, stmt.SearchOwnPrefixInsertV1}, {searchHeadOwnDelete, stmt.SearchOwnPrefixDeleteV1}} {
		spec, _ := v.slot.Specification()
		params := []stmt.Param{}
		args := []any{}
		if spec.Delete {
			params = append(params, t("control"))
			args = append(args, "delete")
		}
		params = append(params, i("rowid"), t("title_prefix_tokens"), t("initial_prefix_tokens"))
		args = append(args, int64(41), "000061", "000061")
		defs = append(defs, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: spec.SQL, Params: params, Cohorts: []stmt.Cohort{{Name: "original_own_tokens", Bindings: args}}, Plan: stmt.Plan{Hot: true, NativeRetained: v.slot}})
	}
	ids := map[stmt.ID]stmt.ID{searchFragmentNoHeadNoDue: searchHeadNoHeadNoDue, searchFragmentNoHeadDue: searchHeadNoHeadDue, searchFragmentHeadNoDue: searchHeadHasHeadNoDue, searchFragmentHeadDue: searchHeadHasHeadDue}
	for _, d := range searchFragmentStageDefinitions() {
		if id, ok := ids[d.ID]; ok {
			_, where, ok := strings.Cut(d.SQL, "\nWHERE ")
			if !ok {
				continue
			}
			d.ID = id
			d.SQL = `UPDATE search_publish_jobs SET published_document_id=?,published_generation=?,document_id=?,phase=?,job_next_attempt_ms=?,job_revision=?,last_command_sequence=?` + "\nWHERE " + where
			d.Params = append([]stmt.Param{i("new_head"), i("new_published_generation"), {Name: "cleared_stage", Type: stmt.Integer, Nullable: true}, t("idle"), {Name: "cleared_due", Type: stmt.Integer, Nullable: true}, i("new_revision"), i("new_sequence")}, d.Params[7:]...)
			d.Cohorts[0].Bindings = append([]any{int64(307), int64(5), nil, "idle", nil, int64(20), int64(8)}, d.Cohorts[0].Bindings[7:]...)
			for at, param := range d.Params {
				switch param.Name {
				case "phase":
					d.Cohorts[0].Bindings[at] = "publish"
				case "after_field":
					d.Cohorts[0].Bindings[at] = int64(1)
				case "after_ordinal":
					d.Cohorts[0].Bindings[at] = int64(2)
				case "after_fragment":
					d.Cohorts[0].Bindings[at] = int64(503)
				case "staged_fragments":
					d.Cohorts[0].Bindings[at] = int64(3)
				case "staged_bytes":
					d.Cohorts[0].Bindings[at] = int64(38)
				}
			}

			// The shared complete WHERE remains a sealed-image cursor, not the
			// first empty stage cursor used by fragment's capture fixture.
			defs = append(defs, d)
		}
		if d.ID == searchFragmentImageRead || d.ID == searchFragmentSealRead || d.ID == searchFragmentHeldRead {
			defs = append(defs, d)
		}
	}
	for _, d := range searchPublisherReceiptDefinitions() {
		switch d.ID {
		case searchPublisherOwnerID, searchPublisherIdentity, searchPublisherAck:
			defs = append(defs, d)
		case searchPublisherImagePut:
			d.ID = searchHeadReceiptPut
			d.Cohorts = []stmt.Cohort{{Name: "saved_accepted_head", Bindings: []any{int64(29), make([]byte, 16), int64(8), make([]byte, 16), make([]byte, 32), searchHeadReceiptKind, int64(20), int64(1000), int64(5), int64(307), nil, int64(1), int64(2), int64(503), make([]byte, 32), int64(5), int64(3), int64(8), int64(3), int64(8), searchmanifest.Algorithm}}}
			defs = append(defs, d)
		case searchPublisherImageRead:
			d.ID = searchHeadReceiptRead
			d.Cohorts = []stmt.Cohort{{Name: "saved_head_without_native_join", Bindings: []any{int64(29), make([]byte, 16), int64(8), searchmanifest.Algorithm}}}
			defs = append(defs, d)
		}
	}
	for _, d := range searchImageBeginDefinitions() {
		switch d.ID {
		case searchImageSource, searchImageSubject, searchImageLibrary, searchImageBank, searchImageHeads, searchImageJob:
			defs = append(defs, d)
		}
	}
	for n := range defs {
		if strings.Contains(string(defs[n].ID), ".head") {
			defs[n].Owner = "writer.search.publisher"
			defs[n].CallSites = []string{"writer/search_head_stage.go"}
		}
	}
	return defs
}

func searchHeadJobValues(c *searchHeadPublishCommand, next searchHeadPublishTransition) (stmt.ID, []any) {
	s := c.request.snapshot.stage
	f := &searchFragmentStageCommand{identity: c.identity, request: searchFragmentStageRequest{snapshot: s}}
	id, args := searchFragmentJobValues(f, 0, searchFragmentTransition{})
	ids := map[stmt.ID]stmt.ID{searchFragmentNoHeadNoDue: searchHeadNoHeadNoDue, searchFragmentNoHeadDue: searchHeadNoHeadDue, searchFragmentHeadNoDue: searchHeadHasHeadNoDue, searchFragmentHeadDue: searchHeadHasHeadDue}
	return ids[id], append([]any{next.imageID, next.inputGeneration, nil, "idle", nil, next.acceptedJobRevision, c.identity.Sequence}, args[7:]...)
}
