package writer

import (
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

var recordBasicShapePlans = flag.Bool("record-basic-shape-plans", false, "deliberately record closed Basic shape dictionary component plans")

// Native effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
func TestBasicShapeStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_shape.go", "204f51c3edfdcf2b4e18c0b50fe5000fd6cb2449e61d638832c9ed2113b0b9b0"},
		{"basic_shape_statements.go", "09d6607a27d49aa0a7331d82258ab5cbfb0347a708b44a1d739728dd9cd443b5"},
		{"basic_shape_test.go", "6cd1abf34e666b8fee4bdcff08a438e1a0364703045265c9ac956d7045131a4e"},
		{"basic_probe.go", "6c7e37f0fdba7a21b42dbe7d64908a22f2f8010ff50d8b7bf536f15c01f893ca"},
		{"basic_probe_statements.go", "0905369ce7ca576a1078070404ad501468d2eb7b5c7b3cf594da25848c1b9262"},
		{"basic_probe_test.go", "77de9ba74cce92bed2665241dc27c0352c62785532de5888b853b4569d643c97"},
		{"basic_stage.go", "b2adbf700ce9928f1a2b388322124455b564780b85d71a6026861342aed52b23"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
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
	defs := basicShapeDefinitions()
	if len(defs) != 4 || defs[0].ID != basicShapeLookup || defs[1].ID != basicShapeInsert || defs[2].ID != basicShapeRefCAS || defs[3].ID != basicShapeHeaderCAS {
		t.Fatal("closed shape inventory")
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohorts", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	// The unchanged owner body installs the actual NEW scalar tuple and commits.
	// Only the four shape definitions below enter Capture.
	w, _ := basicShapeFixture(t)
	var shapes, used int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_info_shapes),(SELECT used_bytes FROM catalog_info_input_budget)`).Scan(&shapes, &used); err != nil || shapes != 0 || used != 2065 {
		t.Fatal("initial empty dictionary population", shapes, used, err)
	}
	// The prerequisite owner probe turn returned its synthetic NORMAL
	// connection to the private pool; discard that idle connection so this
	// startup assertion opens a fresh DSN-configured FULL writer connection.
	w.db.SetMaxIdleConns(0)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), conn, w.config)
	closeErr := conn.Close()
	w.db.SetMaxIdleConns(w.config.Connections)
	if assertErr != nil || closeErr != nil || startup.Synchronous != 2 {
		t.Fatal(startup, assertErr, closeErr)
	}
	t.Logf("Basic FULL fixture startup: %+v", startup)
	// Existing owner helper selects NORMAL on W's private synthetic component
	// turn and already registers its complete component set with an actual receipt.
	turn := basicProbeTestTurn(t, w, basicShapeTestDefinitions())
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
	groups := []struct {
		name string
		defs []stmt.Definition
	}{
		{"initial_empty_dictionary_lookup_and_insert", []stmt.Definition{defs[0], defs[1]}},
		{"seeded_existing_dictionary_ref_and_header_cas", []stmt.Definition{defs[2], defs[3]}},
	}
	for n, group := range groups {
		if n == 1 {
			// The existing branch reuses this exact tuple with two prior refs.
			if _, err := tx.conn.ExecContext(tx.ctx, `INSERT INTO catalog_info_shapes VALUES(61,'mkv','h264',0,2)`); err != nil {
				t.Fatal(err)
			}
			var seeded, prepared int64
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT count(*) FROM catalog_info_shapes WHERE id=61 AND container='mkv' AND video_codec='h264' AND height=0 AND ref_count=2`).Scan(&seeded); err != nil || seeded != 1 {
				t.Fatal("seeded existing dictionary population", seeded, err)
			}
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT target_shape_id IS NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL AND target_available IS NULL AND job_revision=1 AND copy_family=0 AND phase=0 AND expected_epoch=17 AND input_revision=7 AND family_mask=511 FROM catalog_info_changes WHERE id=51`).Scan(&prepared); err != nil || prepared != 1 {
				t.Fatal("prepared header population", prepared, err)
			}
		}
		catalogue, err := stmt.Describe(group.defs)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen basicShapeFixture; " + group.name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group.defs, g.Receipt()); err != nil {
			t.Fatal("exact population Capture receipt", err)
		}
		// UPDATE/read-only populations need the independently executed full FK
		// audit; Capture attaches it automatically only for INSERT destinations.
		if g.ForeignKeys == nil {
			fk, err := plan.ForeignKeys(tx.ctx, tx.conn, config)
			if err != nil {
				t.Fatal(err)
			}
			g.ForeignKeys = &fk
		}
		if len(g.Manifest) != len(group.defs) || len(g.Plans) != len(group.defs) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
			t.Fatal("incomplete Basic statement/destination/FK audit")
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				if p.ID != basicShapeInsert || p.Insert.Table != "catalog_info_shapes" {
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
		captures = append(captures, population{group.name, g})
	}
	if inserts != 1 {
		t.Fatal("INSERT destination inventory", inserts)
	}
	referencePath := filepath.Join("testdata", "scan-basic-media-fk.json.gz")
	referenceGzip, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	reference := readScanFreshFK(t, referencePath)
	if err := plan.CompareExactJSON(reference, fkJSON); err != nil {
		t.Fatal("Basic changed existing complete686-FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-shape.json")
	fkPath := filepath.Join("testdata", "scan-basic-shape-fk.json.gz")
	if *recordBasicShapePlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		// The existing686-FK/2058-probe payload is exact, so preserve its
		// reviewed gzip bytes rather than recompressing an identical image.
		if err := os.WriteFile(fkPath, referenceGzip, 0644); err != nil {
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
	t.Logf("4IDs/4cohorts/two actual shape populations/%dVM/%dSEARCH/1INSERT/686FK2058probes; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}
