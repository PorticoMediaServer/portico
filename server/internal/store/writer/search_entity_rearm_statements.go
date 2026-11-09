package writer

import (
	"database/sql"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	searchRearmEntity      stmt.ID = "search.rearm.owner.entity"
	searchRearmBank        stmt.ID = "search.rearm.owner.bank"
	searchRearmJobRead     stmt.ID = "search.rearm.owner.job"
	searchRearmHead        stmt.ID = "search.rearm.owner.head"
	searchRearmStage       stmt.ID = "search.rearm.owner.stage"
	searchRearmCustodyRead stmt.ID = "search.rearm.owner.custody"
	searchRearmPending     stmt.ID = "search.rearm.cas.pending"
	searchRearmWakeNoDue   stmt.ID = "search.rearm.cas.wake_no_due"
	searchRearmWakeDue     stmt.ID = "search.rearm.cas.wake_due"
)

// Inert inventory for the accepting command's SAME Capture/registry/recipe.
// No new CommandID, SQL executor, source permission or runtime composition.
func searchEntityRearmDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	pk := func(table string) stmt.Plan {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{"rowid=?"}}
	}
	defs := []stmt.Definition{
		{ID: searchRearmEntity, Kind: stmt.Read, SQL: `SELECT library_id,kind,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND retired=0`, Params: []stmt.Param{i("entity")}, Plan: pk("catalog_entities")},
		{ID: searchRearmBank, Kind: stmt.Read, SQL: `SELECT pending_subjects FROM search_generations WHERE id=? AND state=? AND recipe_version=?`, Params: []stmt.Param{i("bank"), t("role"), t("recipe")}, Plan: pk("search_generations")},
		{ID: searchRearmJobRead, Kind: stmt.Read, SQL: `SELECT id,job_revision,input_generation,published_generation,published_document_id,document_id,source_incarnation,source_revision,command_incarnation,phase,after_field,after_ordinal,after_fragment_id,expected_fragments,staged_fragments,staged_bytes,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,last_command_sequence,acknowledged_command_sequence FROM search_publish_jobs WHERE entity_id=? AND search_generation_id=? AND person_id IS NULL AND recipe_version=?`, Params: []stmt.Param{i("entity"), i("bank"), t("recipe")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_search_publish_jobs_1"}, Seeks: []string{"entity_id=? AND search_generation_id=?"}}},
		{ID: searchRearmHead, Kind: stmt.Read, SQL: `SELECT id FROM search_publications WHERE id=? AND entity_id=? AND person_id IS NULL AND search_generation_id=? AND generation=? AND recipe_version=?`, Params: []stmt.Param{i("image"), i("entity"), i("bank"), i("generation"), t("recipe")}, Plan: pk("search_publications")},
		{ID: searchRearmStage, Kind: stmt.Read, SQL: `SELECT id FROM search_publications WHERE id=? AND entity_id=? AND person_id IS NULL AND search_generation_id=? AND generation=? AND source_incarnation=? AND source_revision=? AND recipe_version=?`, Params: []stmt.Param{i("image"), i("entity"), i("bank"), i("generation"), b("source_incarnation"), i("source_revision"), t("recipe")}, Plan: pk("search_publications")},
		{ID: searchRearmCustodyRead, Kind: stmt.Read, SQL: `SELECT id,job_revision,command_incarnation,after_fragment_id,last_command_sequence,acknowledged_command_sequence,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms FROM search_reclaim_jobs WHERE publication_id=? AND live_publication_id=? AND phase=?`, Params: []stmt.Param{i("publication"), i("live_publication"), t("phase")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_search_reclaim_jobs_1"}, Seeks: []string{"publication_id=?"}}},
		// Unlike new-subject pending++, this also admits an exact +0 at MaxInt64.
		{ID: searchRearmPending, Kind: stmt.Write, SQL: `UPDATE search_generations SET pending_subjects=? WHERE id=? AND state=? AND recipe_version=? AND pending_subjects=?`, Params: []stmt.Param{i("next_pending"), i("bank"), t("role"), t("recipe"), i("pending")}, Plan: pk("search_generations")},
	}
	// Reuse the unchanged PB wake CAS, changing only the values it writes.
	for _, due := range []bool{false, true} {
		id := searchRearmWakeNoDue
		q := `UPDATE search_reclaim_jobs SET phase='ready',job_revision=?,job_next_attempt_ms=0 WHERE id=? AND publication_id=? AND live_publication_id=? AND phase='held' AND command_incarnation=? AND job_revision=? AND after_fragment_id=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=?`
		ps := []stmt.Param{i("next_revision"), i("job"), i("publication"), i("live_publication"), b("incarnation"), i("revision"), i("after"), i("last"), i("ack"), i("lease_owner"), i("lease_generation"), i("lease_expires"), i("attempts")}
		if due {
			id, q, ps = searchRearmWakeDue, q+` AND job_next_attempt_ms=?`, append(ps, i("due"))
		} else {
			q += ` AND job_next_attempt_ms IS NULL`
		}
		defs = append(defs, stmt.Definition{ID: id, Kind: stmt.Write, SQL: q, Params: ps, Plan: pk("search_reclaim_jobs")})
	}
	// Eight finite NULL shapes. No nullable equality, OR/sentinel SQL, or
	// caller-selected identifier/statement is admitted.
	for mask := 0; mask < 8; mask++ {
		q := `UPDATE search_publish_jobs SET input_generation=?,source_incarnation=?,source_revision=?,phase='capture',document_id=?,after_field=0,after_ordinal=0,after_fragment_id=0,expected_fragments=0,staged_fragments=0,staged_bytes=0,content_digest='',job_revision=?,job_lease_generation=?,job_lease_owner=0,job_lease_expires_ms=0,job_attempts=0,job_next_attempt_ms=0 WHERE id=? AND entity_id=? AND person_id IS NULL AND search_generation_id=? AND recipe_version=? AND command_incarnation=? AND job_revision=? AND input_generation=? AND published_generation=? AND source_incarnation=? AND source_revision=? AND phase=? AND after_field=? AND after_ordinal=? AND after_fragment_id=? AND expected_fragments=? AND staged_fragments=? AND staged_bytes=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND last_command_sequence=? AND acknowledged_command_sequence=?`
		ps := []stmt.Param{i("next_input"), b("next_incarnation"), i("next_source_revision"), {Name: "cleared_document", Type: stmt.Integer, Nullable: true}, i("next_revision"), i("next_lease"), i("job"), i("entity"), i("bank"), t("recipe"), b("command_incarnation"), i("revision"), i("input"), i("published"), b("source_incarnation"), i("source_revision"), t("phase"), i("field"), i("ordinal"), i("fragment"), i("expected"), i("staged"), i("staged_bytes"), i("lease_owner"), i("lease_generation"), i("lease_expires"), i("attempts"), i("last"), i("ack")}
		for bit, column := range []string{"published_document_id", "document_id", "job_next_attempt_ms"} {
			q += " AND " + column
			if mask&(1<<bit) != 0 {
				q += "=?"
				ps = append(ps, i(column))
			} else {
				q += " IS NULL"
			}
		}
		defs = append(defs, stmt.Definition{ID: searchRearmJobID(mask), Kind: stmt.Write, SQL: q, Params: ps, Plan: pk("search_publish_jobs")})
	}
	for _, d := range searchInputIntentDefinitions() {
		switch d.ID {
		case searchInputHead, searchInputSubject, searchInputExisting, searchInputSequence, searchInputBuilding:
			defs = append(defs, d)
		}
	}
	for n := range defs {
		if !strings.HasPrefix(string(defs[n].ID), "search.rearm.") {
			continue
		}
		d := &defs[n]
		// Complete inert populated/empty binding cohorts. Capture never executes
		// these effects; runtime reads derive all CAS values on the same turn.
		if len(d.Cohorts) == 0 {
			var args []any
			inc, command := [16]byte{23}, [16]byte{17}
			switch d.ID {
			case searchRearmEntity:
				args = []any{int64(41)}
			case searchRearmBank:
				args = []any{int64(7), "active", searchpolicy.RecipeV1}
			case searchRearmJobRead:
				args = []any{int64(41), int64(7), searchpolicy.RecipeV1}
			case searchRearmHead:
				args = []any{int64(13), int64(41), int64(7), int64(3), searchpolicy.RecipeV1}
			case searchRearmStage:
				args = []any{int64(307), int64(41), int64(7), int64(5), inc[:], int64(11), searchpolicy.RecipeV1}
			case searchRearmCustodyRead:
				args = []any{int64(307), int64(307), "held"}
			case searchRearmPending:
				args = []any{int64(3), int64(7), "active", searchpolicy.RecipeV1, int64(3)}
			case searchRearmWakeNoDue, searchRearmWakeDue:
				reclaim := [16]byte{51}
				args = []any{int64(2), int64(1), int64(307), int64(307), reclaim[:], int64(1), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0)}
				if d.ID == searchRearmWakeDue {
					args = append(args, int64(1000))
				}
			default:
				for mask := 0; mask < 8; mask++ {
					if d.ID != searchRearmJobID(mask) {
						continue
					}
					in := searchEntityRearmInput{entity: 41, sourceRevision: 12, incarnation: inc}
					p := searchRearmBankPlan{bank: searchEntityRearmBank{7, searchpolicy.RecipeV1}, job: searchRearmJob{id: 29, revision: 19, input: 5, sourceRevision: 11, sourceIncarnation: inc, commandIncarnation: command, phase: "capture", leaseOwner: 1, leaseGeneration: 2, leaseExpires: 2100000000000, last: 7, ack: 7}, advance: searchCoalesceBankAdvance{InputGeneration: 6, JobRevision: 20}, nextLease: 3}
					if mask&1 != 0 {
						p.job.head = sql.NullInt64{Int64: 13, Valid: true}
						p.job.published = 3
					}
					if mask&2 != 0 {
						p.job.stage = sql.NullInt64{Int64: 307, Valid: true}
						p.job.phase = "stage"
					}
					if mask&4 != 0 {
						p.job.due = sql.NullInt64{Int64: 1000, Valid: true}
					}
					_, args = searchRearmJobValues(&in, &p)
				}
			}
			d.Cohorts = []stmt.Cohort{{Name: "native_point", Bindings: args}}
		}
		defs[n].Owner = "writer.search.rearm"
		defs[n].CallSites = []string{"writer/search_entity_rearm.go:rearmSearchEntity"}
	}
	return defs
}

func searchRearmJobID(mask int) stmt.ID {
	return [...]stmt.ID{"search.rearm.cas.job_0", "search.rearm.cas.job_1", "search.rearm.cas.job_2", "search.rearm.cas.job_3", "search.rearm.cas.job_4", "search.rearm.cas.job_5", "search.rearm.cas.job_6", "search.rearm.cas.job_7"}[mask]
}

// Fixed-size values: checking registration under the writer hold must never
// construct the inert definitions, SQL strings or planner cohorts.
func searchEntityRearmRequiredIDs() [22]stmt.ID {
	return [22]stmt.ID{searchRearmEntity, searchRearmBank, searchRearmJobRead, searchRearmHead, searchRearmStage, searchRearmCustodyRead, searchRearmPending, searchRearmWakeNoDue, searchRearmWakeDue,
		"search.rearm.cas.job_0", "search.rearm.cas.job_1", "search.rearm.cas.job_2", "search.rearm.cas.job_3", "search.rearm.cas.job_4", "search.rearm.cas.job_5", "search.rearm.cas.job_6", "search.rearm.cas.job_7",
		searchInputHead, searchInputSubject, searchInputExisting, searchInputSequence, searchInputBuilding}
}

func searchRearmJobValues(in *searchEntityRearmInput, p *searchRearmBankPlan) (stmt.ID, []any) {
	j, a := p.job, p.advance
	args := []any{a.InputGeneration, in.incarnation[:], in.sourceRevision, nil, a.JobRevision, p.nextLease, j.id, in.entity, p.bank.ID, p.bank.Recipe, j.commandIncarnation[:], j.revision, j.input, j.published, j.sourceIncarnation[:], j.sourceRevision, j.phase,
		j.afterField, j.afterOrdinal, j.afterFragment, j.expected, j.staged, j.stagedBytes, j.leaseOwner, j.leaseGeneration, j.leaseExpires, j.attempts, j.last, j.ack}
	mask := 0
	for bit, v := range [3]sql.NullInt64{j.head, j.stage, j.due} {
		if v.Valid {
			mask |= 1 << bit
			args = append(args, v.Int64)
		}
	}
	return searchRearmJobID(mask), args
}
