package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	uoReadingAccountCut      stmt.ID = "uo.reading.prune.account_cut"
	uoReadingFamilyCut       stmt.ID = "uo.reading.prune.family_cut"
	uoReadingGlobalCut       stmt.ID = "uo.reading.prune.global_cut"
	uoReadingOrphanProof     stmt.ID = "uo.reading.prune.orphan_proof"
	uoReadingSelectAccount   stmt.ID = "uo.reading.prune.account"
	uoReadingSelectFamily    stmt.ID = "uo.reading.prune.family"
	uoReadingSelectOrphan    stmt.ID = "uo.reading.prune.orphan"
	uoReadingSelectGlobal    stmt.ID = "uo.reading.prune.global"
	uoReadingSelectProfile   stmt.ID = "uo.reading.prune.profile"
	uoReadingResultBytes     stmt.ID = "uo.reading.result.bytes"
	uoReadingResultDelete    stmt.ID = "uo.reading.result.delete"
	uoReadingReceiptDelete   stmt.ID = "uo.reading.receipt.delete"
	uoReadingPersonalPoint   stmt.ID = "uo.reading.personal.point"
	uoReadingResumeHead      stmt.ID = "uo.reading.resume.head"
	uoReadingResumeDelete    stmt.ID = "uo.reading.resume.delete"
	uoReadingResumeRemaining stmt.ID = "uo.reading.resume.remaining"
	uoReadingResumeMerge     stmt.ID = "uo.reading.resume.merge"
	uoReadingResumeTarget    stmt.ID = "uo.reading.resume.target"
)

func uoReadingCustodyDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: uoReadingAccountCut, SQL: `SELECT retired_before_ms FROM user_mutation_accounts WHERE id=?`, Params: []stmt.Param{i("scope")}},
		{ID: uoReadingFamilyCut, SQL: `SELECT id,retired_before_ms FROM user_mutation_accounts WHERE authority=? AND account_id=?`, Params: []stmt.Param{t("authority"), t("account")}},
		{ID: uoReadingGlobalCut, SQL: `SELECT retired_before_ms FROM user_mutation_global WHERE singleton=1`},
		{ID: uoReadingOrphanProof, SQL: `SELECT 1 FROM user_mutation_accounts WHERE id=? AND owner_account_id IS NULL`, Params: []stmt.Param{i("scope")}},
		{ID: uoReadingSelectAccount, SQL: `SELECT key FROM user_mutation_receipts INDEXED BY user_mutation_receipts_scope_expiry WHERE scope_id=? AND issued_ms<? ORDER BY issued_ms,key LIMIT 32`, Params: []stmt.Param{i("scope"), i("cut")}},
		{ID: uoReadingSelectFamily, SQL: `SELECT key FROM user_mutation_receipts INDEXED BY user_mutation_receipts_family WHERE scope_id=? AND family_id=? AND issued_ms<? ORDER BY issued_ms,key LIMIT 32`, Params: []stmt.Param{i("scope"), t("family"), i("cut")}},
		{ID: uoReadingSelectOrphan, SQL: `SELECT key FROM user_mutation_receipts INDEXED BY user_mutation_receipts_scope_expiry WHERE scope_id=? ORDER BY issued_ms,key LIMIT 32`, Params: []stmt.Param{i("scope")}},
		{ID: uoReadingSelectGlobal, SQL: `SELECT key FROM user_mutation_receipts INDEXED BY user_mutation_receipts_expiry WHERE issued_ms<? ORDER BY issued_ms,key LIMIT 32`, Params: []stmt.Param{i("cut")}},
		{ID: uoReadingSelectProfile, SQL: `SELECT key FROM user_mutation_receipts INDEXED BY user_mutation_receipts_profile WHERE scope_id=? AND profile_id=? AND profile_incarnation=? ORDER BY key LIMIT 32`, Params: []stmt.Param{i("scope"), t("profile"), t("lifetime")}},
		{ID: uoReadingResultBytes, SQL: `SELECT variable_bytes FROM user_mutation_reading_results WHERE receipt_key=?`, Params: []stmt.Param{t("key")}},
		{ID: uoReadingResultDelete, Kind: stmt.Write, SQL: `DELETE FROM user_mutation_reading_results WHERE receipt_key=? AND variable_bytes=? RETURNING variable_bytes`, Params: []stmt.Param{t("key"), i("bytes")}},
		{ID: uoReadingReceiptDelete, Kind: stmt.Write, SQL: `DELETE FROM user_mutation_receipts WHERE key=? RETURNING is_stop,scope_id`, Params: []stmt.Param{t("key")}},
		{ID: uoReadingPersonalPoint, SQL: `SELECT 1 FROM personal_items WHERE id=? AND profile_id=? AND item_id=? AND library_id=? AND item_public_id=?`, Params: []stmt.Param{i("personal"), t("profile"), i("item"), i("library"), b("public")}},
		{ID: uoReadingResumeHead, SQL: `SELECT asset_id,variable_bytes,observed_ms,revision FROM personal_reading_resumes WHERE personal_item_id=? ORDER BY asset_id LIMIT 1`, Params: []stmt.Param{i("personal")}},
		{ID: uoReadingResumeDelete, Kind: stmt.Write, SQL: `DELETE FROM personal_reading_resumes WHERE personal_item_id=? AND asset_id=? AND variable_bytes=? RETURNING variable_bytes`, Params: []stmt.Param{i("personal"), i("asset"), i("bytes")}},
		{ID: uoReadingResumeRemaining, SQL: `SELECT 1 FROM personal_reading_resumes WHERE personal_item_id=? LIMIT 1`, Params: []stmt.Param{i("personal")}},
		{ID: uoReadingResumeTarget, SQL: `SELECT observed_ms,revision,variable_bytes FROM personal_reading_resumes WHERE personal_item_id=? AND asset_id=?`, Params: []stmt.Param{i("personal"), i("asset")}},
		{ID: uoReadingResumeMerge, Kind: stmt.Write, SQL: `UPDATE personal_reading_resumes SET personal_item_id=? WHERE personal_item_id=? AND asset_id=? AND variable_bytes=? RETURNING personal_item_id`, Params: []stmt.Param{i("into"), i("from"), i("asset"), i("bytes")}},
	}
	for n := range defs {
		d := &defs[n]
		if d.Kind == "" {
			d.Kind = stmt.Read
		}
		d.Owner = "store/writer UO canonical reading custody"
		d.CallSites = []string{"store/writer/uo_reading_custody.go"}
		d.Plan = stmt.Plan{Hot: true}
		switch d.ID {
		case uoReadingAccountCut, uoReadingOrphanProof:
			d.Plan.PrimaryKeys = []string{"user_mutation_accounts"}
			d.Plan.Seeks = []string{"rowid=?"}
		case uoReadingGlobalCut:
			d.Plan.PrimaryKeys = []string{"user_mutation_global"}
			d.Plan.Seeks = []string{"rowid=?"}
		case uoReadingFamilyCut:
			d.Plan.Indexes = []string{"sqlite_autoindex_user_mutation_accounts_1"}
			d.Plan.Seeks = []string{"authority=? AND account_id=?"}
		case uoReadingSelectAccount, uoReadingSelectOrphan:
			d.Plan.Indexes = []string{"user_mutation_receipts_scope_expiry"}
			d.Plan.Seeks = []string{"scope_id=?"}
		case uoReadingSelectFamily:
			d.Plan.Indexes = []string{"user_mutation_receipts_family"}
			d.Plan.Seeks = []string{"scope_id=? AND family_id=?"}
		case uoReadingSelectGlobal:
			d.Plan.Indexes = []string{"user_mutation_receipts_expiry"}
			d.Plan.Seeks = []string{"issued_ms<?"}
		case uoReadingSelectProfile:
			d.Plan.Indexes = []string{"user_mutation_receipts_profile"}
			d.Plan.Seeks = []string{"scope_id=? AND profile_id=? AND profile_incarnation=?"}
		case uoReadingResultBytes, uoReadingResultDelete:
			d.Plan.Indexes = []string{"sqlite_autoindex_user_mutation_reading_results_1"}
			d.Plan.Seeks = []string{"receipt_key=?"}
		case uoReadingReceiptDelete:
			d.Plan.PrimaryKeys = []string{"user_mutation_receipts"}
			d.Plan.Seeks = []string{"key=?"}
		case uoReadingPersonalPoint:
			d.Plan.PrimaryKeys = []string{"personal_items"}
			d.Plan.Seeks = []string{"rowid=?"}
		default:
			d.Plan.Indexes = []string{"sqlite_autoindex_personal_reading_resumes_1"}
			d.Plan.Seeks = []string{"personal_item_id=?"}
		}
	}
	return defs
}
