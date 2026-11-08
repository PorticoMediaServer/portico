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

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUOMembershipPlans = flag.Bool("record-uo-membership-plans", false, "record UO membership native plan golden")

func uoMembershipPlanSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `UPDATE personal_items SET watchlisted=1 WHERE profile_id='p' AND item_id BETWEEN 1 AND 8`)
	f.exec(t, `UPDATE personal_items SET favorite=1 WHERE profile_id='p' AND item_id BETWEEN 9 AND 16`)
	f.exec(t, `UPDATE personal_items SET not_interested=1 WHERE profile_id='p' AND item_id BETWEEN 17 AND 24`)
	return f
}

func TestUOMembershipPlans(t *testing.T) {
	f := uoMembershipPlanSource(t)
	definitions := UOMembershipDefinitions()
	if len(definitions) != 12 {
		t.Fatal("unexpected UO membership statement inventory")
	}
	for _, definition := range definitions {
		for _, cohort := range definition.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), definition.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (cohort.Name == "present") {
				t.Fatal("mislabeled cohort", definition.ID, cohort.Name, err)
			}
		}
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, foregroundFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact definitions", err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/uo-membership-plans.json.gz"
	if *recordUOMembershipPlans {
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

func uoMembershipDefinition(t *testing.T, id string) stmt.Definition {
	t.Helper()
	for _, d := range UOMembershipDefinitions() {
		if string(d.ID) == id {
			return d
		}
	}
	t.Fatal("missing UO membership definition", id)
	return stmt.Definition{}
}

func TestUOMembershipPlanRejectsUnusableIndex(t *testing.T) {
	f := uoMembershipPlanSource(t)
	original := uoMembershipDefinition(t, "read.foreground.uo.membership.watchlist.tail.bytes")
	if err := uoPersonalCaptureError(t, f, original); err != nil {
		t.Fatal("positive sparse keyset", err)
	}
	for name, mutate := range map[string]func(*stmt.Definition){
		"unforced-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " INDEXED BY personal_current_watchlist", "", 1)
		},
		"foreign-flag-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "personal_current_watchlist", "personal_current_favorite", 1)
			for n, index := range d.Plan.Indexes {
				if index == "personal_current_watchlist" {
					d.Plan.Indexes[n] = "personal_current_favorite"
				}
			}
		},
		"wrong-predicate": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "watchlisted=1", "favorite=1", 1)
		},
		"lost-public-id-order": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "ORDER BY p.item_public_id ASC ", "", 1)
		},
		"raw-limit-drift": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "LIMIT 64", "LIMIT 65", 1)
		},
	} {
		changed := original
		mutate(&changed)
		if err := uoPersonalCaptureError(t, f, changed); err == nil {
			t.Fatal("unusable membership byte range was authorized:", name)
		}
	}
}

func TestUOMembershipPlanRejectsMissingOrDriftedIndex(t *testing.T) {
	for name, ddl := range map[string][]string{
		"missing": {"DROP INDEX personal_current_watchlist"},
		"drifted": {
			"DROP INDEX personal_current_watchlist",
			"CREATE INDEX personal_current_watchlist ON personal_items(profile_id,item_public_id) WHERE watchlisted=1 AND favorite=1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := uoMembershipPlanSource(t)
			for _, statement := range ddl {
				f.exec(t, statement)
			}
			d := uoMembershipDefinition(t, "read.foreground.uo.membership.watchlist.first.bytes")
			if err := uoPersonalCaptureError(t, f, d); err == nil {
				t.Fatal("missing or drifted sparse partial index was authorized")
			}
		})
	}
}
