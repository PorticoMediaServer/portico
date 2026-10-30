package catalog

import (
	"reflect"
	"testing"
	"time"
)

func TestA3ForYouActivityChangesProductionTasteOutsideBaseWitness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	now := time.Now()
	w.s.Clock = func() time.Time { return now }
	w.watch("p", w.scifi[0])
	item := w.others[0]
	w.c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,60000,0,'pb')`, item.ID)
	w.c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','m',?,?,'paused')`, item.ID, now.AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano))
	w.c.Drain()
	before, _ := smallOrderBasis(t, w.s, "m", "p")
	tasteBefore, err := w.s.recLoadTaste("p", now)
	if err != nil {
		t.Fatal(err)
	}
	var observationBefore, observationAfter int64
	if err = w.c.DB.QueryRow(`SELECT observation_revision FROM viewer_revisions WHERE profile_id='p' AND library_id='m'`).Scan(&observationBefore); err != nil {
		t.Fatal(err)
	}
	w.c.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='p' AND item_id=?`, now.UTC().Format(time.RFC3339Nano), item.ID)
	after, _ := smallOrderBasis(t, w.s, "m", "p")
	tasteAfter, err := w.s.recLoadTaste("p", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.c.DB.QueryRow(`SELECT observation_revision FROM viewer_revisions WHERE profile_id='p' AND library_id='m'`).Scan(&observationAfter); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("activity-only update unexpectedly authored a shared selection epoch")
	}
	if reflect.DeepEqual(tasteBefore.values, tasteAfter.values) {
		t.Fatal("activity-only update failed to change actual production taste")
	}
	if observationAfter <= observationBefore {
		t.Fatal("activity-only update failed to change observation revision")
	}
	t.Logf("same exact selection basis=%v; production taste changed=true; observation revision=%d->%d; taste facets=%d->%d", before == after, observationBefore, observationAfter, len(tasteBefore.values), len(tasteAfter.values))
}
