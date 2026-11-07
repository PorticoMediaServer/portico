package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usSecurityOwner              stmt.ID = "us.security.owner"
	usPolicyReceiptOctets        stmt.ID = "us.policy.receipt.octets"
	usPolicyReceiptHeader        stmt.ID = "us.policy.receipt.header"
	usPolicyReceiptLibraries     stmt.ID = "us.policy.receipt.libraries"
	usPolicyReceiptLibraryOctets stmt.ID = "us.policy.receipt.library_octets"
	usPolicyReceiptInsert        stmt.ID = "us.policy.receipt.insert"
	usPolicyReceiptLibraryInsert stmt.ID = "us.policy.receipt.library_insert"
	usPolicyOwnerAdvance         stmt.ID = "us.policy.receipt.advance"
	usPolicyOwnerAck             stmt.ID = "us.policy.receipt.ack"
)

// US-001 DRAFT table names; INT owns adoption. None of these definitions opens,
// commits, authenticates, registers an accepting command or mints a governor grant.
const usPolicyReceiptBinding stmt.ID = "us.policy.receipt.binding"

func usPolicyReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	bind := []any{"security-owner", inc, int64(1)}
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "security_command_owners", Name: "sqlite_autoindex_security_command_owners_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	libraryPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_result_libraries"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort=?"}}
	defs := []stmt.Definition{
		{ID: usPolicyReceiptBinding, SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: key, Cohorts: []stmt.Cohort{{Name: "original_binding", Bindings: bind}}, Plan: headerPlan},
		{ID: usSecurityOwner, SQL: `SELECT command_incarnation,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params: []stmt.Param{t("owner"), t("server"), t("account"), t("member"), t("profile"), t("lifetime"), t("installation")}, Cohorts: []stmt.Cohort{{Name: "original_scope", Bindings: []any{"security-owner", "server", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"}}}, Plan: ownerPlan},
		{ID: usPolicyReceiptOctets, SQL: `SELECT variable_bytes FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usPolicyReceiptHeader, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,policy_inherits,result_changed,result_count,variable_bytes
FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: key, Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usPolicyReceiptLibraryOctets, SQL: `SELECT ordinal,variable_bytes FROM security_result_libraries WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort='policy' ORDER BY ordinal LIMIT ?`, Params: append(append([]stmt.Param(nil), key...), i("limit")), Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(1001))}}, Plan: libraryPlan},
		{ID: usPolicyReceiptLibraries, SQL: `SELECT ordinal,library_id FROM security_result_libraries WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort='policy' ORDER BY ordinal LIMIT ?`, Params: append(append([]stmt.Param(nil), key...), i("limit")), Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(1001))}}, Plan: libraryPlan},
		{ID: usPolicyReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,policy_inherits,result_changed,result_count)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied_ms"), b("result"), t("account"), t("member"), nullableText("profile"), nullableText("lifetime"), i("revision"), i("inherits"), i("changed"), i("count")},
			Cohorts: []stmt.Cohort{{Name: "member", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(540), int64(1), "member_libraries_v1", int64(1000), digest, "target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", nil, nil, int64(2), int64(0), int64(1), int64(2)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usPolicyReceiptLibraryInsert, Kind: stmt.Write, SQL: `INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES(?,?,?,?,?,?) RETURNING variable_bytes`, Params: append(append([]stmt.Param(nil), key...), t("cohort"), i("ordinal"), t("library")), Cohorts: []stmt.Cohort{{Name: "one_saved_library", Bindings: append(append([]any(nil), bind...), "policy", int64(0), "movies")}}, Plan: stmt.Plan{Hot: true}},
		{ID: usPolicyOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET last_sequence=?,retained_count=retained_count+1,retained_variable_bytes=retained_variable_bytes+?
WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND retained_count=? AND retained_variable_bytes=? AND retired_ms=0
RETURNING last_sequence,retained_count,retained_variable_bytes`, Params: []stmt.Param{i("next"), i("charged_bytes"), t("owner"), b("incarnation"), i("last"), i("ack"), i("count"), i("bytes")}, Cohorts: []stmt.Cohort{{Name: "advance", Bindings: []any{int64(1), int64(320), "security-owner", inc, int64(0), int64(0), int64(0), int64(0)}}}, Plan: ownerPlan},
		{ID: usPolicyOwnerAck, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET acknowledged_sequence=? WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? RETURNING acknowledged_sequence`, Params: []stmt.Param{i("next"), t("owner"), b("incarnation"), i("last"), i("ack")}, Cohorts: []stmt.Cohort{{Name: "ack", Bindings: []any{int64(1), "security-owner", inc, int64(1), int64(0)}}}, Plan: ownerPlan},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US security receipts"
		defs[n].CallSites = []string{"store/writer/us_policy_receipts.go"}
	}
	return defs
}
