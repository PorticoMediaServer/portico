package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGPersonalPlans = flag.Bool("record-mg-personal-plans", false, "deliberately record private sparse personal command plans")

func TestMGPersonalNativeStatementPlans(t *testing.T) {
	defs := mgPersonalDefinitions()
	if len(defs) != 11 {
		t.Fatal("closed guide inventory", len(defs))
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := mgPersonalFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSource, err := os.ReadFile("mg_personal_fixture_test.go")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append(fixtureSource, []byte(mgLCDraftSQL+mgPersonalReminderDraftSQL)...))
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Exact selected MG-001 4e075198 source/LC/reminder declarations and indexes; closed sparse preference/reminder DML/read VM and FK audits; not current authority, runtime enablement, admission or qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 11 || len(g.Plans) != 11 || g.ForeignKeys == nil {
		t.Fatal("capture inventory", len(g.Manifest), len(g.Plans), g.ForeignKeys)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed effects", before, after, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-personal-plans.json.gz"
	if *recordMGPersonalPlans {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
