package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Fixed inert native inventory only. The caller classifies the original owner
// and binding, then proves current authority before reading any saved payload.
// Library INSERT cohort must be bound to "invitation"; HN VALUES accepts
// parameters only. The caller validates exact 59-byte creation body and NULL
// edit/revoke slots before writes; the draft CHECK only requires >=16 bytes.
// issuerServer comes from that retained owner, never the DTO server_id below.
const (
	usInvitationReceiptOctets        stmt.ID = "us.invitation.receipt.octets"
	usInvitationReceiptHeader        stmt.ID = "us.invitation.receipt.header"
	usInvitationReceiptResultOctets  stmt.ID = "us.invitation.receipt.resultoctets"
	usInvitationReceiptResult        stmt.ID = "us.invitation.receipt.result"
	usInvitationReceiptLibraryOctets stmt.ID = "us.invitation.receipt.libraryoctets"
	usInvitationReceiptLibraries     stmt.ID = "us.invitation.receipt.libraries"
	usInvitationReceiptInsert        stmt.ID = "us.invitation.receipt.insert"
	usInvitationReceiptResultInsert  stmt.ID = "us.invitation.receipt.resultinsert"
	usInvitationReceiptLibraryInsert stmt.ID = "us.invitation.receipt.libraryinsert"
)

func usInvitationReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	nb := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob, Nullable: true} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	bind := []any{"security-owner", inc, int64(1)}
	point := func(table string) stmt.Plan {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}
	}
	libraryPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_result_libraries"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort=? AND ordinal>? AND ordinal<?"}}
	const predicate = " WHERE owner_id=? AND command_incarnation=? AND command_sequence=?"
	const libraryPredicate = " WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND cohort='invitation' AND ordinal>? AND ordinal<? ORDER BY ordinal LIMIT 256"
	defs := []stmt.Definition{
		{ID: usInvitationReceiptOctets, SQL: `SELECT variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usInvitationReceiptHeader, SQL: `SELECT command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body,variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usInvitationReceiptResultOctets, SQL: `SELECT variable_bytes FROM security_invitation_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_invitation_results")},
		{ID: usInvitationReceiptResult, SQL: `SELECT invitation_id,server_id,notes,role,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash,variable_bytes FROM security_invitation_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_invitation_results")},
		{ID: usInvitationReceiptLibraryOctets, SQL: `SELECT ordinal,variable_bytes FROM security_result_libraries` + libraryPredicate, Params: append(append([]stmt.Param(nil), key...), i("after"), i("before")), Cohorts: []stmt.Cohort{{Name: "bounded", Bindings: append(append([]any(nil), bind...), int64(-1), int64(256))}}, Plan: libraryPlan},
		{ID: usInvitationReceiptLibraries, SQL: `SELECT ordinal,library_id FROM security_result_libraries` + libraryPredicate, Params: append(append([]stmt.Param(nil), key...), i("after"), i("before")), Cohorts: []stmt.Cohort{{Name: "bounded", Bindings: append(append([]any(nil), bind...), int64(-1), int64(256))}}, Plan: libraryPlan},
		{ID: usInvitationReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes,result_count`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied"), b("result"), i("count"), nt("key"), nb("nonce"), nb("body")}, Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(550), int64(1), "invitation_create_v1", int64(1000), digest, int64(1), "key", make([]byte, 12), make([]byte, 59)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationReceiptResultInsert, Kind: stmt.Write, SQL: `INSERT INTO security_invitation_results(owner_id,command_incarnation,command_sequence,invitation_id,server_id,notes,role,state,created_ms,expires_ms,accepted_ms,account_id,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,source_code_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("invitation"), t("server"), nt("notes"), t("role"), t("state"), i("created"), i("expires"), ni("accepted"), nt("account"), i("revision"), t("username"), t("mode"), i("streams"), i("remote"), i("tokens"), t("rating"), t("hash")}, Cohorts: []stmt.Cohort{{Name: "dto", Bindings: []any{"security-owner", inc, int64(1), "invite", "", nil, "member", "pending", int64(1000), int64(3601000), nil, nil, int64(1), "", "inherit", int64(0), int64(1), int64(1), "", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationReceiptLibraryInsert, Kind: stmt.Write, SQL: `INSERT INTO security_result_libraries(owner_id,command_incarnation,command_sequence,cohort,ordinal,library_id) VALUES(?,?,?,?,?,?) RETURNING variable_bytes`, Params: append(append([]stmt.Param(nil), key...), t("cohort"), i("ordinal"), t("library")), Cohorts: []stmt.Cohort{{Name: "library", Bindings: append(append([]any(nil), bind...), "invitation", int64(0), "movies")}}, Plan: stmt.Plan{Hot: true}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US invitation receipts"
		defs[n].CallSites = []string{"store/writer/us_invitation_receipt_statements.go"}
	}
	return defs
}
