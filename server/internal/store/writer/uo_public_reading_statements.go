package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoPublicReadingRoot            stmt.ID = "uo.public_reading.root"
	uoPublicReadingAccount         stmt.ID = "uo.public_reading.account"
	uoPublicReadingGlobal          stmt.ID = "uo.public_reading.global"
	uoPublicReadingHeaderOriginal  stmt.ID = "uo.public_reading.header_original"
	uoPublicReadingHeader          stmt.ID = "uo.public_reading.header"
	uoPublicReadingGlobalBirth     stmt.ID = "uo.public_reading.global_birth"
	uoPublicReadingAccountBirth    stmt.ID = "uo.public_reading.account_birth"
	uoPublicReadingAccountReserve  stmt.ID = "uo.public_reading.account_reserve"
	uoPublicReadingGlobalReserve   stmt.ID = "uo.public_reading.global_reserve"
	uoPublicReadingHeaderBirth     stmt.ID = "uo.public_reading.header_birth"
	uoPublicReadingHeaderSave      stmt.ID = "uo.public_reading.header_save"
	uoPublicReadingResultBirth     stmt.ID = "uo.public_reading.result_birth"
	uoPublicReadingGlobalReconcile stmt.ID = "uo.public_reading.global_reconcile"
	uoPublicReadingResultBytes     stmt.ID = "uo.public_reading.result_bytes"
	uoPublicReadingResultRead      stmt.ID = "uo.public_reading.result_read"
)

func uoPublicReadingDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { p := t(n); p.Nullable = true; return p }
	ni := func(n string) stmt.Param { p := i(n); p.Nullable = true; return p }
	defs := []stmt.Definition{
		{ID: uoPublicReadingRoot, SQL: `SELECT state FROM user_mutation_roots WHERE id=? AND account_id=? AND profile_id=? AND account_incarnation=? AND profile_incarnation=? AND family_id=? AND installation_incarnation=?`, Params: []stmt.Param{t("root"), t("account"), t("profile"), t("member"), t("lifetime"), t("family"), t("installation")}},
		{ID: uoPublicReadingAccount, SQL: `SELECT id,retired_before_ms,ordinary_count FROM user_mutation_accounts WHERE authority='local' AND account_id=? AND owner_account_id=?`, Params: []stmt.Param{t("account"), t("owner")}},
		{ID: uoPublicReadingGlobal, SQL: `SELECT retired_before_ms,ordinary_count,ordinary_bytes FROM user_mutation_global WHERE singleton=1`},
		{ID: uoPublicReadingHeader, SQL: `SELECT scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,operation,digest,issued_ms,is_stop,status FROM user_mutation_receipts WHERE key=?`, Params: []stmt.Param{t("key")}},
		{ID: uoPublicReadingHeaderOriginal, SQL: `SELECT status,target_id,revision,generation,position_ms,position_status,committed_ms FROM user_mutation_receipts WHERE key=? AND scope_id=? AND digest=? AND status='applied' AND is_stop=0`, Params: []stmt.Param{t("key"), i("scope"), t("digest")}},
		{ID: uoPublicReadingGlobalBirth, Kind: stmt.Write, SQL: `INSERT INTO user_mutation_global(singleton) VALUES(?) RETURNING singleton`, Params: []stmt.Param{i("singleton")}},
		{ID: uoPublicReadingAccountBirth, Kind: stmt.Write, SQL: `INSERT INTO user_mutation_accounts(authority,account_id,owner_account_id,charged_stop) VALUES(?,?,?,?) RETURNING id`, Params: []stmt.Param{t("authority"), t("account"), t("owner"), i("charged_stop")}},
		{ID: uoPublicReadingAccountReserve, Kind: stmt.Write, SQL: `UPDATE user_mutation_accounts SET ordinary_count=ordinary_count+1 WHERE id=? AND ordinary_count=? AND ordinary_count<8192 RETURNING id`, Params: []stmt.Param{i("scope"), i("old_count")}},
		{ID: uoPublicReadingGlobalReserve, Kind: stmt.Write, SQL: `UPDATE user_mutation_global SET ordinary_count=?,ordinary_bytes=?,retired_before_ms=? WHERE singleton=1 AND ordinary_count=? AND ordinary_bytes=? AND retired_before_ms=? RETURNING singleton`, Params: []stmt.Param{i("count"), i("bytes"), i("cut"), i("old_count"), i("old_bytes"), i("old_cut")}},
		{ID: uoPublicReadingHeaderBirth, Kind: stmt.Write, SQL: `INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,profile_incarnation,installation_incarnation,root_id,operation,digest,issued_ms,is_stop,status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING issued_ms`, Params: []stmt.Param{t("key"), i("scope"), t("profile"), t("family"), t("lifetime"), t("installation"), t("root"), t("operation"), t("digest"), i("issued"), i("stop"), t("status")}},
		{ID: uoPublicReadingHeaderSave, Kind: stmt.Write, SQL: `UPDATE user_mutation_receipts SET status=?,target_id=?,revision=?,generation=?,position_ms=?,position_status=?,committed_ms=? WHERE key=? AND scope_id=? AND status='reserved' AND digest=? AND operation=? AND profile_id=? AND family_id=? AND profile_incarnation=? AND root_id=? AND installation_incarnation=? RETURNING issued_ms`, Params: []stmt.Param{t("status"), t("target"), i("revision"), i("generation"), i("position"), t("position_status"), i("committed"), t("key"), i("scope"), t("digest"), t("operation"), t("profile"), t("family"), t("lifetime"), t("root"), t("installation")}},
		{ID: uoPublicReadingResultBirth, Kind: stmt.Write, SQL: `INSERT INTO user_mutation_reading_results(receipt_key,command_kind,result_version,item_public_id,asset_id,source_token,source_revision,format,cfi,section,page_index,progression,reading_revision,personal_revision,observed_ms,accepted,stale,cleared,reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{t("key"), i("kind"), i("version"), b("public"), ni("asset"), nt("token"), nt("source_revision"), nt("format"), nt("cfi"), nt("section"), ni("page"), {Name: "progression", Type: stmt.Real, Nullable: true}, i("reading_revision"), i("personal_revision"), i("observed"), i("accepted"), i("stale"), i("cleared"), t("reason")}},
		{ID: uoPublicReadingGlobalReconcile, Kind: stmt.Write, SQL: `UPDATE user_mutation_global SET ordinary_bytes=? WHERE singleton=1 AND ordinary_count=? AND ordinary_bytes=? RETURNING singleton`, Params: []stmt.Param{i("actual"), i("count"), i("reserved")}},
		{ID: uoPublicReadingResultBytes, SQL: `SELECT variable_bytes FROM user_mutation_reading_results WHERE receipt_key=? AND command_kind=? AND result_version=1`, Params: []stmt.Param{t("key"), i("kind")}},
		{ID: uoPublicReadingResultRead, SQL: `SELECT item_public_id,asset_id,source_token,source_revision,format,cfi,section,page_index,progression,reading_revision,personal_revision,observed_ms,accepted,stale,cleared,reason FROM user_mutation_reading_results WHERE receipt_key=? AND command_kind=? AND result_version=1 AND variable_bytes=?`, Params: []stmt.Param{t("key"), i("kind"), i("bytes")}},
	}
	for n := range defs {
		d := &defs[n]
		if d.Kind == "" {
			d.Kind = stmt.Read
		}
		d.Owner = "store/writer UO canonical public reading receipt"
		d.CallSites = []string{"store/writer/uo_public_reading_receipts.go"}
		d.Plan = stmt.Plan{Hot: true}
		switch d.ID {
		case uoPublicReadingAccount:
			d.Plan.Indexes = []string{"sqlite_autoindex_user_mutation_accounts_1"}
			d.Plan.Seeks = []string{"authority=? AND account_id=?"}
		case uoPublicReadingAccountReserve:
			d.Plan.PrimaryKeys = []string{"user_mutation_accounts"}
			d.Plan.Seeks = []string{"rowid=?"}
		case uoPublicReadingGlobal, uoPublicReadingGlobalReserve, uoPublicReadingGlobalReconcile:
			d.Plan.PrimaryKeys = []string{"user_mutation_global"}
			d.Plan.Seeks = []string{"rowid=?"}
		case uoPublicReadingRoot:
			d.Plan.Indexes = []string{"sqlite_autoindex_user_mutation_roots_1"}
			d.Plan.Seeks = []string{"id=?"}
		case uoPublicReadingHeader, uoPublicReadingHeaderOriginal, uoPublicReadingHeaderSave:
			d.Plan.PrimaryKeys = []string{"user_mutation_receipts"}
			d.Plan.Seeks = []string{"key=?"}
		case uoPublicReadingResultBytes, uoPublicReadingResultRead:
			d.Plan.Indexes = []string{"sqlite_autoindex_user_mutation_reading_results_1"}
			d.Plan.Seeks = []string{"receipt_key=?"}
		}
	}
	return defs
}
