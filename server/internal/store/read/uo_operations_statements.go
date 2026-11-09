package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// uoOperationsAuto pins one exact PK autoindex. It never accepts a name from a
// request.
func uoOperationsAuto(table, name string, columns ...string) stmt.AutoIndexBinding {
	out := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
	for _, column := range columns {
		out.Columns = append(out.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return out
}

const uoOperationsTokenJoin = `FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id`

// uoOperationsConflictsSQL and its device sibling are fixed single-key keysets:
// the first page binds an empty after key and every later page binds the exact
// last delivered key. Both order by the forced index key only.
func uoOperationsConflictsSQL(sizes bool) string {
	columns := `c.id,c.field,c.base_value,c.item_id,c.base_revision,c.resolved`
	if sizes {
		columns = `octet_length(c.id),octet_length(c.field),octet_length(c.base_value),c.item_id,c.base_revision,c.resolved`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN personal_conflicts AS c INDEXED BY personal_conflicts_profile ON c.profile_id=f.profile_id
WHERE t.token_hash=? AND c.id>?
ORDER BY c.id ASC LIMIT ?`
}

func uoOperationsDevicesSQL(sizes bool) string {
	columns := `d.device_id,d.sequence`
	if sizes {
		columns = `octet_length(d.device_id),d.sequence`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN personal_device_sequences AS d INDEXED BY sqlite_autoindex_personal_device_sequences_1 ON d.profile_id=f.profile_id
WHERE t.token_hash=? AND d.device_id>?
ORDER BY d.device_id ASC LIMIT ?`
}

const uoOperationsJobsColumns = `j.id,j.state,j.error_code,j.total,j.done,j.failed,j.cursor,j.revision,j.created_ms,j.updated_ms,j.job_due_ms,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,j.job_next_attempt_ms`

func uoOperationsJobsSQL(sizes bool) string {
	columns := uoOperationsJobsColumns
	if sizes {
		columns = `octet_length(j.id),octet_length(j.state),octet_length(j.error_code),j.total,j.done,j.failed,j.cursor,j.revision,j.created_ms,j.updated_ms,j.job_due_ms,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,j.job_next_attempt_ms`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN personal_jobs AS j INDEXED BY sqlite_autoindex_personal_jobs_1 ON j.id=?
WHERE t.token_hash=? AND j.profile_id=f.profile_id`
}

func uoOperationsJobsDueSQL(sizes bool) string {
	columns := uoOperationsJobsColumns
	if sizes {
		columns = `octet_length(j.id),octet_length(j.state),octet_length(j.error_code),j.total,j.done,j.failed,j.cursor,j.revision,j.created_ms,j.updated_ms,j.job_due_ms,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,j.job_next_attempt_ms`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN personal_jobs AS j INDEXED BY personal_jobs_governor_due ON j.profile_id=f.profile_id
WHERE t.token_hash=? AND job_due_ms IS NOT NULL AND j.job_due_ms<=?
ORDER BY j.job_due_ms LIMIT ?`
}

const uoOperationsSettingsHeadSQL = `SELECT r.revision,s.updated_ms,s.configuration_pending_revision
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN console_settings_revision AS r ON r.singleton=1
CROSS JOIN server_settings AS s ON s.singleton=1
WHERE t.token_hash=?`

func uoOperationsSettingsPendingSQL(sizes bool) string {
	columns := `s.field,s.revision`
	if sizes {
		columns = `octet_length(s.field),s.revision`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN server_configuration_pending AS s INDEXED BY sqlite_autoindex_server_configuration_pending_1
WHERE t.token_hash=? AND s.field>?
ORDER BY s.field ASC LIMIT ?`
}

const uoOperationsDeliveryGateSQL = `SELECT w.revision,w.authority_generation
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN webhooks AS w INDEXED BY sqlite_autoindex_webhooks_1 ON w.id=?
WHERE t.token_hash=?`

func uoOperationsDeliverySQL(variant string, sizes bool) string {
	columns := `d.id,d.type,d.state,d.error,d.job_attempts,d.created_ms,d.finished_ms,COALESCE(d.job_next_attempt_ms,0),d.status,d.duration_ms,d.job_revision`
	if sizes {
		columns = `octet_length(d.id),octet_length(d.type),octet_length(d.state),octet_length(d.error),d.job_attempts,d.created_ms,d.finished_ms,COALESCE(d.job_next_attempt_ms,0),d.status,d.duration_ms,d.job_revision`
	}
	seek := ""
	switch variant {
	case "same":
		seek = ` AND d.created_ms=? AND d.id<?`
	case "older":
		seek = ` AND d.created_ms<?`
	}
	return `SELECT ` + columns + `
` + uoOperationsTokenJoin + `
CROSS JOIN webhook_deliveries AS d INDEXED BY webhook_deliveries_log ON d.webhook_id=?
WHERE t.token_hash=?` + seek + `
ORDER BY d.created_ms DESC,d.id DESC LIMIT ?`
}

// UOOperationsDefinitions returns the inert UO operational definitions for
// explicit harness composition alongside HarnessDefinitions. Capture them on the
// actual reader before registration. The product does not select this layer
// before cutover.
func UOOperationsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	token := uoOperationsAuto("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := uoOperationsAuto("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	devices := uoOperationsAuto("personal_device_sequences", "sqlite_autoindex_personal_device_sequences_1", "profile_id", "device_id")
	jobs := uoOperationsAuto("personal_jobs", "sqlite_autoindex_personal_jobs_1", "id")
	pending := uoOperationsAuto("server_configuration_pending", "sqlite_autoindex_server_configuration_pending_1", "field")
	webhooks := uoOperationsAuto("webhooks", "sqlite_autoindex_webhooks_1", "id")
	owner := "store/read UO operations"
	site := []string{"store/read/uo_operations.go:ForegroundReader.operationsWindow"}
	var definitions []stmt.Definition
	add := func(id string, sql string, params []stmt.Param, bindings []any, plan stmt.Plan) {
		empty := append([]any(nil), bindings...)
		for n, v := range empty {
			if _, ok := v.(string); ok {
				empty[n] = "missing"
			}
		}
		definitions = append(definitions, stmt.Definition{
			ID: stmt.ID(id), SQL: sql, Owner: owner, CallSites: site, Kind: stmt.Read, Params: params,
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}},
			Plan:    plan,
		})
	}
	afterParams := []stmt.Param{s("token"), s("after"), i("limit")}
	afterBindings := []any{"token", "", int64(MaxRaw + 1)}
	add("read.uo.operations.conflicts.sizes", uoOperationsConflictsSQL(true), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_conflicts_profile"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "profile_id=?"}})
	add("read.uo.operations.conflicts", uoOperationsConflictsSQL(false), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_conflicts_profile"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "profile_id=?"}})
	add("read.uo.operations.devices.sizes", uoOperationsDevicesSQL(true), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, devices.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, devices}, Seeks: []string{"token_hash=?", "profile_id=?"}})
	add("read.uo.operations.devices", uoOperationsDevicesSQL(false), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, devices.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, devices}, Seeks: []string{"token_hash=?", "profile_id=?"}})
	jobsPointParams := []stmt.Param{s("job"), s("token")}
	jobsPointBindings := []any{"job", "token"}
	add("read.uo.operations.jobs.sizes", uoOperationsJobsSQL(true), jobsPointParams, jobsPointBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, jobs.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, jobs}, Seeks: []string{"token_hash=?", "id=?"}})
	add("read.uo.operations.jobs", uoOperationsJobsSQL(false), jobsPointParams, jobsPointBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, jobs.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, jobs}, Seeks: []string{"token_hash=?", "id=?"}})
	dueParams := []stmt.Param{s("token"), i("now"), i("limit")}
	dueBindings := []any{"token", int64(1), int64(2)}
	add("read.uo.operations.jobs-due.sizes", uoOperationsJobsDueSQL(true), dueParams, dueBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_jobs_governor_due"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?"}})
	add("read.uo.operations.jobs-due", uoOperationsJobsDueSQL(false), dueParams, dueBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_jobs_governor_due"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?"}})
	add("read.uo.operations.settings.head", uoOperationsSettingsHeadSQL, []stmt.Param{s("token")}, []any{"token"},
		stmt.Plan{Hot: true, PrimaryKeys: []string{"r", "s"}, Indexes: []string{token.Name, family.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?"}})
	add("read.uo.operations.settings.pending.sizes", uoOperationsSettingsPendingSQL(true), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, pending.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, pending}, Seeks: []string{"token_hash=?"}})
	add("read.uo.operations.settings.pending", uoOperationsSettingsPendingSQL(false), afterParams, afterBindings,
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, pending.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, pending}, Seeks: []string{"token_hash=?"}})
	add("read.uo.operations.delivery-log.gate", uoOperationsDeliveryGateSQL, []stmt.Param{s("webhook"), s("token")}, []any{"webhook", "token"},
		stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, webhooks.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, webhooks}, Seeks: []string{"token_hash=?", "id=?"}})
	for _, variant := range []string{"first", "same", "older"} {
		params := []stmt.Param{s("webhook"), s("token"), i("limit")}
		bindings := []any{"webhook", "token", int64(MaxRaw + 1)}
		if variant == "same" {
			params = []stmt.Param{s("webhook"), s("token"), i("created"), s("id"), i("limit")}
			bindings = []any{"webhook", "token", int64(1), "delivery", int64(MaxRaw + 1)}
		}
		if variant == "older" {
			params = []stmt.Param{s("webhook"), s("token"), i("created"), i("limit")}
			bindings = []any{"webhook", "token", int64(1), int64(MaxRaw + 1)}
		}
		plan := stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "webhook_deliveries_log"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "webhook_id=?"}}
		add("read.uo.operations.delivery-log."+variant+".sizes", uoOperationsDeliverySQL(variant, true), params, bindings, plan)
		add("read.uo.operations.delivery-log."+variant, uoOperationsDeliverySQL(variant, false), params, bindings, plan)
	}
	return definitions
}
