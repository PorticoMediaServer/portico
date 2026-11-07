package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordBrowsePlans = flag.Bool("record-browse-plans", false, "deliberately record reviewed native browse plans")

func TestBrowsePlans(t *testing.T) {
	f := newBrowseSource(t)
	fixtureHash := browsePlanFixtureHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := browseDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Manifest()) != 17 {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/browse-plans.json.gz"
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
	if *recordBrowsePlans {
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
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		if len(capture.Golden.Manifest) != 17 || len(capture.Golden.Plans) != 34 {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}

func TestBrowseCaptureRejectsPlanRegressions(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, "ANALYZE")
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
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
	fixtureHash := browsePlanFixtureHash(t)
	// Capture is the only complete qualification API. The original statement
	// must pass here; a blanket refusal cannot make these negatives green.
	var original *stmt.Definition
	for _, definition := range browseDefinitions() {
		if definition.ID == statementID("title.ignore.asc.tail") {
			d := definition
			original = &d
		}
	}
	if original == nil {
		t.Fatal("negative-control source statement absent")
	}
	captureOne := func(d stmt.Definition) (plan.Golden, error) {
		r, err := stmt.Describe([]stmt.Definition{d})
		if err != nil {
			return plan.Golden{}, err
		}
		return plan.Capture(t.Context(), conn, r, browsePlanFixture(cfg, fixtureHash, plan.Analyzed), policy)
	}
	positive, err := captureOne(*original)
	if err != nil || len(positive.Plans) != len(original.Cohorts) {
		t.Fatalf("positive Capture failed: %v", err)
	}
	wantRejection := map[string]string{
		"unforced":        "not forced in SQL",
		"computed-sort":   "unapproved SQL lint sql:outside-grammar",
		"wrong-collation": "forbidden plan USE TEMP B-TREE",
	}
	for name, query := range map[string]string{
		"unforced":        strings.Replace(original.SQL, " INDEXED BY catalog_entities_title", "", 1),
		"computed-sort":   strings.Replace(original.SQL, "ORDER BY sort_key COLLATE NOCASE ASC", "ORDER BY lower(sort_key) ASC", 1),
		"wrong-collation": strings.ReplaceAll(original.SQL, "COLLATE NOCASE", "COLLATE BINARY"),
	} {
		t.Run(name, func(t *testing.T) {
			d := *original
			d.SQL = query
			if _, err := captureOne(d); err == nil || !strings.Contains(err.Error(), wantRejection[name]) {
				t.Fatalf("Capture did not reject the intended regression: got %v, want %q", err, wantRejection[name])
			} else {
				t.Logf("intended rejection: %v", err)
			}
		})
	}
}

func browseReaderConfig() sqliteconn.Config { return readfixture.ReaderConfig() }

func browsePlanFixture(cfg sqliteconn.Config, hash string, statistics plan.Statistics) plan.Fixture {
	return plan.Fixture{ConnectionPolicy: cfg, SHA256: hash, Statistics: statistics, Implementations: "canonical " + schema.SHA256 + "; bundled 3.53.4 builtin functions/collations; seedBrowse source journal"}
}

// Mint a fresh receipt on the actual fixture's Reader before GV composition.
// Stored JSON is only a comparison baseline, never registration authority.
func captureBrowse(t *testing.T, f *browseFixture, definitions []stmt.Definition, statistics plan.Statistics, fixtureHashes ...string) plan.Golden {
	t.Helper()
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
	catalogue, err := stmt.Describe(definitions)
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
	hash := browsePlanFixtureHash(t)
	for _, d := range definitions {
		if strings.HasPrefix(string(d.ID), "read.foreground.") {
			hash = foregroundFixtureHash(t)
			break
		}
	}
	if len(fixtureHashes) > 1 {
		t.Fatal("more than one fixture source hash")
	}
	if len(fixtureHashes) == 1 {
		hash = fixtureHashes[0]
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, hash, statistics), policy)
	if err != nil {
		t.Fatal(err)
	}
	return golden
}

func browsePlanFixtureHash(t *testing.T) string { return readfixture.BrowseHash(t) }
