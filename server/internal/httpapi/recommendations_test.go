package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestRelatedMoviesDetailPermissionAndRevocation(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	a := c.Library("a", "Allowed", "movie", "/a")
	b := c.Library("b", "Private", "movie", "/b")
	source := c.Movie(a, "/a/source.mkv", "Source", 2020)
	related := c.Movie(a, "/a/related.mkv", "Related", 2019)
	secret := c.Movie(b, "/b/secret.mkv", "Secret related movie", 2020)
	setGenre := func(item catalogtest.Item) {
		t.Helper()
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetTermsTx(ctx, tx, item.ID, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "16", Name: "Animation"}})
		})
	}
	setGenre(source)
	setGenre(related)
	setGenre(secret)
	c.Drain()
	var facetCount int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_related_facets WHERE entity_id IN(?,?) AND relation=1 AND provider='name' AND facet_id='animation'`, source.ID, related.ID).Scan(&facetCount); e != nil {
		t.Fatal(e)
	}
	if facetCount != 2 {
		t.Fatalf("compact source and related genre facets: %d", facetCount)
	}
	var candidates int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_related_facets f JOIN catalog_entities i ON i.id=f.entity_id WHERE f.library_id=(SELECT id FROM catalog_libraries WHERE library_id='a') AND f.relation=1 AND f.provider='name' AND f.facet_id='animation' AND f.entity_id<>? AND f.year<2020 AND EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=i.id AND l.available=1)`, source.ID).Scan(&candidates); e != nil {
		t.Fatal(e)
	}
	if candidates != 1 {
		t.Fatalf("matching available compact related candidates: %d", candidates)
	}
	target, getErr := catalog.New(db).Get("test-profile", related.Public)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if target.Kind != "movie" || target.LibraryID != "a" || !target.Available {
		t.Fatalf("related item hydration: %+v", target)
	}
	control, e := hosted.New(db, ident, "http://127.0.0.1:19436", tl6HostedRootPin(), tl6HostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	apply := func(revision int64, libraries []string) {
		encoded, _ := json.Marshal(libraries)
		if _, e = db.Exec(`UPDATE direct_memberships SET allowed_libraries=?,revision=revision+1 WHERE account_id='member'`, string(encoded)); e != nil {
			t.Fatal(e)
		}
		p := hosted.Policy{ServerID: ident.ID(), Revision: revision, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: libraries}}}
		raw, _ := json.Marshal(p)
		if e = storeHTTPFixturePolicy(db, raw); e != nil {
			t.Fatal(e)
		}
	}
	apply(1, []string{"a"})
	session, e := issuePorticoFixture(t, db, ident, "member", "p", "member")
	if e != nil {
		t.Fatal(e)
	}
	settleCompactCatalogue(t, db)
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	relatedQuery := ""
	request := func(item string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/items/"+item+"/detail"+relatedQuery, nil)
		r.Header.Set("Authorization", "Bearer "+session.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request(source.Public)
	var detail catalog.Detail
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Related == nil || len(detail.Related.Rows) != 1 || len(detail.Related.Rows[0].Entries) != 1 || strings.Contains(w.Body.String(), "Secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, value := range []string{"none", "all", "movies", ""} {
		relatedQuery = "?related=" + value
		w = request(source.Public)
		var body map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		_, present := body["related"]
		if present != (value != "none") {
			t.Fatalf("related=%s: %s", value, w.Body.String())
		}
	}
	relatedQuery = ""
	if w = request(secret.Public); w.Code != 404 {
		t.Fatal("denied source leaked", w.Code)
	}
	// Preserve the movie-only related contract for older installed clients.
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2026)
	otherAlbum := c.Album(artist, "Other release", 2026)
	trackOne := c.Song(album, 1, "/music/track-1.m4a", "Track 1")
	c.Song(otherAlbum, 2, "/music/track-2.m4a", "Track 2")
	c.Drain()
	apply(2, []string{"a", "music"})
	w = request(trackOne.Public)
	detail = catalog.Detail{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Related != nil {
		t.Fatal("legacy listening response changed", w.Code, w.Body.String())
	}
	relatedQuery = "?related=all"
	w = request(trackOne.Public)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Related == nil || len(detail.Related.Rows) != 1 || detail.Related.Rows[0].Entries[0].ID != otherAlbum.Public {
		t.Fatal("new related rows absent", w.Code, w.Body.String())
	}
	relatedQuery = ""
	apply(3, []string{"b"})
	if w = request(source.Public); w.Code != 404 {
		t.Fatal("revoked library read returned old recommendations", w.Code)
	}
	db.Exec(`UPDATE authorization_session_families SET revoked=1`)
	if w = request(secret.Public); w.Code != 401 {
		t.Fatal("revoked session returned recommendations", w.Code)
	}
}
