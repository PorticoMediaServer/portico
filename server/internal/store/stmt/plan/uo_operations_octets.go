package plan

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// uoOperationsConflictsOctetSQL mirrors the read package byte for byte.
func uoOperationsConflictsOctetSQL() string {
	return `SELECT octet_length(c.id),octet_length(c.field),octet_length(c.base_value),c.item_id,c.base_revision,c.resolved
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_conflicts AS c INDEXED BY personal_conflicts_profile ON c.profile_id=f.profile_id
WHERE t.token_hash=? AND c.id>?
ORDER BY c.id ASC LIMIT ?`
}

func uoOperationsDevicesOctetSQL() string {
	return `SELECT octet_length(d.device_id),d.sequence
FROM personal_device_sequences AS d INDEXED BY sqlite_autoindex_personal_device_sequences_1
WHERE d.profile_id=? AND d.device_id>?
ORDER BY d.device_id ASC LIMIT ?`
}

const uoOperationsJobsOctetColumns = `octet_length(j.id),octet_length(j.state),octet_length(j.error_code),j.total,j.done,j.failed,j.cursor,j.revision,j.created_ms,j.updated_ms,j.job_due_ms,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,j.job_next_attempt_ms`

func uoOperationsJobsOctetSQL() string {
	return `SELECT ` + uoOperationsJobsOctetColumns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_jobs AS j INDEXED BY sqlite_autoindex_personal_jobs_1 ON j.id=?
WHERE t.token_hash=? AND j.profile_id=f.profile_id`
}

func uoOperationsJobsDueOctetSQL() string {
	return `SELECT ` + uoOperationsJobsOctetColumns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_jobs AS j INDEXED BY personal_jobs_governor_due ON j.profile_id=f.profile_id
WHERE t.token_hash=? AND job_due_ms IS NOT NULL AND j.job_due_ms<=?
ORDER BY j.job_due_ms LIMIT ?`
}

func uoOperationsPendingOctetSQL() string {
	return `SELECT octet_length(s.field),s.revision
FROM server_configuration_pending AS s INDEXED BY sqlite_autoindex_server_configuration_pending_1
WHERE s.field>?
ORDER BY s.field ASC LIMIT ?`
}

func uoOperationsDeliveryOctetSQL(variant string) string {
	seek := ""
	switch variant {
	case "same":
		seek = ` AND d.created_ms=? AND d.id<?`
	case "older":
		seek = ` AND d.created_ms<?`
	}
	return `SELECT octet_length(d.id),octet_length(d.type),octet_length(d.state),octet_length(d.error),d.job_attempts,d.created_ms,d.finished_ms,COALESCE(d.job_next_attempt_ms,0),d.status,d.duration_ms,d.job_revision
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN webhook_deliveries AS d INDEXED BY webhook_deliveries_log ON d.webhook_id=?
WHERE t.token_hash=?` + seek + `
ORDER BY d.created_ms DESC,d.id DESC LIMIT ?`
}

