package plan_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Tiny metadata falsifiers only, not canonical product populations/goldens.
func directoryOctetFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy, []stmt.Definition) {
	t.Helper()
	conn, _, f, _ := fixture(t)
	var defs []stmt.Definition
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE inventory_runs(job_id TEXT,original_library_key TEXT,original_resolved_root TEXT,source_id TEXT,root_incarnation TEXT,root_identity TEXT,phase TEXT,finished_at TEXT,scan_mode TEXT,PRIMARY KEY(job_id))"); err != nil {
		t.Fatal(err)
	}
	defs = append(defs, stmt.Definition{ID: "scan.directory.run_lengths", Kind: stmt.Read, SQL: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode) FROM inventory_runs WHERE job_id=?", Owner: "directory-octet-guard", CallSites: []string{"directory_octets_test.go"}, Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}}, Cohorts: []stmt.Cohort{{Name: "empty_metadata_fixture", Bindings: []any{"missing"}}}})
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE jobs(id TEXT,library_id TEXT,status TEXT,created_at TEXT,error TEXT,PRIMARY KEY(id))"); err != nil {
		t.Fatal(err)
	}
	defs = append(defs, stmt.Definition{ID: "scan.directory.job_lengths", Kind: stmt.Read, SQL: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", Owner: "directory-octet-guard", CallSites: []string{"directory_octets_test.go"}, Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "empty_metadata_fixture", Bindings: []any{"missing"}}}})
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE inventory_directories(job_id TEXT,relative_path TEXT,identity TEXT,revision TEXT,cursor TEXT,state TEXT,PRIMARY KEY(job_id,relative_path))"); err != nil {
		t.Fatal(err)
	}
	defs = append(defs, stmt.Definition{ID: "scan.directory.root_lengths", Kind: stmt.Read, SQL: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", Owner: "directory-octet-guard", CallSites: []string{"directory_octets_test.go"}, Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "path", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, Seeks: []string{"job_id=? AND relative_path=?"}}, Cohorts: []stmt.Cohort{{Name: "empty_metadata_fixture", Bindings: []any{"missing", "missing"}}}})
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE scan_command_receipts(run_id TEXT,command_incarnation BLOB,command_sequence INTEGER,source_id TEXT,source_incarnation TEXT,source_revision TEXT,next_cursor TEXT,cursor_kind TEXT,cursor_directory_path TEXT,cursor_directory_identity TEXT,cursor_directory_revision TEXT,cursor_expected_payload TEXT,cursor_next_payload TEXT,result_directory_path TEXT,result_directory_identity TEXT,result_directory_revision TEXT,result_directory_cursor TEXT,result_directory_state TEXT,PRIMARY KEY(run_id,command_incarnation,command_sequence)) WITHOUT ROWID"); err != nil {
		t.Fatal(err)
	}
	defs = append(defs, stmt.Definition{ID: "scan.directory.receipt_lengths", Kind: stmt.Read, SQL: "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload),octet_length(result_directory_path),octet_length(result_directory_identity),octet_length(result_directory_revision),octet_length(result_directory_cursor),octet_length(result_directory_state) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Owner: "directory-octet-guard", CallSites: []string{"directory_octets_test.go"}, Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}}, Cohorts: []stmt.Cohort{{Name: "empty_metadata_fixture", Bindings: []any{"missing", make([]byte, 16), int64(1)}}}})
	return conn, f, indexPolicy(t), defs
}

func TestDirectoryOctetClosedPoints(t *testing.T) {
	conn, f, p, defs := directoryOctetFixture(t)
	for _, d := range defs {
		if len(plan.LintSQL(d.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), conn, d.SQL)) == 0 {
			t.Fatal("generic lint opened", d.ID)
		}
		g, err := captureIssuerOctets(t, conn, f, p, d)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err != nil {
			t.Fatal(err)
		}
		for _, change := range []string{"id", "sql", "param", "nullable", "range", "key", "kind"} {
			t.Run(string(d.ID)+"/"+change, func(t *testing.T) {
				copy := d
				copy.Params = append([]stmt.Param(nil), d.Params...)
				switch change {
				case "id":
					copy.ID += ".other"
				case "sql":
					copy.SQL = strings.Replace(copy.SQL, "octet_length(", "length(", 1)
				case "param":
					copy.Params[0].Name = "other"
				case "nullable":
					copy.Params[0].Nullable = true
				case "range":
					copy.SQL = strings.Replace(copy.SQL, "=?", ">?", 1)
				case "key":
					copy.Plan.Indexes = []string{"other"}
					copy.Plan.PrimaryKeys = nil
				case "kind":
					copy.Kind = stmt.Write
				}
				denied, err := captureIssuerOctets(t, conn, f, p, copy)
				if err == nil {
					t.Fatal("non-closed declaration qualified")
				}
				if _, err := stmt.New([]stmt.Definition{copy}, denied.Receipt()); err == nil {
					t.Fatal("refused capture minted authority")
				}
			})
		}
	}
}

func TestDirectoryOctetRejectsKeyPrefix(t *testing.T) {
	conn, f, p, defs := directoryOctetFixture(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE inventory_runs"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE inventory_runs(job_id TEXT,original_library_key TEXT,original_resolved_root TEXT,source_id TEXT,root_incarnation TEXT,root_identity TEXT,phase TEXT,finished_at TEXT,scan_mode TEXT,extra TEXT,PRIMARY KEY(job_id,extra))"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureIssuerOctets(t, conn, f, p, defs[0]); err == nil || !strings.Contains(err.Error(), "complete native key") {
		t.Fatal("prefix key certified as a point", err)
	}
}

func TestDirectoryOctetRejectsForgedBuiltin(t *testing.T) {
	const child = "PORTICO_DIRECTORY_OCTET_OVERRIDE_CHILD"
	if os.Getenv(child) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDirectoryOctetRejectsForgedBuiltin$", "-test.v")
		cmd.Env = append(os.Environ(), child+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated override control: %v\n%s", err, out)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	conn, f, p, defs := directoryOctetFixture(t)
	var actual int64
	if err := conn.QueryRowContext(t.Context(), "SELECT octet_length('x')").Scan(&actual); err != nil || actual != 777 {
		t.Fatal("override not active", actual, err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE TEMP TABLE pragma_function_list AS SELECT * FROM main.pragma_function_list() WHERE builtin=1"); err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if _, err := captureIssuerOctets(t, conn, f, p, d); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("forged builtin catalogue qualified", d.ID, err)
		}
	}
}
