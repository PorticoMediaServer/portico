package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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

// Runner captures are always retained as bounded evidence. Recording a new
// comparison baseline is deliberately separate from minting a live receipt.
func iaTVCompareGolden(t *testing.T, path string, actual []byte) {
	t.Helper()
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), ".gz")), actual, 0644); e != nil {
			t.Fatal(e)
		}
	}
	expected, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.HasSuffix(path, ".gz") {
		z, e := gzip.NewReader(bytes.NewReader(expected))
		if e != nil {
			t.Fatal(e)
		}
		expected, e = io.ReadAll(z)
		closeErr := z.Close()
		if e != nil {
			t.Fatal(e)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if e = plan.CompareJSON(expected, actual); e != nil {
		t.Fatal(e)
	}
}

func TestIATVPlans(t *testing.T) {
	f := newIATVSource(t)
	defs := IATVHarnessDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			has := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil || has != (c.Name == "present") {
				t.Fatalf("%s/%s mislabeled population: %v", d.ID, c.Name, e)
			}
		}
	}
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats, iaTVHash(t))
		if _, e := stmt.New(defs, g.Receipt()); e != nil {
			t.Fatal(e)
		}
		if len(g.Manifest) != len(defs) || len(g.Plans) != 2*len(defs) {
			t.Fatalf("incomplete TV capture: %d/%d", len(g.Manifest), len(g.Plans))
		}
		captures = append(captures, capture{string(stats), g})
	}
	actual, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	iaTVCompareGolden(t, "testdata/ia-tv-plans.json.gz", actual)
}

func TestIATVCaptureRejectsMutants(t *testing.T) {
	f := newIATVSource(t)
	cfg := browseReaderConfig()
	db, _, e := sqliteconn.Open(t.Context(), f.path, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	conn, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	contract, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := plan.ReadIndexPolicy(contract)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"show_octets", "season_episodes_first", "link_octets"} {
		t.Run(name, func(t *testing.T) {
			var original stmt.Definition
			for _, d := range IATVHarnessDefinitions() {
				if d.ID == readStatementID("foreground.ia.tv."+name) {
					original = d
				}
			}
			if original.ID == "" {
				t.Fatal("missing production statement", name)
			}
			capture := func(d stmt.Definition) error {
				cat, e := stmt.Describe([]stmt.Definition{d})
				if e != nil {
					return e
				}
				_, e = plan.Capture(t.Context(), conn, cat, browsePlanFixture(cfg, iaTVHash(t), plan.Unanalyzed), policy)
				return e
			}
			if e := capture(original); e != nil {
				t.Fatal("positive production statement", e)
			}
			switch name {
			case "show_octets":
				original.SQL = strings.Replace(original.SQL, "octet_length(title)", "octet_length(health)", 1)
			case "season_episodes_first":
				original.SQL = strings.Replace(original.SQL, "season_id=?", "season_id>?", 1)
			default:
				original.SQL = strings.Replace(original.SQL, "octet_length(edition)", "octet_length(entity_id)", 1)
			}
			if e := capture(original); e == nil {
				t.Fatal("production SQL mutant survived native Capture")
			}
		})
	}
}
