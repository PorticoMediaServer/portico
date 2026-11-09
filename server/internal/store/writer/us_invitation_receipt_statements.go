package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usInvitationOwner              stmt.ID = "us.invitation.owner"
	usInvitationReceiptOperation   stmt.ID = "us.invitation.receipt.operation"
	usInvitationReceiptBinding     stmt.ID = "us.invitation.receipt.binding"
	usInvitationReceiptHeader      stmt.ID = "us.invitation.receipt.header"
	usInvitationReceiptOctets      stmt.ID = "us.invitation.receipt.octets"
	usInvitationReceiptResult      stmt.ID = "us.invitation.receipt.result"
	usInvitationReceiptLibOctets   stmt.ID = "us.invitation.receipt.library_octets"
	usInvitationReceiptLibraries   stmt.ID = "us.invitation.receipt.libraries"
	usInvitationReceiptInsert      stmt.ID = "us.invitation.receipt.insert"
	usInvitationReceiptResultWrite stmt.ID = "us.invitation.receipt.result_insert"
	usInvitationReceiptLibInsert   stmt.ID = "us.invitation.receipt.library_insert"
	usInvitationOwnerAdvance       stmt.ID = "us.invitation.receipt.advance"
	usInvitationOwnerAck           stmt.ID = "us.invitation.receipt.ack"
)

