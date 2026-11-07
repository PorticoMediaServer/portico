package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRSSavedCollectionPlans(t *testing.T) {
	f := newRSSavedCollectionSource(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	defs := SavedCollectionDefinitions()
	catalogue, err := stmt.Describe(defs)
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
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, browsePlanFixtureHash(t), stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(defs, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, golden)
	}
	data, err := json.Marshal(captures)
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "rs-saved-collection-capture.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Goldens are kept in an owned Go file so this slice changes no shared fixture.
	if os.Getenv("PORTICO_RECORD_RS_SAVED_COLLECTION") == "1" {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		_, err = z.Write(data)
		if err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile("testdata/rs-saved-collection-plans.json.gz", b.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	} else {
		z, e := gzip.NewReader(bytes.NewReader(rsSavedCollectionGoldenGzip))
		if e != nil {
			t.Fatal(e)
		}
		expected, e := io.ReadAll(z)
		if e != nil {
			t.Fatal(e)
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(data, expected) {
			t.Fatal("native Capture golden drift")
		}
	}
}
