package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Fixed inert purpose-542 native inventory. Callers bind kind 542, version 1,
// profile_restrictions_v1 and NULL secret slots; labels use bounds (-1,64).
const (
	usRestrictionsReceiptOctets       stmt.ID = "us.restrictions.receipt.octets"
	usRestrictionsReceiptHeader       stmt.ID = "us.restrictions.receipt.header"
	usRestrictionsReceiptResultOctets stmt.ID = "us.restrictions.receipt.resultoctets"
	usRestrictionsReceiptResult       stmt.ID = "us.restrictions.receipt.result"
	usRestrictionsReceiptLabelOctets  stmt.ID = "us.restrictions.receipt.labeloctets"
	usRestrictionsReceiptLabels       stmt.ID = "us.restrictions.receipt.labels"
	usRestrictionsReceiptInsert       stmt.ID = "us.restrictions.receipt.insert"
	usRestrictionsReceiptResultInsert stmt.ID = "us.restrictions.receipt.resultinsert"
	usRestrictionsReceiptLabelInsert  stmt.ID = "us.restrictions.receipt.labelinsert"
)

func usRestrictionsReceiptDefinitions() []stmt.Definition {
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
	labelPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"security_restriction_result_labels"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? AND ordinal<?"}}
	const predicate = " WHERE owner_id=? AND command_incarnation=? AND command_sequence=?"
	const labelPredicate = " WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? AND ordinal<? ORDER BY ordinal LIMIT 64"
	defs := []stmt.Definition{
		{ID: usRestrictionsReceiptOctets, SQL: `SELECT variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usRestrictionsReceiptHeader, SQL: `SELECT command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body,variable_bytes FROM security_command_receipts` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_command_receipts")},
		{ID: usRestrictionsReceiptResultOctets, SQL: `SELECT variable_bytes FROM security_restriction_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_restriction_results")},
		{ID: usRestrictionsReceiptResult, SQL: `SELECT account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,variable_bytes FROM security_restriction_results` + predicate, Params: key, Cohorts: []stmt.Cohort{{Name: "saved", Bindings: bind}}, Plan: point("security_restriction_results")},
		{ID: usRestrictionsReceiptLabelOctets, SQL: `SELECT ordinal,variable_bytes FROM security_restriction_result_labels` + labelPredicate, Params: append(append([]stmt.Param(nil), key...), i("after"), i("before")), Cohorts: []stmt.Cohort{{Name: "bounded", Bindings: append(append([]any(nil), bind...), int64(-1), int64(64))}}, Plan: labelPlan},
		{ID: usRestrictionsReceiptLabels, SQL: `SELECT ordinal,label FROM security_restriction_result_labels` + labelPredicate, Params: append(append([]stmt.Param(nil), key...), i("after"), i("before")), Cohorts: []stmt.Cohort{{Name: "bounded", Bindings: append(append([]any(nil), bind...), int64(-1), int64(64))}}, Plan: labelPlan},
		{ID: usRestrictionsReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,result_key_id,result_nonce,result_body) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes,result_count`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied"), b("result"), i("count"), nt("key"), nb("nonce"), nb("body")}, Cohorts: []stmt.Cohort{{Name: "create", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(542), int64(1), "profile_restrictions_v1", int64(1000), digest, int64(1), nil, nil, nil}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usRestrictionsReceiptResultInsert, Kind: stmt.Write, SQL: `INSERT INTO security_restriction_results(owner_id,command_incarnation,command_sequence,command_kind,account_id,member_incarnation,profile_id,profile_lifetime,revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), i("kind"), t("account"), t("memberIncarnation"), t("profile"), t("profileLifetime"), i("revision"), t("ratingSystem"), t("maximumAgeRating"), i("maximumAge"), i("allowUnrated"), i("allowDownloads"), i("allowLiveTV"), i("allowDVR"), i("allowWatchTogether")}, Cohorts: []stmt.Cohort{{Name: "dto", Bindings: []any{"security-owner", inc, int64(1), int64(542), "target", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(2), "", "", int64(-1), int64(1), int64(1), int64(1), int64(0), int64(1)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: usRestrictionsReceiptLabelInsert, Kind: stmt.Write, SQL: `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,?,?) RETURNING variable_bytes`, Params: append(append([]stmt.Param(nil), key...), i("ordinal"), t("label")), Cohorts: []stmt.Cohort{{Name: "label", Bindings: append(append([]any(nil), bind...), int64(0), "label")}}, Plan: stmt.Plan{Hot: true}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US restrictions receipts"
		defs[n].CallSites = []string{"store/writer/us_restrictions_receipt_statements.go"}
	}
	return defs
}
