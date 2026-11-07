package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"io"
	"os"
	"path/filepath"
	"testing"
)

var recordWCProgressCompositionPlans = flag.Bool("record-wc-progress-composition-plans", false, "deliberately record WC progress composition plans")

func TestWCProgressCompositionPlans(t *testing.T) {
	w, _, _ := wcProgressCompositionFixtureClock(t, *recordWCProgressCompositionPlans)
	e := w.effective.executor
	// Native birth executed through the shared NORMAL frame. The additional
	// Capture is compile-only: no source acceptance or latency qualification.
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := e.config
	policy.Synchronous = sqliteconn.Normal
	if _, err = sqliteconn.Assert(t.Context(), conn, policy); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range []string{"wc_component_fixture_test.go", "wc_fixture_clock_test.go", "wc_progress_composition_test.go", "wc_progress_composition_plan_test.go", "wc_progress_protocol.go", "wc_progress_native.go", "wc_progress_execution.go", "wc_progress_protocol_receipt.go", "wc_progress_fixture.go", "wc_progress_report_policy.go"} {
		data, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	defs := append(wcProgressCommandDefinitions(), wcProgressBirthDefinitions()...)
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "WC actual owned opening/native births; NORMAL compiled progress/history/ACK/snapshot/fixture definitions; no authority or qualification inferred from cohorts"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != len(defs) || len(g.Plans) != len(defs) || g.ForeignKeys == nil || len(g.ForeignKeys.Plans) != 686 {
		t.Fatal("incomplete capture")
	}
	fk, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	g.ForeignKeys = nil
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join("testdata", "wc-progress-composition.json")
	fkPath := filepath.Join("testdata", "wc-progress-composition-fk.json.gz")
	if *recordWCProgressCompositionPlans {
		if err = os.WriteFile(jsonPath, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err = gz.Write(fk); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Compare(expected); err != nil {
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
	if err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareExactJSON(expectedFK, fk); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d exact IDs/plans; complete 686FK/2058 probes, actual NORMAL; no runtime/qualification grant", len(defs))
}
