package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PERF-12: a grid page carries a validator; revalidating with it answers 304
// until anything is written, and only an authorized viewer gets the 304.
func TestGridProjectionsRevalidateWithTheirValidator(t *testing.T) {
	h, owner, member, db := editorHTTPFixtureDB(t)
	renamed := 0
	for _, path := range []string{"/v1/libraries/private-library/content?view=browse&limit=20", "/v1/libraries/private-library/categories", "/v1/libraries/private-library/collections"} {
		first := editorRequest(t, h, "GET", path, owner, "", nil)
		tag := first.Header().Get("ETag")
		if first.Code != 200 || tag == "" || first.Header().Get("Cache-Control") != revalidate {
			t.Fatalf("%s: first page %d etag %q cache %q: %s", path, first.Code, tag, first.Header().Get("Cache-Control"), first.Body.String())
		}
		again := editorRequestWithHeader(t, h, "GET", path, owner, map[string]string{"If-None-Match": tag})
		if again.Code != 304 || again.Body.Len() != 0 {
			t.Fatalf("%s: unchanged revalidation %d %q", path, again.Code, again.Body.String())
		}
		// Another viewer's validator never matches, and an unauthorized viewer
		// is refused rather than told the page is unchanged.
		if other := editorRequestWithHeader(t, h, "GET", path, member, map[string]string{"If-None-Match": tag}); other.Code == 304 {
			t.Fatalf("%s: another viewer revalidated the owner's page", path)
		}
		if _, e := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO configuration(key,value) VALUES('grid-etag-test',hex(randomblob(4))) ON CONFLICT(key) DO UPDATE SET value=excluded.value`); e != nil {
			t.Fatal(e)
		}
		// A write that has nothing to do with this library's content keeps the
		// validator: an open grid is not refetched for somebody else's change.
		after := editorRequestWithHeader(t, h, "GET", path, owner, map[string]string{"If-None-Match": tag})
		if after.Code != 304 || after.Header().Get("ETag") != tag {
			t.Fatalf("%s: an unrelated settings write changed the content validator: %d %q", path, after.Code, after.Header().Get("ETag"))
		}
		// A change to the library's own content does change it: a renamed
		// title is never answered "not modified".
		renamed++
		var film int64
		if e := db.QueryRow(`SELECT e.id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id='private-library' AND e.kind=1 ORDER BY e.id LIMIT 1`).Scan(&film); e != nil {
			t.Fatal(e)
		}
		fixtures := catalogtest.New(t, db)
		fixtures.Fields(film, map[string]any{"title": fmt.Sprint("Renamed ", renamed)})
		fixtures.Drain()
		changed := editorRequestWithHeader(t, h, "GET", path, owner, map[string]string{"If-None-Match": tag})
		if changed.Code != 200 || changed.Header().Get("ETag") == tag || changed.Header().Get("ETag") == "" {
			t.Fatalf("%s: a change to the library did not change the validator: %d %q", path, changed.Code, changed.Header().Get("ETag"))
		}
	}
	// A failed request carries no validator.
	if bad := editorRequest(t, h, "GET", "/v1/libraries/private-library/content?start=x", owner, "", nil); bad.Code == 200 || bad.Header().Get("ETag") != "" {
		t.Fatalf("error response carries a validator: %d %q", bad.Code, bad.Header().Get("ETag"))
	}
}
