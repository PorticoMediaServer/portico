package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-19 missing-window statement family. Every read is a point read or one
// bounded indexed window over the accepted run's own directory/object frontier;
// every write is one finite CAS on the exact inventory row or the exact group C
// draft receipt/member relation. There is no OFFSET, temporary sort,
// library-wide scan or caller-selected SQL. The window is keyset-paged by object
// id and capped at iaMissingMaxRows, so one turn can only change that many rows.
const (
	iaMissingOwner          stmt.ID = "ia.missing.owner"
	iaMissingHeader         stmt.ID = "ia.missing.header"
	iaMissingMembers        stmt.ID = "ia.missing.members"
	iaMissingLibrary        stmt.ID = "ia.missing.library"
	iaMissingSource         stmt.ID = "ia.missing.source"
	iaMissingSealRow        stmt.ID = "ia.missing.seal"
	iaMissingCoverage       stmt.ID = "ia.missing.coverage"
	iaMissingPending        stmt.ID = "ia.missing.pending"
	iaMissingBlocked        stmt.ID = "ia.missing.blocked"
	iaMissingProtected      stmt.ID = "ia.missing.protected"
	iaMissingSkipped        stmt.ID = "ia.missing.skipped"
	iaMissingWindowStmt     stmt.ID = "ia.missing.window"
	iaMissingMark           stmt.ID = "ia.missing.mark"
	iaMissingRunCAS         stmt.ID = "ia.missing.run_cas"
	iaMissingReceiptInsert  stmt.ID = "ia.missing.receipt_insert"
	iaMissingMemberInsert   stmt.ID = "ia.missing.member_insert"
	iaMissingReceiptKind            = "inventory_missing"
	iaMissingDisposition            = "missing"
	iaMissingMaxRows                = 8
	iaMissingMaxWindowBytes         = 256 << 10
)

