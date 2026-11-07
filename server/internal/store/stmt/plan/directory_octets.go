package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Four compiled directory pre-decode points only. This does not register a
// command, widen public scalar grammar, or authorize payload decode/acceptance.
type directoryOctetPoint struct {
	id                stmt.ID
	sql, table, index string
	keys              []string
	params            []stmt.Param
}

var directoryOctetPoints = []directoryOctetPoint{
	{id: "scan.directory.run_lengths", sql: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode) FROM inventory_runs WHERE job_id=?", table: "inventory_runs", index: "sqlite_autoindex_inventory_runs_1", keys: []string{"job_id"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}},
	{id: "scan.directory.job_lengths", sql: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", table: "jobs", index: "sqlite_autoindex_jobs_1", keys: []string{"id"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}},
	{id: "scan.directory.root_lengths", sql: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", table: "inventory_directories", index: "sqlite_autoindex_inventory_directories_1", keys: []string{"job_id", "relative_path"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "path", Type: stmt.Text}}},
	{id: "scan.directory.receipt_lengths", sql: "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload),octet_length(result_directory_path),octet_length(result_directory_identity),octet_length(result_directory_revision),octet_length(result_directory_cursor),octet_length(result_directory_state) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", table: "scan_command_receipts", index: "", keys: []string{"run_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}},
}

func directoryOctetPointFor(id stmt.ID) (directoryOctetPoint, bool) {
	for _, p := range directoryOctetPoints {
		if p.id == id {
			return p, true
		}
	}
	return directoryOctetPoint{}, false
}
func directoryOctetSQL(sql string) bool {
	for _, p := range directoryOctetPoints {
		if p.sql == sql {
			return true
		}
	}
	return false
}
func directoryOctetSeek(p directoryOctetPoint) string {
	parts := make([]string, len(p.keys))
	for i, key := range p.keys {
		parts[i] = key + "=?"
	}
	return strings.Join(parts, " AND ")
}
func directoryOctetDefinition(d stmt.Definition) bool {
	p, found := directoryOctetPointFor(d.ID)
	if !found || d.SQL != p.sql || d.Kind != stmt.Read || !d.Plan.Hot || !slices.Equal(d.Params, p.params) ||
		!slices.Equal(d.Plan.Seeks, []string{directoryOctetSeek(p)}) || len(d.Plan.AutoIndexes) != 0 ||
		len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if p.index != "" {
		return slices.Equal(d.Plan.Indexes, []string{p.index}) && len(d.Plan.PrimaryKeys) == 0
	}
	return len(d.Plan.Indexes) == 0 && slices.Equal(d.Plan.PrimaryKeys, []string{p.table})
}
func auditDirectoryOctetPoint(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	p, _ := directoryOctetPointFor(d.ID)
	return auditClosedOctetKey(ctx, conn, p.table, p.index, p.keys)
}
func checkDirectoryOctetPoint(d stmt.Definition, eqp Rows) error {
	p, _ := directoryOctetPointFor(d.ID)
	kind := "PRIMARY KEY"
	if p.index != "" {
		kind = "INDEX " + p.index
	}
	want := "SEARCH " + p.table + " USING " + kind + " (" + directoryOctetSeek(p) + ")"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("plan %s: exact full-key byte-preflight SEARCH required: %s", d.ID, want)
	}
	return nil
}
