package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipPolicyOwner         stmt.ID = "ip.policy.owner"
	ipPolicyReceipt       stmt.ID = "ip.policy.receipt"
	ipPolicyLibrary       stmt.ID = "ip.policy.library"
	ipPolicyProvider      stmt.ID = "ip.policy.provider"
	ipPolicyScreenConsent stmt.ID = "ip.policy.screen_consent"
	ipPolicyMusicConsent  stmt.ID = "ip.policy.music_consent"
	ipPolicyInsert        stmt.ID = "ip.policy.insert"
	ipPolicyUpdate        stmt.ID = "ip.policy.update"
	ipPolicyRecord        stmt.ID = "ip.policy.record"

	ipRefreshOwner   stmt.ID = "ip.refresh.owner"
	ipRefreshReceipt stmt.ID = "ip.refresh.receipt"
	ipRefreshInsert  stmt.ID = "ip.refresh.insert"
	ipRefreshUpdate  stmt.ID = "ip.refresh.update"
	ipRefreshRecord  stmt.ID = "ip.refresh.record"
)

// The finite IP policy/sweep family. Every mutable policy/provider/runtime value
// is compared in SQLite against an owned request witness; no caller bool, role
// name, JSON permission document or unbounded filter scan is admitted. The
// policy and sweep owner rows carry the receipt identity/floor; their typed
// receipts retain the immutable accepted result across later owner revisions.
func ipPolicyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{
			ID: ipPolicyOwner, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider FROM metadata_provider_policy WHERE library_id=? AND provider=?`,
			Params: []stmt.Param{i("library"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_policy"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(777), "tmdb"}},
				{Name: "missing", Bindings: []any{int64(777), "tvdb"}},
			},
		},
		{
			ID: ipPolicyReceipt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,policy_incarnation,accepted_revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider FROM metadata_policy_receipts WHERE library_id=? AND provider=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("library"), t("provider"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_policy_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(777), "tmdb", make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(777), "tmdb", make([]byte, 16), int64(2)}},
			},
		},
		{
			ID: ipPolicyLibrary, SQL: `SELECT CASE WHEN library_id=? THEN 1 ELSE 0 END FROM catalog_libraries WHERE id=? AND retired=0`,
			Params: []stmt.Param{t("library_key"), i("library")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"ip-library", int64(777)}},
				{Name: "missing", Bindings: []any{"ip-library", int64(778)}},
			},
		},
		{
			ID: ipPolicyProvider, SQL: `SELECT 1 FROM metadata_providers WHERE code=? AND enabled=1`,
			Params: []stmt.Param{t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_providers"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb"}},
				{Name: "missing", Bindings: []any{"tvdb"}},
			},
		},
		{
			ID: ipPolicyScreenConsent, SQL: `SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND confirmed=1 AND revision=?`,
			Params: []stmt.Param{i("revision")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"screen_metadata_consent"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1)}},
				{Name: "missing", Bindings: []any{int64(2)}},
			},
		},
		{
			ID: ipPolicyMusicConsent, SQL: `SELECT 1 FROM metadata_runtime_settings WHERE singleton=1 AND online_lookups=1 AND lookup_epoch=?`,
			Params: []stmt.Param{i("epoch")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_runtime_settings"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(3)}},
				{Name: "missing", Bindings: []any{int64(4)}},
			},
		},
		{
			ID: ipPolicyInsert, SQL: `INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,?,1,?,?,?,?,?,?,?,?,?,1,0) RETURNING revision`,
			Params:  []stmt.Param{i("library"), t("provider"), b("incarnation"), i("enabled"), i("priority"), t("region"), t("algorithm"), t("refresh_mode"), i("credit_enabled"), ni("credit_after_item"), nt("credit_after_provider"), b("command_incarnation")},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{int64(777), "tmdb", make([]byte, 16), int64(1), int64(5), "US", "v1", "fill_missing", int64(1), nil, nil, make([]byte, 16)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: ipPolicyUpdate, SQL: `UPDATE metadata_provider_policy SET revision=revision+1,enabled=?,priority=?,region=?,algorithm=?,refresh_mode=?,credit_enabled=?,credit_after_item=?,credit_after_provider=?,last_command_sequence=? WHERE library_id=? AND provider=? AND command_incarnation=? AND incarnation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params:  []stmt.Param{i("enabled"), i("priority"), t("region"), t("algorithm"), t("refresh_mode"), i("credit_enabled"), ni("credit_after_item"), nt("credit_after_provider"), i("new_sequence"), i("library"), t("provider"), b("command_incarnation"), b("incarnation"), i("revision"), i("last_sequence"), i("acknowledged_sequence")},
			Cohorts: []stmt.Cohort{{Name: "cas", Bindings: []any{int64(1), int64(5), "US", "v1", "fill_missing", int64(1), nil, nil, int64(1), int64(777), "tmdb", make([]byte, 16), make([]byte, 16), int64(1), int64(0), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_policy"}},
		},
		{
			ID: ipPolicyRecord, SQL: `INSERT INTO metadata_policy_receipts(library_id,provider,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,policy_incarnation,accepted_revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params:  []stmt.Param{i("library"), t("provider"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), b("policy_incarnation"), i("accepted_revision"), i("enabled"), i("priority"), t("region"), t("algorithm"), t("refresh_mode"), i("credit_enabled"), ni("credit_after_item"), nt("credit_after_provider")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(777), "tmdb", make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), make([]byte, 16), int64(2), int64(1), int64(5), "US", "v1", "fill_missing", int64(1), nil, nil}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: ipRefreshOwner, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,generation,revision,epoch_ms,phase,after_id,page_from_id,page_until_id,page_waiting,job_lease_owner FROM metadata_refresh_sweeps WHERE provider=? AND purpose=?`,
			Params: []stmt.Param{t("provider"), t("purpose")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", "metadata"}},
				{Name: "missing", Bindings: []any{"tvdb", "metadata"}},
			},
		},
		{
			ID: ipRefreshReceipt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting FROM metadata_refresh_receipts WHERE provider=? AND purpose=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("provider"), t("purpose"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"tmdb", "metadata", make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{"tmdb", "metadata", make([]byte, 16), int64(2)}},
			},
		},
		{
			ID: ipRefreshInsert, SQL: `INSERT INTO metadata_refresh_sweeps(provider,purpose,generation,revision,epoch_ms,phase,after_id,page_from_id,page_until_id,page_waiting,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_next_attempt_ms,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES(?,?,1,1,?,'entities',0,0,0,0,0,0,0,NULL,?,1,0) RETURNING revision,generation,epoch_ms`,
			Params:  []stmt.Param{t("provider"), t("purpose"), i("epoch_ms"), b("command_incarnation")},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"tmdb", "metadata", int64(1), make([]byte, 16)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: ipRefreshUpdate, SQL: `UPDATE metadata_refresh_sweeps SET revision=revision+1,epoch_ms=?,phase='entities',after_id=0,page_from_id=0,page_until_id=0,page_waiting=0,job_lease_owner=0,job_lease_generation=0,job_lease_expires_ms=0,job_next_attempt_ms=NULL,last_command_sequence=? WHERE provider=? AND purpose=? AND command_incarnation=? AND generation=? AND revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=0 RETURNING revision,generation,epoch_ms`,
			Params:  []stmt.Param{i("epoch_ms"), i("new_sequence"), t("provider"), t("purpose"), b("command_incarnation"), i("generation"), i("revision"), i("last_sequence"), i("acknowledged_sequence")},
			Cohorts: []stmt.Cohort{{Name: "cas", Bindings: []any{int64(1), int64(1), "tmdb", "metadata", make([]byte, 16), int64(1), int64(1), int64(0), int64(0)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_refresh_sweeps"}},
		},
		{
			ID: ipRefreshRecord, SQL: `INSERT INTO metadata_refresh_receipts(provider,purpose,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,generation,epoch_ms,phase,after_id,scheduled_count,page_waiting) VALUES(?,?,?,?,?,?,?,?,'request',?,?,?,?,0,0,0)`,
			Params:  []stmt.Param{t("provider"), t("purpose"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("accepted_revision"), i("generation"), i("epoch_ms"), t("phase")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"tmdb", "metadata", make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(1), int64(1), int64(1), "entities"}}},
			Plan:    stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		if defs[n].ID == ipPolicyInsert || defs[n].ID == ipPolicyUpdate || defs[n].ID == ipPolicyRecord ||
			defs[n].ID == ipRefreshInsert || defs[n].ID == ipRefreshUpdate || defs[n].ID == ipRefreshRecord {
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP provider policy"
		defs[n].CallSites = []string{"store/writer/ip_policy.go"}
	}
	return append(usAuthorityDefinitions(), defs...)
}
