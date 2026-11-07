package read

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
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
		if err = os.WriteFile("rs_saved_collection_golden_test.go", []byte(fmt.Sprintf("package read\n\nconst rsSavedCollectionGolden = `%s`\n", data)), 0600); err != nil {
			t.Fatal(err)
		}
	} else if string(data) != rsSavedCollectionGolden {
		t.Fatal("native Capture golden drift")
	}
}