func init() {
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	token := uoOctetAuto("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := uoOctetAuto("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	devices := uoOctetAuto("personal_device_sequences", "sqlite_autoindex_personal_device_sequences_1", "profile_id", "device_id")
	jobs := uoOctetAuto("personal_jobs", "sqlite_autoindex_personal_jobs_1", "id")
	pending := uoOctetAuto("server_configuration_pending", "sqlite_autoindex_server_configuration_pending_1", "field")
	tokenEQP := "t USING INDEX sqlite_autoindex_authorization_family_tokens_1 (token_hash=?)"
	familyEQP := "f USING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)"
	afterParams := []stmt.Param{text("token"), text("after"), integer("limit")}
	addUOReadOctet(uoReadOctet{
		id: stmt.ID("read.uo.operations.conflicts.sizes"), sql: uoOperationsConflictsOctetSQL(), label: "UO conflicts bytes", owner: "store/read UO operations",
		params: afterParams,
		plan:   stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_conflicts_profile"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "profile_id=?"}},
		table:  "personal_conflicts", index: "personal_conflicts_profile",
		keys:    []uoIndexKey{{"profile_id", "BINARY", 0}, {"id", "BINARY", 0}},
		ddl:     "create index personal_conflicts_profile on personal_conflicts(profile_id,id)",
		eqp:     []string{tokenEQP, familyEQP, "c USING INDEX personal_conflicts_profile ("},
		eqpRows: 3,
	})
	addUOReadOctet(uoReadOctet{
		id: stmt.ID("read.uo.operations.devices.sizes"), sql: uoOperationsDevicesOctetSQL(), label: "UO devices bytes", owner: "store/read UO operations",
		params: []stmt.Param{text("profile"), text("after"), integer("limit")},
		plan:   stmt.Plan{Hot: true, Indexes: []string{devices.Name}, AutoIndexes: []stmt.AutoIndexBinding{devices}, Seeks: []string{"profile_id=?", "device_id>?"}},
		table:  "personal_device_sequences", index: devices.Name, autoKey: []string{"profile_id", "device_id"},
		eqp:     []string{"d USING INDEX sqlite_autoindex_personal_device_sequences_1 ("},
		eqpRows: 1,
	})
	addUOReadOctet(uoReadOctet{
		id: stmt.ID("read.uo.operations.jobs.sizes"), sql: uoOperationsJobsOctetSQL(), label: "UO jobs bytes", owner: "store/read UO operations",
		params: []stmt.Param{text("job"), text("token")},
		plan:   stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, jobs.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, jobs}, Seeks: []string{"token_hash=?", "id=?"}},
		table:  "personal_jobs", index: jobs.Name, autoKey: []string{"id"},
		eqp:     []string{tokenEQP, familyEQP, "j USING INDEX sqlite_autoindex_personal_jobs_1 (id=?)"},
		eqpRows: 3,
	})
	addUOReadOctet(uoReadOctet{
		id: stmt.ID("read.uo.operations.jobs-due.sizes"), sql: uoOperationsJobsDueOctetSQL(), label: "UO due jobs bytes", owner: "store/read UO operations",
		params: []stmt.Param{text("token"), integer("now"), integer("limit")},
		plan:   stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "personal_jobs_governor_due"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?"}},
		table:  "personal_jobs", index: "personal_jobs_governor_due", partial: true,
		keys:    []uoIndexKey{{"job_due_ms", "BINARY", 0}},
		ddl:     "create index personal_jobs_governor_due on personal_jobs(job_due_ms) where job_due_ms is not null",
		eqp:     []string{tokenEQP, familyEQP, "j USING INDEX personal_jobs_governor_due ("},
		eqpRows: 3,
	})
	addUOReadOctet(uoReadOctet{
		id: stmt.ID("read.uo.operations.settings.pending.sizes"), sql: uoOperationsPendingOctetSQL(), label: "UO settings pending bytes", owner: "store/read UO operations",
		params: []stmt.Param{text("after"), integer("limit")},
		plan:   stmt.Plan{Hot: true, Indexes: []string{pending.Name}, AutoIndexes: []stmt.AutoIndexBinding{pending}, Seeks: []string{"field>?"}},
		table:  "server_configuration_pending", index: pending.Name, autoKey: []string{"field"},
		eqp:     []string{"s USING INDEX sqlite_autoindex_server_configuration_pending_1 ("},
		eqpRows: 1,
	})
	for _, variant := range []string{"first", "same", "older"} {
		params := []stmt.Param{text("webhook"), text("token"), integer("limit")}
		eqp := []string{tokenEQP, "f USING COVERING INDEX sqlite_autoindex_authorization_session_families_1 (id=?)", "d USING INDEX webhook_deliveries_log ("}
		switch variant {
		case "same":
			params = []stmt.Param{text("webhook"), text("token"), integer("created"), text("id"), integer("limit")}
			eqp = append(eqp, "created_ms=? AND id<?")
		case "older":
			params = []stmt.Param{text("webhook"), text("token"), integer("created"), integer("limit")}
			eqp = append(eqp, "created_ms<?")
		}
		addUOReadOctet(uoReadOctet{
			id: stmt.ID("read.uo.operations.delivery-log." + variant + ".sizes"), sql: uoOperationsDeliveryOctetSQL(variant), label: "UO delivery log bytes", owner: "store/read UO operations",
			params: params,
			plan:   stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, "webhook_deliveries_log"}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "webhook_id=?"}},
			table:  "webhook_deliveries", index: "webhook_deliveries_log",
			keys: []uoIndexKey{{"webhook_id", "BINARY", 0}, {"created_ms", "BINARY", 1}, {"id", "BINARY", 1}},
			ddl:  "create index webhook_deliveries_log on webhook_deliveries(webhook_id,created_ms desc,id desc)",
			eqp:  eqp, eqpRows: 3,
		})
	}
}
