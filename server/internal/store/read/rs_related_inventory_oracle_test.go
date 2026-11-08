package read

import (
	"errors"
	"fmt"
	"testing"
)

// These controls live outside rs_related_inventory_test.go on purpose: that
// file is the embedded fixture journal whose hash is recorded in the plan
// golden, so adding behaviour tests here must not rewrite the plan baseline.

// rsRelatedInventoryStaleObjects appends count stale object rows for the
// fixture's ready asset. Their ids sort before the ready "object" row, which is
// exactly the truncation shape a too-small object window hides.
func rsRelatedInventoryStaleObjects(t *testing.T, f *browseFixture, count int) {
	t.Helper()
	for n := 0; n < count; n++ {
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES(?, 'source','asset','inc',?,'r','{}',1000,1,'missing')`, fmt.Sprintf("a%02d", n), fmt.Sprintf("stale/%d.mkv", n))
	}
}

// TestRSRelatedInventoryObjectWindowCapacity pins the object-evidence window:
// the read binds MaxRaw+1 lookahead and refuses capacity above MaxRaw instead
// of publishing a truncated exact negative. A full window of stale objects with
// one ready object behind it must never be answered as unavailable.
func TestRSRelatedInventoryObjectWindowCapacity(t *testing.T) {
	t.Run("exact-bound-is-complete", func(t *testing.T) {
		f := rsRelatedInventorySource(t)
		// MaxRaw-1 stale rows plus the fixture's ready object is exactly MaxRaw.
		rsRelatedInventoryStaleObjects(t, f, MaxRaw-1)
		q := rsRelatedInventoryQuery(t, f)
		if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryAvailable {
			t.Fatalf("verdict %s at the exact object bound", got)
		}
	})
	t.Run("full-stale-window-refuses", func(t *testing.T) {
		f := rsRelatedInventorySource(t)
		// MaxRaw stale rows sort first; the ready fixture object is row MaxRaw+1.
		rsRelatedInventoryStaleObjects(t, f, MaxRaw)
		q := rsRelatedInventoryQuery(t, f)
		v := newRSRelatedInventory(1, 1, "movies")
		for n := 0; n < 4096; n++ {
			done, err := v.step(q, nil)
			if errors.Is(err, ErrRelatedCapacity) {
				if v.done || v.Available() {
					t.Fatal("capacity refusal after a published verdict")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if done {
				t.Fatalf("truncated object window published %s", v.Verdict())
			}
		}
		t.Fatal("object capacity refusal missing")
	})
	t.Run("truncated-verify-refuses", func(t *testing.T) {
		f := rsRelatedInventorySource(t)
		rsRelatedInventoryStaleObjects(t, f, MaxRaw-1)
		q := rsRelatedInventoryQuery(t, f)
		v := newRSRelatedInventory(1, 1, "movies")
		if got := rsRelatedInventoryRun(t, v, q); got != rsRelatedInventoryAvailable {
			t.Fatalf("initial %s", got)
		}
		if err := v.verify(q); err != nil {
			t.Fatalf("unchanged object window: %v", err)
		}
		// One more object pushes the window over the bound; the final proof must
		// refuse capacity rather than accept a truncated re-read as unchanged.
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('z99','source','asset','inc','stale/z.mkv','r','{}',1000,1,'missing')`)
		if err := v.verify(q); !errors.Is(err, ErrRelatedCapacity) {
			t.Fatalf("truncated final proof accepted: %v", err)
		}
	})
}

// rsRelatedInventoryCanonical is the oracle: the canonical
// inventory_item_availability view for one item.
func rsRelatedInventoryCanonical(t *testing.T, f *browseFixture, item int64) int {
	t.Helper()
	var available int
	if err := f.conn.QueryRowContext(t.Context(), `SELECT COALESCE(MAX(available),0) FROM inventory_item_availability WHERE item_id=?`, item).Scan(&available); err != nil {
		t.Fatal(err)
	}
	return available
}

// TestRSRelatedInventoryMatchesCanonicalAvailability compares the adapter with
// the canonical view over the object, source-health and fallback paths, so the
// object path is held by behaviour and not only by the recorded plan.
func TestRSRelatedInventoryMatchesCanonicalAvailability(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"healthy-source", `SELECT 1`},
		{"zero-sources", `DELETE FROM inventory_objects; DELETE FROM library_sources`},
		{"health-missing-source", `DELETE FROM inventory_objects; UPDATE library_sources SET health='missing'`},
		{"removing-source", `DELETE FROM inventory_objects; UPDATE library_sources SET health='removing'`},
		{"disabled-source", `DELETE FROM inventory_objects; UPDATE library_sources SET enabled=0`},
		{"root-changed-source", `DELETE FROM inventory_objects; UPDATE library_sources SET health='root_changed'`},
		{"stale-incarnation", `UPDATE inventory_objects SET root_incarnation='old'`},
		{"two-objects-mixed", `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('a01','source','asset','inc','stale/1.mkv','r','{}',1000,1,'missing')`},
		{"two-objects-unavailable", `UPDATE inventory_objects SET state='missing'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rsRelatedInventorySource(t)
			f.exec(t, tc.sql)
			q := rsRelatedInventoryQuery(t, f)
			available := rsRelatedInventoryCanonical(t, f, 1)
			verdict := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q)
			want := rsRelatedInventoryUnavailable
			if available == 1 {
				want = rsRelatedInventoryAvailable
			}
			if verdict != want {
				t.Fatalf("adapter %s, canonical available=%d", verdict, available)
			}
		})
	}
}

// TestRSRelatedInventoryLaterLinkAloneAvailable covers a first link that is a
// proven negative followed by a second link that alone is available. Stopping
// at the first link would publish a false negative.
func TestRSRelatedInventoryLaterLinkAloneAvailable(t *testing.T) {
	f := rsRelatedInventorySource(t)
	f.exec(t, `UPDATE inventory_objects SET state='missing' WHERE id='object'`)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id)VALUES(2,'asset-2','/media/2.mkv',1000,1,'matroska','','',1,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1,2,1)`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)VALUES('object-2','source','asset-2','inc','2.mkv','r','{}',1000,1,'available')`)
	q := rsRelatedInventoryQuery(t, f)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryAvailable {
		t.Fatalf("second link alone %s", got)
	}
	if available := rsRelatedInventoryCanonical(t, f, 1); available != 1 {
		t.Fatalf("canonical available=%d, want 1", available)
	}
	// Removing the only available link must become the canonical negative.
	f.exec(t, `UPDATE inventory_objects SET state='missing' WHERE id='object-2'`)
	if got := rsRelatedInventoryRun(t, newRSRelatedInventory(1, 1, "movies"), q); got != rsRelatedInventoryUnavailable {
		t.Fatalf("all links negative %s", got)
	}
	if available := rsRelatedInventoryCanonical(t, f, 1); available != 0 {
		t.Fatalf("canonical available=%d, want 0", available)
	}
}
