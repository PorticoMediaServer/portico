package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Page 202's finite statement family. The owner and receipt statements are the
// IP-03 definitions (metadata_refresh_sweeps/metadata_refresh_receipts); page
// scheduling adds only the raw keyset walk and the one CAS that consumes the
// request's page allowance. The raw index is a group D candidate, so these
// definitions are fixture-qualified only until that group is embedded.
const (
	ipPageCurrent       stmt.ID = "ip.refresh.page.current"
	ipPageRaw           stmt.ID = "ip.refresh.page.raw"
	ipPageNext          stmt.ID = "ip.refresh.page.next"
	ipPageWaiting       stmt.ID = "ip.refresh.page.waiting"
	ipPageCandidate     stmt.ID = "ip.refresh.page.candidate"
	ipPageProvider      stmt.ID = "ip.refresh.page.provider"
	ipPageSchedule      stmt.ID = "ip.refresh.page.schedule"
	ipPageUpdate        stmt.ID = "ip.refresh.page.update"
	ipPageTransition    stmt.ID = "ip.refresh.page.transition"
	ipPagePeopleUpdate  stmt.ID = "ip.refresh.page.people_update"
	ipPageComplete      stmt.ID = "ip.refresh.page.complete"
	ipPageTerminal      stmt.ID = "ip.refresh.page.terminal"
	ipPageRecord        stmt.ID = "ip.refresh.page.record"
	ipPagePerson        stmt.ID = "ip.refresh.page.person"
	ipPagePersonNext    stmt.ID = "ip.refresh.page.person_next"
	ipPageRawLimit      int64   = 16
	ipPagePersonPerPage int64   = 16
)

func ipRefreshPageDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// Current sweep owner state, including the raw cursor segment and the
			// operation incarnation the page receipts must share.
			ID: ipPageCurrent, SQL: `SELECT generation,revision,epoch_ms,phase,after_id,page_from_id,page_until_id,page_waiting,command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`,
			Params: []stmt.Param{t("provider"), t("purpose")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", "metadata"}},
				{Name: "missing", Bindings: []any{"tvdb", "artwork"}},
			},
		},
		{
			// The page walks the provider/purpose RAW keyset, never a due sort.
			// LIMIT applies to raw keys, so excluded rows cannot turn the walk
			// into an unbounded filtered scan.
			ID: ipPageRaw, SQL: `SELECT entity_id FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? ORDER BY entity_id LIMIT 16`,
			Params: []stmt.Param{t("provider"), t("purpose"), i("after")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_acquisition_sweep"}, Seeks: []string{"provider=? AND purpose=? AND entity_id>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", "metadata", int64(0)}},
				{Name: "missing", Bindings: []any{"tvdb", "artwork", int64(0)}},
			},
		},
		{
			ID: ipPageNext, SQL: `SELECT entity_id FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? ORDER BY entity_id LIMIT 1`,
			Params: []stmt.Param{t("provider"), t("purpose"), i("after")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_acquisition_sweep"}, Seeks: []string{"provider=? AND purpose=? AND entity_id>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", "metadata", int64(0)}},
				{Name: "missing", Bindings: []any{"tvdb", "artwork", int64(0)}},
			},
		},
		{
			// A previous page's scheduled jobs keep the window open. This probe
			// is bounded by the same raw page width plus one drift proof.
			ID: ipPageWaiting, SQL: `SELECT entity_id,CASE WHEN status='pending' AND next_attempt_ms<=? AND lease='' THEN 1 ELSE 0 END FROM metadata_acquisition_jobs INDEXED BY metadata_acquisition_sweep WHERE provider=? AND purpose=? AND entity_id>? AND entity_id<=? ORDER BY entity_id LIMIT 17`,
			Params: []stmt.Param{i("now"), t("provider"), t("purpose"), i("from"), i("until")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_acquisition_sweep"}, Seeks: []string{"provider=? AND purpose=? AND entity_id>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), "tmdb", "metadata", int64(0), int64(1 << 40)}},
				{Name: "missing", Bindings: []any{int64(1), "tvdb", "artwork", int64(0), int64(1 << 40)}},
			},
		},
		{
			// One exact eligibility point per raw key. Eligibility never changes
			// the cursor; every raw key is consumed whether or not it schedules.
			ID: ipPageCandidate, SQL: `SELECT j.generation,j.revision,j.status,j.selected_id,j.lease,j.lease_until_ms,j.next_attempt_ms,e.retired,l.retired,p.enabled FROM metadata_acquisition_jobs AS j JOIN catalog_entities AS e ON e.id=j.entity_id JOIN catalog_libraries AS l ON l.id=e.library_id JOIN metadata_provider_policy AS p ON p.library_id=e.library_id AND p.provider=j.provider WHERE j.entity_id=? AND j.provider=? AND j.purpose=?`,
			Params: []stmt.Param{i("entity"), t("provider"), t("purpose")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"j", "e", "l", "p"}, Seeks: []string{"entity_id=? AND provider=? AND purpose=?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), "tmdb", "metadata"}},
				{Name: "missing", Bindings: []any{int64(1 << 40), "tvdb", "artwork"}},
			},
		},
		{
			// Provider enablement and its single backoff allowance gate a whole
			// page, but never reorder the raw keyset.
			ID: ipPageProvider, SQL: `SELECT enabled,next_attempt_ms FROM metadata_providers WHERE code=?`,
			Params: []stmt.Param{t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_providers"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb"}},
				{Name: "missing", Bindings: []any{"tvdb"}},
			},
		},
		{
			ID: ipPageSchedule, SQL: `UPDATE metadata_acquisition_jobs SET status='pending',generation=generation+1,revision=revision+1,force_refresh=1,attempts=0,next_attempt_ms=?,error='' WHERE entity_id=? AND provider=? AND purpose=? AND generation=? AND revision=? AND status='matched' AND selected_id<>'' AND lease='' AND lease_until_ms<=? AND next_attempt_ms<=?`,
			Params:  []stmt.Param{i("now"), i("entity"), t("provider"), t("purpose"), i("generation"), i("revision"), i("lease_until"), i("next_attempt")},
			Cohorts: []stmt.Cohort{{Name: "scheduled", Bindings: []any{int64(1), int64(1), "tmdb", "metadata", int64(1), int64(1), int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_acquisition_jobs"}},
		},
		{
			ID: ipPageUpdate, SQL: `UPDATE metadata_refresh_sweeps SET revision=?,after_id=?,page_from_id=?,page_until_id=?,page_waiting=?,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND page_from_id=? AND page_until_id=? AND page_waiting=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='entities' RETURNING revision`,
			Params:  []stmt.Param{i("revision"), i("after"), i("from"), i("until"), i("waiting"), i("sequence"), t("provider"), t("purpose"), i("generation"), i("old_revision"), i("old_after"), i("old_from"), i("old_until"), i("old_waiting"), b("incarnation"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "page", Bindings: []any{int64(2), int64(1), int64(0), int64(1), int64(1), int64(2), "tmdb", "metadata", int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), make([]byte, 16), int64(1), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			ID: ipPageTransition, SQL: `UPDATE metadata_refresh_sweeps SET revision=?,phase='people',after_id=0,page_from_id=0,page_until_id=0,page_waiting=0,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND page_from_id=? AND page_until_id=? AND page_waiting=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='entities' RETURNING revision`,
			Params:  []stmt.Param{i("revision"), i("sequence"), t("provider"), t("purpose"), i("generation"), i("old_revision"), i("old_after"), i("old_from"), i("old_until"), i("old_waiting"), b("incarnation"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "transition", Bindings: []any{int64(2), int64(2), "tmdb", "metadata", int64(1), int64(1), int64(0), int64(0), int64(1), int64(0), make([]byte, 16), int64(1), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			ID: ipPagePeopleUpdate, SQL: `UPDATE metadata_refresh_sweeps SET revision=?,after_id=?,page_from_id=?,page_until_id=?,page_waiting=0,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND page_from_id=? AND page_until_id=? AND page_waiting=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='people' RETURNING revision`,
			Params:  []stmt.Param{i("revision"), i("after"), i("from"), i("until"), i("sequence"), t("provider"), t("purpose"), i("generation"), i("old_revision"), i("old_after"), i("old_from"), i("old_until"), i("old_waiting"), b("incarnation"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "people", Bindings: []any{int64(2), int64(1), int64(0), int64(1), int64(2), "tmdb", "metadata", int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), make([]byte, 16), int64(1), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			// The completing page leaves the sweep not due: job_next_attempt_ms
			// returns to NULL (so the generated job_due_ms leaves the
			// metadata_refresh_sweep_due index) and the claim lease is released.
			// A later begin re-arms both fields through IP-03's request CAS.
			ID: ipPageComplete, SQL: `UPDATE metadata_refresh_sweeps SET revision=?,phase='complete',after_id=?,page_from_id=?,page_until_id=?,page_waiting=0,job_lease_owner=0,job_lease_generation=0,job_lease_expires_ms=0,job_next_attempt_ms=NULL,last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND page_from_id=? AND page_until_id=? AND page_waiting=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='people' RETURNING revision`,
			Params:  []stmt.Param{i("revision"), i("after"), i("from"), i("until"), i("sequence"), t("provider"), t("purpose"), i("generation"), i("old_revision"), i("old_after"), i("old_from"), i("old_until"), i("old_waiting"), b("incarnation"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: []any{int64(2), int64(1), int64(0), int64(1), int64(2), "tmdb", "metadata", int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), make([]byte, 16), int64(1), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			// A page command that arrives on a sweep which is ALREADY complete
			// consumes only its own command sequence: revision, phase, cursor and
			// the job lease stay exactly as the completing page saved them. The
			// completion CAS below requires phase='people' and can never match
			// here, and re-running it would advance the terminal revision a
			// second time.
			ID: ipPageTerminal, SQL: `UPDATE metadata_refresh_sweeps SET last_command_sequence=? WHERE provider=? AND purpose=? AND generation=? AND revision=? AND after_id=? AND page_from_id=? AND page_until_id=? AND page_waiting=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='complete' RETURNING revision`,
			Params:  []stmt.Param{i("sequence"), t("provider"), t("purpose"), i("generation"), i("revision"), i("after"), i("from"), i("until"), i("waiting"), b("incarnation"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "completed", Bindings: []any{int64(2), "tmdb", "metadata", int64(1), int64(1), int64(0), int64(0), int64(0), int64(0), make([]byte, 16), int64(1), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			ID: ipPageRecord, SQL: `INSERT INTO metadata_refresh_receipts(provider,purpose,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting) VALUES(?,?,?,?,?,?,?,?,'page',?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{t("provider"), t("purpose"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("accepted_revision"), i("generation"), i("epoch_ms"), t("phase"), i("after"), i("scheduled"), i("waiting")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"tmdb", "metadata", make([]byte, 16), int64(2), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(2), int64(1), int64(1), "entities", int64(1), int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// People phase walks immutable accepted person sources by provider.
			// No entity key stands in for a person, and no due order is used.
			ID: ipPagePerson, SQL: `SELECT person_id FROM metadata_person_sources INDEXED BY metadata_person_source_provider WHERE provider=? AND person_id>? ORDER BY person_id LIMIT 16`,
			Params: []stmt.Param{t("provider"), i("after")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_person_source_provider"}, Seeks: []string{"provider=? AND person_id>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", int64(0)}},
				{Name: "missing", Bindings: []any{"musicbrainz", int64(1 << 40)}},
			},
		},
		{
			ID: ipPagePersonNext, SQL: `SELECT person_id FROM metadata_person_sources INDEXED BY metadata_person_source_provider WHERE provider=? AND person_id>? ORDER BY person_id LIMIT 1`,
			Params: []stmt.Param{t("provider"), i("after")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_person_source_provider"}, Seeks: []string{"provider=? AND person_id>?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", int64(0)}},
				{Name: "missing", Bindings: []any{"musicbrainz", int64(1 << 40)}},
			},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipPageSchedule, ipPageUpdate, ipPageTransition, ipPagePeopleUpdate, ipPageComplete, ipPageTerminal, ipPageRecord:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP refresh page"
		defs[n].CallSites = []string{"store/writer/ip_refresh_page.go"}
	}
	return defs
}
