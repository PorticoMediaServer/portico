package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these five literal pre-decode points extend the issuer-only scalar
// parser. Owners still supply complete definitions and obtain a real Capture
// receipt; this table is neither a registry nor execution authority.
type n1OctetPoint struct {
	id                stmt.ID
	sql, table, index string
	keys              []string
	params            []stmt.Param
}

var n1OctetPoints = []n1OctetPoint{
	{id: "scan.issuer.run_lengths", sql: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode),octet_length(create_operation_id),octet_length(create_request_digest),octet_length(command_incarnation),octet_length(reconcile_cursor),octet_length(verify_cursor),octet_length(last_turn),octet_length(pause_reason),octet_length(inventory_completed_at) FROM inventory_runs WHERE job_id=?", table: "inventory_runs", index: "sqlite_autoindex_inventory_runs_1", keys: []string{"job_id"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}},
	{id: "scan.issuer.job_lengths", sql: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", table: "jobs", index: "sqlite_autoindex_jobs_1", keys: []string{"id"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}},
	{id: "scan.issuer.root_lengths", sql: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", table: "inventory_directories", index: "sqlite_autoindex_inventory_directories_1", keys: []string{"job_id", "relative_path"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "root_path", Type: stmt.Text}}},
	{id: "scan.issuer.receipt_lengths", sql: "SELECT octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(result_digest),octet_length(result_root_identity),octet_length(result_root_revision),octet_length(result_root_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", table: "scan_command_receipts", index: "", keys: []string{"run_id", "command_incarnation", "command_sequence"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}},
	{id: "scan.inventory_accept.member_lengths", sql: "SELECT octet_length(object_id) FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?", table: "scan_command_results", index: "", keys: []string{"run_id", "command_incarnation", "command_sequence", "input_ordinal", "child_kind", "child_ordinal"}, params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "input_ordinal", Type: stmt.Integer}, {Name: "child_kind", Type: stmt.Integer}, {Name: "child_ordinal", Type: stmt.Integer}}},
}

func init() {
	for _, point := range n1OctetPoints {
		definition := stmt.Definition{ID: point.id}
		pointOctets.add(octetShape{
			id: point.id, sql: point.sql,
			mismatch:   "N1 byte preflight requires its exact closed point definition",
			definition: n1OctetDefinition,
			audit: func(ctx context.Context, conn *sql.Conn) error {
				return auditN1OctetPoint(ctx, conn, definition)
			},
			point: func(eqp Rows) error { return checkN1OctetPoint(definition, eqp) },
		})
	}
}

func n1OctetPointFor(id stmt.ID) (n1OctetPoint, bool) {
	for _, point := range n1OctetPoints {
		if point.id == id {
			return point, true
		}
	}
	return n1OctetPoint{}, false
}

// The DML classifier reparses already admitted SELECTs without a Definition.
// Only these exact strings may use the scalar there; public lint remains closed.
func n1OctetSQL(sql string) bool {
	for _, point := range n1OctetPoints {
		if point.sql == sql {
			return true
		}
	}
	return false
}

func n1OctetDefinition(d stmt.Definition) bool {
	p, found := n1OctetPointFor(d.ID)
	if !found || d.SQL != p.sql || d.Kind != stmt.Read || !d.Plan.Hot ||
		!slices.Equal(d.Params, p.params) || len(d.Plan.AutoIndexes) != 0 ||
		len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 ||
		d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if p.index != "" {
		if !slices.Equal(d.Plan.Indexes, []string{p.index}) || len(d.Plan.PrimaryKeys) != 0 {
			return false
		}
	} else if len(d.Plan.Indexes) != 0 || !slices.Equal(d.Plan.PrimaryKeys, []string{p.table}) {
		return false
	}
	return len(d.Plan.Seeks) == 0 || slices.Equal(d.Plan.Seeks, []string{n1OctetSeek(p)})
}

func n1OctetSeek(p n1OctetPoint) string {
	terms := make([]string, len(p.keys))
	for i, key := range p.keys {
		terms[i] = key + "=?"
	}
	return strings.Join(terms, " AND ")
}

func checkN1OctetPoint(d stmt.Definition, eqp Rows) error {
	p, _ := n1OctetPointFor(d.ID)
	path := "PRIMARY KEY"
	if p.index != "" {
		path = "INDEX " + p.index
	}
	want := "SEARCH " + p.table + " USING " + path + " (" + n1OctetSeek(p) + ")"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("plan %s: exact full-key byte-preflight SEARCH required: %s", d.ID, want)
	}
	return nil
}

func auditN1OctetPoint(ctx context.Context, conn *sql.Conn, d stmt.Definition) error {
	p, _ := n1OctetPointFor(d.ID)
	return auditClosedOctetKey(ctx, conn, p.table, p.index, p.keys)
}
