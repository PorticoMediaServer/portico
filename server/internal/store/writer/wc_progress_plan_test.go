package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

var recordWCProgressPlans = flag.Bool("record-wc-progress-plans", false, "deliberately record closed WC progress component plans")

// Separate real capture-start and pending-drain populations. These compile the
// frozen component statements, without issuing or admitting a production command.
func TestWCProgressStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"wc_008_native_fixture_test.go", "029dda9c571f6361c1914d86ce49d03c98c697312b442eda54e001dd03012adc"},
		{"wc_008_native_test.go", "7a9ad4743191726eff7756d217ed6a813258e26fdc814c5f1748325cfa4ab404"},
		{"wc_008_draft_test.go", "6a2cd67038c053e55f78054d3add8585a25d7e23da17929d5c8ba92e5e64884d"},
		{"wc_progress_capture_test.go", "c0b751bb94fcc97ff525cf5132416df1271a74f454c0f9fa4136f7b8810a720c"},
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
	if journal != "d61a00e7bd19412ce8cec7ae1b920a96f5bf0afd1938faf2a4d2177648415d59" {
		t.Fatal("fixture journal changed")
	}
	defs := wcProgressDefinitions()
	if len(defs) != 37 {
		t.Fatalf("definitions %d, want37", len(defs))
	}
	cohorts := 0
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohort inventory changed", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		cohorts += len(d.Cohorts)
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	for _, population := range []string{"capture_start", "pending_drain"} {
		t.Run(population, func(t *testing.T) {
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
			if population == "capture_start" {
				wcCaptureFixture(t, w)
			} else {
				wc008NativeProgress(t, w, 1000)
			}
			turn := wc008NativeTurn(t, w, ProgressAccept, defs)
			tx := turn.transaction
			connectionPolicy := w.config
			connectionPolicy.Synchronous = sqliteconn.Normal
			settings, err := sqliteconn.Assert(tx.ctx, tx.conn, connectionPolicy)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("progress NORMAL acquired physical settings: %+v", settings)
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
			if len(golden.Manifest) != 37 || len(golden.Plans) != cohorts || golden.ForeignKeys == nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
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
			fkJSON, err := json.MarshalIndent(golden.ForeignKeys, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			golden.ForeignKeys = nil
			actual, err := golden.JSON()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "wc-progress-"+population+".json")
			fkPath := filepath.Join("testdata", "wc-progress-fk.json.gz")
			if *recordWCProgressPlans {
				if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
				if population == "capture_start" {
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
			t.Logf("37IDs/%dcohorts/%dVM/%dEQP SEARCH rows/%dINSERT destinations/shared-schema FK audit; exact receipt; no production profile", cohorts, vm, search, inserts)
		})
	}
}
