package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	scanTerminalReceiptLengths   stmt.ID = "scan.directory_terminal.receipt_lengths"
	scanTerminalBody             stmt.ID = "scan.directory_terminal.receipt_body"
	scanTerminalRunState         stmt.ID = "scan.directory_terminal.run_state"
	scanTerminalIssueConflict    stmt.ID = "scan.directory_terminal.issue_conflict"
	scanTerminalPending          stmt.ID = "scan.directory_terminal.pending"
	scanTerminalBlocked          stmt.ID = "scan.directory_terminal.blocked"
	scanTerminalProtected        stmt.ID = "scan.directory_terminal.protected"
	scanTerminalParentCAS        stmt.ID = "scan.directory_terminal.refuse_parent_cas"
	scanTerminalRefuseRunCAS     stmt.ID = "scan.directory_terminal.refuse_run_cas"
	scanTerminalTraversalCAS     stmt.ID = "scan.directory_terminal.traversal_cas"
	scanTerminalRefuseReceipt    stmt.ID = "scan.directory_terminal.refuse_receipt"
	scanTerminalTraversalReceipt stmt.ID = "scan.directory_terminal.traversal_receipt"
	scanTerminalIssueInsert      stmt.ID = "scan.directory_terminal.issue_insert"
)

// This exact point REQUIRES a new HN closed-octet entry. The existing four
// directory points do not certify these raw fields. Until HN installs/captures
// this exact definition the normal registry fails closed; no waiver/fallback.
const scanTerminalReceiptLengthsSQL = "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload),octet_length(result_directory_path),octet_length(result_directory_identity),octet_length(result_directory_revision),octet_length(result_directory_cursor),octet_length(result_directory_state),octet_length(result_inventory_completed_at),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(refused_entry_kind),octet_length(refused_directory_identity),octet_length(refused_directory_revision) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?"

func scanDirectoryTerminalDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	// Reuse actual shared definitions byte-for-byte, not aliases/registrations.
	for _, d := range scanDirectoryDefinitions() {
		switch d.ID {
		case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey,
			scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot, scanRunHistoryOwner, scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot, inventoryFreshLibrary, scanFreshSource,
			scanDirectoryRunLengths, scanDirectoryJobLengths, scanDirectoryRootLengths, scanDirectoryActive, scanDirectoryVisited:
			defs = append(defs, d)
		}
	}
	return append(defs, scanDirectoryTerminalOwnDefinitions()...)
}

func scanDirectoryTerminalOwnDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	point := func(index, seek string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
	}
	runPlan := point("sqlite_autoindex_inventory_runs_1", "job_id=?")
	rootPlan := point("sqlite_autoindex_inventory_directories_1", "job_id=? AND relative_path=?")
	receiptPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}
	nonce, op, digest := make([]byte, 16), make([]byte, 16), make([]byte, 32)
	nonce[0], op[0] = 1, 9
	tripleParams := []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")}
	triple := []any{"lifecycle-run", nonce, int64(2)}
	defs := []stmt.Definition{
		{ID: scanTerminalReceiptLengths, Kind: stmt.Read, SQL: scanTerminalReceiptLengthsSQL, Params: tripleParams, Plan: receiptPlan, Cohorts: []stmt.Cohort{{Name: "saved_terminal_bytes", Bindings: triple}}},
		{ID: scanTerminalBody, Kind: stmt.Read, SQL: `SELECT result_directory_path,result_directory_identity,result_directory_revision,result_directory_cursor,result_directory_state,result_directory_pages,result_directory_skipped,result_inventory_completed_at,result_traversal_blocked,result_traversal_protected,refused_relative_path,refusal_code,refused_entry_kind,refused_directory_identity,refused_directory_revision,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND change_id IS NULL AND next_family=0 AND next_copy_phase=0 AND next_copy_id=0 AND next_copy_aux=0 AND next_copy_key='' AND next_copy_key2='' AND next_copy_key3='' AND after_entity=0 AND expected_epoch=0 AND result_lease_owner IS NULL AND result_parent_pages IS NULL AND result_parent_skipped IS NULL`, Params: tripleParams, Plan: receiptPlan, Cohorts: []stmt.Cohort{{Name: "saved_terminal_body", Bindings: triple}}},
		{ID: scanTerminalRunState, Kind: stmt.Read, SQL: `SELECT warnings FROM inventory_runs WHERE job_id=? AND inventory_completed_at=''`, Params: []stmt.Param{text("run_id")}, Plan: runPlan, Cohorts: []stmt.Cohort{{Name: "incomplete_run", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanTerminalIssueConflict, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_scan_issues WHERE job_id=? AND relative_path=? AND reason='filename_unrepresentable' AND line=0 LIMIT 1`, Params: []stmt.Param{text("run_id"), text("display")}, Plan: point("sqlite_autoindex_inventory_scan_issues_1", "job_id=? AND relative_path=? AND reason=? AND line=?"), Cohorts: []stmt.Cohort{{Name: "same_display_any_kind", Bindings: []any{"lifecycle-run", `bad\xff`}}}},
		{ID: scanTerminalPending, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_directories WHERE job_id=? AND state='pending' LIMIT 1`, Params: []stmt.Param{text("run_id")}, Plan: point("inventory_directories_pending", "job_id=? AND state=?"), Cohorts: []stmt.Cohort{{Name: "pending_frontier", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanTerminalBlocked, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_directories WHERE job_id=? AND state='blocked' LIMIT 1`, Params: []stmt.Param{text("run_id")}, Plan: point("inventory_directories_pending", "job_id=? AND state=?"), Cohorts: []stmt.Cohort{{Name: "blocked_frontier", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanTerminalProtected, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_scan_issues WHERE job_id=? AND protect=1 LIMIT 1`, Params: []stmt.Param{text("run_id")}, Plan: point("inventory_scan_issues_protected", "job_id=?"), Cohorts: []stmt.Cohort{{Name: "protected_frontier", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanTerminalParentCAS, Kind: stmt.Write, SQL: `UPDATE inventory_directories SET cursor=?,pages=?,skipped=? WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending' RETURNING relative_path,identity,revision,cursor,state,pages,skipped`, Params: []stmt.Param{text("next"), integer("next_pages"), integer("next_skipped"), text("run_id"), text("path"), text("identity"), text("revision"), text("expected"), integer("pages"), integer("skipped")}, Plan: rootPlan, Cohorts: []stmt.Cohort{{Name: "refuse_parent", Bindings: []any{"42", int64(1), int64(1), "lifecycle-run", ".", "actual-directory", "directory-revision", "", int64(0), int64(0)}}}},
	}
	casParams := []stmt.Param{integer("next_revision"), integer("next_sequence"), text("run_id"), blob("incarnation"), integer("revision"), integer("last"), integer("ack"), integer("owner"), integer("generation"), integer("expiry")}
	casArgs := []any{int64(2), int64(2), "lifecycle-run", nonce, int64(1), int64(1), int64(1), int64(11), int64(1), int64(4000000000000)}
	fence := ` WHERE job_id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND phase='inventory' AND authoritative=0 AND root_rebound=0 AND finished_at='' AND inventory_completed_at=''`
	casPlan := point("sqlite_autoindex_inventory_runs_2", "job_id=? AND command_incarnation=?")
	defs = append(defs,
		stmt.Definition{ID: scanTerminalRefuseRunCAS, Kind: stmt.Write, SQL: `UPDATE inventory_runs SET warnings=?,job_revision=?,last_command_sequence=?` + fence + ` AND warnings=? RETURNING job_revision,last_command_sequence,warnings`, Params: append(append([]stmt.Param{integer("next_warnings")}, casParams...), integer("warnings")), Plan: casPlan, Cohorts: []stmt.Cohort{{Name: "warning_cas", Bindings: append(append([]any{int64(1)}, casArgs...), int64(0))}}},
		stmt.Definition{ID: scanTerminalTraversalCAS, Kind: stmt.Write, SQL: `UPDATE inventory_runs SET inventory_completed_at=?,job_revision=?,last_command_sequence=?,phase='verifying',authoritative=0` + fence + ` RETURNING job_revision,last_command_sequence,inventory_completed_at`, Params: append([]stmt.Param{text("completed_at")}, casParams...), Plan: casPlan, Cohorts: []stmt.Cohort{{Name: "listing_only_cas", Bindings: append([]any{"2026-11-07T00:00:00.123456789Z"}, casArgs...)}}},
	)
	commonCols := `run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest`
	commonParams := []stmt.Param{text("run_id"), blob("incarnation"), integer("sequence"), blob("operation"), blob("request"), text("kind"), integer("revision"), integer("applied_ms"), text("source_id"), text("source_incarnation"), {Name: "source_revision", Type: stmt.Text, Nullable: true}, text("next_cursor"), integer("result_count"), blob("digest")}
	commonArgs := []any{"lifecycle-run", nonce, int64(2), op, digest, "inventory_directory_refuse", int64(2), int64(1000), "lifecycle-source", "source-incarnation", nil, "42", int64(0), digest}
	refuseParams := append(append([]stmt.Param(nil), commonParams...), blob("raw_path"), text("refusal_code"), text("entry_kind"), blob("child_identity"), blob("child_revision"), text("cursor_kind"), blob("parent_path"), blob("parent_identity"), blob("parent_revision"), blob("expected"), blob("next"))
	refuseArgs := append(append([]any(nil), commonArgs...), []byte{'b', 0xff}, "filename_unrepresentable", "directory", []byte("bad-child"), []byte("child-revision"), inventoryLinuxCursor, []byte("."), []byte("actual-directory"), []byte("directory-revision"), []byte{}, []byte("42"))
	traversalParams := append(append([]stmt.Param(nil), commonParams...), text("root_path"), text("root_identity"), text("root_revision"), text("root_cursor"), text("root_state"), integer("root_pages"), integer("root_skipped"), text("completed_at"), integer("blocked"), integer("protected"))
	traversalArgs := append(append([]any(nil), commonArgs...), ".", "actual-directory", "directory-revision", "42", "done", int64(1), int64(0), "2026-11-07T00:00:00.123456789Z", int64(0), int64(0))
	traversalArgs[5], traversalArgs[11] = "inventory_traversal_complete", ""
	insert := func(id stmt.ID, cols string, params []stmt.Param, args []any) stmt.Definition {
		return stmt.Definition{ID: id, Kind: stmt.Write, SQL: `INSERT INTO scan_command_receipts(` + cols + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(params)), ",") + `)`, Params: params, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "terminal_scalar", Bindings: args}}}
	}
	defs = append(defs,
		insert(scanTerminalRefuseReceipt, commonCols+`,refused_relative_path,refusal_code,refused_entry_kind,refused_directory_identity,refused_directory_revision,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload`, refuseParams, refuseArgs),
		insert(scanTerminalTraversalReceipt, commonCols+`,result_directory_path,result_directory_identity,result_directory_revision,result_directory_cursor,result_directory_state,result_directory_pages,result_directory_skipped,result_inventory_completed_at,result_traversal_blocked,result_traversal_protected`, traversalParams, traversalArgs),
		stmt.Definition{ID: scanTerminalIssueInsert, Kind: stmt.Write, SQL: `INSERT INTO inventory_scan_issues(job_id,relative_path,reason,line,protect,warning,refused_entry_kind,refusal_operation_id) VALUES(?,?,'filename_unrepresentable',0,1,1,'directory',?) RETURNING relative_path,reason,protect,warning,refused_entry_kind,refusal_operation_id`, Params: []stmt.Param{text("run_id"), text("display"), blob("operation")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "receipt_pinned_directory", Bindings: []any{"lifecycle-run", `b\xff`, op}}}},
	)
	for i := range defs {
		defs[i].Owner = "store/writer directory terminal lifecycle"
		defs[i].CallSites = []string{"store/writer/scan_directory_terminal_effects.go", "store/writer/scan_directory_terminal_history.go"}
	}
	return defs
}

// Whole fresh turn, including original history and final stored readback.
// No header/member deletion statements belong to these floor-only ACKs.
func directoryTerminalStatementMaxUses(id stmt.ID) int {
	switch id {
	case scanTerminalReceiptLengths, scanTerminalBody:
		return 2
	case scanTerminalRunState, scanTerminalIssueConflict, scanTerminalPending, scanTerminalBlocked, scanTerminalProtected, scanTerminalParentCAS, scanTerminalRefuseRunCAS, scanTerminalTraversalCAS, scanTerminalRefuseReceipt, scanTerminalTraversalReceipt, scanTerminalIssueInsert:
		return 1
	case scanDirectoryRootLengths:
		return 3 // root before, parent before, root after
	case scanLifecycleRoot:
		return 3
	default:
		return directoryStatementMaxUses(id)
	}
}
