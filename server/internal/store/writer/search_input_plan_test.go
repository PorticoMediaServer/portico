package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

var recordSearchInputPlans = flag.Bool("record-search-input-plans", false, "deliberately record search input statement plans")

type searchInputPlanPopulation struct {
	Name   string      `json:"name"`
	Golden plan.Golden `json:"golden"`
}

// These captures compile the actual private SQL. They do not execute the source
// helper, construct Applied, validate a receipt digest or measure writer holds.
func TestSearchInputStatementPlans(t *testing.T) {
	seed, err := os.ReadFile("search_intent_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end := bytes.Index(seed, []byte("func searchInputFixture(")), bytes.Index(seed, []byte("func searchInputBound("))
	if start < 0 || end <= start {
		t.Fatal("missing search input fixture")
	}
	seed = seed[start:end]
	seedHash := sha256.Sum256(seed)
	if hex.EncodeToString(seedHash[:]) != "d0e1a5a8fe1fb6efbf9fa0ac4b3c50ec13e6ab6b0ffbb77661e7f379eaddbff9" {
		t.Fatal("search input fixture changed; review populations before recording")
	}
	// Exact existing metadata-only BuildingClockCAS fixture. This is not a
	// second native bank or a compatible publisher/profile population.
	const buildingSeed = `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(2,'clock_fixture_only','fixture_recipe','fixture_engine','fixture_unicode','building',1000)`
	const buildingHead = `UPDATE search_revision SET building_generation_id=? WHERE singleton=1`
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	var manifest []stmt.Entry
	var populations []searchInputPlanPopulation
	var fk *plan.ForeignKeyAudit
	seen := map[string]bool{}
	for _, building := range []bool{false, true} {
		name := "active_only"
		if building {
			name = "metadata_building_clock"
		}
		t.Run(name, func(t *testing.T) {
			w, actualRegistry := searchInputFixture(t)
			if len(actualRegistry.Manifest()) != 9 {
				t.Fatal("search input definition inventory changed")
			}
			if manifest == nil {
				manifest = actualRegistry.Manifest()
			}
			if building {
				if _, err := w.db.ExecContext(t.Context(), buildingSeed); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), buildingHead, int64(2)); err != nil {
					t.Fatal(err)
				}
			}
			startupConn, err := w.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			startup, assertErr := sqliteconn.Assert(t.Context(), startupConn, w.config)
			closeErr := startupConn.Close()
			if assertErr != nil || closeErr != nil {
				t.Fatalf("startup assertion=%v close=%v", assertErr, closeErr)
			}
			t.Logf("startup FULL physical settings: %+v", startup)
			turn := searchInputTestTurn(t, w, actualRegistry, &testCustody{})
			tx := turn.transaction
			policy := w.config
			policy.Synchronous = sqliteconn.Normal
			actual, err := sqliteconn.Assert(tx.ctx, tx.conn, policy)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Inventory NORMAL physical settings: %+v", actual)
			// Use only the compiled clock variant matching this real state;
			// together the two immutable captures cover all9IDs/12cohorts.
			var selected []stmt.Definition
			for _, d := range searchInputIntentDefinitions() {
				if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
					t.Fatalf("%s must be hot without waivers", d.ID)
				}
				if (building && d.ID == searchInputBuilding) || (!building && d.ID != searchInputBuilding) {
					selected = append(selected, d)
				}
			}
			registry, err := stmt.Describe(selected)
			if err != nil {
				t.Fatal(err)
			}
			var before int64
			if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			fixtureBytes := append([]byte(nil), seed...)
			fixtureBytes = append(fixtureBytes, []byte(name)...)
			if building {
				fixtureBytes = append(fixtureBytes, []byte(buildingSeed+buildingHead+"|2")...)
			}
			fixtureHash := sha256.Sum256(fixtureBytes)
			golden, err := cachedCapture(tx.ctx, tx.conn, registry, plan.Fixture{
				ConnectionPolicy: policy, SHA256: hex.EncodeToString(fixtureHash[:]), Statistics: plan.Unanalyzed,
				Implementations: "bundled SQLite3.53.4; builtin functions/collations; searchInputFixture fixed stale tuple; " + name + "; compile only, no native-v2 claim",
			}, indexPolicy)
			if err != nil {
				t.Fatal(err)
			}
			var after int64
			if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("capture mutated fixture: total_changes %d -> %d", before, after)
			}
			for _, p := range golden.Plans {
				key := string(p.ID) + "/" + p.Cohort
				if seen[key] || len(p.Explain.Values) == 0 {
					t.Fatalf("duplicate/empty capture %s", key)
				}
				seen[key] = true
			}
			if golden.ForeignKeys != nil {
				if fk != nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
					t.Fatal("incomplete/duplicate FK audit")
				}
				fk = golden.ForeignKeys
				golden.ForeignKeys = nil
			}
			populations = append(populations, searchInputPlanPopulation{name, golden})
		})
	}
	if t.Failed() {
		t.FailNow()
	}
	if len(seen) != 12 || fk == nil {
		t.Fatalf("capture inventory cohorts=%d FK=%v", len(seen), fk != nil)
	}
	actual, err := json.MarshalIndent(struct {
		Manifest    []stmt.Entry                `json:"manifest"`
		Populations []searchInputPlanPopulation `json:"populations"`
	}{manifest, populations}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "search-input-intent.json")
	fkPath := filepath.Join("testdata", "search-input-intent-fk.json.gz")
	if *recordSearchInputPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
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
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(gz)
	closeErr := gz.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("FK read=%v close=%v", err, closeErr)
	}
	if err := plan.CompareJSON(expected, fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Log("9compiled definitions/12cohorts; two actual clock states; one INSERT destination;shared-schema FK audit; no accepting helper executed")
}
