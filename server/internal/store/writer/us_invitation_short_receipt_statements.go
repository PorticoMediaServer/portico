package writer

import "strings"

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert purpose-553 native inventory only; no runtime admission or registration.
// Headers bind version 1, count 0 and NULL generic secret slots.
const (
	usInvitationShortReceiptOctets       stmt.ID = "us.invitation_short.receipt.octets"
	usInvitationShortReceiptHeader       stmt.ID = "us.invitation_short.receipt.header"
	usInvitationShortReceiptResultOctets stmt.ID = "us.invitation_short.receipt.resultoctets"
	usInvitationShortReceiptResult       stmt.ID = "us.invitation_short.receipt.result"
	usInvitationShortReceiptInsert       stmt.ID = "us.invitation_short.receipt.insert"
	usInvitationShortReceiptResultInsert stmt.ID = "us.invitation_short.receipt.resultinsert"
)

func usInvitationShortReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	bind := []any{"security-owner", inc, int64(1)}
	point := func(table string) stmt.Plan {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	}
	const predicate = " WHERE owner_id=? AND command_incarnation=? AND command_sequence=?"
	defs := []stmt.Definition{
		{ID: usInvitationShortReceiptOctets, SQL: `SELECT variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usInvitationShortReceiptHeader, SQL: `SELECT command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body,variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usInvitationShortReceiptResultOctets, SQL: `SELECT variable_bytes FROM security_invitation_short_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_invitation_short_results")},
		{ID: usInvitationShortReceiptResult, SQL: `SELECT invitation_id,invitation_revision,invitation_code_hash,invitation_role,invitation_expires_ms,server_id,server_public_key,server_key_incarnation,server_reset_generation,sign_in_kind,kind_revision,issued_ms,expires_ms,lookup_hash,binding_hash,result_key_id,result_nonce,result_body,result_aad_digest,variable_bytes FROM security_invitation_short_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_invitation_short_results")},
		{ID: usInvitationShortReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes,result_count`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied"), b("result"), i("count"), nt("key"), nb("nonce"), nb("body")}, Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(553), int64(1), "invitation_short_issue_v1", int64(1000), digest, int64(0), nil, nil, nil}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationShortReceiptResultInsert, Kind: stmt.Write, SQL: `INSERT INTO security_invitation_short_results(owner_id,command_incarnation,command_sequence,command_kind,invitation_id,invitation_revision,invitation_code_hash,invitation_role,invitation_expires_ms,server_id,server_public_key,server_key_incarnation,server_reset_generation,sign_in_kind,kind_revision,issued_ms,expires_ms,lookup_hash,binding_hash,result_key_id,result_nonce,result_body,result_aad_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), i("kind"), t("invitation_id"), i("invitation_revision"), t("invitation_code_hash"), t("invitation_role"), i("invitation_expires_ms"), t("server_id"), b("server_public_key"), t("server_key_incarnation"), i("server_reset_generation"), t("sign_in_kind"), i("kind_revision"), i("issued_ms"), i("expires_ms"), t("lookup_hash"), t("binding_hash"), t("result_key_id"), b("result_nonce"), b("result_body"), b("result_aad_digest")}, Cohorts: []stmt.Cohort{{Name: "wire", Bindings: []any{"security-owner", inc, int64(1), int64(553), "invite", int64(7), strings.Repeat("a", 64), "member", int64(2000000), "srv_" + strings.Repeat("A", 43), make([]byte, 32), "key-life", int64(3), "local", int64(4), int64(1000), int64(601000), strings.Repeat("b", 64), strings.Repeat("c", 64), "payload-key", make([]byte, 12), make([]byte, 35), make([]byte, 32)}}}, Plan: stmt.Plan{Hot: true}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US invitation_short receipts"
		defs[n].CallSites = []string{"store/writer/us_invitation_short_receipt_statements.go"}
	}
	return defs
}
