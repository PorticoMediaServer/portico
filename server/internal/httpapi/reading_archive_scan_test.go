//go:build portico_rar7z

package httpapi

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

type comicArchivePublication struct {
	path, format, item string
}

// TestReadingScanPublishesTaggedComicArchives starts from a real comic library
// folder and admits the built-in CBZ plus genuine CBR and (when the capability
// is built) CB7 bytes through the ordinary scan. The second scan is the same
// source rescanned: every publication must survive with one row and one item.
func TestReadingScanPublishesTaggedComicArchives(t *testing.T) {
	r := newReadingFixture(t, "comic", "cbz")
	publications := []comicArchivePublication{{path: r.path, format: "cbz"}}
	publications = append(publications, comicArchivePublication{
		path:   readingTreeFile(t, r, "Archive CBR.cbr", readingtest.CBR()),
		format: "cbr",
	})
	if readingcap.CB7() {
		publications = append(publications, comicArchivePublication{
			path:   readingTreeFile(t, r, "Archive CB7.cb7", readingtest.CB7()),
			format: "cb7",
		})
	} else {
		t.Log("CB7 is not enabled in this build; this scan covers CBZ and CBR only")
	}
	readingScan(t, r.f, r.library)
	for i := range publications {
		publications[i].item = assertComicArchivePublication(t, r, publications[i].path, publications[i].format)
	}
	assertComicArchiveItems(t, r, publications)
	readingScan(t, r.f, r.library)
	for i := range publications {
		item := assertComicArchivePublication(t, r, publications[i].path, publications[i].format)
		if item != publications[i].item {
			t.Errorf("%s item changed on rescan: %s -> %s", publications[i].path, publications[i].item, item)
		}
	}
	assertComicArchiveItems(t, r, publications)
	var rows, retired int
	if err := r.f.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(e.retired),0) FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=?`, r.library).Scan(&rows, &retired); err != nil {
		t.Fatal(err)
	}
	if rows != len(publications) || retired != 0 {
		t.Fatalf("publications=%d retired=%d want %d/0", rows, retired, len(publications))
	}
}

func assertComicArchivePublication(t *testing.T, r *readingFixture, path, format string) string {
	t.Helper()
	var item, gotFormat string
	var itemRetired, itemPublications, pathPublications int
	if err := r.f.db.QueryRow(`SELECT pid(e.public_id),p.format,e.retired,
	 (SELECT COUNT(*) FROM reading_publications x WHERE x.item_id=e.id),
	 (SELECT COUNT(*) FROM reading_publications y JOIN catalog_assets ya ON ya.id=y.asset_id WHERE ya.path=?)
	 FROM catalog_assets a JOIN reading_publications p ON p.asset_id=a.id JOIN catalog_entities e ON e.id=p.item_id WHERE a.path=?`, path, path).Scan(&item, &gotFormat, &itemRetired, &itemPublications, &pathPublications); err != nil {
		t.Fatal(err)
	}
	if gotFormat != format {
		t.Fatalf("%s format=%q want %q", path, gotFormat, format)
	}
	if itemRetired != 0 || itemPublications != 1 || pathPublications != 1 {
		t.Fatalf("%s retired=%d itemPublications=%d pathPublications=%d", path, itemRetired, itemPublications, pathPublications)
	}
	return item
}

func assertComicArchiveItems(t *testing.T, r *readingFixture, publications []comicArchivePublication) {
	t.Helper()
	h := New(r.d)
	w := readingHTTP(t, h, "GET", "/v1/reading/items?libraryId="+url.QueryEscape(r.library)+"&limit=10", r.f.owner.AccessToken, "", nil)
	var list ReadingItemsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatalf("reading items: %d %s", w.Code, w.Body.String())
	}
	if len(list.Items) != len(publications) {
		t.Fatalf("reading items=%d want %d: %s", len(list.Items), len(publications), w.Body.String())
	}
	formats := map[string]ReadingSourceFormat{}
	for _, item := range list.Items {
		formats[item.ID] = item.Format
	}
	for _, publication := range publications {
		if formats[publication.item] != ReadingSourceFormat(publication.format) {
			t.Errorf("%s item %s format=%q want %q", publication.path, publication.item, formats[publication.item], publication.format)
		}
	}
}
