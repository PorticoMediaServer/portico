package writer

import (
	"bytes"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const basicAcceptRunCAS stmt.ID = "scan.basic_accept.run_cas"

func basicAcceptControlDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: basicAcceptRunCAS, Kind: stmt.Write, SQL: `UPDATE inventory_runs SET job_revision=?,last_command_sequence=?,analyzed=? WHERE job_id=? AND command_incarnation=? AND original_library_id=? AND original_library_key=? AND original_resolved_root=? AND original_follow_symlinks=? AND source_id=? AND source_generation=? AND root_incarnation=? AND root_identity=? AND policy_revision=? AND phase=? AND scan_mode=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_revision=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND authoritative=0 AND root_rebound=0 AND finished_at='' AND discovered=? AND analyzed=? AND warnings=? RETURNING job_revision,last_command_sequence,discovered,analyzed,warnings`, Params: []stmt.Param{stmt.Param{Name: "next_revision", Type: stmt.Integer}, stmt.Param{Name: "next_sequence", Type: stmt.Integer}, stmt.Param{Name: "next_analyzed", Type: stmt.Integer}, stmt.Param{Name: "run_id", Type: stmt.Text}, stmt.Param{Name: "command_incarnation", Type: stmt.Blob}, stmt.Param{Name: "library_id", Type: stmt.Integer}, stmt.Param{Name: "library_key", Type: stmt.Text}, stmt.Param{Name: "resolved_root", Type: stmt.Text}, stmt.Param{Name: "follow_symlinks", Type: stmt.Integer}, stmt.Param{Name: "source_id", Type: stmt.Text}, stmt.Param{Name: "generation", Type: stmt.Integer}, stmt.Param{Name: "incarnation", Type: stmt.Text}, stmt.Param{Name: "root_identity", Type: stmt.Text}, stmt.Param{Name: "policy_revision", Type: stmt.Integer}, stmt.Param{Name: "phase", Type: stmt.Text}, stmt.Param{Name: "scan_mode", Type: stmt.Text}, stmt.Param{Name: "lease_owner", Type: stmt.Integer}, stmt.Param{Name: "lease_generation", Type: stmt.Integer}, stmt.Param{Name: "lease_expires_ms", Type: stmt.Integer}, stmt.Param{Name: "run_revision", Type: stmt.Integer}, stmt.Param{Name: "last_sequence", Type: stmt.Integer}, stmt.Param{Name: "ack_sequence", Type: stmt.Integer}, stmt.Param{Name: "discovered", Type: stmt.Integer}, stmt.Param{Name: "analyzed", Type: stmt.Integer}, stmt.Param{Name: "warnings", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_2"}, Seeks: []string{"job_id=? AND command_incarnation=?"}}, Cohorts: []stmt.Cohort{{Name: "actual_post_control_revision", Bindings: []any{int64(42), int64(19), int64(44), "basic-run", bytes.Repeat([]byte{1}, 16), int64(7), "basic-library", "/media", int64(0), "basic-source", int64(23), "basic-incarnation", "root:17", int64(29), "inventory", "first-import", int64(13), int64(17), int64(4000000000000), int64(41), int64(18), int64(18), int64(59), int64(43), int64(47)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_accept_control.go"}
	}
	return defs
}
