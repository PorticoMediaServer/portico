package api

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

type stillProber struct{}

func (stillProber) Probe(context.Context, string) (scan.Facts, error) {
	return scan.Facts{Container: "mkv", DurationMS: 60_000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// shelf is a server with a film library scanned and looked up at a stand-in for TMDB, the
// owner's token, an admin's whose profile is limited to titles rated 13 and under, and a
// member's.
type shelf struct {
	t                           *testing.T
	s                           *Server
	tmdb                        *tmdbtest.Server
	library                     string
	owner, limitedAdmin, member string
}

func newShelf(t *testing.T, files ...string) *shelf {
	t.Helper()
	ctx := context.Background()
	s := newTestServer(t)
	sh := &shelf{t: t, s: s, tmdb: tmdbtest.New(t)}
	signedIn := setup(t, s)
	sh.owner = onMain(t, s, signedIn).AccessToken
	s.UseTMDBHost(sh.tmdb.URL)
	key := "test-token"
	sh.do("PUT", "/v1/admin/metadata/settings", MetadataSettingsEdit{TMDBKey: &key}, nil, 200)
	root := filepath.Join(t.TempDir(), "films")
	for i, name := range files {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100+i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := s.db.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: s.db, Prober: stillProber{}}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.metadata.MatchLibrary(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	sh.library = lib.PublicID
	viewers, err := seed.Profiles(ctx, s.db, 2)
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.tokens.CheckAccess(signedIn.Session.AccessToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mint := func(v db.Viewer, role string) string {
		token, _ := s.tokens.Access(auth.Claims{UserID: v.UserID, ProfileID: v.ProfileID, Role: role, Manage: true, Boot: started.Boot},
			time.Now(), time.Now().Add(time.Hour))
		return token
	}
	sh.member, sh.limitedAdmin = mint(viewers[0], "member"), mint(viewers[1], "admin")
	return sh
}

// do sends one request as the owner and fails the test unless it answers want.
func (sh *shelf) do(method, path string, body, out any, want int) {
	sh.t.Helper()
	if w := call(sh.t, sh.s, method, path, sh.owner, body, out); w.Code != want {
		sh.t.Fatalf("%s %s answered %d, want %d: %s", method, path, w.Code, want, w.Body)
	}
}

func (sh *shelf) unmatched() []UnmatchedEntry {
	sh.t.Helper()
	var page Page[UnmatchedEntry]
	sh.do("GET", "/v1/admin/libraries/"+sh.library+"/unmatched", nil, &page, 200)
	return page.Items
}

// The owner sees why a film was not matched, finds it by name or by a pasted address, uses a
// match, keeps another as it is, merges two by choosing the same match, splits them again,
// and refreshes; a provider that is down changes nothing and says so.
func TestFixMatchRoutes(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv", "Heat.mkv", "Zzyzx Home Video (2011).mkv")
	list := sh.unmatched()
	if len(list) != 2 || list[0].Title != "Heat" || list[0].Reason != "several_titles" || len(list[0].Matches) != 3 ||
		list[1].Reason != "no_results" {
		t.Fatalf("unmatched: %+v", list)
	}
	heat, zzyzx := list[0].ItemID, list[1].ItemID
	var page Page[UnmatchedEntry]
	sh.do("GET", "/v1/admin/libraries/"+sh.library+"/unmatched?limit=1", nil, &page, 200)
	cursor := page.NextCursor
	page = Page[UnmatchedEntry]{}
	sh.do("GET", "/v1/admin/libraries/"+sh.library+"/unmatched?limit=1&cursor="+cursor, nil, &page, 200)
	if len(page.Items) != 1 || page.Items[0].ItemID != zzyzx || page.NextCursor != "" {
		t.Fatalf("second page: %+v", page)
	}

	var found MatchCandidates
	sh.do("GET", "/v1/admin/items/"+zzyzx+"/matches?ref="+url.QueryEscape("https://www.themoviedb.org/movie/949-heat"), nil, &found, 200)
	if len(found.Items) != 1 || found.Items[0].ID != "949" || !found.Items[0].Exact {
		t.Fatalf("by address: %+v", found)
	}
	sh.do("GET", "/v1/admin/items/"+zzyzx+"/matches?q=Heat&year=1995", nil, &found, 200)
	if len(found.Items) < 2 || found.Items[0].ID != "949" {
		t.Fatalf("by name: %+v", found)
	}
	sh.do("GET", "/v1/admin/items/"+zzyzx+"/matches?ref="+url.QueryEscape("https://www.themoviedb.org/tv/1396"), nil, nil, 422)

	var result MatchResult
	sh.do("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, &result, 200)
	if result.ItemID != heat {
		t.Fatalf("using a match moved the title to %s", result.ItemID)
	}
	var item Item
	sh.do("GET", "/v1/items/"+heat, nil, &item, 200)
	if item.Title != "Heat" || item.Year != 1995 || item.ExternalIDs["tmdb"] != "949" {
		t.Fatalf("after using a match: %+v", item)
	}
	sh.do("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{KeepAsIs: true}, nil, 409) // matched: nothing to keep
	sh.do("PUT", "/v1/admin/items/"+zzyzx+"/match", MatchRequest{KeepAsIs: true}, nil, 200)
	if list := sh.unmatched(); len(list) != 0 {
		t.Fatalf("after Keep as is: %+v", list)
	}

	// Choosing for one film the match another already has makes them one, with both versions.
	sh.do("PUT", "/v1/admin/items/"+zzyzx+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, &result, 200)
	sh.do("GET", "/v1/items/"+heat, nil, &item, 200)
	if result.ItemID != heat || len(item.Versions) != 2 {
		t.Fatalf("choosing a match the library has: kept %s with %d versions", result.ItemID, len(item.Versions))
	}
	var kept Item
	sh.do("GET", "/v1/items/"+zzyzx, nil, &kept, 200) // a link to the merged-away title opens the one that remains
	if kept.ID != heat {
		t.Fatalf("the merged-away title's link opened %s", kept.ID)
	}
	sh.do("POST", "/v1/admin/versions/"+item.Versions[1].ID+"/split", nil, &result, 200)
	var split Item
	sh.do("GET", "/v1/items/"+result.ItemID, nil, &split, 200)
	if split.Title != "Zzyzx Home Video (2011)" || len(split.Versions) != 1 {
		t.Fatalf("split off: %+v", split)
	}
	sh.do("POST", "/v1/admin/versions/"+split.Versions[0].ID+"/split", nil, nil, 409)

	var refreshed MetadataRefreshed
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, &refreshed, 200)
	if !refreshed.Matched {
		t.Fatal("a matched film was refreshed and is not matched")
	}
	sh.tmdb.Down.Store(true)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil, 502)
	sh.do("GET", "/v1/items/"+heat, nil, &item, 200)
	if item.Title != "Heat" || item.Summary == "" {
		t.Fatalf("a refresh the provider did not answer changed the film: %+v", item)
	}
}

// A member may not use these routes, and an admin whose profile is limited is never told of a
// title hidden from it, not even by the Unmatched items list.
func TestMatchRoutesHideWhatTheCallerMayNotSee(t *testing.T) {
	sh := newShelf(t, "Heat.mkv")
	heat := sh.unmatched()[0].ItemID
	for _, path := range []string{"/v1/admin/libraries/" + sh.library + "/unmatched", "/v1/admin/items/" + heat + "/matches"} {
		if w := call(t, sh.s, "GET", path, sh.member, nil, nil); w.Code != 403 {
			t.Errorf("a member asked %s and was answered %d", path, w.Code)
		}
	}
	var page Page[UnmatchedEntry]
	if w := call(t, sh.s, "GET", "/v1/admin/libraries/"+sh.library+"/unmatched", sh.limitedAdmin, nil, &page); w.Code != 200 || len(page.Items) != 0 {
		t.Errorf("the limited admin's Unmatched items: %d %+v", w.Code, page.Items)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{{"GET", "/v1/admin/items/" + heat + "/matches", nil}, {"PUT", "/v1/admin/items/" + heat + "/match", MatchRequest{KeepAsIs: true}},
		{"POST", "/v1/admin/items/" + heat + "/metadata/refresh", nil}} {
		if w := call(t, sh.s, c.method, c.path, sh.limitedAdmin, c.body, nil); w.Code != 404 {
			t.Errorf("the limited admin sent %s %s and was answered %d", c.method, c.path, w.Code)
		}
	}
}
