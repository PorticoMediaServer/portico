package plan_test

import (
	"database/sql/driver"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func n1OctetTestDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: "scan.issuer.run_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_octets_test.go"}, SQL: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode),octet_length(create_operation_id),octet_length(create_request_digest),octet_length(command_incarnation),octet_length(reconcile_cursor),octet_length(verify_cursor),octet_length(last_turn),octet_length(pause_reason),octet_length(inventory_completed_at) FROM inventory_runs WHERE job_id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "empty_native_table", Bindings: []any{"absent"}}}},
		{ID: "scan.issuer.job_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_octets_test.go"}, SQL: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "empty_native_table", Bindings: []any{"absent"}}}},
		{ID: "scan.issuer.root_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_octets_test.go"}, SQL: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "root_path", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "empty_native_table", Bindings: []any{"absent", "absent"}}}},
		{ID: "scan.issuer.receipt_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_octets_test.go"}, SQL: "SELECT octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(result_digest),octet_length(result_root_identity),octet_length(result_root_revision),octet_length(result_root_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_receipts"}}, Cohorts: []stmt.Cohort{{Name: "empty_native_table", Bindings: []any{"absent", make([]byte, 16), int64(1)}}}},
		{ID: "scan.inventory_accept.member_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_octets_test.go"}, SQL: "SELECT octet_length(object_id) FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "input_ordinal", Type: stmt.Integer}, {Name: "child_kind", Type: stmt.Integer}, {Name: "child_ordinal", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_results"}}, Cohorts: []stmt.Cohort{{Name: "empty_native_table", Bindings: []any{"absent", make([]byte, 16), int64(1), int64(1), int64(1), int64(1)}}}},
	}
}

func TestN1OctetPreflightClosure(t *testing.T) {
	conn, f, policy := issuerOctetsFixture(t)
	cases := []struct {
		name   string
		mutate func(*stmt.Definition)
	}{
		{"other_id", func(d *stmt.Definition) { d.ID += ".other" }},
		{"projection", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length(", "length(", 1) }},
		{"expression", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length(", "octet_length(lower(", 1) }},
		{"range", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "=?", ">?", 1) }},
		{"param_name", func(d *stmt.Definition) { d.Params[0].Name = "other" }},
		{"param_type", func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer; d.Cohorts[0].Bindings[0] = int64(1) }},
		{"nullable", func(d *stmt.Definition) { d.Params[0].Nullable = true }},
		{"cold", func(d *stmt.Definition) { d.Plan.Hot = false }},
		{"write_kind", func(d *stmt.Definition) { d.Kind = stmt.Write }},
		{"waiver", func(d *stmt.Definition) {
			d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "eqp", Form: "SCAN", Match: "SCAN anything", Reason: "not permission"}}
		}},
		{"key_contract", func(d *stmt.Definition) { d.Plan.Indexes = []string{"other"}; d.Plan.PrimaryKeys = nil }},
	}
	for i := range n1OctetTestDefinitions() {
		d := n1OctetTestDefinitions()[i]
		if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
			t.Fatal("generic lint admitted closed octet scalar", d.ID)
		}
		if _, err := captureIssuerOctets(t, conn, f, policy, d); err != nil {
			t.Fatal(d.ID, err)
		}
		for _, tc := range cases {
			t.Run(string(d.ID)+"/"+tc.name, func(t *testing.T) {
				changed := n1OctetTestDefinitions()[i]
				tc.mutate(&changed)
				g, err := captureIssuerOctets(t, conn, f, policy, changed)
				if err == nil {
					t.Fatal("non-closed point qualified")
				}
				if _, err := stmt.New([]stmt.Definition{changed}, g.Receipt()); err == nil {
					t.Fatal("failed Capture authorized registry")
				}
			})
		}
	}
	t.Run("temp_shadow", func(t *testing.T) {
		if _, err := conn.ExecContext(t.Context(), "CREATE TEMP TABLE inventory_runs AS SELECT * FROM main.inventory_runs"); err != nil {
			t.Fatal(err)
		}
		d := n1OctetTestDefinitions()[0]
		if _, err := captureIssuerOctets(t, conn, f, policy, d); err == nil || !strings.Contains(err.Error(), "TEMP") {
			t.Fatal("TEMP source accepted", err)
		}
	})
}

func TestN1OctetPreflightNativeKey(t *testing.T) {
	for _, test := range []struct{ name, extra, key string }{
		{"partial_key", "extra TEXT,", "PRIMARY KEY(job_id,extra)"},
		{"renumbered_key", "extra TEXT UNIQUE,", "PRIMARY KEY(job_id)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, _, f, _ := fixture(t)
			columns := []string{"job_id", "original_library_key", "original_resolved_root", "source_id", "root_incarnation", "root_identity", "phase", "finished_at", "scan_mode", "create_operation_id", "create_request_digest", "command_incarnation"}
			ddl := "CREATE TABLE inventory_runs(" + test.extra
			for _, name := range columns {
				ddl += name + " TEXT,"
			}
			ddl += test.key + ")"
			if _, err := conn.ExecContext(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			d := n1OctetTestDefinitions()[0]
			if _, err := captureIssuerOctets(t, conn, f, indexPolicy(t), d); err == nil {
				t.Fatal("non-full or renumbered native key accepted")
			}
		})
	}
}

func TestN1OctetPreflightNativeOverride(t *testing.T) {
	const child = "PORTICO_N1_OCTET_OVERRIDE_CHILD"
	if os.Getenv(child) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestN1OctetPreflightNativeOverride$", "-test.v")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated override control: %v\n%s", err, out)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	conn, f, p := issuerOctetsFixture(t)
	if _, err := conn.ExecContext(t.Context(), "CREATE TEMP TABLE pragma_function_list AS SELECT * FROM main.pragma_function_list() WHERE builtin=1"); err != nil {
		t.Fatal(err)
	}
	for _, d := range n1OctetTestDefinitions() {
		g, err := captureIssuerOctets(t, conn, f, p, d)
		if err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("application octet_length qualified", d.ID, err)
		}
		if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
			t.Fatal("override receipt authorized registry")
		}
	}
}
