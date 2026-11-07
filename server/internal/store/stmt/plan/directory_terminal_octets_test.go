package plan_test

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

const terminalOctetSQL = "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload),octet_length(result_directory_path),octet_length(result_directory_identity),octet_length(result_directory_revision),octet_length(result_directory_cursor),octet_length(result_directory_state),octet_length(result_inventory_completed_at),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(refused_entry_kind),octet_length(refused_directory_identity),octet_length(refused_directory_revision) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?"

func TestDirectoryTerminalOctetClosedPoint(t *testing.T) {
	conn, f, policy, defs := directoryOctetFixture(t)
	// Extend only this tiny metadata fixture, not the owner's canonical seed.
	for _, column := range []string{"result_inventory_completed_at", "refused_relative_path", "refusal_code", "refused_entry_kind", "refused_directory_identity", "refused_directory_revision"} {
		if _, err := conn.ExecContext(t.Context(), "ALTER TABLE scan_command_receipts ADD COLUMN "+column+" BLOB"); err != nil {
			t.Fatal(err)
		}
	}
	d := defs[3]
	d.ID, d.SQL = "scan.directory_terminal.receipt_lengths", terminalOctetSQL
	if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
		t.Fatal("generic scalar grammar widened")
	}
	golden, err := captureIssuerOctets(t, conn, f, policy, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Plans) != 1 || golden.ForeignKeys != nil {
		t.Fatal("unexpected capture inventory")
	}
	if _, err := stmt.New([]stmt.Definition{d}, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"id", "projection", "param", "nullable", "range", "key", "hot"} {
		t.Run(change, func(t *testing.T) {
			bad := d
			bad.Params = append([]stmt.Param(nil), d.Params...)
			switch change {
			case "id":
				bad.ID += ".other"
			case "projection":
				bad.SQL = strings.Replace(bad.SQL, "octet_length(source_id)", "length(source_id)", 1)
			case "param":
				bad.Params[0].Name = "other"
			case "nullable":
				bad.Params[1].Nullable = true
			case "range":
				bad.SQL = strings.Replace(bad.SQL, "command_sequence=?", "command_sequence>?", 1)
			case "key":
				bad.Plan.PrimaryKeys = []string{"other"}
			case "hot":
				bad.Plan.Hot = false
			}
			refused, err := captureIssuerOctets(t, conn, f, policy, bad)
			if err == nil {
				t.Fatal("non-closed terminal point qualified")
			}
			if _, err := stmt.New([]stmt.Definition{bad}, refused.Receipt()); err == nil {
				t.Fatal("refused terminal point minted registration")
			}
		})
	}
	// A native SEARCH on a PK prefix is not a certified full point.
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE scan_command_receipts"); err != nil {
		t.Fatal(err)
	}
	columns := strings.Split(strings.Split(terminalOctetSQL, " FROM ")[0][len("SELECT "):], ",")
	ddl := "CREATE TABLE scan_command_receipts(run_id TEXT,command_incarnation BLOB,command_sequence INTEGER,extra INTEGER"
	for _, column := range columns {
		ddl += "," + strings.TrimSuffix(strings.TrimPrefix(column, "octet_length("), ")") + " BLOB"
	}
	ddl += ",PRIMARY KEY(run_id,command_incarnation,command_sequence,extra)) WITHOUT ROWID"
	if _, err := conn.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	if _, err := captureIssuerOctets(t, conn, f, policy, d); err == nil || !strings.Contains(err.Error(), "complete native key") {
		t.Fatal("prefix native key qualified", err)
	}
}