func iaMissingStatements() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	boolean := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Boolean} }
	inc, operation, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	key := []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence")}
	subtree := []stmt.Param{t("run_id"), t("subtree_low"), t("subtree_high")}
	defs := []stmt.Definition{
		{ID: iaMissingOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"missing-run"}},
				{Name: "missing_owner", Bindings: []any{"missing-none"}},
			}},
		{ID: iaMissingHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,
source_id,source_incarnation,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,
ia_finished_ms,ia_source_health
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_missing_receipt", Bindings: []any{"missing-run", inc, int64(3)}},
				{Name: "missing_receipt", Bindings: []any{"missing-run", inc, int64(9)}},
			}},
		{ID: iaMissingMembers, Kind: stmt.Read,
			SQL: `SELECT ordinal,object_id,before_revision,after_revision,before_path,after_path
FROM scan_reconcile_members
WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='inventory_missing'
ORDER BY ordinal LIMIT ?`,
			Params: append(append([]stmt.Param{}, key...), i("row_limit")),
			Cohorts: []stmt.Cohort{
				{Name: "saved_missing_members", Bindings: []any{"missing-run", inc, int64(3), int64(iaMissingMaxRows + 1)}},
				{Name: "missing_members", Bindings: []any{"missing-run", inc, int64(9), int64(iaMissingMaxRows + 1)}},
			}},
		{ID: iaMissingLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "missing-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "missing-retired"}},
			}},
		{ID: iaMissingSource, Kind: stmt.Read,
			SQL: `SELECT id,health FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"missing-source", "missing-library", int64(3), "missing-incarnation", "missing-root-identity", "/media/missing", false}},
				{Name: "offline_source", Bindings: []any{"missing-source", "missing-library", int64(3), "missing-incarnation", "missing-root-identity", "/media/missing", false}},
			}},
		{ID: iaMissingSealRow, Kind: stmt.Read,
			SQL:    `SELECT identity,revision,cursor,state,pages,skipped FROM inventory_directories WHERE job_id=? AND relative_path=?`,
			Params: []stmt.Param{t("run_id"), t("relative_path")},
			Cohorts: []stmt.Cohort{
				{Name: "sealed_directory", Bindings: []any{"missing-run", "Missing"}},
				{Name: "missing_directory", Bindings: []any{"missing-run", "Missing/Absent"}},
			}},
		{ID: iaMissingCoverage, Kind: stmt.Read,
			SQL: `SELECT command_kind,ia_verified_directory,ia_verified_identity,ia_verified_revision,ia_phase,ia_authoritative,
ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,source_id,source_incarnation
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_coverage_receipt", Bindings: []any{"missing-run", inc, int64(2)}},
				{Name: "missing_coverage_receipt", Bindings: []any{"missing-run", inc, int64(1)}},
			}},
		{ID: iaMissingPending, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending
WHERE job_id=? AND state='pending' AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "partial_subtree", Bindings: []any{"missing-run", "Missing/", "Missing0"}},
				{Name: "sealed_subtree", Bindings: []any{"missing-run", "Root/", "Root0"}},
			}},
		{ID: iaMissingBlocked, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending
WHERE job_id=? AND state='blocked' AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "unreadable_subtree", Bindings: []any{"missing-run", "Missing/", "Missing0"}},
				{Name: "sealed_subtree", Bindings: []any{"missing-run", "Root/", "Root0"}},
			}},
		{ID: iaMissingProtected, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_scan_issues INDEXED BY inventory_scan_issues_protected
WHERE job_id=? AND protect=1 AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "refused_subtree", Bindings: []any{"missing-run", "Missing/", "Missing0"}},
				{Name: "clean_subtree", Bindings: []any{"missing-run", "Root/", "Root0"}},
			}},
		// A skip anywhere in the verified subtree can never authorize absence. This
		// point is re-read in the mark transaction immediately before the first
		// missing effect, so a late skip still blocks new marks.
		{ID: iaMissingSkipped, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_skipped
WHERE job_id=? AND skipped>0 AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "skipped_subtree", Bindings: []any{"missing-run", "Missing/", "Missing0"}},
				{Name: "clean_subtree", Bindings: []any{"missing-run", "Root/", "Root0"}},
			}},
		{ID: iaMissingWindowStmt, Kind: stmt.Read,
			SQL: `SELECT id,asset_id,revision,relative_path,missing_since,missing_observations,last_missing_job
FROM inventory_objects INDEXED BY inventory_objects_directory
WHERE source_id=? AND root_incarnation=? AND directory_path=? AND retired=0 AND state='available' AND id>?
AND seen_job<>? AND missing_observations>=?
ORDER BY id LIMIT ?`,
			Params: []stmt.Param{t("source_id"), t("incarnation"), t("directory_path"), t("before_key"), t("run_id"), i("min_observations"), i("row_limit")},
			Cohorts: []stmt.Cohort{
				{Name: "unseen_window", Bindings: []any{"missing-source", "missing-incarnation", "Missing", "", "missing-run", int64(0), int64(iaMissingMaxRows)}},
				{Name: "skipped_window", Bindings: []any{"missing-source", "missing-incarnation", "Missing/Skipped", "", "missing-run", int64(1), int64(iaMissingMaxRows)}},
			}},
		{ID: iaMissingMark, Kind: stmt.Write,
			SQL: `UPDATE inventory_objects SET state='missing',missing_since=?,missing_observations=?,last_missing_job=?
WHERE id=? AND source_id=? AND asset_id=? AND root_incarnation=? AND relative_path=? AND revision=? AND directory_path=?
AND state='available' AND retired=0 AND seen_job<>? AND missing_since=? AND missing_observations=? AND last_missing_job=?
RETURNING id`,
			Params: []stmt.Param{t("missing_since"), i("observations"), t("last_missing_job"), t("object_id"), t("source_id"), t("asset_token"),
				t("incarnation"), t("relative_path"), t("revision"), t("directory_path"), t("run_id"), t("old_missing_since"),
				i("old_observations"), t("old_last_missing_job")},
			Cohorts: []stmt.Cohort{
				{Name: "first_missing_mark", Bindings: []any{"2026-11-08T00:00:00.000000000Z", int64(1), "missing-run", "object:missing:1", "missing-source", "missing-asset-1",
					"missing-incarnation", "Missing/One.mkv", "object-revision:1", "Missing", "missing-run", "", int64(0), ""}},
			}},
		{ID: iaMissingRunCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_runs SET job_revision=?,last_command_sequence=?,authoritative=1
WHERE job_id=? AND command_incarnation=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=?
AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase='reconciling' AND scan_mode=?
AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND authoritative IN(0,1) AND root_rebound=0 AND finished_at='' AND inventory_completed_at<>''
RETURNING job_revision,last_command_sequence,authoritative`,
			Params: []stmt.Param{i("next_revision"), i("next_sequence"), t("run_id"), b("command_incarnation"), i("library_id"), t("library_key"),
				t("resolved_root"), boolean("follow_symlinks"), t("source_id"), i("generation"), t("incarnation"), t("root_identity"),
				i("policy_revision"), t("scan_mode"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"),
				i("run_revision"), i("last_sequence"), i("ack_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "actual_post_missing_revision", Bindings: []any{int64(6), int64(3), "missing-run", inc, int64(7), "missing-library", "/media/missing", false,
					"missing-source", int64(3), "missing-incarnation", "missing-root-identity", int64(4), "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(2), int64(2)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(3), "missing-run", inc, int64(7), "missing-library", "/media/missing", false,
					"missing-source", int64(3), "missing-incarnation", "missing-root-identity", int64(4), "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(2), int64(1)}},
			}},
		{ID: iaMissingReceiptInsert, Kind: stmt.Write,
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
				{Name: "new_missing_receipt", Bindings: []any{"missing-run", inc, int64(3), operation, digest, iaMissingReceiptKind, int64(6), int64(1),
					"missing-source", "missing-incarnation", nil, nil, "", int64(1), digest,
					"", "object:missing:1", "reconciling", int64(1), int64(1), int64(1), int64(0), int64(0), "ready"}},
			}},
		{ID: iaMissingMemberInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_reconcile_members(
run_id,command_incarnation,command_sequence,command_kind,ordinal,object_id,prior_object_id,
before_revision,after_revision,before_path,after_path,disposition)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), t("command_kind"), i("ordinal"),
				t("object_id"), nt("prior_object_id"), t("before_revision"), t("after_revision"), t("before_path"), t("after_path"), t("disposition")},
			Cohorts: []stmt.Cohort{
				{Name: "new_missing_member", Bindings: []any{"missing-run", inc, int64(3), iaMissingReceiptKind, int64(0), "object:missing:1", nil,
					"object-revision:1", "object-revision:1", "Missing/One.mkv", "Missing/One.mkv", iaMissingDisposition}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA missing window"
		defs[n].CallSites = []string{"store/writer/ia_missing.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaMissingOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaMissingHeader, iaMissingCoverage:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaMissingMembers:
			defs[n].Plan.PrimaryKeys = []string{"scan_reconcile_members"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=?"}
		case iaMissingLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaMissingSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaMissingSealRow:
			defs[n].Plan.PrimaryKeys = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaMissingPending, iaMissingBlocked:
			defs[n].Plan.Indexes = []string{"inventory_directories_pending"}
			defs[n].Plan.Seeks = []string{"job_id=? AND state=? AND relative_path>? AND relative_path<?"}
		case iaMissingProtected:
			defs[n].Plan.Indexes = []string{"inventory_scan_issues_protected"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>? AND relative_path<?"}
		case iaMissingSkipped:
			defs[n].Plan.Indexes = []string{"inventory_directories_skipped"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>? AND relative_path<?"}
		case iaMissingWindowStmt:
			defs[n].Plan.Indexes = []string{"inventory_objects_directory"}
			defs[n].Plan.Seeks = []string{"source_id=? AND root_incarnation=? AND directory_path=? AND retired=? AND id>?"}
		case iaMissingMark:
			defs[n].Plan.PrimaryKeys = []string{"sqlite_autoindex_inventory_objects_1"}
			defs[n].Plan.Seeks = []string{"id=?"}
		case iaMissingRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority plus the finite missing
// points/CAS writes. No configuration, media-child or legacy result definition
// is composed here.
func iaMissingDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaMissingStatements()...)
}
