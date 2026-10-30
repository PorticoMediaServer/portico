package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// M14 contract: the listening entity facts, popular tracks, release roles and
// category artwork validate against the published schemas on real handler
// responses, so the client lane can build on them.
func TestListeningAndCategoryResponsesMatchSpec(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	music := c.Library("music", "Music", "music", "/music")
	movies := c.Library("movies", "Movies", "movie", "/movies")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2020)
	songOne := c.Song(album, 1, "/music/one.m4a", "One")
	songTwo := c.Song(album, 2, "/music/two.m4a", "Two")
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`)
	owner, err := ident.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	key := identity.PersonalKey(owner.Viewer)
	c.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES(?,?,2),(?,?,1)`, key, songOne.ID, key, songTwo.ID)
	movieOne := c.Movie(movies, "/movies/alpha.mkv", "Alpha", 1995)
	movieTwo := c.Movie(movies, "/movies/beta.mkv", "Beta", 2005)
	c.Fields(movieOne.ID, map[string]any{"poster_url": "/p1"})
	c.Fields(movieTwo.ID, map[string]any{"poster_url": "/p2"})
	c.Genres(movieOne.ID, "tmdb", "Drama")
	c.Genres(movieTwo.ID, "tmdb", "Drama")
	c.Attributes(movieOne.ID, "studio", "Pixar")
	c.Drain()
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)})
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, bytes.NewBufferString(""))
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	_, entrySchema, err := apispec.Schema("ContentEntry")
	if err != nil {
		t.Fatal(err)
	}
	_, categorySchema, err := apispec.Schema("Category")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := apispec.Documents()
	if err != nil {
		t.Fatal(err)
	}
	validateEntries := func(body []byte) {
		t.Helper()
		var envelope struct {
			Entity   map[string]any `json:"entity"`
			Sections []struct {
				Entries []map[string]any `json:"entries"`
			} `json:"sections"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		for _, doc := range docs {
			if envelope.Entity != nil {
				if problems := doc.Validate(entrySchema, envelope.Entity); len(problems) > 0 {
					t.Fatalf("entity: %v", problems)
				}
			}
			for _, section := range envelope.Sections {
				for _, entry := range section.Entries {
					if problems := doc.Validate(entrySchema, entry); len(problems) > 0 {
						t.Fatalf("entry %v: %v", entry["id"], problems)
					}
				}
			}
			break
		}
	}
	for _, path := range []string{
		"/v1/libraries/music/content?view=artist&entityId=" + artist.Public,
		"/v1/libraries/music/content?view=album&entityId=" + album.Public,
		"/v1/libraries/movies/content?view=categories",
		"/v1/libraries/movies/content?view=browse&category=genre:Drama",
	} {
		w := request(path)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		tl6AssertSpecResponse(t, "GET", "/v1/libraries/{id}/content", w)
		validateEntries(w.Body.Bytes())
	}
	artistResponse := request("/v1/libraries/music/content?view=artist&entityId=" + artist.Public)
	var projection catalog.ContentEnvelope
	if err := json.Unmarshal(artistResponse.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if len(projection.Sections) != 3 || projection.Sections[1].ID != "popularTracks" || len(projection.Sections[1].Entries) != 2 {
		t.Fatalf("popular tracks missing: %+v", projection.Sections)
	}
	albumResponse := request("/v1/libraries/music/content?view=album&entityId=" + album.Public)
	projection = catalog.ContentEnvelope{}
	if err := json.Unmarshal(albumResponse.Body.Bytes(), &projection); err != nil || projection.Entity == nil {
		t.Fatal(projection, err)
	}
	if projection.Entity.Year == nil || *projection.Entity.Year != 2020 || projection.Entity.Artist == nil || projection.Entity.Duration == nil {
		t.Fatalf("album facts missing: %+v", projection.Entity)
	}
	w := request("/v1/libraries/movies/categories")
	if w.Code != 200 {
		t.Fatalf("categories: %d %s", w.Code, w.Body.String())
	}
	tl6AssertSpecResponse(t, "GET", "/v1/libraries/{id}/categories", w)
	var listing struct {
		Categories []map[string]any `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Categories) == 0 {
		t.Fatal("no categories")
	}
	for _, doc := range docs {
		for _, category := range listing.Categories {
			if problems := doc.Validate(categorySchema, category); len(problems) > 0 {
				t.Fatalf("category %v: %v", category["id"], problems)
			}
		}
		break
	}
}
