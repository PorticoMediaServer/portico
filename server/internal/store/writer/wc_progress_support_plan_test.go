package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordWCProgressSupportPlans = flag.Bool("record-wc-progress-support-plans", false, "deliberately record closed WC progress component plans")

// Separate real NULL/existing-debt Progress ACK and FULL parent-event populations.
// Compile only; these fixtures issue no production authority or resource profile.
func TestWCProgressSupportStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"wc_008_native_fixture_test.go", "029dda9c571f6361c1914d86ce49d03c98c697312b442eda54e001dd03012adc"},
		{"wc_008_native_test.go", "7a9ad4743191726eff7756d217ed6a813258e26fdc814c5f1748325cfa4ab404"},
		{"wc_008_draft_test.go", "6a2cd67038c053e55f78054d3add8585a25d7e23da17929d5c8ba92e5e64884d"},
		{"wc_progress_spans_test.go", "2ebd52d78e1fdadd0e0f041bbad1cdd6be06ac6160668d94776bf0e3269a8017"},
		{"wc_progress_support_test.go", "6ce65215d872867d5766adca6990078daee4e74e4f3a5f4e36550bff2e5c9a5c"},
	}

	hash := sha256.New()
	for _, part := range parts {
		data, err := os.ReadFile(part.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != part.sha {
			t.Fatal("frozen fixture source changed; review before recording", part.name)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part.name))))
		hash.Write([]byte(part.name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	journal := hex.EncodeToString(hash.Sum(nil))
	if journal != "e9eb4676b400aefd052678a3432759897533c0889ba989d3f9b0a4a4d49f4dd0" {
		t.Fatal("fixture journal changed")
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, population := range []string{"source_ack_null_due", "source_ack_existing_due", "api_parent_full"} {
		t.Run(population, func(t *testing.T) {
			defs := wcAPIEventDefinitions()
			purpose, synchronous := ProgressAccept, sqliteconn.Normal
			if population == "api_parent_full" {
				purpose, synchronous = SecurityAccept, sqliteconn.Full
			} else {
				defs = append(wcProgressAckDefinitions(), defs...)
			}
			wantDefinitions := 5
			if population == "api_parent_full" {
				wantDefinitions = 3
			}
			if len(defs) != wantDefinitions {
				t.Fatal("definition inventory changed")
			}
			for _, d := range defs {
				if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
					t.Fatal("closed hot cohort inventory changed", d.ID)
				}
				if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
					t.Fatal(d.ID, issues)
				}
			}
			catalogue, err := stmt.Describe(defs)
			if err != nil {
				t.Fatal(err)
			}
			w := wc008NativeExecutor(t)
			conn, err := w.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
			closeErr := conn.Close()
			if assertErr != nil || closeErr != nil {
				t.Fatal(startup, assertErr, closeErr)
			}
			t.Logf("fixture startup physical settings: %+v", startup)
			if population != "api_parent_full" {
				due := sql.NullInt64{}
				if population == "source_ack_existing_due" {
					due = sql.NullInt64{Int64: 17, Valid: true}
				}
				q := wcNativeProgressAckFixture(t, w, 0, due)
				if q.receiptRows != 1 || q.receiptVariableBytes != 135 || q.owner.lastSequence != 1 || q.owner.acknowledgedSequence != 0 || q.due != due {
					t.Fatal("ACK population no longer matches frozen cohort", q)
				}
			}
			wcNativeAPIEventCapacity(t, w)
			turn := wc008NativeTurn(t, w, purpose, defs)
			tx := turn.transaction
			connectionPolicy := w.config
			connectionPolicy.Synchronous = synchronous
			settings, err := sqliteconn.Assert(tx.ctx, tx.conn, connectionPolicy)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s acquired physical settings: %+v", population, settings)
			var before, after int64
			if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			golden, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: connectionPolicy, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "WC actual " + population + " fixture; synthetic component custody/forecasts, not production admission or resource qualification"}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stmt.New(defs, golden.Receipt()); err != nil {
				t.Fatal("exact full-set Capture receipt", err)
			}
			if len(golden.Manifest) != wantDefinitions || len(golden.Plans) != wantDefinitions {
				t.Fatal("incomplete statement/destination/FK capture")
			}
			vm, search, inserts := 0, 0, 0
			for _, p := range golden.Plans {
				if len(p.Explain.Values) == 0 {
					t.Fatal("empty VM", p.ID, p.Cohort)
				}
				vm += len(p.Explain.Values)
				search += len(p.QueryPlan.Values)
				if p.Insert != nil {
					inserts++
				}
			}
			if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
				t.Fatal("Capture mutated population", before, after, err)
			}
			// Independent audit also covers UPDATE-only inventories; never rely on
			// INSERT-triggered Capture auditing as a substitute for the full graph.
			fk, err := cachedForeignKeys(tx.ctx, tx.conn, connectionPolicy)
			if err != nil {
				t.Fatal(err)
			}
			if plan.CheckSchemaForeignKeys(&fk) != nil {
				t.Fatal("FK coverage changed")
			}
			for _, p := range fk.Plans {
				if len(p.Delete.Values) == 0 || len(p.Update.Values) == 0 || len(p.Lookup.Values) == 0 {
					t.Fatal("missing FK probe", p.Child, p.ID)
				}
			}
			fkJSON, err := json.MarshalIndent(fk, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden.ForeignKeys = nil
			actual, err := golden.JSON()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "wc-progress-support-"+population+".json")
			fkPath := filepath.Join("testdata", "wc-progress-support-normal-fk.json.gz")
			if population == "api_parent_full" {
				fkPath = filepath.Join("testdata", "wc-progress-support-full-fk.json.gz")
			}
			if *recordWCProgressSupportPlans {
				if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
				if population != "source_ack_existing_due" {
					var compressed bytes.Buffer
					gz := gzip.NewWriter(&compressed)
					if _, err := gz.Write(fkJSON); err != nil {
						t.Fatal(err)
					}
					if err := gz.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := golden.Compare(expected); err != nil {
				t.Fatal(err)
			}
			compressed, err := os.ReadFile(fkPath)
			if err != nil {
				t.Fatal(err)
			}
			gz, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatal(err)
			}
			expectedFK, err := io.ReadAll(gz)
			closeErr = gz.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if err := plan.CompareExactJSON(expectedFK, fkJSON); err != nil {
				t.Fatal(err)
			}
			t.Logf("%dIDs/%dcohorts/%dVM/%dEQP SEARCH rows/%dINSERT destinations/shared-schema FK audit; exact receipt; no production profile", wantDefinitions, len(golden.Plans), vm, search, inserts)
		})
	}
}
