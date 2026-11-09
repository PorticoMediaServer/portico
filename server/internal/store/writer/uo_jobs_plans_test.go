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
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUOJobLeasePlans = flag.Bool("record-uo-job-lease-plans", false, "record UO operational lease component native plans")

func TestUOJobLeasePlans(t *testing.T) {
	w := uoJobExecutor(t)
	uoJobSeed(t, w)
	defs := uoJobDefinitions()
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
	// The fixture journal is the closed native population seeded above. It is
	// not a shared baseline: this golden owns only the UO operational lease
	// statements and is recorded against the schema on main.
	hash := sha256.Sum256([]byte("uo-job-lease-native-fixture-v1"))
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed} {
		golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{
			ConnectionPolicy: w.config,
			SHA256:           hex.EncodeToString(hash[:]),
			Statistics:       stats,
			Implementations:  "UO operational lease component native fixture; no command registration, authority, readiness or resource qualification",
		}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(defs, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/uo-job-lease-plans.json.gz"
	if *recordUOJobLeasePlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
