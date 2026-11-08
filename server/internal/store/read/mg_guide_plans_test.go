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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGGuidePlans = flag.Bool("record-mg-guide-plans", false, "deliberately record MG guide SEARCH-only plans")

func mgGuidePlanFixture(cfg sqliteconn.Config, hash string, statistics plan.Statistics) plan.Fixture {
	return plan.Fixture{
		ConnectionPolicy: cfg, SHA256: hash, Statistics: statistics,
		Implementations: "canonical " + schema.SHA256 + "; bundled SQLite3.53.4; mgGuideSeed immutable generation facts; inert guide reader, no authority or latency qualification",
	}
}

func mgGuideCapture(t *testing.T, f *browseFixture, definitions []stmt.Definition, statistics plan.Statistics) plan.Golden {
	t.Helper()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
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
	golden, err := plan.Capture(t.Context(), conn, catalogue, mgGuidePlanFixture(cfg, mgGuidePlanHash(t), statistics), policy)
	if err != nil {
		t.Fatal(err)
	}
	return golden
}

func TestMGGuidePlans(t *testing.T) {
	f := mgGuideSource(t)
	definitions := mgGuideDefinitions()
	for _, d := range definitions {
		for _, cohort := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatalf("%s/%s: %v", d.ID, cohort.Name, err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present == (cohort.Name == "empty" || cohort.Name == "missing") {
				t.Fatalf("%s/%s mislabeled: present=%v err=%v", d.ID, cohort.Name, present, err)
			}
		}
	}
	var captures []plan.Golden
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := mgGuideCapture(t, f, definitions, statistics)
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatalf("actual Capture receipt: %v", err)
		}
		if len(golden.Manifest) != 8 || len(golden.Plans) != 21 {
			t.Fatalf("changed closed inventory %d/%d", len(golden.Manifest), len(golden.Plans))
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/mg-guide-plans.json.gz"
	if *recordMGGuidePlans {
		var data bytes.Buffer
		z := gzip.NewWriter(&data)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
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

func TestMGGuideCaptureRejectsUnsafePlans(t *testing.T) {
	f := mgGuideSource(t)
	definitions := mgGuideDefinitions()
	original := definitions[2] // channels keyset
	positive := mgGuideCapture(t, f, []stmt.Definition{original}, plan.Unanalyzed)
	if _, err := stmt.New([]stmt.Definition{original}, positive.Receipt()); err != nil {
		t.Fatal(err)
	}
	for name, sql := range map[string]string{
		"unforced":       strings.Replace(original.SQL, " INDEXED BY sqlite_autoindex_live_channel_versions_2", "", 1),
		"temporary_sort": strings.Replace(original.SQL, "ORDER BY position", "ORDER BY name", 1),
		"unbound_head":   strings.Replace(original.SQL, "generation_id=? AND position>?", "position>? AND ? IS NOT NULL", 1),
	} {
		t.Run(name, func(t *testing.T) {
			d := original
			d.SQL = sql
			catalogue, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			data, err := schema.ReadContract("INT-INDEX-POLICY.json")
			if err != nil {
				t.Fatal(err)
			}
			policy, err := plan.ReadIndexPolicy(data)
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
			hash := mgGuidePlanHash(t)
			if _, err = plan.Capture(t.Context(), conn, catalogue, mgGuidePlanFixture(cfg, hash, plan.Unanalyzed), policy); err == nil {
				t.Fatal("unsafe guide plan accepted")
			}
		})
	}
	changed := original
	changed.SQL = strings.Replace(original.SQL, "ORDER BY position", "ORDER BY position DESC", 1)
	if _, err := stmt.New([]stmt.Definition{changed}, positive.Receipt()); err == nil {
		t.Fatal("partial/mismatched Capture receipt accepted")
	}
}
