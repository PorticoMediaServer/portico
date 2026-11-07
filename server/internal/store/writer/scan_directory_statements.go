package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	scanDirectoryActive         stmt.ID = "scan.directory.active"
	scanDirectoryRunLengths     stmt.ID = "scan.directory.run_lengths"
	scanDirectoryJobLengths     stmt.ID = "scan.directory.job_lengths"
	scanDirectoryRootLengths    stmt.ID = "scan.directory.root_lengths"
	scanDirectoryReceiptLengths stmt.ID = "scan.directory.receipt_lengths"
	scanDirectoryBody           stmt.ID = "scan.directory.receipt_body"
	scanDirectoryPath           stmt.ID = "scan.directory.path"
	scanDirectoryVisited        stmt.ID = "scan.directory.visited"
	scanDirectoryInsert         stmt.ID = "scan.directory.insert_child"
	scanDirectoryDiscoverCAS    stmt.ID = "scan.directory.discover_cas"
	scanDirectoryEOFCAS         stmt.ID = "scan.directory.eof_cas"
	scanDirectoryRunCAS         stmt.ID = "scan.directory.run_cas"
	scanDirectoryReceiptInsert  stmt.ID = "scan.directory.receipt_insert"
)

// Exact shared SQL definitions are reused, not restated or re-registered under
// aliases. Their finite union is installed once in W's existing registry.
func scanDirectoryDefinitions() []stmt.Definition {
	defs := inventoryDisclosureDefinitions()
	for _, d := range scanReceiptDefinitions() {
		switch d.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey:
			defs = append(defs, d)
		}
	}
	for _, d := range scanRunLifecycleDefinitions() {
		switch d.ID {
		case scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot:
			defs = append(defs, d)
		}
	}
	for _, d := range scanRunLifecycleHistoryDefinitions() {
		if d.ID == scanRunHistoryOwner {
			defs = append(defs, d)
		}
	}
	defs = append(defs, scanSourceDefinitions()...)
	for _, d := range scanFreshDefinitions() {
		if d.ID == scanFreshSource {
			defs = append(defs, d)
		}
	}
	return append(defs, scanDirectoryOwnDefinitions()...)
}

func scanDirectoryOwnDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nullableInteger := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nullableText := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	point := func(index, seek string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
	}
	rootPlan := point("sqlite_autoindex_inventory_directories_1", "job_id=? AND relative_path=?")
	runPlan := point("sqlite_autoindex_inventory_runs_1", "job_id=?")
	receiptPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}
	nonce := make([]byte, 16)
	nonce[0] = 1
	operation := make([]byte, 16)
	operation[0] = 9
	digest := make([]byte, 32)
	tripleParams := []stmt.Param{text("run_id"), blob("command_incarnation"), integer("command_sequence")}
	triple := []any{"lifecycle-run", nonce, int64(2)}
	sumBytes := func(fields string) string {
		a := strings.Split(fields, ",")
		for i, s := range a {
			a[i] = "octet_length(" + s + ")"
		}
		return strings.Join(a, ",")
	}
	runFields := "job_id,original_library_key,original_resolved_root,source_id,root_incarnation,root_identity,phase,finished_at,scan_mode"
	receiptFields := "source_id,source_incarnation,source_revision,next_cursor,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload,result_directory_path,result_directory_identity,result_directory_revision,result_directory_cursor,result_directory_state"
	defs := []stmt.Definition{
		{ID: scanDirectoryActive, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_source_active WHERE source_id=? AND job_id=?`, Params: []stmt.Param{text("source_id"), text("run_id")}, Plan: point("sqlite_autoindex_inventory_source_active_2", "job_id=?"), Cohorts: []stmt.Cohort{{Name: "active_run", Bindings: []any{"lifecycle-source", "lifecycle-run"}}, {Name: "wrong_run", Bindings: []any{"lifecycle-source", "other"}}}},
		{ID: scanDirectoryRunLengths, Kind: stmt.Read, SQL: `SELECT ` + sumBytes(runFields) + ` FROM inventory_runs WHERE job_id=?`, Params: []stmt.Param{text("run_id")}, Plan: runPlan, Cohorts: []stmt.Cohort{{Name: "original_bytes", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanDirectoryJobLengths, Kind: stmt.Read, SQL: `SELECT ` + sumBytes("library_id,status,created_at,error") + ` FROM jobs WHERE id=?`, Params: []stmt.Param{text("run_id")}, Plan: point("sqlite_autoindex_jobs_1", "id=?"), Cohorts: []stmt.Cohort{{Name: "job_bytes", Bindings: []any{"lifecycle-run"}}}},
		{ID: scanDirectoryRootLengths, Kind: stmt.Read, SQL: `SELECT ` + sumBytes("identity,revision,cursor,state") + ` FROM inventory_directories WHERE job_id=? AND relative_path=?`, Params: []stmt.Param{text("run_id"), text("path")}, Plan: rootPlan, Cohorts: []stmt.Cohort{{Name: "root_bytes", Bindings: []any{"lifecycle-run", "."}}}},
		{ID: scanDirectoryReceiptLengths, Kind: stmt.Read, SQL: `SELECT ` + sumBytes(receiptFields) + ` FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`, Params: tripleParams, Plan: receiptPlan, Cohorts: []stmt.Cohort{{Name: "saved_bytes", Bindings: triple}}},
		{ID: scanDirectoryBody, Kind: stmt.Read, SQL: `SELECT result_directory_path,result_directory_identity,result_directory_revision,result_directory_cursor,result_directory_state,result_directory_pages,result_directory_skipped,result_parent_pages,result_parent_skipped,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`, Params: tripleParams, Plan: receiptPlan, Cohorts: []stmt.Cohort{{Name: "saved_discovery", Bindings: triple}}},
		{ID: scanDirectoryPath, Kind: stmt.Read, SQL: `SELECT relative_path FROM inventory_directories WHERE job_id=? AND relative_path=?`, Params: []stmt.Param{text("run_id"), text("child_path")}, Plan: rootPlan, Cohorts: []stmt.Cohort{{Name: "absent_child", Bindings: []any{"lifecycle-run", "child"}}, {Name: "existing_path", Bindings: []any{"lifecycle-run", "."}}}},
		{ID: scanDirectoryVisited, Kind: stmt.Read, SQL: `SELECT identity FROM inventory_directories INDEXED BY inventory_directories_visited WHERE job_id=? AND identity=? AND identity!='' LIMIT 1`, Params: []stmt.Param{text("run_id"), text("identity")}, Plan: point("inventory_directories_visited", "job_id=? AND identity=?"), Cohorts: []stmt.Cohort{{Name: "new_identity", Bindings: []any{"lifecycle-run", "new-directory"}}, {Name: "identity_alias", Bindings: []any{"lifecycle-run", "actual-directory"}}}},
		{ID: scanDirectoryInsert, Kind: stmt.Write, SQL: `INSERT INTO inventory_directories(job_id,relative_path,identity,revision) VALUES(?,?,?,?) RETURNING relative_path,identity,revision,cursor,state,pages,skipped`, Params: []stmt.Param{text("run_id"), text("child_path"), text("identity"), text("revision")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_child", Bindings: []any{"lifecycle-run", "child", "new-directory", "new-revision"}}}},
	}
	directoryParams := []stmt.Param{text("run_id"), text("path"), text("identity"), text("revision"), text("cursor"), integer("pages"), integer("skipped")}
	directoryArgs := []any{"lifecycle-run", ".", "actual-directory", "directory-revision", "", int64(0), int64(0)}
	for _, x := range []struct {
		id    stmt.ID
		state string
	}{{scanDirectoryDiscoverCAS, "pending"}, {scanDirectoryEOFCAS, "done"}} {
		defs = append(defs, stmt.Definition{ID: x.id, Kind: stmt.Write, SQL: `UPDATE inventory_directories SET cursor=?,pages=?,state='` + x.state + `' WHERE job_id=? AND relative_path=? AND identity=? AND revision=? AND cursor=? AND pages=? AND skipped=? AND state='pending' RETURNING relative_path,identity,revision,cursor,state,pages,skipped`, Params: append([]stmt.Param{text("next_cursor"), integer("next_pages")}, directoryParams...), Plan: rootPlan, Cohorts: []stmt.Cohort{{Name: "pending_cas", Bindings: append([]any{"42", int64(1)}, directoryArgs...)}, {Name: "stale_cursor", Bindings: []any{"42", int64(1), "lifecycle-run", ".", "actual-directory", "directory-revision", "stale", int64(0), int64(0)}}}})
	}
	defs = append(defs, stmt.Definition{ID: scanDirectoryRunCAS, Kind: stmt.Write, SQL: `UPDATE inventory_runs SET job_revision=?,last_command_sequence=? WHERE job_id=? AND command_incarnation=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND phase='inventory' AND authoritative=0 AND root_rebound=0 AND finished_at='' RETURNING job_revision,last_command_sequence`, Params: []stmt.Param{integer("next_revision"), integer("next_sequence"), text("run_id"), blob("incarnation"), integer("revision"), integer("last"), integer("ack"), integer("owner"), integer("generation"), integer("expiry")}, Plan: point("sqlite_autoindex_inventory_runs_2", "job_id=? AND command_incarnation=?"), Cohorts: []stmt.Cohort{{Name: "run_cas", Bindings: []any{int64(2), int64(2), "lifecycle-run", nonce, int64(1), int64(1), int64(1), int64(11), int64(1), int64(4000000000000)}}}})
	cols := `run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest,cursor_kind,cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload,result_directory_path,result_directory_identity,result_directory_revision,result_directory_cursor,result_directory_state,result_directory_pages,result_directory_skipped,result_parent_pages,result_parent_skipped`
	params := []stmt.Param{text("run_id"), blob("incarnation"), integer("sequence"), blob("operation"), blob("request"), text("kind"), integer("revision"), integer("applied_ms"), text("source_id"), text("source_incarnation"), nullableText("source_revision"), text("next_cursor"), integer("result_count"), blob("digest"), text("cursor_kind"), blob("parent_path"), blob("parent_identity"), blob("parent_revision"), blob("expected_cursor"), blob("next_payload"), text("result_path"), text("result_identity"), text("result_revision"), text("result_cursor"), text("result_state"), integer("result_pages"), integer("result_skipped"), nullableInteger("parent_pages"), nullableInteger("parent_skipped")}
	bindings := []any{"lifecycle-run", nonce, int64(2), operation, digest, "inventory_directory_discover", int64(2), int64(1000), "lifecycle-source", "source-incarnation", nil, "42", int64(0), digest, inventoryLinuxCursor, []byte("."), []byte("actual-directory"), []byte("directory-revision"), []byte{}, []byte("42"), "child", "new-directory", "new-revision", "", "pending", int64(0), int64(0), int64(1), int64(0)}
	eof := append([]any(nil), bindings...)
	eof[5] = "inventory_directory_eof"
	eof[20] = "."
	eof[21] = "actual-directory"
	eof[22] = "directory-revision"
	eof[23] = "42"
	eof[24] = "done"
	eof[25] = int64(1)
	eof[27] = nil
	eof[28] = nil
	defs = append(defs, stmt.Definition{ID: scanDirectoryReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO scan_command_receipts(` + cols + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(params)), ",") + `)`, Params: params, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "discovery_scalar", Bindings: bindings}, {Name: "eof_scalar", Bindings: eof}}})
	for i := range defs {
		defs[i].Owner = "store/writer directory lifecycle"
		defs[i].CallSites = []string{"store/writer/scan_directory_effects.go", "store/writer/scan_directory_history.go"}
	}
	return defs
}

func directoryStatementMaxUses(id stmt.ID) int {
	switch id {
	case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptAck, scanLifecycleRun, scanLifecycleJob, scanDirectoryActive,
		scanSourcePayload, scanSourcePolicy, scanLibraryRoot, inventoryFreshLibrary,
		scanDirectoryJobLengths, scanFreshSource, scanDirectoryPath, scanDirectoryVisited, scanDirectoryInsert, scanDirectoryDiscoverCAS, scanDirectoryEOFCAS, scanDirectoryRunCAS, scanDirectoryReceiptInsert:
		return 1
	case scanRunHistoryOwner, scanReceiptLookup, scanReceiptReclaimKey, scanDirectoryReceiptLengths, scanDirectoryBody, scanLifecycleRoot:
		return 2
	case scanSourceLengths:
		return 2
	case scanLibraryRootLengths:
		return 2
	case scanDirectoryRunLengths:
		return 3
	case scanDirectoryRootLengths:
		return 2
	default:
		return 0
	}
}
