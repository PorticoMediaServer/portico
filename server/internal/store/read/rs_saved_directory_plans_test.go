package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

//go:embed rs_saved_directory_plans_test.go
var rsSavedDirectoryPlansJournal string

var recordRSSavedDirectoryPlans = flag.Bool("record-rs-saved-directory-plans", false, "deliberately record reviewed saved-directory SEARCH-only plans")

func rsSavedDirectoryPlansHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsSavedDirectoryHash(t)+rsSavedDirectoryPlansJournal)))
}

// TestRSSavedDirectoryPlans captures the actual compiled directory statements
// under both statistics populations and holds every plan to keyed SEARCH: no
// full SCAN, no USE TEMP B-TREE. The recorded golden is comparison bytes only;
// the actual Capture receipt above must authorize these exact definitions.
func TestRSSavedDirectoryPlans(t *testing.T) {
	f := newRSSavedDirectorySource(t)
	definitions := SavedDirectoryDefinitions()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	cohorts := 0
	for _, definition := range definitions {
		cohorts += len(definition.Cohorts)
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, rsSavedDirectoryPlansHash(t), stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Reader Capture cannot authorize exact definitions", err)
		}
		if len(golden.Manifest) != len(definitions) || len(golden.Plans) != cohorts {
			t.Fatalf("incomplete directory Capture coverage %d/%d, want %d/%d", len(golden.Manifest), len(golden.Plans), len(definitions), cohorts)
		}
		for _, statement := range golden.Plans {
			for _, value := range statement.QueryPlan.Values {
				detail := value[len(value)-1].(string)
				if !strings.HasPrefix(detail, "SEARCH ") || strings.Contains(detail, "SCAN ") || strings.Contains(detail, "USE TEMP B-TREE") {
					t.Fatalf("%s/%s is not keyed SEARCH: %s", statement.ID, statement.Cohort, detail)
				}
			}
		}
		captures = append(captures, capture{Profile: string(stats), Golden: golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/rs-saved-directory-plans.json.gz"
	if *recordRSSavedDirectoryPlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
