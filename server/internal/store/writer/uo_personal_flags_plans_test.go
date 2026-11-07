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
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUOFlagsPlans = flag.Bool("record-uo-flags-plans", false, "record UO personal flag component native plans")

func TestUOFlagsPlans(t *testing.T) {
	w, q := uoFlagsFixture(t)
	wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id) VALUES('profile-one',?,2111,7)`, q.publicID[:])
	defs := uoPersonalFlagsDefinitions()
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
	// The exact fixture constructor is the source journal; unrelated test edits
	// do not change it. The insert above is included as its own journal entry.
	data, err := os.ReadFile("uo_personal_flags_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start, end := strings.Index(source, "func uoFlagsFixture("), strings.Index(source, "// Component execution")
	if start < 0 || end <= start {
		t.Fatal("fixture constructor journal missing")
	}
	hash := sha256.Sum256([]byte(source[start:end] + "\x00INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id) VALUES('profile-one',?,2111,7)"))
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var captures []plan.Golden
	// Follow the merged WC full-writer Capture under its declared unanalyzed
	// statistics. Analyzed full-schema FK plans belong on QS's representative
	// corpus; tiny unrelated child tables legitimately choose scans after ANALYZE.
	for _, stats := range []plan.Statistics{plan.Unanalyzed} {
		golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: stats, Implementations: "UO personal flag component native fixture; no command registration, authority or resource qualification"}, policy)
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
	path := "testdata/uo-personal-flags-plans.json.gz"
	if *recordUOFlagsPlans {
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
	data, err = os.ReadFile(path)
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
