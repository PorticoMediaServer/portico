package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-19 finish-rescan statement family. Every read is a point read or one
// bounded indexed keyset window over the accepted run's own directory/object
// frontier; every write is one finite CAS on the exact run row or the exact
// group C draft receipt relation. There is no OFFSET, temporary sort,
// library-wide scan or caller-selected SQL. Exempt (blocked, protected and
// skipped) paths and exempt unseen objects are paged iaFinishPageRows at a
// time and prefix-compressed into one bounded topmost-prefix set, so nested
// exempt rows never accumulate individually; more than
// iaFinishExemptMaxPrefixes disjoint prefixes is a typed hold rather than
// silent success. Only genuinely unseen objects outside every exempt subtree
// keep the iaFinishMaxRows refusal.
const (
	iaFinishOwner          stmt.ID = "ia.finish.owner"
	iaFinishHeader         stmt.ID = "ia.finish.header"
	iaFinishLibrary        stmt.ID = "ia.finish.library"
	iaFinishSource         stmt.ID = "ia.finish.source"
	iaFinishRunStateStmt   stmt.ID = "ia.finish.run_state"
	iaFinishRoot           stmt.ID = "ia.finish.root"
	iaFinishPending        stmt.ID = "ia.finish.pending"
	iaFinishBlocked        stmt.ID = "ia.finish.blocked"
	iaFinishProtected      stmt.ID = "ia.finish.protected"
	iaFinishSkipped        stmt.ID = "ia.finish.skipped"
	iaFinishBlockedCharged stmt.ID = "ia.finish.blocked_charged"
	iaFinishUnseen         stmt.ID = "ia.finish.unseen"
	iaFinishRunCAS         stmt.ID = "ia.finish.run_cas"
	iaFinishReceiptInsert  stmt.ID = "ia.finish.receipt_insert"
	iaFinishReceiptKind            = "inventory_finish"
	iaFinishComplete               = "complete"
	iaFinishWithWarnings           = "complete_with_warnings"
	iaFinishMaxRows                = 8
	// iaFinishPageRows bounds one keyset window, not a count. Exempt rows are
	// paged until the frontier is exhausted so a run is never wedged by their
	// number; a page that fills is followed by its keyset successor.
	iaFinishPageRows = 64
	// iaFinishExemptMaxPrefixes bounds the TOTAL distinct topmost exempt prefixes
	// one finish may hold at once. Nested exempt rows collapse into their nearest
	// accumulated ancestor, so only disjoint subtrees count against this cap.
	// Exceeding it is the typed iaFinishExemptUnbounded hold, never success.
	iaFinishExemptMaxPrefixes = 4096
	iaFinishRootPath          = "."
	iaFinishMaxKeyBytes       = 512
)

