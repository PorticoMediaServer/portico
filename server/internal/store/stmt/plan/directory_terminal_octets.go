package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const directoryTerminalOctetID stmt.ID = "scan.directory_terminal.receipt_lengths"
const directoryTerminalOctetSQL = "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload),octet_length(result_directory_path),octet_length(result_directory_identity),octet_length(result_directory_revision),octet_length(result_directory_cursor),octet_length(result_directory_state),octet_length(result_inventory_completed_at),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(refused_entry_kind),octet_length(refused_directory_identity),octet_length(refused_directory_revision) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?"
const directoryTerminalOctetSeek = "run_id=? AND command_incarnation=? AND command_sequence=?"

func init() {
	pointOctets.add(octetShape{
		id: directoryTerminalOctetID, sql: directoryTerminalOctetSQL,
		mismatch:   "terminal receipt byte preflight requires its exact closed point definition",
		definition: directoryTerminalOctetDefinition,
		audit:      auditDirectoryTerminalOctetPoint,
		point:      checkDirectoryTerminalOctetPoint,
	})
}

// Only the compiled terminal receipt projection; this confers no authority to
// decode a saved body, execute a command or use octet_length in generic SQL.
func directoryTerminalOctetDefinition(d stmt.Definition) bool {
	return d.ID == directoryTerminalOctetID && d.SQL == directoryTerminalOctetSQL &&
		d.Kind == stmt.Read && d.Plan.Hot && slices.Equal(d.Params, []stmt.Param{
		{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer},
	}) && slices.Equal(d.Plan.PrimaryKeys, []string{"scan_command_receipts"}) &&
		slices.Equal(d.Plan.Seeks, []string{directoryTerminalOctetSeek}) &&
		len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 &&
		len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 &&
		d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}

func auditDirectoryTerminalOctetPoint(ctx context.Context, conn *sql.Conn) error {
	return auditClosedOctetKey(ctx, conn, "scan_command_receipts", "", []string{"run_id", "command_incarnation", "command_sequence"})
}

func checkDirectoryTerminalOctetPoint(eqp Rows) error {
	const want = "SEARCH scan_command_receipts USING PRIMARY KEY (" + directoryTerminalOctetSeek + ")"
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
		return fmt.Errorf("terminal receipt byte preflight: exact full-key SEARCH required: %s", want)
	}
	return nil
}
