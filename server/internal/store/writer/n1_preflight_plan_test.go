package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordN1PreflightPlans = flag.Bool("record-n1-preflight-plans", false, "deliberately record the eight closed N1 byte-preflight/support points")

// Literal qualification definitions only. These introduce no runtime provider,
// command permission or accepting handler. W owns the replacement composition.
func n1PreflightPlanDefinitions(lifecycle bool) []stmt.Definition {
	defs := []stmt.Definition{
		{ID: "scan.issuer.run_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT octet_length(job_id),octet_length(original_library_key),octet_length(original_resolved_root),octet_length(source_id),octet_length(root_incarnation),octet_length(root_identity),octet_length(phase),octet_length(finished_at),octet_length(scan_mode),octet_length(create_operation_id),octet_length(create_request_digest),octet_length(command_incarnation),octet_length(reconcile_cursor),octet_length(verify_cursor),octet_length(last_turn),octet_length(pause_reason),octet_length(inventory_completed_at) FROM inventory_runs WHERE job_id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "existing_run", Bindings: []any{"lifecycle-run"}}, {Name: "missing_run", Bindings: []any{"absent"}}}},
		{ID: "scan.issuer.job_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT octet_length(library_id),octet_length(status),octet_length(created_at),octet_length(error) FROM jobs WHERE id=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "existing_run", Bindings: []any{"lifecycle-run"}}, {Name: "missing_run", Bindings: []any{"absent"}}}},
		{ID: "scan.issuer.root_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "root_path", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_directories_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "existing_root", Bindings: []any{"lifecycle-run", "."}}, {Name: "missing_root", Bindings: []any{"lifecycle-run", "absent"}}}},
		{ID: "scan.issuer.receipt_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT octet_length(operation_id),octet_length(request_digest),octet_length(command_kind),octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(result_digest),octet_length(result_root_identity),octet_length(result_root_revision),octet_length(result_root_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3),octet_length(refused_relative_path),octet_length(refusal_code),octet_length(cursor_kind),octet_length(cursor_directory_path),octet_length(cursor_directory_identity),octet_length(cursor_directory_revision),octet_length(cursor_expected_payload),octet_length(cursor_next_payload) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_receipts"}}, Cohorts: []stmt.Cohort{{Name: "existing_header", Bindings: []any{"scan-hit", make([]byte, 16), int64(1)}}, {Name: "missing_header", Bindings: []any{"scan-missing", make([]byte, 16), int64(1)}}}},
		{ID: "scan.inventory_accept.member_lengths", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT octet_length(object_id) FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? AND input_ordinal=? AND child_kind=? AND child_ordinal=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "input_ordinal", Type: stmt.Integer}, {Name: "child_kind", Type: stmt.Integer}, {Name: "child_ordinal", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_results"}}, Cohorts: []stmt.Cohort{{Name: "existing_member", Bindings: []any{"scan-hit", make([]byte, 16), int64(1), int64(0), int64(0), int64(0)}}, {Name: "drained_member", Bindings: []any{"scan-drained", make([]byte, 16), int64(1), int64(0), int64(0), int64(0)}}}},
		{ID: "scan.issuer.receipt_owner", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,original_library_id,create_operation_id,create_request_digest FROM inventory_runs WHERE job_id=? AND original_library_key=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "library_key", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, PrimaryKeys: []string{}}, Cohorts: []stmt.Cohort{{Name: "wrong_library", Bindings: []any{"lifecycle-run", "other-library"}}, {Name: "existing_run", Bindings: []any{"lifecycle-run", "lifecycle-library"}}, {Name: "missing_run", Bindings: []any{"absent", "lifecycle-library"}}}},
		{ID: "scan.inventory_accept.receipt_identity", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT operation_id,request_digest,command_kind,result_count,result_digest FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_receipts"}}, Cohorts: []stmt.Cohort{{Name: "existing_header", Bindings: []any{"scan-hit", make([]byte, 16), int64(1)}}, {Name: "missing_header", Bindings: []any{"scan-missing", make([]byte, 16), int64(1)}}}},
		{ID: "scan.inventory_accept.member_keys", Kind: stmt.Read, Owner: "N1 byte-preflight", CallSites: []string{"n1_preflight_plan_test.go"}, SQL: "SELECT input_ordinal,child_kind,child_ordinal FROM scan_command_results WHERE run_id=? AND command_incarnation=? AND command_sequence=? ORDER BY input_ordinal,child_kind,child_ordinal LIMIT ?", Params: []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}, {Name: "result_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{}, PrimaryKeys: []string{"scan_command_results"}}, Cohorts: []stmt.Cohort{{Name: "existing_keys", Bindings: []any{"scan-hit", make([]byte, 16), int64(1), int64(2)}}, {Name: "gapped_keys", Bindings: []any{"scan-remaining", make([]byte, 16), int64(1), int64(2)}}, {Name: "drained_keys", Bindings: []any{"scan-drained", make([]byte, 16), int64(1), int64(2)}}}},
	}
	var selected []stmt.Definition
	for _, d := range defs {
		isLifecycle := d.ID == "scan.issuer.run_lengths" || d.ID == "scan.issuer.job_lengths" || d.ID == "scan.issuer.root_lengths" || d.ID == "scan.issuer.receipt_owner"
		if isLifecycle == lifecycle {
			selected = append(selected, d)
		}
	}
	return selected
}

func TestN1PreflightStatementPlans(t *testing.T) {
	h := sha256.New()
	for _, name := range []string{"scan_run_lifecycle_test.go", "scan_receipts_test.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		h.Write([]byte(name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
		h.Write(b)
	}
	journal := hex.EncodeToString(h.Sum(nil))
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	type population struct {
		Name   string      `json:"name"`
		Golden plan.Golden `json:"golden"`
	}
	var populations []population
	plans, vm, seeks := 0, 0, 0
	for _, lifecycle := range []bool{true, false} {
		w, _, _ := testExecutor(t)
		name := "receipt_native_facts"
		if lifecycle {
			seedScanLifecycle(t, w)
			name = "lifecycle_native_facts"
		} else {
			seedScanReceiptCohorts(t, w)
		}
		conn, err := w.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
		closeErr := conn.Close()
		if assertErr != nil || closeErr != nil || startup.Synchronous != 2 {
			t.Fatal(startup, assertErr, closeErr)
		}
		t.Logf("%s FULL startup: %+v", name, startup)
		// Existing private synthetic frame/virtual clock, not a new executor/profile.
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = tx.rollback(nil) })
		config := w.config
		config.Synchronous = sqliteconn.Normal
		settings, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s NORMAL capture: %+v", name, settings)
		defs := n1PreflightPlanDefinitions(lifecycle)
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "existing owner lifecycle/receipt SQL-fact seeds; " + name + "; synthetic W frame only, no command authority/resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(defs, g.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Manifest) != 4 || g.ForeignKeys != nil {
			t.Fatal("closed four-ID read population", len(g.Manifest), g.ForeignKeys)
		}
		for _, p := range g.Plans {
			plans++
			vm += len(p.Explain.Values)
			if len(p.QueryPlan.Values) != 1 || !strings.HasPrefix(p.QueryPlan.Values[0][3].(string), "SEARCH ") || p.Insert != nil {
				t.Fatal("not one indexed read", p.ID, p.Cohort)
			}
			seeks++
		}
		// Execute only the read definitions on their actual seeded populations.
		// NULL and empty byte counts remain distinct before any owner body decoder.
		for _, d := range defs {
			for _, c := range d.Cohorts {
				statement, err := registry.Lookup(d.ID)
				if err != nil {
					t.Fatal(err)
				}
				bound, err := statement.Bind(c.Bindings...)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := bound.QueryContext(tx.ctx, tx.conn)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					count++
					if strings.Contains(string(d.ID), "_lengths") {
						values := make([]sql.NullInt64, len(columns))
						pointers := make([]any, len(values))
						for i := range values {
							pointers[i] = &values[i]
						}
						if err := rows.Scan(pointers...); err != nil {
							t.Fatal(err)
						}
						switch d.ID {
						case "scan.issuer.run_lengths":
							if len(values) != 17 {
								t.Fatal("complete preserved run projection", len(values))
							}
							for _, n := range values[12:] {
								if n != (sql.NullInt64{Int64: 0, Valid: true}) {
									t.Fatal("preserved empty run TEXT is not NULL", values)
								}
							}
							if values[9] != (sql.NullInt64{Int64: 16, Valid: true}) || values[10] != (sql.NullInt64{Int64: 32, Valid: true}) {
								t.Fatal("native BLOB byte widths", values)
							}
						case "scan.issuer.job_lengths":
							if values[3] != (sql.NullInt64{Int64: 0, Valid: true}) {
								t.Fatal("empty TEXT must be zero, not NULL", values)
							}
						case "scan.issuer.receipt_lengths":
							if values[0] != (sql.NullInt64{Int64: 16, Valid: true}) || values[1] != (sql.NullInt64{Int64: 32, Valid: true}) || values[8].Valid {
								t.Fatal("fixed BLOB and nullable group byte widths", values)
							}
						case "scan.inventory_accept.member_lengths":
							if values[0] != (sql.NullInt64{Int64: int64(len("object-hit")), Valid: true}) {
								t.Fatal("native object byte width", values)
							}
						}
					} else {
						values := make([]any, len(columns))
						pointers := make([]any, len(values))
						for i := range values {
							pointers[i] = &values[i]
						}
						if err := rows.Scan(pointers...); err != nil {
							t.Fatal(err)
						}
						if c.Name == "gapped_keys" && values[0] != int64(1) {
							t.Fatal("actual gap population", values)
						}
					}
				}
				err = errors.Join(rows.Err(), rows.Close())
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if strings.HasPrefix(c.Name, "missing_") || strings.HasPrefix(c.Name, "drained_") || c.Name == "wrong_library" {
					want = 0
				}
				if count != want {
					t.Fatal(d.ID, c.Name, "population rows", count, "want", want)
				}
			}
		}
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("read Capture/queries changed population", before, after, err)
		}
		populations = append(populations, population{name, g})
		if _, err := tx.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	if plans != 18 || seeks != 18 {
		t.Fatal("eight IDs/eighteen actual cohorts", plans, seeks)
	}
	actual, err := json.MarshalIndent(populations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "n1-byte-preflights.json")
	if *recordN1PreflightPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	t.Logf("8 IDs/18 actual cohorts/%d static VM/%d SEARCH/no DML; source journal %s; no N1 authority/profile/runtime qualification", vm, seeks, journal)
}
