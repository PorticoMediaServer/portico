package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// The public Reading projection publishes only the accepted attribution facts
// already selected in the same snapshot: closed author/series objects on the
// existing list and continue routes, unknown facts omitted, and the existing
// string fields untouched.
func TestReadingPrivateAttributionProjectionOnListAndContinue(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	onePath := readingTreeFile(t, r, "Author One/Series/One.epub", readingtest.EPUBWithoutCreator())
	twoPath := readingTreeFile(t, r, "Author One/Series/Two.epub", readingtest.EPUBWithoutCreator())
	readingScan(t, r.f, r.library)
	one, two := readingPathItem(t, r, onePath), readingPathItem(t, r, twoPath)
	h := New(r.d)
	token := r.f.owner.AccessToken
	serve := func(path string) []map[string]json.RawMessage {
		t.Helper()
		w := readingHTTP(t, h, "GET", path, token, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
		var wire struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		return wire.Items
	}
	byID := func(items []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
		t.Helper()
		out := map[string]map[string]json.RawMessage{}
		for _, item := range items {
			var id string
			if err := json.Unmarshal(item["id"], &id); err != nil {
				t.Fatal(err)
			}
			out[id] = item
		}
		return out
	}
	decode := func(raw json.RawMessage, target any) {
		t.Helper()
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatalf("decode %s: %v", string(raw), err)
		}
	}
	checkAttribution := func(item map[string]json.RawMessage, author, series, source string) {
		t.Helper()
		if item == nil {
			t.Fatal("title missing from the response")
		}
		if author == "" {
			if _, ok := item["author"]; ok {
				t.Fatalf("unknown author fact leaked: %s", string(item["author"]))
			}
		} else {
			var shape map[string]json.RawMessage
			decode(item["author"], &shape)
			if len(shape) != 2 {
				t.Fatalf("author object not closed: %s", string(item["author"]))
			}
			var value, src string
			decode(shape["value"], &value)
			decode(shape["source"], &src)
			if value != author || src != source {
				t.Fatalf("author=%s want %q/%q", string(item["author"]), author, source)
			}
		}
		if series == "" {
			if _, ok := item["series"]; ok {
				t.Fatalf("unknown series fact leaked: %s", string(item["series"]))
			}
			return
		}
		var shape map[string]json.RawMessage
		decode(item["series"], &shape)
		if len(shape) < 3 || len(shape) > 4 {
			t.Fatalf("series object not closed: %s", string(item["series"]))
		}
		var id, name, src string
		decode(shape["id"], &id)
		decode(shape["name"], &name)
		decode(shape["source"], &src)
		if id != compactcatalog.ReadingSeriesID(r.library, series) || name != series || src != source {
			t.Fatalf("series=%s want %q/%q", string(item["series"]), series, source)
		}
	}
	rows := byID(serve("/v1/reading/items?limit=100"))
	checkAttribution(rows[one], "Author One", "Series", "folder")
	checkAttribution(rows[two], "Author One", "Series", "folder")
	checkAttribution(rows[r.item], "", "", "")
	// Continue carries the same projection on its nested item.
	var oneToken string
	if err := r.f.db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, onePath).Scan(&oneToken); err != nil {
		t.Fatal(err)
	}
	session, _, err := r.s.Prepare(ctx, r.p, one, oneToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.s.SaveSessionProgress(ctx, r.p, session.ID, 0, reading.Location{Format: "epub", CFI: "epubcfi(/6/4!/4/2:0)", Section: "EPUB/chapter.xhtml", Progression: .4}); err != nil {
		t.Fatal(err)
	}
	if err = r.s.CloseSession(r.p, session.ID); err != nil {
		t.Fatal(err)
	}
	var continued map[string]json.RawMessage
	for _, item := range serve("/v1/reading/continue?limit=100") {
		var nested map[string]json.RawMessage
		var id string
		decode(item["item"], &nested)
		decode(nested["id"], &id)
		if id == one {
			continued = nested
		}
	}
	checkAttribution(continued, "Author One", "Series", "folder")
	// An owner override wins in the same projection and keeps the owner source.
	actor := identity.PersonalKey(r.p.Viewer)
	for field, value := range map[string]string{"author": "Owner Author", "series": "Owner Series"} {
		if _, err := r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at)
 SELECT 'item',id,?,?,'',1,?,datetime('now') FROM catalog_entities WHERE public_id=pid_blob(?)`, field, value, actor, one); err != nil {
			t.Fatal(err)
		}
	}
	checkAttribution(byID(serve("/v1/reading/items?limit=100"))[one], "Owner Author", "Owner Series", "owner")
	// Clearing the owner falls back to the real source facts.
	if _, err := r.f.db.Exec(`UPDATE metadata_owner_fields SET value='',locked=0 WHERE kind='item' AND entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, one); err != nil {
		t.Fatal(err)
	}
	checkAttribution(byID(serve("/v1/reading/items?limit=100"))[one], "Author One", "Series", "folder")
}
