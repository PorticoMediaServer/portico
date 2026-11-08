package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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

var recordBasicBeginPlans = flag.Bool("record-basic-begin-plans", false, "deliberately record closed Basic probe scalar component plans")

// Native effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
func TestBasicBeginStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_begin.go", "5d5a49244c6b8a7424f8da07bf22d6ce4d47feb2132546fc4fbf4f34e241dd0d"},
		{"basic_begin_statements.go", "aaa49e541f13dea66b53f0276b7cc93bcdba90e73c5bd051d7f37208180f43e0"},
		{"basic_begin_first.go", "5f95ebedaa11b72e3ec75d36b781c60dc13621f674e611f6343bd77595fbfbce"},
		{"basic_begin_first_statements.go", "6c15bb199de1c04488f92da7667e1dcfe6d4a5d5bb7489d85fbc1e8cd8a4c5b2"},
		{"basic_begin_test.go", "25ecc3929159193d471a047d39473d628c2927824b20a6b8a73e560ec1dd274a"},
		{"basic_begin_first_test.go", "dacddb738f798ce60ac69053daec8bf97fae17a6dfc236453c6a5d413750f605"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
		{"basic_stage.go", "b2adbf700ce9928f1a2b388322124455b564780b85d71a6026861342aed52b23"},
		{"basic_media_stage_test.go", "22e19f73b2d0bf0fca6f6a7040a71314bc9f0657d5f3ae884abcbd02d3acf1a6"},
		{"native_registry_test.go", "27c84869c377c70f0332fbaedc6c621cb0a07a8a768012f7bb10cca457d19e50"},
	}
	h := sha256.New()
	for _, p := range parts {
		b, err := os.ReadFile(p.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != p.sha {
			t.Fatal("frozen Basic source changed; review before recording", p.name)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(p.name))))
		h.Write([]byte(p.name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
		h.Write(b)
	}
	journal := hex.EncodeToString(h.Sum(nil))
	defs := basicBeginDefinitions()
	if len(defs) != 7 {
		t.Fatal("closed probe inventory", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohorts", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w, _ := basicStreamFixture(t)
	seedBasicFirstOrdinaryOwner(t, w)
	var hit, eligible bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=41 AND asset_token='basic-asset'), (SELECT count(*)=1 FROM catalog_asset_links WHERE asset_id=41 AND entity_id=31 AND info_shape_id IS NULL AND info_pending_change IS NULL) AND (SELECT info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL FROM catalog_assets WHERE id=41)`).Scan(&hit, &eligible); err != nil || !hit || !eligible {
		t.Fatal("capture population", hit, eligible, err)
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
	t.Logf("Basic FULL fixture startup: %+v", startup)
	// Existing owner helper selects NORMAL on W's private synthetic component
	// turn and already registers its complete fifteen-ID set with an actual receipt.
	turn := basicProbeTestTurn(t, w, basicBeginTestDefinitions())
	tx := turn.transaction
	config := w.config
	config.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Basic NORMAL capture boundary: %+v", settings)
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
	captures := []population{}
	var fkJSON []byte
	vm, search, inserts := 0, 0, 0
	for _, group := range [][]stmt.Definition{defs} {
		name := "existing_input_hit_and_unfenced_sole_ordinary_owner"
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen basicStreamFixture + seedBasicFirstOrdinaryOwner; EXPLAIN INSERT is compile/destination only, no eligibility or Applied claim; " + name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group, g.Receipt()); err != nil {
			t.Fatal("exact population Capture receipt", err)
		}
		// UPDATE/read-only populations need the independently executed full FK
		// audit; Capture attaches it automatically only for INSERT destinations.
		if g.ForeignKeys == nil {
			fk, err := cachedForeignKeys(tx.ctx, tx.conn, config)
			if err != nil {
				t.Fatal(err)
			}
			g.ForeignKeys = &fk
		}
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
			t.Fatal("incomplete Basic statement/destination/FK audit")
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				if !((p.ID == basicBeginChangeInsert && p.Insert.Table == "catalog_info_changes") || (p.ID == basicBeginWitnessInsert && p.Insert.Table == "catalog_info_input_witnesses") || (p.ID == basicBeginFamiliesInsert && p.Insert.Table == "catalog_info_input_families")) {
					t.Fatal("unexpected INSERT destination", p.ID, p.Insert.Table)
				}
				inserts++
			}
			for _, row := range p.QueryPlan.Values {
				if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
					t.Fatal("unexpected native plan", p.ID, row)
				}
				search++
			}
		}
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
			t.Fatal("Capture changed population", before, after, err)
		}
		fk, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if fkJSON == nil {
			fkJSON = fk
		} else if err := plan.CompareExactJSON(fkJSON, fk); err != nil {
			t.Fatal("population changed global FK audit", err)
		}
		g.ForeignKeys = nil
		captures = append(captures, population{name, g})
	}
	if inserts != 3 {
		t.Fatal("INSERT destination inventory", inserts)
	}
	reference := readScanFreshFK(t, filepath.Join("testdata", "scan-basic-media-fk.json.gz"))
	if err := plan.CompareExactJSON(reference, fkJSON); err != nil {
		t.Fatal("Basic changed existing complete686-FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-begin.json")
	fkPath := filepath.Join("testdata", "scan-basic-begin-fk.json.gz")
	if *recordBasicBeginPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err := z.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, b.Bytes(), 0644); err != nil {
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
	if err := plan.CompareExactJSON(readScanFreshFK(t, fkPath), fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("7IDs/7cohorts/one actual begin population/%dVM/%dSEARCH/3INSERT/686FK2058probes; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}
