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

var recordWCSecurityCompositionPlans = flag.Bool("record-wc-security-composition-plans", false, "deliberately record WC security composition plans")

func TestWCSecurityCompositionPlans(t *testing.T) {
	w, driver, family := wcSecurityCompositionFixtureClock(t, *recordWCSecurityCompositionPlans, "owner", "member")
	if _, err := w.CurrentSecurity(t.Context(), driver.Disclosure(), family); err != nil {
		t.Fatal(err)
	}
	e := w.effective.executor
	// A real current target snapshot committed through the shared FULL frame. The additional
	// Capture is compile-only: no source acceptance or latency qualification.
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := e.config
	policy.Synchronous = sqliteconn.Full
	if _, err = sqliteconn.Assert(t.Context(), conn, policy); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range []string{"wc_fixture_clock_test.go", "wc_security_composition_test.go", "wc_security_composition_plan_test.go", "wc_security_composition.go", "wc_security_dispatch.go", "wc_security_protocol.go", "wc_security_protocol_execution.go", "wc_security_protocol_receipt.go", "wc_security_008_draft.go", "wc_security_receipts_008_draft.go", "wc_progress_native.go", "wc_progress_fixture.go", "wc_api_events.go"} {
		data, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	defs := wcSecurityCommandDefinitions()
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
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "WC actual owned opening/native admin and target births; FULL current snapshot then security/original/ACK/event definitions; no authority or qualification inferred from cohorts"}, indexPolicy)
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
	jsonPath := filepath.Join("testdata", "wc-security-composition.json")
	fkPath := filepath.Join("testdata", "wc-security-composition-fk.json.gz")
	if *recordWCSecurityCompositionPlans {
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
	t.Logf("%d exact IDs/plans; complete 686FK/2058 probes, actual FULL; no runtime/qualification grant", len(defs))
}
