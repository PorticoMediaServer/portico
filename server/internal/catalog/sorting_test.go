package catalog

import (
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestBrowseStoredSortAndUnicodeIndex(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	c := catalogtest.New(t, db)
	c.Fields(names["m1"].ID, map[string]any{"title": "The Zebra", "metadata_language": "en"})
	c.Fields(names["m2"].ID, map[string]any{"title": "L’Éclair", "metadata_language": "fr"})
	c.Fields(names["m3"].ID, map[string]any{"title": "日本語", "metadata_language": "ja"})
	c.Fields(names["m4"].ID, map[string]any{"sort_title": "Aardvark"})
	settleCompact(t, db)
	out, e := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 50})
	if e != nil {
		t.Fatal(e)
	}
	var ids []string
	for _, x := range out.Entries {
		ids = append(ids, x.ID)
	}
	if got := names.All(ids); !reflect.DeepEqual(got, []string{"m5", "m4", "m2", "m1", "m3"}) {
		t.Fatal(got)
	}
	var key, head string
	if e = db.QueryRow(`SELECT sort_key,head FROM catalog_browse_rows WHERE entity_id=?`, names["m2"].ID).Scan(&key, &head); e != nil || key != "eclair" || head != "e" {
		t.Fatal(key, head, e)
	}
	var total int
	if e = db.QueryRow(`SELECT SUM(total) FROM catalog_browse_buckets WHERE library_id=? AND value='日'`, c.Handle("a")).Scan(&total); e != nil || total != 1 {
		t.Fatal(total, e)
	}
	// Explicit metadata edits must move an existing row, not just new inserts.
	c.Fields(names["m2"].ID, map[string]any{"sort_title": "Ωμέγα"})
	settleCompact(t, db)
	if e = db.QueryRow(`SELECT sort_key FROM catalog_browse_rows WHERE entity_id=?`, names["m2"].ID).Scan(&key); e != nil || key != "ωμεγα" {
		t.Fatal(key, e)
	}
}
