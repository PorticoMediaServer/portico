package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

func TestSearchGenresHandlerUsesCurrentLibraryAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	allowed := c.Library("allowed", "Allowed", "movie", "/allowed")
	private := c.Library("private", "Private", "movie", "/private")
	one := c.Movie(allowed, "/allowed/one.mkv", "One", 2026)
	secret := c.Movie(private, "/private/secret.mkv", "Secret", 2026)
	c.Genres(one.ID, "fixture", "Drama")
	c.Genres(secret.ID, "fixture", "Secret genre")
	c.Drain()
	control, err := hosted.New(db, ident, "http://127.0.0.1:19410", tl6HostedRootPin(), tl6HostedRootID())
	if err != nil {
		t.Fatal(err)
	}
	apply := func(revision int64, libraries []string) {
		policy := hosted.Policy{ServerID: ident.ID(), Revision: revision, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: libraries}}}
		raw, _ := json.Marshal(policy)
		if err := storeHTTPFixturePolicy(db, raw); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, []string{"allowed"})
	session, err := tl6IssueHostedFixture(t, db, ident, "member", "p", "member")
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control}
	preparations, err := readprep.New(readprep.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer preparations.Close()
	bases, err := catalog.NewFacetRuntime(db, preparations, catalog.CheckFacetWindowAuthority, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	genres, err := catalog.NewGenreRuntime(db, preparations, catalog.CheckFacetWindowAuthority, bases)
	if err != nil {
		t.Fatal(err)
	}
	d.GenreReads = genres
	life, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	supervise.Go("test.genre-http-bases", func() { done <- bases.Run(life) })
	defer func() {
		stop()
		if err := <-done; err != nil && err != context.Canceled {
			t.Error(err)
		}
	}()
	request := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		d.searchGenresHandler(w, r)
		return w
	}
	path := "/v1/search/genres?limit=8"
	if w := request(path, ""); w.Code != 401 {
		t.Fatal("anonymous genre disclosure", w.Code, w.Body.String())
	}
	read := func() catalog.SearchGenrePage {
		w := request(path, session.AccessToken)
		deadline := time.Now().Add(10 * time.Second)
		for w.Code == 202 && time.Now().Before(deadline) {
			var pending catalog.SearchGenrePreparing
			if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", "/v1/genre-queries/"+pending.QueryID, nil)
			r.Header.Set("Authorization", "Bearer "+session.AccessToken)
			r.SetPathValue("id", pending.QueryID)
			time.Sleep(time.Millisecond)
			w = httptest.NewRecorder()
			d.genreQueryHandler(w, r)
		}
		var page catalog.SearchGenrePage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return page
	}
	if page := read(); len(page.Genres) != 1 || page.Genres[0].Value != "Drama" || page.Genres[0].Count != 1 {
		t.Fatal("genre authority diverged from browse", page)
	}
	openGenre := func(value string) catalog.BrowseResult {
		body, _ := json.Marshal(map[string]any{"pivot": "video-titles", "query": map[string]any{"variant": "predicate", "field": "genre", "operator": "equals", "value": value}, "limit": 1})
		r := httptest.NewRequest("POST", "/v1/browse", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+session.AccessToken)
		w := httptest.NewRecorder()
		d.browseAcrossHandler(w, r)
		var result catalog.BrowseResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return result
	}
	if opened := openGenre("Drama"); len(opened.Entries) != 1 || opened.Entries[0].ID != one.Public {
		t.Fatal("tile did not open its authorized title", opened)
	}
	apply(2, []string{"private"})
	if _, err = db.Exec(`UPDATE direct_memberships SET allowed_libraries='["private"]',revision=revision+1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	if page := read(); len(page.Genres) != 1 || page.Genres[0].Value != "Secret genre" {
		t.Fatal("cached genres survived a library revocation", page)
	}
	if opened := openGenre("Drama"); len(opened.Entries) != 0 {
		t.Fatal("tile destination retained revoked library", opened)
	}
	if opened := openGenre("Secret genre"); len(opened.Entries) != 1 || opened.Entries[0].ID != secret.Public {
		t.Fatal("tile destination missed current library", opened)
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=8&limit=4", "?source=/private"} {
		if w := request("/v1/search/genres"+query, session.AccessToken); w.Code != 400 {
			t.Fatal("unbounded or invalid request accepted", query, w.Code, w.Body.String())
		}
	}
	if w := request("/v1/search/genres?cursor=forged", session.AccessToken); w.Code != 409 {
		t.Fatal("forged continuation did not request re-anchor", w.Code, w.Body.String())
	}
	if _, err = db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	if w := request(path, session.AccessToken); w.Code != 401 {
		t.Fatal("disabled member retained genre access", w.Code, w.Body.String())
	}
}
