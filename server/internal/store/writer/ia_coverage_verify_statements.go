package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-18 statement family. Every statement is a point read, a bounded indexed
// window over one job's sealed directory frontier or one finite CAS/insert on the
// exact group C draft receipt relation and the embedded inventory relations. There
// is no OFFSET, temporary sort, library-wide scan or caller-selected SQL. The
// coverage proof reads only the accepted run's own directory/issue frontier.
const (
	iaCoverageOwner         stmt.ID = "ia.coverage.owner"
	iaCoverageHeader        stmt.ID = "ia.coverage.header"
	iaCoverageLibrary       stmt.ID = "ia.coverage.library"
	iaCoverageSource        stmt.ID = "ia.coverage.source"
	iaCoverageSeal          stmt.ID = "ia.coverage.seal"
	iaCoveragePending       stmt.ID = "ia.coverage.pending"
	iaCoverageBlocked       stmt.ID = "ia.coverage.blocked"
	iaCoverageProtected     stmt.ID = "ia.coverage.protected"
	iaCoverageSkipped       stmt.ID = "ia.coverage.skipped"
	iaCoverageRunCAS        stmt.ID = "ia.coverage.run_cas"
	iaCoverageReceiptInsert stmt.ID = "ia.coverage.receipt_insert"
)

const iaCoverageReceiptKind = "inventory_verify"

