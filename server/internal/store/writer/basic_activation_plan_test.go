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

var recordBasicActivationPlans = flag.Bool("record-basic-activation-plans", false, "deliberately record closed Basic activation component plans")

// Native effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
func TestBasicActivationStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_activation.go", "698e4a835b3248f22827fee55c0010c4b18cd203a0ec510e6b4b2f15881eaadf"},
		{"basic_activation_statements.go", "b9e9679887ea01c52cfafb30fbebb3add49d88343ea2a8da91ae17be5633c828"},
		{"basic_activation_test.go", "ae94d63290255b4854df9f98206a70619810611235d61e15022e85dbf404f8a3"},
		{"basic_stage.go", "b2adbf700ce9928f1a2b388322124455b564780b85d71a6026861342aed52b23"},
		{"basic_stage_test.go", "8e8da49da22747d1d709405398198f8c3fd46400bc63c9fb434758c553a17966"},
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
	defs := basicActivationDefinitions()
	if len(defs) != 4 {
		t.Fatal("closed activation inventory", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohorts", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w, _ := basicActivationFixture(t)
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
	// turn and already registers its complete fourteen-ID set with an actual receipt.
	turn := basicActivationTestTurn(t, w, basicActivationTestDefs())
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
		name := "original_activation_vector"
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen basicActivationFixture; " + name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
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
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) || g.ForeignKeys == nil || plan.CheckSchemaForeignKeys(g.ForeignKeys) != nil {
			t.Fatal("incomplete Basic statement/destination/FK audit")
		}
		for _, p := range g.Plans {
			if len(p.Explain.Values) == 0 {
				t.Fatal("missing actual VM", p.ID)
			}
			vm += len(p.Explain.Values)
			if p.Insert != nil {
				t.Fatal("unexpected INSERT destination", p.ID, p.Insert.Table)
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
	if inserts != 0 {
		t.Fatal("INSERT destination inventory", inserts)
	}
	reference := readScanFreshFK(t, filepath.Join("testdata", "scan-inventory-accept-fk.json.gz"))
	if err := plan.CompareExactJSON(reference, fkJSON); err != nil {
		t.Fatal("Basic changed existing complete shared FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-activation.json")
	fkPath := filepath.Join("testdata", "scan-basic-activation-fk.json.gz")
	if *recordBasicActivationPlans {
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
	t.Logf("4IDs/4cohorts/one actual activation population/%dVM/%dSEARCH/0INSERT/complete shared FK/probe graph; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}
