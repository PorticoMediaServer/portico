package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Private receipt adapter statements for the profile-restrictions replacement.
// Group B tables are canonical; these closed statements only read/write the
// retained owner, the typed 542 header, its one typed restriction row and its
// ordered label cohort. None opens, commits, authenticates, registers an
// accepting command or mints a governor grant. The generated variable_bytes
// columns are the exact octet preflight: no octet_length function is needed.
const (
	usRestrictionsOwner             stmt.ID = "us.restrictions.owner"
	usRestrictionsReceiptBinding    stmt.ID = "us.restrictions.receipt.binding"
	usRestrictionsReceiptOperation  stmt.ID = "us.restrictions.receipt.operation"
	usRestrictionsReceiptHeader     stmt.ID = "us.restrictions.receipt.header"
	usRestrictionsReceiptOctets     stmt.ID = "us.restrictions.receipt.octets"
	usRestrictionsReceiptResult     stmt.ID = "us.restrictions.receipt.result"
	usRestrictionsReceiptResultSize stmt.ID = "us.restrictions.receipt.result_octets"
	usRestrictionsReceiptLabelSize  stmt.ID = "us.restrictions.receipt.label_octets"
	usRestrictionsReceiptLabels     stmt.ID = "us.restrictions.receipt.labels"
	usRestrictionsReceiptInsert     stmt.ID = "us.restrictions.receipt.insert"
	usRestrictionsResultInsert      stmt.ID = "us.restrictions.receipt.result_insert"
	usRestrictionsLabelInsert       stmt.ID = "us.restrictions.receipt.label_insert"
	usRestrictionsOwnerAdvance      stmt.ID = "us.restrictions.receipt.advance"
	usRestrictionsOwnerAck          stmt.ID = "us.restrictions.receipt.ack"
)

func usRestrictionsReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	bind := []any{"security-owner", inc, int64(1)}
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usRestrictionsPoint("security_command_owners", "sqlite_autoindex_security_command_owners_1", "pk", "id")}}
	operationPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_receipts_2"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND operation_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usRestrictionsPoint("security_command_receipts", "sqlite_autoindex_security_command_receipts_2", "u", "owner_id", "command_incarnation", "operation_id")}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	resultPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_restriction_results"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	labelPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_restriction_result_labels"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	defs := []stmt.Definition{
		{ID: usRestrictionsOwner, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  []stmt.Param{t("owner"), t("server"), t("account"), t("member"), t("profile"), t("lifetime"), t("installation")},
			Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}},
			Plan:    ownerPlan},
		{ID: usRestrictionsReceiptOperation, SQL: `SELECT command_sequence FROM security_command_receipts INDEXED BY sqlite_autoindex_security_command_receipts_2 WHERE owner_id=? AND command_incarnation=? AND operation_id=? LIMIT 2`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), b("operation")},
			Cohorts: []stmt.Cohort{{Name: "original_operation", Bindings: []any{"security-owner", inc, op}}},
			Plan:    operationPlan},
		{ID: usRestrictionsReceiptBinding, SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original_binding", Bindings: bind}}, Plan: headerPlan},
		{ID: usRestrictionsReceiptOctets, SQL: `SELECT variable_bytes FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usRestrictionsReceiptHeader, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,variable_bytes,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,result_key_id,result_nonce,result_body
FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usRestrictionsReceiptResult, SQL: `SELECT account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together
FROM security_restriction_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: resultPlan},
		{ID: usRestrictionsReceiptResultSize, SQL: `SELECT variable_bytes FROM security_restriction_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: resultPlan},
		{ID: usRestrictionsReceiptLabelSize, SQL: `SELECT ordinal,variable_bytes FROM security_restriction_result_labels WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT ?`,
			Params:  append(append([]stmt.Param(nil), key...), i("limit")),
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(65))}},
			Plan:    labelPlan},
		{ID: usRestrictionsReceiptLabels, SQL: `SELECT ordinal,label FROM security_restriction_result_labels WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT ?`,
			Params:  append(append([]stmt.Param(nil), key...), i("limit")),
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(65))}},
			Plan:    labelPlan},
		{ID: usRestrictionsReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count)
VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied_ms"), b("result"), i("count")},
			Cohorts: []stmt.Cohort{{Name: "one_header", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(542), int64(1), "profile_restrictions_v1", int64(1000), digest, int64(2)}}},
			Plan:    stmt.Plan{Hot: true}},
		{ID: usRestrictionsResultInsert, Kind: stmt.Write, SQL: `INSERT INTO security_restriction_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("account"), t("member"), t("profile"), t("lifetime"), i("revision"), t("rating_system"), t("maximum_age_rating"), i("maximum_age"), i("allow_unrated"), i("allow_downloads"), i("allow_live_tv"), i("allow_dvr"), i("allow_watch_together")},
			Cohorts: []stmt.Cohort{{Name: "one_result", Bindings: []any{"security-owner", inc, int64(1), "issuer", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "restricted-profile", "ffffffffffffffffffffffffffffffff", int64(2), "mpaa", "PG-13", int64(13), int64(1), int64(1), int64(1), int64(0), int64(1)}}},
			Plan:    stmt.Plan{Hot: true}},
		{ID: usRestrictionsLabelInsert, Kind: stmt.Write, SQL: `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,?,?) RETURNING variable_bytes`,
			Params:  append(append([]stmt.Param(nil), key...), i("ordinal"), t("label")),
			Cohorts: []stmt.Cohort{{Name: "one_label", Bindings: append(append([]any(nil), bind...), int64(0), "violence")}},
			Plan:    stmt.Plan{Hot: true}},
		{ID: usRestrictionsOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET last_sequence=?,retained_count=retained_count+1,retained_variable_bytes=retained_variable_bytes+?
WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND retained_count=? AND retained_variable_bytes=? AND retired_ms=0
RETURNING last_sequence,retained_count,retained_variable_bytes`,
			Params:  []stmt.Param{i("next"), i("charged_bytes"), t("owner"), b("incarnation"), i("last"), i("ack"), i("count"), i("bytes")},
			Cohorts: []stmt.Cohort{{Name: "advance", Bindings: []any{int64(1), int64(320), "security-owner", inc, int64(0), int64(0), int64(0), int64(0)}}},
			Plan:    ownerPlan},
		{ID: usRestrictionsOwnerAck, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET acknowledged_sequence=? WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? RETURNING acknowledged_sequence`,
			Params:  []stmt.Param{i("next"), t("owner"), b("incarnation"), i("last"), i("ack")},
			Cohorts: []stmt.Cohort{{Name: "ack", Bindings: []any{int64(1), "security-owner", inc, int64(1), int64(0)}}},
			Plan:    ownerPlan},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US security receipts"
		defs[n].CallSites = []string{"store/writer/us_restrictions_receipts.go", "store/writer/us_restrictions_receipt_statements.go"}
	}
	return defs
}