func iaCoverageVerifyStatements() []stmt.Definition {
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
		{ID: iaCoverageOwner, Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM inventory_runs WHERE job_id=?`,
			Params: []stmt.Param{t("run_id")},
			Cohorts: []stmt.Cohort{
				{Name: "current_owner", Bindings: []any{"coverage-run"}},
				{Name: "missing_owner", Bindings: []any{"coverage-missing"}},
			}},
		{ID: iaCoverageHeader, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind,result_count,result_digest,accepted_revision,applied_ms,
source_id,source_incarnation,ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,
ia_finished_ms,ia_source_health,ia_verified_directory,ia_verified_identity,ia_verified_revision
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key,
			Cohorts: []stmt.Cohort{
				{Name: "saved_coverage_receipt", Bindings: []any{"coverage-run", inc, int64(2)}},
				{Name: "missing_receipt", Bindings: []any{"coverage-missing", inc, int64(2)}},
			}},
		{ID: iaCoverageLibrary, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_libraries WHERE id=? AND library_id=? AND retired=0`,
			Params: []stmt.Param{i("library_id"), t("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "live_library", Bindings: []any{int64(7), "coverage-library"}},
				{Name: "retired_library", Bindings: []any{int64(8), "coverage-retired"}},
			}},
		{ID: iaCoverageSource, Kind: stmt.Read,
			SQL: `SELECT id,health FROM library_sources
WHERE id=? AND library_id=? AND generation=? AND incarnation=? AND root_identity=? AND root=? AND follow_symlinks=?
AND kind='local' AND classification='local' AND enabled=1 AND identity_confirmed=1 AND health='ready' AND remote_source_id IS NULL`,
			Params: []stmt.Param{t("source_id"), t("library_key"), i("generation"), t("incarnation"), t("root_identity"), t("resolved_root"), boolean("follow_symlinks")},
			Cohorts: []stmt.Cohort{
				{Name: "current_source", Bindings: []any{"coverage-source", "coverage-library", int64(3), "coverage-incarnation", "coverage-root-identity", "/media/coverage", false}},
				{Name: "stale_source", Bindings: []any{"coverage-source", "coverage-library", int64(4), "coverage-incarnation", "coverage-root-identity", "/media/coverage", false}},
			}},
		{ID: iaCoverageSeal, Kind: stmt.Read,
			SQL:    `SELECT identity,revision,cursor,state,pages,skipped FROM inventory_directories WHERE job_id=? AND relative_path=?`,
			Params: []stmt.Param{t("run_id"), t("relative_path")},
			Cohorts: []stmt.Cohort{
				{Name: "sealed_directory", Bindings: []any{"coverage-run", "Coverage"}},
				{Name: "missing_directory", Bindings: []any{"coverage-run", "Coverage/Missing"}},
			}},
		{ID: iaCoveragePending, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending
WHERE job_id=? AND state='pending' AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "partial_subtree", Bindings: []any{"coverage-run", "Coverage/", "Coverage0"}},
				{Name: "sealed_subtree", Bindings: []any{"coverage-run", "Root/", "Root0"}},
			}},
		{ID: iaCoverageBlocked, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_pending
WHERE job_id=? AND state='blocked' AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "unreadable_subtree", Bindings: []any{"coverage-run", "Coverage/", "Coverage0"}},
				{Name: "sealed_subtree", Bindings: []any{"coverage-run", "Root/", "Root0"}},
			}},
		{ID: iaCoverageProtected, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_scan_issues INDEXED BY inventory_scan_issues_protected
WHERE job_id=? AND protect=1 AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "refused_subtree", Bindings: []any{"coverage-run", "Coverage/", "Coverage0"}},
				{Name: "clean_subtree", Bindings: []any{"coverage-run", "Root/", "Root0"}},
			}},
		// Absence fails closed unless the whole sealed subtree is skip-free. The
		// statement forces the bounded INT-requested index; on a schema without it
		// preparation fails and the caller holds instead of accepting coverage.
		{ID: iaCoverageSkipped, Kind: stmt.Read,
			SQL: `SELECT 1 FROM inventory_directories INDEXED BY inventory_directories_skipped
WHERE job_id=? AND skipped>0 AND relative_path>=? AND relative_path<? LIMIT 1`,
			Params: subtree,
			Cohorts: []stmt.Cohort{
				{Name: "skipped_subtree", Bindings: []any{"coverage-run", "Coverage/", "Coverage0"}},
				{Name: "clean_subtree", Bindings: []any{"coverage-run", "Root/", "Root0"}},
			}},
		{ID: iaCoverageRunCAS, Kind: stmt.Write,
			SQL: `UPDATE inventory_runs SET job_revision=?,last_command_sequence=?,phase='reconciling'
WHERE job_id=? AND command_incarnation=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=?
AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase='verifying' AND scan_mode=?
AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=?
AND authoritative=0 AND root_rebound=0 AND finished_at=''
RETURNING job_revision,last_command_sequence,phase`,
			Params: []stmt.Param{i("next_revision"), i("next_sequence"), t("run_id"), b("command_incarnation"), i("library_id"), t("library_key"),
				t("resolved_root"), boolean("follow_symlinks"), t("source_id"), i("generation"), t("incarnation"), t("root_identity"),
				i("policy_revision"), t("scan_mode"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"),
				i("run_revision"), i("last_sequence"), i("ack_sequence")},
			Cohorts: []stmt.Cohort{
				{Name: "actual_post_verify_revision", Bindings: []any{int64(6), int64(2), "coverage-run", inc, int64(7), "coverage-library", "/media/coverage", false,
					"coverage-source", int64(3), "coverage-incarnation", "coverage-root-identity", int64(4), "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(1)}},
				{Name: "stale_run_cas", Bindings: []any{int64(6), int64(2), "coverage-run", inc, int64(7), "coverage-library", "/media/coverage", false,
					"coverage-source", int64(3), "coverage-incarnation", "coverage-root-identity", int64(4), "first-import",
					int64(11), int64(13), int64(4000000000000), int64(5), int64(1), int64(0)}},
			}},
		{ID: iaCoverageReceiptInsert, Kind: stmt.Write,
			SQL: `INSERT INTO scan_command_receipts(
run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,
source_id,source_incarnation,source_revision,change_id,next_cursor,result_count,result_digest,
ia_before_key,ia_after_key,ia_phase,ia_authoritative,ia_processed,ia_analyzed,ia_warnings,ia_finished_ms,ia_source_health,
ia_verified_directory,ia_verified_identity,ia_verified_revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence"), b("operation_id"), b("request_digest"), t("command_kind"),
				i("accepted_revision"), i("applied_ms"), t("source_id"), t("source_incarnation"), nt("source_revision"), ni("change_id"), t("next_cursor"),
				i("result_count"), b("result_digest"), t("before_key"), t("after_key"), t("phase"), i("authoritative"), i("processed"), i("analyzed"),
				i("warnings"), i("finished_ms"), t("source_health"), t("verified_directory"), b("verified_identity"), b("verified_revision")},
			Cohorts: []stmt.Cohort{
				{Name: "new_coverage_receipt", Bindings: []any{"coverage-run", inc, int64(2), operation, digest, iaCoverageReceiptKind, int64(6), int64(1),
					"coverage-source", "coverage-incarnation", nil, nil, "", int64(0), digest,
					"Coverage", "Coverage", "reconciling", int64(0), int64(2), int64(2), int64(0), int64(0), "ready",
					"Coverage", []byte("coverage-directory-identity"), []byte("coverage-directory-revision")}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA coverage verify"
		defs[n].CallSites = []string{"store/writer/ia_coverage_verify.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaCoverageOwner:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_1"}
			defs[n].Plan.Seeks = []string{"job_id=?"}
		case iaCoverageHeader:
			defs[n].Plan.PrimaryKeys = []string{"scan_command_receipts"}
			defs[n].Plan.Seeks = []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}
		case iaCoverageLibrary:
			defs[n].Plan.PrimaryKeys = []string{"catalog_libraries"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaCoverageSource:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_library_sources_2"}
			defs[n].Plan.Seeks = []string{"library_id=? AND root=?"}
		case iaCoverageSeal:
			defs[n].Plan.PrimaryKeys = []string{"sqlite_autoindex_inventory_directories_1"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path=?"}
		case iaCoveragePending, iaCoverageBlocked:
			defs[n].Plan.Indexes = []string{"inventory_directories_pending"}
			defs[n].Plan.Seeks = []string{"job_id=? AND state=? AND relative_path>? AND relative_path<?"}
		case iaCoverageProtected:
			defs[n].Plan.Indexes = []string{"inventory_scan_issues_protected"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>? AND relative_path<?"}
		case iaCoverageSkipped:
			defs[n].Plan.Indexes = []string{"inventory_directories_skipped"}
			defs[n].Plan.Seeks = []string{"job_id=? AND relative_path>? AND relative_path<?"}
		case iaCoverageRunCAS:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_inventory_runs_2"}
			defs[n].Plan.Seeks = []string{"job_id=? AND command_incarnation=?"}
		}
	}
	return defs
}

// Complete private family: current US owner authority plus the finite coverage
// points/CAS writes. No configuration, media-child or legacy result definition is
// composed here.
func iaCoverageVerifyDefinitions() []stmt.Definition {
	return append(usAuthorityDefinitions(), iaCoverageVerifyStatements()...)
}
