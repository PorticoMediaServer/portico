package read

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// The UO activity and operations recipes are captured live. Stored JSON goldens
// are the train desk's to record; this test proves the exact definitions pass HN
// Capture (native point audits, octet preflights, EQP and closed SQL grammar) on
// the fixture.
func TestUOActivityPlanCapture(t *testing.T) {
	f := newForegroundSource(t)
	definitions := UOActivityDefinitions()
	if len(definitions) != 25 {
		t.Fatal("unexpected UO activity statement inventory", len(definitions))
	}
	for _, definition := range definitions {
		for _, cohort := range definition.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), definition.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, foregroundFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact definitions", err)
		}
	}
}

func TestUOActivityPlanRejectsUnusableSparseIndex(t *testing.T) {
	f := newForegroundSource(t)
	original := uoActivityDefinition(t, UOActivityDefinitions(), "read.foreground.uo.activity.activity.older.bytes")
	if err := uoPersonalCaptureError(t, f, original); err != nil {
		t.Fatal("positive sparse keyset", err)
	}
	for name, mutate := range map[string]func(*stmt.Definition){
		"unforced-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " INDEXED BY personal_current_activity", "", 1)
		},
		"foreign-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "personal_current_activity", "personal_current_recent", 1)
			for n, index := range d.Plan.Indexes {
				if index == "personal_current_activity" {
					d.Plan.Indexes[n] = "personal_current_recent"
				}
			}
		},
		"lost-time-order": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "ORDER BY p.activity_at DESC,p.item_public_id ASC ", "", 1)
		},
		"raw-limit-drift": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "LIMIT ?", "LIMIT 65", 1)
		},
		"octet-projection-drift": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.title)", "octet_length(e.public_id)", 1)
		},
	} {
		changed := original
		mutate(&changed)
		if err := uoPersonalCaptureError(t, f, changed); err == nil {
			t.Fatal("unusable activity byte range was authorized:", name)
		}
	}
}

func TestUOOperationsPlanCapture(t *testing.T) {
	f := newForegroundSource(t)
	definitions := UOOperationsDefinitions()
	if len(definitions) != 18 {
		t.Fatal("unexpected UO operations statement inventory", len(definitions))
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, foregroundFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact operations definitions", err)
		}
	}
}

func TestUOOperationsPlanRejectsUnusableKeysets(t *testing.T) {
	f := newForegroundSource(t)
	original := uoActivityDefinition(t, UOOperationsDefinitions(), "read.uo.operations.delivery-log.older.sizes")
	if err := uoPersonalCaptureError(t, f, original); err != nil {
		t.Fatal("positive delivery log keyset", err)
	}
	for name, mutate := range map[string]func(*stmt.Definition){
		"unforced-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " INDEXED BY webhook_deliveries_log", "", 1)
		},
		"foreign-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "webhook_deliveries_log", "webhook_deliveries_queue", 1)
			for n, index := range d.Plan.Indexes {
				if index == "webhook_deliveries_log" {
					d.Plan.Indexes[n] = "webhook_deliveries_queue"
				}
			}
		},
		"lost-time-order": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "ORDER BY d.created_ms DESC,d.id DESC ", "", 1)
		},
		"payload-in-preflight": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(d.error)", "d.error", 1)
		},
	} {
		changed := original
		mutate(&changed)
		if err := uoPersonalCaptureError(t, f, changed); err == nil {
			t.Fatal("unusable operations byte range was authorized:", name)
		}
	}
}

func uoActivityDefinition(t *testing.T, definitions []stmt.Definition, id string) stmt.Definition {
	t.Helper()
	for _, d := range definitions {
		if string(d.ID) == id {
			return d
		}
	}
	t.Fatal("missing UO definition", id)
	return stmt.Definition{}
}
