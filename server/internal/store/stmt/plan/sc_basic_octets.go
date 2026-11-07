package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const scBasicHeaderOctetsID stmt.ID = "scan.basic_receipt.header_lengths"
const scBasicHeaderOctetsSQL = "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?"
const scBasicHeaderSeek = "run_id=? AND command_incarnation=? AND command_sequence=?"

// One SC-owned compiled contribution. Generic SQL still cannot select scalar
// functions; this exact ID/SQL/type/key/native/EQP combination is the exception.
func init() {
	pointOctets.add(octetShape{scBasicHeaderOctetsID, scBasicHeaderOctetsSQL,
		"Basic receipt bytes require the exact full-key preflight definition",
		scBasicHeaderOctetDefinition,
		func(ctx context.Context, c *sql.Conn) error {
			return auditClosedOctetKey(ctx, c, "scan_command_receipts", "", []string{"run_id", "command_incarnation", "command_sequence"})
		},
		checkSCBasicHeaderOctetPoint})
}
func scBasicHeaderOctetDefinition(d stmt.Definition) bool {
	return d.ID == scBasicHeaderOctetsID && d.SQL == scBasicHeaderOctetsSQL && d.Kind == stmt.Read && d.Plan.Hot &&
		slices.Equal(d.Params, []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}) &&
		slices.Equal(d.Plan.PrimaryKeys, []string{"scan_command_receipts"}) && slices.Equal(d.Plan.Seeks, []string{scBasicHeaderSeek}) &&
		len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}
func checkSCBasicHeaderOctetPoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH scan_command_receipts USING PRIMARY KEY ("+scBasicHeaderSeek+")" {
		return fmt.Errorf("Basic receipt bytes require exactly one full PRIMARY KEY SEARCH")
	}
	return nil
}
