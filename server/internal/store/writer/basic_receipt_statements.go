package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicReceiptScope         stmt.ID = "scan.basic_receipt.scope"
	basicReceiptIdentity      stmt.ID = "scan.basic_receipt.identity"
	basicReceiptHeaderLengths stmt.ID = "scan.basic_receipt.header_lengths"
	basicReceiptHeader        stmt.ID = "scan.basic_receipt.header"
)

// Draft finite points for W's sole registry/dispatcher. In particular the exact
// header_lengths octet rule still needs HN Capture support. Describe is inert;
// there is no alternate executor, grammar waiver or registration authority here.
func basicReceiptDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	key := []stmt.Param{t("run_id"), b("command_incarnation"), i("command_sequence")}
	defs := []stmt.Definition{
		{ID: basicReceiptScope, Kind: stmt.Read,
			// Compare the native ORIGINAL tuple without copying its possibly
			// large key. No current source/root/lease/job state filters history.
			SQL:    `SELECT job_id FROM inventory_runs WHERE job_id=? AND original_library_id=? AND original_library_key=?`,
			Params: []stmt.Param{t("run_id"), i("original_library_id"), t("original_library_key")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}}},
		{ID: basicReceiptIdentity, Kind: stmt.Read,
			SQL:    `SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key},
		{ID: basicReceiptHeaderLengths, Kind: stmt.Read,
			SQL:    `SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: key},
		{ID: basicReceiptHeader, Kind: stmt.Read,
			// FIRST Stage/Seal have closed native prefixes and NO other result
			// variants. Guards avoid allocating unused native TEXT/BLOBs.
			SQL: `SELECT accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,change_id,expected_epoch,next_family,after_entity
FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?
AND next_cursor='' AND next_copy_phase=0 AND next_copy_id=0 AND next_copy_aux=0
AND next_copy_key='' AND next_copy_key2='' AND next_copy_key3=''
AND result_lease_owner IS NULL AND result_lease_generation IS NULL AND result_lease_expires_ms IS NULL
AND result_acknowledged_sequence IS NULL AND result_discovered IS NULL AND result_processed IS NULL
AND result_root_identity IS NULL AND result_root_revision IS NULL AND result_root_cursor IS NULL
AND result_root_pages IS NULL AND result_root_skipped IS NULL
AND refused_relative_path IS NULL AND refusal_code IS NULL AND refused_entry_kind IS NULL
AND refused_directory_identity IS NULL AND refused_directory_revision IS NULL
AND cursor_kind IS NULL AND cursor_directory_path IS NULL AND cursor_directory_identity IS NULL
AND cursor_directory_revision IS NULL AND cursor_expected_payload IS NULL AND cursor_next_payload IS NULL
AND result_directory_path IS NULL AND result_directory_identity IS NULL AND result_directory_revision IS NULL
AND result_directory_cursor IS NULL AND result_directory_state IS NULL AND result_directory_pages IS NULL
AND result_directory_skipped IS NULL AND result_parent_pages IS NULL AND result_parent_skipped IS NULL
AND result_inventory_completed_at IS NULL AND result_traversal_blocked IS NULL AND result_traversal_protected IS NULL`,
			Params: key},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "store/writer Basic history adapter"
		d.CallSites = []string{"store/writer/basic_receipts.go"}
		if d.ID != basicReceiptScope {
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}
		}
		for _, c := range []struct {
			name, run string
		}{{"saved_stage", "basic-run"}, {"missing", "basic-missing"}} {
			args := []any{c.run, make([]byte, 16), int64(19)}
			if d.ID == basicReceiptScope {
				args = []any{c.run, int64(7), "basic-library"}
			} else {
				for k := range args[1].([]byte) {
					args[1].([]byte)[k] = 1
				}
			}
			d.Cohorts = append(d.Cohorts, stmt.Cohort{Name: c.name, Bindings: args})
		}
	}
	return defs
}
