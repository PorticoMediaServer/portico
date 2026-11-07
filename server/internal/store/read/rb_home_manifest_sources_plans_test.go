package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestRBHomeCommittedSourcePlans(t *testing.T) {
	f := newRBHomeCommittedSourcePlanSource(t)
	fixtureHash := rbHomeCommittedSourcePlanFixtureHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := RBHomeManifestSourceDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Manifest()) != len(definitions) {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/rb-home-committed-source-plans.json.gz"
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
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, fixtureHash, stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Reader Capture cannot authorize exact definitions", err)
		}
		captures = append(captures, capture{Profile: string(stats), Golden: golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "rb-home-committed-source-plans.json.gz"), compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("adopt reviewed runner Capture artifact:", err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
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
	for _, capture := range captures {
		if len(capture.Golden.Manifest) != len(definitions) || len(capture.Golden.Plans) != len(definitions) {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}

// BEGIN_RB_HOME_COMMITTED_SOURCE_PLAN_JOURNAL
func newRBHomeCommittedSourcePlanSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO console_settings_revision(singleton,revision) VALUES(1,4)`)
	f.exec(t, `INSERT INTO server_configuration_pending(field,revision) VALUES('home.communityActivityEnabled',4)`)
	return f
}

// END_RB_HOME_COMMITTED_SOURCE_PLAN_JOURNAL
func rbHomeCommittedSourcePlanFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_home_manifest_sources_plans_test.go")
	if e != nil {
		t.Fatal(e)
	}
	a := strings.Index(string(data), "// BEGIN_RB_HOME_COMMITTED_SOURCE_PLAN_JOURNAL\n")
	b := strings.Index(string(data), "// END_RB_HOME_COMMITTED_SOURCE_PLAN_JOURNAL\n")
	if a < 0 || b <= a {
		t.Fatal("source journal missing")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[a:b]...)))
}
