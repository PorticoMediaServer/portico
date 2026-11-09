package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP entity acquisition job 203/204/205. The exact provider operation owner
// (metadata_provider_operations, provider namespace) carries the lease and the
// command floor; the acquisition job row carries the scheduled subject state.
// The IP receipt header is group D and fixture-qualified until that group is
// embedded, so these definitions make no Capture claim.
const (
	ipEntityJobCandidateStmt   stmt.ID = "ip.entity.job.candidate"
	ipEntityJobOwnerStmt       stmt.ID = "ip.entity.job.owner"
	ipEntityJobSubjectStmt     stmt.ID = "ip.entity.job.subject"
	ipEntityJobCurrentStmt     stmt.ID = "ip.entity.job.current"
	ipEntityJobReceiptStmt     stmt.ID = "ip.entity.job.receipt"
	ipEntityJobInsertOwnerStmt stmt.ID = "ip.entity.job.insert_owner"
	ipEntityJobInsertSubjStmt  stmt.ID = "ip.entity.job.insert_subject"
	ipEntityJobClaimStmt       stmt.ID = "ip.entity.job.claim"
	ipEntityJobRenewJobStmt    stmt.ID = "ip.entity.job.renew"
	ipEntityJobRenewOwnerStmt  stmt.ID = "ip.entity.job.renew_owner"
	ipEntityJobFinishJobStmt   stmt.ID = "ip.entity.job.finish"
	ipEntityJobFinishOwnerStmt stmt.ID = "ip.entity.job.finish_owner"
	ipEntityJobRecordStmt      stmt.ID = "ip.entity.job.record"
)

func ipEntityJobDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// One exact scheduled subject: the job owner key, its raw selected
			// provider object and the current entity/library/policy source.
			ID: ipEntityJobCandidateStmt, SQL: `SELECT j.status,j.selected_id,j.generation,j.revision,j.lease,j.lease_until_ms,j.next_attempt_ms,j.attempts,j.manual,j.review_search,j.episode_order,e.revision,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.retired,l.id,l.library_id,l.retired,p.incarnation,p.revision,p.enabled,p.algorithm,p.refresh_mode FROM metadata_acquisition_jobs AS j JOIN catalog_entities AS e ON e.id=j.entity_id JOIN catalog_libraries AS l ON l.id=e.library_id JOIN metadata_provider_policy AS p ON p.library_id=e.library_id AND p.provider=j.provider WHERE j.entity_id=? AND j.provider=? AND j.purpose=?`,
			Params: []stmt.Param{i("entity"), t("provider"), t("purpose")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"j", "e", "l", "p"}, Seeks: []string{"entity_id=? AND provider=? AND purpose=?"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), "tmdb", "metadata"}},
				{Name: "missing", Bindings: []any{int64(1 << 40), "tvdb", "metadata"}},
			},
		},
		{
			// The provider namespace is (provider,id); a cross-provider lookup
			// cannot borrow another provider's owner row.
			ID: ipEntityJobOwnerStmt, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,generation,status,lease,lease_until,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,policy_incarnation,policy_revision,consent_revision,selected_id FROM metadata_provider_operations WHERE id=? AND provider=?`,
			Params: []stmt.Param{t("operation"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operations"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"operation-1", "tmdb"}},
				{Name: "missing", Bindings: []any{"operation-1", "tvdb"}},
			},
		},
		{
			// The immutable subject binding is the operation's exact observation
			// witness: entity revision and every source/identity revision.
			ID: ipEntityJobSubjectStmt, SQL: `SELECT revision,entity_incarnation,source_revision,identity_revision,metadata_revision FROM metadata_operation_subjects WHERE operation_id=? AND provider=? AND role='primary' AND entity_id=?`,
			Params: []stmt.Param{t("operation"), t("provider"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_operation_subjects"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"operation-1", "tmdb", int64(1)}},
				{Name: "missing", Bindings: []any{"operation-1", "tvdb", int64(1)}},
			},
		},
		{
			// Current entity source, library and provider policy for a claimed
			// operation. A new observation or policy revision makes it stale.
			ID: ipEntityJobCurrentStmt, SQL: `SELECT e.revision,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.retired,l.retired,p.incarnation,p.revision,p.enabled FROM catalog_entities AS e JOIN catalog_libraries AS l ON l.id=e.library_id JOIN metadata_provider_policy AS p ON p.library_id=e.library_id AND p.provider=? WHERE e.id=?`,
			Params: []stmt.Param{t("provider"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l", "p"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", int64(1)}},
				{Name: "missing", Bindings: []any{"tvdb", int64(1 << 40)}},
			},
		},
		{
			ID: ipEntityJobReceiptStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_phase,terminal FROM metadata_ip_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"operation-1", make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{"operation-1", make([]byte, 16), int64(2)}},
			},
		},
		{
			ID: ipEntityJobInsertOwnerStmt, SQL: `INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,source_content_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,?,'ip-acquisition','claimed',1,?,?,?,?,?,?,0,0,0,?,?,?,?,?,'','',0,'','',?,?,?,'ip-acquisition-v1',?,1,?,1,0) RETURNING generation,last_command_sequence`,
			Params:  []stmt.Param{t("operation"), i("entity"), t("provider"), t("lease"), t("lease_until"), b("entity_incarnation"), i("source_revision"), i("identity_revision"), i("metadata_revision"), b("policy_incarnation"), i("policy_revision"), i("consent_revision"), t("algorithm"), t("refresh_mode"), t("selected"), i("manual"), i("review_search"), t("created_at"), b("command_incarnation")},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"operation-1", int64(1), "tmdb", "lease-1", "2026-01-01T00:05:00Z", make([]byte, 16), int64(1), int64(1), int64(1), make([]byte, 16), int64(1), int64(1), "v1", "fill_missing", "tmdb-1", int64(0), int64(0), "2026-01-01T00:00:00Z", make([]byte, 16)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityJobInsertSubjStmt, SQL: `INSERT INTO metadata_operation_subjects(provider,evidence_kind,operation_id,role,entity_id,entity_incarnation,revision,source_revision,identity_revision,metadata_revision,membership_revision,accepted_evidence_id) VALUES(?,NULL,?,'primary',?,?,?,?,?,?,0,NULL)`,
			Params:  []stmt.Param{t("provider"), t("operation"), i("entity"), b("entity_incarnation"), i("revision"), i("source_revision"), i("identity_revision"), i("metadata_revision")},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"tmdb", "operation-1", int64(1), make([]byte, 16), int64(1), int64(1), int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// One scheduled subject becomes the operation's exact owner. The old
			// generation/revision/lease are required witnesses; an expired lease
			// still consumes exactly one new generation.
			ID: ipEntityJobClaimStmt, SQL: `UPDATE metadata_acquisition_jobs SET status='claimed',generation=generation+1,revision=revision+1,lease=?,lease_until_ms=?,attempts=attempts+1,next_attempt_ms=?,error='' WHERE entity_id=? AND provider=? AND purpose=? AND generation=? AND revision=? AND status IN('pending','claimed') AND selected_id=? AND lease=? AND lease_until_ms=? AND next_attempt_ms<=? RETURNING generation,revision`,
			Params:  []stmt.Param{t("lease"), i("lease_until"), i("now"), i("entity"), t("provider"), t("purpose"), i("generation"), i("revision"), t("selected"), t("old_lease"), i("old_lease_until"), i("old_next_attempt")},
			Cohorts: []stmt.Cohort{{Name: "claimed", Bindings: []any{"lease-1", int64(300_000), int64(0), int64(1), "tmdb", "metadata", int64(1), int64(1), "tmdb-1", "", int64(0), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_acquisition_jobs"}},
		},
		{
			ID: ipEntityJobRenewJobStmt, SQL: `UPDATE metadata_acquisition_jobs SET revision=revision+1,lease_until_ms=? WHERE entity_id=? AND provider=? AND purpose=? AND generation=? AND revision=? AND status='claimed' AND lease=? AND lease_until_ms=? RETURNING revision`,
			Params:  []stmt.Param{i("lease_until"), i("entity"), t("provider"), t("purpose"), i("generation"), i("revision"), t("lease"), i("old_lease_until")},
			Cohorts: []stmt.Cohort{{Name: "renewed", Bindings: []any{int64(600_000), int64(1), "tmdb", "metadata", int64(2), int64(2), "lease-1", int64(300_000)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_acquisition_jobs"}},
		},
		{
			ID: ipEntityJobRenewOwnerStmt, SQL: `UPDATE metadata_provider_operations SET lease_until=?,last_command_sequence=? WHERE id=? AND provider=? AND command_incarnation=? AND generation=? AND status='claimed' AND lease=? AND lease_until=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING last_command_sequence`,
			Params:  []stmt.Param{t("lease_until"), i("sequence"), t("operation"), t("provider"), b("incarnation"), i("generation"), t("lease"), t("old_lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "renewed", Bindings: []any{"2026-01-01T00:10:00Z", int64(2), "operation-1", "tmdb", make([]byte, 16), int64(1), "lease-1", "2026-01-01T00:05:00Z", int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operations"}},
		},
		{
			ID: ipEntityJobFinishJobStmt, SQL: `UPDATE metadata_acquisition_jobs SET status=?,revision=revision+1,lease='',lease_until_ms=?,attempts=attempts+1,next_attempt_ms=?,error=? WHERE entity_id=? AND provider=? AND purpose=? AND generation=? AND revision=? AND status='claimed' AND lease=? AND lease_until_ms=? RETURNING revision`,
			Params:  []stmt.Param{t("status"), i("now"), i("next_attempt"), t("reason"), i("entity"), t("provider"), t("purpose"), i("generation"), i("revision"), t("lease"), i("old_lease_until")},
			Cohorts: []stmt.Cohort{{Name: "finished", Bindings: []any{"done", int64(600_000), int64(600_000), "", int64(1), "tmdb", "metadata", int64(2), int64(3), "lease-1", int64(600_000)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_acquisition_jobs"}},
		},
		{
			ID: ipEntityJobFinishOwnerStmt, SQL: `UPDATE metadata_provider_operations SET status=?,lease='',lease_until=?,finished_at=?,reason=?,last_command_sequence=? WHERE id=? AND provider=? AND command_incarnation=? AND generation=? AND status='claimed' AND lease=? AND lease_until=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING last_command_sequence`,
			Params:  []stmt.Param{t("status"), t("lease_until"), t("finished_at"), t("reason"), i("sequence"), t("operation"), t("provider"), b("incarnation"), i("generation"), t("lease"), t("old_lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "finished", Bindings: []any{"staged", "2026-01-01T00:10:00Z", "2026-01-01T00:10:00Z", "", int64(3), "operation-1", "tmdb", make([]byte, 16), int64(1), "lease-1", "2026-01-01T00:10:00Z", int64(2), int64(2)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operations"}},
		},
		{
			ID: ipEntityJobRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,entity_id,provider) VALUES(?,?,?,?,?,?,?,?,?,?,0,-1,'',1,0,?,?,?)`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), t("kind"), i("accepted_revision"), i("applied_ms"), b("result_digest"), t("phase"), i("terminal"), i("entity"), t("provider")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"operation-1", make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), "claim", int64(2), int64(1), make([]byte, 32), "claimed", int64(0), int64(1), "tmdb"}}},
			Plan:    stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipEntityJobInsertOwnerStmt, ipEntityJobInsertSubjStmt, ipEntityJobClaimStmt, ipEntityJobRenewJobStmt,
			ipEntityJobRenewOwnerStmt, ipEntityJobFinishJobStmt, ipEntityJobFinishOwnerStmt, ipEntityJobRecordStmt:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP entity acquisition job"
		defs[n].CallSites = []string{"store/writer/ip_entity_job.go"}
	}
	return defs
}
