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

var recordBasicRetirementPlans = flag.Bool("record-basic-retirement-plans", false, "deliberately record closed Basic retirement component plans")

// Native effect-fact populations only. No complete Basic command, receipt,
// source authority, production admission or physical budget is qualified here.
func TestBasicRetirementStatementPlans(t *testing.T) {
	parts := []struct{ name, sha string }{
		{"basic_retire_first.go", "8dd244b93d0b65b58b5a49c962240bb1ec19f1ce01d7923b5158058b257335f8"},
		{"basic_retire_first_statements.go", "a24528562548c9f3ea0e6b257d3b0a996eb16c45748a397c03c5fc1f8ce0f6ae"},
		{"basic_retire_first_test.go", "afbad3b0436ea59dc5fd5ace757e676c548623956c44761f4a6c63edb5c8fdee"},
		{"basic_begin_first_test.go", "dacddb738f798ce60ac69053daec8bf97fae17a6dfc236453c6a5d413750f605"},
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
	defs := basicRetireFirstDefinitions()
	if len(defs) != 32 {
		t.Fatal("closed retirement inventory", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) != 1 {
			t.Fatal("closed hot cohorts", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	w, source := basicStreamFixture(t)
	seedBasicRetiredFirstInput(t, w, source)
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
	// Existing owner helper selects NORMAL on the private component frame
	// after FULL startup Capture of the complete body recipe.
	turn := basicRetireFirstComponentTurn(t, w, basicRetireFirstTestDefinitions())
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
		name := "retired_first_input"
		catalogue, err := stmt.Describe(group)
		if err != nil {
			t.Fatal(err)
		}
		var before, after int64
		if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
			t.Fatal(err)
		}
		g, err := plan.Capture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed, Implementations: "frozen basicRetireFirstFixture; " + name + "; synthetic W component turn/forecasts; no command or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(group, g.Receipt()); err != nil {
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
		if len(g.Manifest) != len(group) || len(g.Plans) != len(group) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
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
		t.Fatal("Basic changed existing complete686-FK image", err)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-basic-retirement.json")
	fkPath := filepath.Join("testdata", "scan-basic-retirement-fk.json.gz")
	if *recordBasicRetirementPlans {
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
	t.Logf("32IDs/32cohorts/one actual retirement population/%dVM/%dSEARCH/0INSERT/686FK2058probes; seed journal%s; no Basic admission/authority/HOLD qualification", vm, search, journal)
}
