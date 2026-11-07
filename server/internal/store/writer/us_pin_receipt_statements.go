package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	usPINTargetSelf    stmt.ID = "us.pin.target.self"
	usPINTargetManaged stmt.ID = "us.pin.target.managed"
	usPINReceiptHeader stmt.ID = "us.pin.receipt.header"
	usPINReceiptInsert stmt.ID = "us.pin.receipt.insert"
)

func usPINReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	key := []stmt.Param{t("owner"), b("incarnation"), i("sequence")}
	targetPlan := usPolicyDefinitions()[3].Plan
	const target = `SELECT p.revision,p.pin_revision FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=m.account_id
WHERE m.account_id=? AND m.incarnation=? AND p.id=? AND p.lifetime=? AND m.disabled=0 AND p.deleted=0 AND m.role='owner'`
	inc, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	defs := []stmt.Definition{
		{ID: usPINTargetSelf, SQL: target, Params: []stmt.Param{t("account"), t("member"), t("profile"), t("lifetime")}, Cohorts: []stmt.Cohort{{Name: "own_account", Bindings: []any{"issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}, Plan: targetPlan},
		{ID: usPINReceiptHeader, SQL: `SELECT command_kind,result_version,result_codec,applied_ms,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,result_pin_revision,result_pin_required,result_changed,result_count,variable_bytes FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: key, Cohorts: []stmt.Cohort{{Name: "saved_pin", Bindings: []any{"security-owner", inc, int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"security_command_receipts"}, Seeks: []string{"owner_id=? AND command_incarnation=? AND command_sequence=?"}}},
		{ID: usPINReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,target_account_id,target_member_incarnation,target_profile_id,target_profile_lifetime,result_revision,result_changed,result_pin_revision,result_pin_required,result_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"), i("kind"), i("version"), t("codec"), i("applied"), b("result"), t("account"), t("member"), t("profile"), t("lifetime"), i("revision"), i("changed"), i("pin_revision"), i("pin_required"), i("count")}, Cohorts: []stmt.Cohort{{Name: "pin_set", Bindings: []any{"security-owner", inc, int64(1), op, digest, int64(532), int64(1), "pin_set_v1", int64(1000), digest, "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "issuer-profile", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(2), int64(1), int64(2), int64(1), int64(0)}}}, Plan: stmt.Plan{Hot: true}},
	}
	managed := defs[0]
	managed.ID = usPINTargetManaged
	managed.SQL = strings.Replace(managed.SQL, "m.role='owner'", "m.role IN('admin','member')", 1)
	defs = append(defs, managed)
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US PIN receipts"
		defs[n].CallSites = []string{"store/writer/us_pin_receipts.go"}
	}
	return defs
}
