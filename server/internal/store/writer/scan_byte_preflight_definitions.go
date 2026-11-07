package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Exact LEAD-approved points only. No generic function/executor allowance.
const (
	scanRunByteLengths        stmt.ID = "scan.issuer.run_lengths"
	scanJobByteLengths        stmt.ID = "scan.issuer.job_lengths"
	scanRootByteLengths       stmt.ID = "scan.issuer.root_lengths"
	scanReceiptByteLengths    stmt.ID = "scan.issuer.receipt_lengths"
	scanMemberByteLengths     stmt.ID = "scan.inventory_accept.member_lengths"
	scanReceiptOwnerPrefix    stmt.ID = "scan.issuer.receipt_owner"
	scanReceiptIdentityPrefix stmt.ID = "scan.inventory_accept.receipt_identity"
	scanMemberKeys            stmt.ID = "scan.inventory_accept.member_keys"
)

func scanBytePreflightDefinitions() []stmt.Definition {
	inc := make([]byte, 16)
	inc[0] = 1
	return []stmt.Definition{
		{ID: scanRunByteLengths, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode),octet_length(create_operation_id),octet_length(create_request_digest),octet_length(command_incarnation) FROM inventory_runs WHERE job_id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"lifecycle-run"}}, {Name: "missing", Bindings: []any{"lifecycle-missing"}}}},
		{ID: scanJobByteLengths, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"lifecycle-run"}}, {Name: "missing", Bindings: []any{"lifecycle-missing"}}}},
		{ID: scanRootByteLengths, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "root_path", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, Seeks: []string{"job_id=? AND relative_path=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"lifecycle-run", "."}}, {Name: "missing", Bindings: []any{"lifecycle-missing", "."}}}},
		{ID: scanReceiptByteLengths, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(result_digest),octet_length(result_root_identity),octet_length(result_root_revision),octet_length(result_root_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"scan-hit", inc, int64(1)}}, {Name: "missing", Bindings: []any{"scan-absent", inc, int64(1)}}}},
		{ID: scanMemberByteLengths, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT octet_length(object_id) FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "input_ordinal", Type: stmt.Integer}, {Name: "child_kind", Type: stmt.Integer}, {Name: "child_ordinal", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_results"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"scan-hit", inc, int64(1), int64(0), int64(0), int64(0)}}, {Name: "missing", Bindings: []any{"scan-absent", inc, int64(1), int64(0), int64(0), int64(0)}}}},
		{ID: scanReceiptOwnerPrefix, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,original_library_id,create_operation_id,create_request_digest FROM inventory_runs WHERE job_id=? AND original_library_key=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "library_key", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"lifecycle-run", "lifecycle-library"}}, {Name: "missing", Bindings: []any{"lifecycle-missing", "lifecycle-library"}}}},
		{ID: scanReceiptIdentityPrefix, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"scan-hit", inc, int64(1)}}, {Name: "missing", Bindings: []any{"scan-absent", inc, int64(1)}}}},
		{ID: scanMemberKeys, Owner: "store/writer scan byte preflight", CallSites: []string{"store/writer/scan_byte_preflight.go"}, Kind: stmt.Read,
			SQL: "SELECT input_ordinal,child_kind,child_ordinal FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? ORDER BY input_ordinal,child_kind,child_ordinal LIMIT ?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "result_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_results"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing", Bindings: []any{"scan-hit", inc, int64(1), int64(2)}}, {Name: "missing", Bindings: []any{"scan-absent", inc, int64(1), int64(2)}}}},
	}
}

func scanLifecycleByteDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range scanBytePreflightDefinitions() {
		switch d.ID {
		case scanRunByteLengths, scanJobByteLengths, scanRootByteLengths, scanReceiptByteLengths, scanReceiptOwnerPrefix, scanReceiptIdentityPrefix:
			defs = append(defs, d)
		}
	}
	return defs
}

func scanHistoryByteDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range scanBytePreflightDefinitions() {
		switch d.ID {
		case scanRunByteLengths, scanReceiptByteLengths, scanReceiptOwnerPrefix, scanReceiptIdentityPrefix:
			defs = append(defs, d)
		}
	}
	return defs
}

func inventoryHistoryByteDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range scanBytePreflightDefinitions() {
		switch d.ID {
		case scanReceiptByteLengths, scanMemberByteLengths, scanReceiptIdentityPrefix, scanMemberKeys:
			defs = append(defs, d)
		}
	}
	return defs
}

func scanSnapshotByteDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, d := range scanBytePreflightDefinitions() {
		switch d.ID {
		case scanRunByteLengths, scanJobByteLengths, scanRootByteLengths:
			defs = append(defs, d)
		}
	}
	return defs
}
