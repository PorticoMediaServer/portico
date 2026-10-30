package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// MOB-030: displayed containers carry their aggregate selection offers on an
// artist's release rows, a container page's sections and a library page, so
// multi-select offers whole-album commands. Leaves keep their Detail actions.
func TestContainerProjectionsCarryAggregateSelectionOffers(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	c := catalogtest.New(t, d.DB)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	c.Song(album, 1, "/music/song.mp3", "Song")
	c.Drain()
	h := New(d)
	offered := func(what string, actions *catalog.HomeActions) {
		t.Helper()
		if actions == nil || !actions.Selection.Favorite || !actions.Details {
			t.Fatalf("%s carries no aggregate offer: %+v", what, actions)
		}
	}

	w := editorRequest(t, h, "GET", "/v1/artists/"+artist.Public+"/albums", session.AccessToken, "", nil)
	if w.Code != 200 {
		t.Fatalf("artist albums %d: %s", w.Code, w.Body.String())
	}
	var releases struct {
		Albums []catalog.Album `json:"albums"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &releases); err != nil || len(releases.Albums) != 1 || releases.Albums[0].ID != album.Public {
		t.Fatalf("artist albums: %v %s", err, w.Body.String())
	}
	offered("artist release row", releases.Albums[0].HomeActions)

	w = editorRequest(t, h, "GET", "/v1/libraries/music/content?view=artist&entityId="+artist.Public, session.AccessToken, "", nil)
	if w.Code != 200 {
		t.Fatalf("artist content %d: %s", w.Code, w.Body.String())
	}
	var page catalog.ContentEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range page.Sections {
		for _, entry := range section.Entries {
			switch entry.Kind {
			case "album":
				found = true
				offered("artist page album", entry.HomeActions)
			case "song":
				if entry.HomeActions != nil {
					t.Fatalf("a leaf borrowed container offers: %+v", entry)
				}
			}
		}
	}
	if !found {
		t.Fatalf("artist page shows no album: %s", w.Body.String())
	}

	w = editorRequest(t, h, "POST", "/v1/libraries/music/browse", session.AccessToken, "application/json", []byte(`{"pivot":"albums","sort":[{"field":"title","direction":"asc"}],"limit":10}`))
	if w.Code != 200 {
		t.Fatalf("library browse %d: %s", w.Code, w.Body.String())
	}
	var browse catalog.BrowseResult
	if err := json.Unmarshal(w.Body.Bytes(), &browse); err != nil || len(browse.Entries) != 1 {
		t.Fatalf("library browse: %v %s", err, w.Body.String())
	}
	offered("library album", browse.Entries[0].HomeActions)
}
