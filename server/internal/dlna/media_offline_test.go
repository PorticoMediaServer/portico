package dlna

import (
	"net/http"
	"testing"
)

// A renderer asking for a title whose source is only offline gets a temporary
// answer, not a withdrawn title: the catalogue keeps the item, and the same
// request works once the storage reconnects.
func TestMediaNamesOfflineStorageAndRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	token := f.items["heat"].Token
	f.c.Exec(`UPDATE catalog_assets SET available=1 WHERE token=?`, token)
	f.c.Exec(`UPDATE catalog_asset_links SET available=1 WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, token)
	f.c.Exec(`UPDATE library_sources SET health='offline' WHERE id='films'`)

	w := f.request("GET", "/dlna/media/"+token, "192.168.1.20", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline source: %d %s", w.Code, w.Body.String())
	}
	if retry := w.Header().Get("Retry-After"); retry != "2" {
		t.Fatalf("offline source retry hint: %q", retry)
	}

	f.c.Exec(`UPDATE library_sources SET health='ready' WHERE id='films'`)
	w = f.request("GET", "/dlna/media/"+token, "192.168.1.20", "")
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("reconnected source still refused: %d %s", w.Code, w.Body.String())
	}
}