func iaFinishRescanStatements() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence")}
	defs := []stmt.Definition{
		{ID: iaFinishOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"finish-run"}},
				{Name: "missing_owner", Bindings: []any{"finish-none"}},
			}},
		{ID: iaFinishHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,
source_id,source_incarnation,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,
ia_finished_ms,ia_source_health
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_finish_receipt", Bindings: []any{"finish-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"finish-run", inc, int64(9)}},
			}},
		{ID: iaFinishLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "finish-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "finish-retired"}},
			}},
		{ID: iaFinishSource, Kind: stmt.Read,
			SQL: `SELECT id,health FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"finish-source", "finish-library", int64(3), "finish-incarnation", "finish-root-identity", "/media/finish", false}},
				{Name: "offline_source", Bindings: []any{"finish-source", "finish-library", int64(3), "finish-incarnation", "finish-root-identity", "/media/finish", false}},
			}},
		{ID: iaFinishRunStateStmt, Kind: stmt.Read,
			SQL: `SELECT job_revision,last_command_sequence,acknowledged_command_sequence,phase,authoritative,discovered,analyzed,warnings,inventory_completed_at,finished_at
FROM inventory_runs WHERE job_id=? AND command_incarnation=?`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation")},
			Cohorts: []stmt.Cohort{
				{Name: "reconciling_run", Bindings: []any{"finish-run", inc}},
				{Name: "missing_run", Bindings: []any{"finish-none", inc}},
			}},
		{ID: iaFinishRoot, Kind: stmt.Read,
			SQL:    `SELECT state,identity,revision FROM inventory_directories WHERE job_id=? AND relative_path='.'`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "sealed_root", Bindings: []any{"finish-run"}},
				{Name: "missing_root", Bindings: []any{"finish-none"}},
			}},
		{ID: iaFinishPending, Kind: stmt.Read,
			SQL:    `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending WHERE job_id=? AND state='pending' LIMIT 1`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "open_frontier", Bindings: []any{"finish-run"}},
				{Name: "closed_frontier", Bindings: []any{"finish-closed"}},
			}},
		{ID: iaFinishBlocked, Kind: stmt.Read,
			SQL:    `SELECT relative_path FROM inventory_directories INDEXED BY inventory_directories_pending WHERE job_id=? AND state='blocked' AND relative_path>? ORDER BY relative_path LIMIT ?`,
			Params: []stmt.Param{t("run_id"), t("after_path"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "one_blocked_directory", Bindings: []any{"finish-run", "", int64(iaFinishPageRows)}},
				{Name: "no_blocked_directory", Bindings: []any{"finish-closed", "", int64(iaFinishPageRows)}},
			}},
		{ID: iaFinishProtected, Kind: stmt.Read,
			SQL:    `SELECT relative_path FROM inventory_scan_issues INDEXED BY inventory_scan_issues_protected WHERE job_id=? AND protect=1 AND relative_path>? ORDER BY relative_path LIMIT ?`,
			Params: []stmt.Param{t("run_id"), t("after_path"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "one_protected_path", Bindings: []any{"finish-run", "", int64(iaFinishPageRows)}},
				{Name: "no_protected_path", Bindings: []any{"finish-closed", "", int64(iaFinishPageRows)}},
			}},
		{ID: iaFinishSkipped, Kind: stmt.Read,
			SQL:    `SELECT relative_path FROM inventory_directories INDEXED BY inventory_directories_skipped WHERE job_id=? AND skipped>0 AND relative_path>? ORDER BY relative_path LIMIT ?`,
			Params: []stmt.Param{t("run_id"), t("after_path"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "skipped_directory", Bindings: []any{"finish-run", "", int64(iaFinishPageRows)}},
				{Name: "no_skipped_directory", Bindings: []any{"finish-closed", "", int64(iaFinishPageRows)}},
			}},
		{ID: iaFinishBlockedCharged, Kind: stmt.Read,
			SQL: `SELECT d.relative_path FROM inventory_directories AS d INDEXED BY inventory_directories_pending
LEFT JOIN inventory_scan_issues AS x INDEXED BY sqlite_autoindex_inventory_scan_issues_1 ON x.job_id=d.job_id AND x.relative_path=d.relative_path AND x.warning=1
WHERE d.job_id=? AND d.state='blocked' AND d.relative_path>? AND x.relative_path IS NULL
ORDER BY d.relative_path LIMIT ?`,
			Params: []stmt.Param{t("run_id"), t("after_path"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "uncharged_blocked_directory", Bindings: []any{"finish-run", "", int64(iaFinishPageRows)}},
				{Name: "charged_blocked_directory", Bindings: []any{"finish-closed", "", int64(iaFinishPageRows)}},
			}},
		{ID: iaFinishUnseen, Kind: stmt.Read,
			SQL: `SELECT id,relative_path FROM inventory_objects INDEXED BY inventory_objects_source
WHERE source_id=? AND retired=0 AND id>? AND root_incarnation=? AND state='available' AND seen_job<>?
ORDER BY id LIMIT ?`,
			Params: []stmt.Param{t("source_id"), t("after_object_id"), t("incarnation"), t("run_id"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "unseen_available_object", Bindings: []any{"finish-source", "", "finish-incarnation", "finish-run", int64(iaFinishPageRows)}},
				{Name: "no_unseen_available_object", Bindings: []any{"finish-source", "", "finish-incarnation", "finish-closed", int64(iaFinishPageRows)}},
			}},
		{ID: iaFinishRunCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_runs SET phase=?,finished_at=?,reconcile_cursor='',authoritative=1,warnings=?,job_revision=?,last_command_sequence=?
WHERE job_id=? AND command_incarnation=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=?
AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase='reconciling' AND scan_mode=?
AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND authoritative IN(0,1) AND root_rebound=0 AND finished_at='' AND inventory_completed_at<>''
AND discovered=? AND analyzed=? AND warnings=?
RETURNING job_revision,last_command_sequence,phase`,
			Params: []stmt.Param{t("phase"), t("finished_at"), i("warnings"), i("next_revision"), i("next_sequence"), t("run_id"), b("command_incarnation"),
				i("library_id"), t("library_key"), t("resolved_root"), boolean("follow_symlinks"), t("source_id"), i("generation"), t("incarnation"),
				t("root_identity"), i("policy_revision"), t("scan_mode"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"),
				i("run_revision"), i("last_sequence"), i("ack_sequence"), i("discovered"), i("analyzed"), i("old_warnings")},
			Cohorts: []stmt.Cohort{
				{Name: "actual_post_finish_revision", Bindings: []any{"complete", "2026-11-08T00:00:00.000000000Z", int64(0), int64(6), int64(2),
					"finish-run", inc, int64(7), "finish-library", "/media/finish", false, "finish-source", int64(3), "finish-incarnation",
					"finish-root-identity", int64(4), "first-import", int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1),
					int64(4), int64(2), int64(0)}},
				{Name: "warning_finish", Bindings: []any{"complete_with_warnings", "2026-11-08T00:00:00.000000000Z", int64(1), int64(6), int64(2),
					"finish-run", inc, int64(7), "finish-library", "/media/finish", false, "finish-source", int64(3), "finish-incarnation",
					"finish-root-identity", int64(4), "first-import", int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1),
					int64(4), int64(2), int64(0)}},
			}},
		{ID: iaFinishReceiptInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_receipts(
run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,
source_id,source_incarnation,source_revision,change_id,next_cursor,result_count,result_digest,
ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,ia_source_health)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), t("command_kind"),
				i("accepted_revision"), i("applied_ms"), t("source_id"), t("source_incarnation"), nt("source_revision"), ni("change_id"), t("next_cursor"),
				i("result_count"), b("result_digest"), t("before_key"), t("after_key"), t("phase"), i("authoritative"), i("processed"), i("analyzed"),
				i("warnings"), i("finished_ms"), t("source_health")},
			Cohorts: []stmt.Cohort{
				{Name: "new_finish_receipt", Bindings: []any{"finish-run", inc, int64(2), operation, digest, iaFinishReceiptKind, int64(6), int64(1),
					"finish-source", "finish-incarnation", nil, nil, "", int64(0), digest,
					".", ".", "complete", int64(1), int64(4), int64(2), int64(0), int64(1), "ready"}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA finish rescan"
		defs[n].CallSites = []string{"store/writer/ia_finish_rescan.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaFinishOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaFinishHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaFinishLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaFinishSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaFinishRunStateStmt:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		case iaFinishRoot:
			defs[n].Plan.PrimaryKeys = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaFinishPending:
			defs[n].Plan.Indexes = []string{"inventory_directories_pending"}
			defs[n].Plan.Seeks = []string{"job_id=? AND state=?"}
		case iaFinishBlocked:
			defs[n].Plan.Indexes = []string{"inventory_directories_pending"}
			defs[n].Plan.Seeks = []string{"job_id=? AND state=? AND relative_path>?"}
		case iaFinishProtected:
			defs[n].Plan.Indexes = []string{"inventory_scan_issues_protected"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>?"}
		case iaFinishSkipped:
			defs[n].Plan.Indexes = []string{"inventory_directories_skipped"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>?"}
		case iaFinishBlockedCharged:
			defs[n].Plan.Indexes = []string{"inventory_directories_pending", "sqlite_autoindex_inventory_scan_issues_1"}
			defs[n].Plan.Seeks = []string{"job_id=? AND state=? AND relative_path>?"}
		case iaFinishUnseen:
			defs[n].Plan.Indexes = []string{"inventory_objects_source"}
			defs[n].Plan.Seeks = []string{"source_id=? AND retired=? AND id>?"}
		case iaFinishRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority plus the finite finish
// points/CAS writes. No configuration, media-child or legacy result definition
// is composed here.
func iaFinishRescanDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaFinishRescanStatements()...)
}
