package catalog

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
	"time"
)

func TestAbandonedTasteLoadsTheWindowInTwoStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	old := time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano)
	for _, film := range w.others {
		w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'pb')`, film.ID)
		w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, film.ID, old)
	}
	w.c.Drain()
	ctx, cost := dbwork.Measure(context.Background())
	values := map[string]float64{}
	if err := w.s.WithContext(ctx).recAbandoned("p", time.Now(), values); err != nil {
		t.Fatal(err)
	}
	if len(values) == 0 {
		t.Fatal("missing abandonment contributions")
	}
	if cost().Statements != 2 {
		t.Fatalf("window needed %d statements", cost().Statements)
	}
}

// A long history with no abandoned matches costs the same recent window as
// one with matches: LIMIT must bound candidates before the progress checks.
func TestAbandonedTasteBoundsCandidatesBeforeCheckingProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	old := time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano)
	film := w.others[0]
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'pb')`, film.ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'ended')`, film.ID, old)
	w.c.Drain()
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	if err := w.s.WithContext(ctx).recAbandoned("p", time.Now(), map[string]float64{}); err != nil {
		t.Fatal(err)
	}
	for _, q := range trace() {
		if strings.Contains(q, "FROM progress_activity") && !strings.Contains(q, "ORDER BY updated_at DESC LIMIT 200) a") {
			t.Fatalf("history is not bounded before checks: %s", q)
		}
	}
}