// usInvitationReceiptDefinitions is the closed retained-result set for 550-552:
// the W receipt header, the typed security_invitation_results child, its ordered
// invitation library cohort and the owner count/byte debt. None of these opens,
// commits, authenticates, registers a command or mints authority.
func usInvitationReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	nullableBlob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	bind := []any{"security-owner", inc, int64(1)}
	member, lifetime, installation := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	// The owner row is addressed by the CURRENT issuer's six stable columns.
	// original_family_id is selected as provenance and never compared, exactly
	// like readUSPolicyOwner: a renewed family still resolves its original.
	ownerScope := []any{"server", "us-owner", member, "issuer-profile", lifetime, installation}
	ownerScopeParams := []stmt.Param{t("server"), t("account"), t("member"), t("profile"), t("lifetime"), t("installation")}
	ownerPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_owners_1"}, Seeks: []string{"id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "security_command_owners", Name: "sqlite_autoindex_security_command_owners_1", Origin: "pk",
			Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	operationPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_security_command_receipts_2"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND operation_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "security_command_receipts", Name: "sqlite_autoindex_security_command_receipts_2", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "owner_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}, {Name: "operation_id", Collation: "BINARY"}}}}}
	headerPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	resultPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_invitation_results"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	libraryPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_result_libraries"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort=?"}}
	defs := []stmt.Definition{
		{ID: usInvitationOwner, SQL: `SELECT command_incarnation,original_family_id,last_sequence,acknowledged_sequence,reclaimed_sequence,retained_count,retained_variable_bytes,retired_ms
FROM security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1
WHERE id=? AND ingress='authenticated' AND server_id=? AND issuer_account_id=? AND issuer_member_incarnation=? AND issuer_profile_id=? AND issuer_profile_lifetime=? AND issuer_installation_incarnation=?`,
			Params:  append([]stmt.Param{t("owner")}, ownerScopeParams...),
			Cohorts: []stmt.Cohort{{Name: "original_issuer", Bindings: append([]any{"security-owner"}, ownerScope...)}}, Plan: ownerPlan},
		{ID: usInvitationReceiptOperation, SQL: `SELECT command_sequence FROM security_command_receipts INDEXED BY sqlite_autoindex_security_command_receipts_2 WHERE owner_id=? AND command_incarnation=? AND operation_id=? LIMIT 2`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), b("operation")},
			Cohorts: []stmt.Cohort{{Name: "original_operation", Bindings: []any{"security-owner", inc, op}}}, Plan: operationPlan},
		{ID: usInvitationReceiptBinding, SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  key,
			Cohorts: []stmt.Cohort{{Name: "original_binding", Bindings: bind}}, Plan: headerPlan},
		{ID: usInvitationReceiptHeader, SQL: `SELECT operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body,variable_bytes
FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  key,
			Cohorts: []stmt.Cohort{{Name: "original", Bindings: bind}}, Plan: headerPlan},
		{ID: usInvitationReceiptOctets, SQL: `SELECT octet_length(invitation_id),octet_length(server_id),coalesce(octet_length(notes),0),octet_length(role),octet_length(state),coalesce(octet_length(account_id),0),octet_length(bound_username),octet_length(max_streams_mode),octet_length(max_content_rating),octet_length(source_code_hash),variable_bytes
FROM security_invitation_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  key,
			Cohorts: []stmt.Cohort{{Name: "original_result", Bindings: bind}}, Plan: resultPlan},
		{ID: usInvitationReceiptResult, SQL: `SELECT invitation_id,server_id,notes,role,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash,variable_bytes
FROM security_invitation_results WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params:  key,
			Cohorts: []stmt.Cohort{{Name: "original_result", Bindings: bind}}, Plan: resultPlan},
		{ID: usInvitationReceiptLibOctets, SQL: `SELECT ordinal,variable_bytes FROM security_result_libraries WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort='invitation' ORDER BY ordinal LIMIT ?`,
			Params:  append(append([]stmt.Param(nil), key...), i("limit")),
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(257))}}, Plan: libraryPlan},
		{ID: usInvitationReceiptLibraries, SQL: `SELECT ordinal,library_id FROM security_result_libraries WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort='invitation' ORDER BY ordinal LIMIT ?`,
			Params:  append(append([]stmt.Param(nil), key...), i("limit")),
			Cohorts: []stmt.Cohort{{Name: "complete", Bindings: append(append([]any(nil), bind...), int64(257))}}, Plan: libraryPlan},
		{ID: usInvitationReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied_ms"), b("result"), i("count"), nullableText("key_id"), nullableBlob("nonce"), nullableBlob("body")},
			Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(550), int64(1), "invitation_create_v1", int64(1000), digest, int64(1), "invitation-key-id", make([]byte, 12), make([]byte, 59)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationReceiptResultWrite, Kind: stmt.Write, SQL: `INSERT INTO security_invitation_results(owner_id,command_incarnation,command_sequence,invitation_id,server_id,notes,role,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("invitation"), t("server"), nullableText("notes"), t("role"), t("state"), i("created"), i("expires"), {Name: "accepted", Type: stmt.Integer, Nullable: true}, nullableText("account"), i("revision"), t("bound"), t("mode"), i("streams"), i("remote"), i("tokens"), t("rating"), t("code_hash")},
			Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"security-owner", inc, int64(1), "us-invite", "server", nil, "member", "pending", int64(1000), int64(604801000), nil, nil, int64(1), "", "inherit", int64(0), int64(1), int64(1), "", usInvitationFixtureHash}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationReceiptLibInsert, Kind: stmt.Write, SQL: `INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES(?,?,?,?,?,?) RETURNING variable_bytes`,
			Params:  append(append([]stmt.Param(nil), key...), t("cohort"), i("ordinal"), t("library")),
			Cohorts: []stmt.Cohort{{Name: "one_saved_library", Bindings: append(append([]any(nil), bind...), "invitation", int64(0), "movies")}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET last_sequence=?,retained_count=retained_count+1,retained_variable_bytes=retained_variable_bytes+?
WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND retained_count=? AND retained_variable_bytes=? AND retired_ms=0
RETURNING last_sequence,retained_count,retained_variable_bytes`,
			Params:  []stmt.Param{i("next"), i("charged_bytes"), t("owner"), b("incarnation"), i("last"), i("ack"), i("count"), i("bytes")},
			Cohorts: []stmt.Cohort{{Name: "advance", Bindings: []any{int64(1), int64(320), "security-owner", inc, int64(0), int64(0), int64(0), int64(0)}}}, Plan: ownerPlan},
		{ID: usInvitationOwnerAck, Kind: stmt.Write, SQL: `UPDATE security_command_owners INDEXED BY sqlite_autoindex_security_command_owners_1 SET acknowledged_sequence=? WHERE id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? RETURNING acknowledged_sequence`,
			Params:  []stmt.Param{i("next"), t("owner"), b("incarnation"), i("last"), i("ack")},
			Cohorts: []stmt.Cohort{{Name: "ack", Bindings: []any{int64(1), "security-owner", inc, int64(1), int64(0)}}}, Plan: ownerPlan},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US invitation receipts"
		defs[n].CallSites = []string{"store/writer/us_invitation_receipts.go"}
	}
	return defs
}
