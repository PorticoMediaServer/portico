package api

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
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
	library, root               string
	owner, limitedAdmin, member string
}

func newShelf(t *testing.T, files ...string) *shelf {
	t.Helper()
	return shelfOf(t, db.LibraryMovies, files...)
}

// shelfOf is newShelf with a library of the given kind; files may be in folders.
func shelfOf(t *testing.T, kind string, files ...string) *shelf {
	t.Helper()
	ctx := context.Background()
	s := newTestServer(t)
	sh := &shelf{t: t, s: s, tmdb: tmdbtest.New(t)}
	signedIn := setup(t, s)
	sh.owner = onMain(t, s, signedIn).AccessToken
	s.UseProvider(tmdb.New("test-token", sh.tmdb.URL))
	root := filepath.Join(t.TempDir(), "films")
	for i, name := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100+i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := s.db.CreateLibrary(ctx, "Films", kind, "en", []string{root})
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
	sh.library, sh.root = lib.PublicID, root
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
// match, keeps another as it is (TestKeepAsIsTakesAWrongMatchOff for a matched one), merges two by choosing the same match, splits them again,
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
	sh.do("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{Provider: "tmdb", ID: "949", EpisodeOrder: "dvd"}, nil, 422) // a film has no episodes
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

// A show matched in an episode order its provider does not have gets aired order, never
// blank episodes, and the answer says so in a sentence for the person.
func TestAnEpisodeOrderTheProviderLacksFallsBackToAired(t *testing.T) {
	sh := shelfOf(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv")
	var shows, episodes ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=show", nil, &shows, 200)
	if len(shows.Items) != 1 {
		t.Fatalf("shows: %+v", shows.Items)
	}
	show := shows.Items[0].ID
	var result MatchResult
	sh.do("PUT", "/v1/admin/items/"+show+"/match", MatchRequest{Provider: "tmdb", ID: "1396", EpisodeOrder: "absolute"}, &result, 200)
	if result.Notice != "This show has no absolute order at its provider, so its episodes use aired order." {
		t.Fatalf("absolute order for a show with none: %+v", result)
	}
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=episode", nil, &episodes, 200)
	if len(episodes.Items) != 1 || episodes.Items[0].Title != "Pilot" {
		t.Fatalf("its episode: %+v", episodes.Items)
	}
	result = MatchResult{}
	sh.do("PUT", "/v1/admin/items/"+show+"/match", MatchRequest{Provider: "tmdb", ID: "1396", EpisodeOrder: "dvd"}, &result, 200)
	if result.Notice != "" {
		t.Fatalf("DVD order, which the show has: %+v", result)
	}
}

// An owner takes a wrong match off a film with Keep as is (the demo's "Portico HDR10 Test
// Pattern" matched to Portico (2014), 16 Nov): the provider's ids, details, cast and pictures
// go, the film is named as its file names it, it is not on Unmatched items, and neither a scan
// nor a lookup pass matches it again by itself; Look it up again does.
func TestKeepAsIsTakesAWrongMatchOff(t *testing.T) {
	sh := newShelf(t, "the.matrix.1999.1080p.mkv")
	ctx := context.Background()
	libraryID, err := sh.s.db.LibraryID(ctx, sh.library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sh.s.metadata.FetchPictures(ctx, libraryID, 100); err != nil {
		t.Fatal(err)
	}
	var page ItemPage
	sh.do("GET", "/v1/items?library="+sh.library+"&kind=movie", nil, &page, 200)
	film := page.Items[0].ID
	if before := sh.detail(film); before.ExternalIDs["tmdb"] != "603" || len(before.Credits) == 0 || before.Images["poster"] == "" {
		t.Fatalf("the film before: ids %v, %d credits, poster %q", before.ExternalIDs, len(before.Credits), before.Images["poster"])
	}
	sh.do("PUT", "/v1/admin/items/"+film+"/match", MatchRequest{KeepAsIs: true}, nil, 200)
	title, year := scan.FileTitle(db.LibraryMovies, "the.matrix.1999.1080p.mkv")
	check := func(when string) {
		t.Helper()
		after := sh.detail(film)
		if len(after.ExternalIDs) != 0 || len(after.Credits) != 0 || after.Images["poster"] != "" || after.Summary != "" ||
			after.Title != title || after.Year != year {
			t.Fatalf("%s: %q (%d), ids %v, %d credits, poster %q, summary %q; want %q (%d) with nothing from the provider",
				when, after.Title, after.Year, after.ExternalIDs, len(after.Credits), after.Images["poster"], after.Summary, title, year)
		}
	}
	check("after Keep as is")
	if list := sh.unmatched(); len(list) != 0 {
		t.Fatalf("a film kept as it is is on Unmatched items: %+v", list)
	}
	libraries, err := sh.s.db.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: sh.s.db, Prober: stillProber{}}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.s.metadata.MatchLibrary(ctx, libraryID); err != nil {
		t.Fatal(err)
	}
	check("after a scan and a lookup pass")
	var refreshed MetadataRefreshed
	sh.do("POST", "/v1/admin/items/"+film+"/metadata/refresh", nil, &refreshed, 200)
	if after := sh.detail(film); !refreshed.Matched || after.ExternalIDs["tmdb"] != "603" {
		t.Fatalf("Look it up again: matched %v, ids %v", refreshed.Matched, after.ExternalIDs)
	}
}

// A title offered as a match shows its poster and why it was suggested (definition area 4,
// section 1; owner-metadata frames 2 and 10). The poster is the server's copy, behind an
// address of its own that an <img> can load: no provider address reaches an app.
func TestMatchesCarryTheServersPosterAndWhy(t *testing.T) {
	sh := newShelf(t, "Heat.mkv", "Zzyzx Home Video (2011).mkv")
	list := sh.unmatched()
	if len(list) != 2 || list[0].Title != "Heat" || len(list[0].Matches) != 3 {
		t.Fatalf("unmatched: %+v", list)
	}
	first := list[0].Matches[0]
	if !strings.HasPrefix(first.PosterURL, "/v1/match-posters/") || strings.Contains(first.PosterURL, sh.tmdb.URL) ||
		first.Why.Title != "same" || first.Why.Year != "" {
		t.Fatalf("Heat's first match: %+v; want the server's poster address, the same title and no year to compare", first)
	}
	var found MatchCandidates
	sh.do("GET", "/v1/admin/items/"+list[1].ItemID+"/matches?q=Heat&year=1995", nil, &found, 200)
	if len(found.Items) == 0 || found.Items[0].PosterURL == "" || found.Items[0].Why.Title != "different" || found.Items[0].Why.Year != "different" {
		t.Fatalf("a search by hand from Zzyzx Home Video (2011): %+v", found.Items)
	}
	// The poster's address needs no token, as an image's does not, and leads to the server's copy.
	w := call(t, sh.s, "GET", first.PosterURL+"?size=small", "", nil, nil)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "/v1/images/") || !strings.HasSuffix(w.Header().Get("Location"), "?size=small") {
		t.Fatalf("the poster address answered %d, Location %q", w.Code, w.Header().Get("Location"))
	}
	if w := call(t, sh.s, "GET", w.Header().Get("Location"), "", nil, nil); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
		t.Fatalf("the poster answered %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	asked := sh.tmdb.PictureRequests.Load()
	if w := call(t, sh.s, "GET", first.PosterURL, "", nil, nil); w.Code != 302 || sh.tmdb.PictureRequests.Load() != asked {
		t.Errorf("a poster already fetched was fetched again (%d)", w.Code)
	}
	if w := call(t, sh.s, "GET", "/v1/match-posters/0123456789abcdef0123456789abcdef", "", nil, nil); w.Code != 404 {
		t.Errorf("an unknown poster address answered %d", w.Code)
	}
}

// A merge sent again after its answer was lost answers as the first did and changes nothing:
// the title merged away is the kept one now. Another title that was never merged into this
// one is still not found.
func TestAMergeSentAgainAnswersTheSame(t *testing.T) {
	sh := newShelf(t, "Heat.mkv", "Zzyzx Home Video (2011).mkv")
	list := sh.unmatched()
	if len(list) != 2 {
		t.Fatalf("unmatched: %+v", list)
	}
	heat, zzyzx := list[0].ItemID, list[1].ItemID
	var first, again MatchResult
	sh.do("POST", "/v1/admin/items/"+heat+"/merge", MergeRequest{ItemID: zzyzx}, &first, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/merge", MergeRequest{ItemID: zzyzx}, &again, 200)
	if first.ItemID != heat || again.ItemID != heat {
		t.Fatalf("a merge and its repeat kept %s and %s, want %s", first.ItemID, again.ItemID, heat)
	}
	var item Item
	sh.do("GET", "/v1/items/"+heat, nil, &item, 200)
	if len(item.Versions) != 2 {
		t.Fatalf("after a merge and its repeat the title has %d versions, want 2", len(item.Versions))
	}
	sh.do("POST", "/v1/admin/items/"+heat+"/merge", MergeRequest{ItemID: "itm_neverthere"}, nil, 404)
}

// A device that was offline while two titles became one still sends its edits to the old id:
// they go to the title that remains, alone and in a batch.
func TestStateSentToAMergedAwayTitleReachesTheOneThatRemains(t *testing.T) {
	sh := newShelf(t, "Heat.mkv", "Zzyzx Home Video (2011).mkv")
	list := sh.unmatched()
	heat, zzyzx := list[0].ItemID, list[1].ItemID
	sh.do("POST", "/v1/admin/items/"+heat+"/merge", MergeRequest{ItemID: zzyzx}, nil, 200)
	sh.do("PUT", "/v1/items/"+zzyzx+"/state", map[string]any{"favorite": true}, nil, 200)
	var batch StateBatchReply
	sh.do("POST", "/v1/items/state:batch", map[string]any{"items": []map[string]any{{"itemId": zzyzx, "inList": true}}}, &batch, 200)
	if len(batch.Items) != 1 || batch.Items[0].Error != nil {
		t.Fatalf("a batched edit to the merged-away id: %+v", batch.Items)
	}
	var item Item
	sh.do("GET", "/v1/items/"+heat, nil, &item, 200)
	if !item.State.Favorite || !item.State.InList {
		t.Fatalf("the title that remains did not get the edits: %+v", item.State)
	}
}

// A match keeps the language the title was made in, from TMDB's answer, and playback is given
// it: what the default audio choice "the original language" plays.
func TestAMatchKeepsTheOriginalLanguageForPlayback(t *testing.T) {
	sh := newShelf(t, "Heat.mkv")
	heat := sh.unmatched()[0].ItemID
	sh.do("PUT", "/v1/admin/items/"+heat+"/match", MatchRequest{Provider: "tmdb", ID: "949"}, nil, 200)
	// A refresh also records whether the title is still running: both must be kept.
	for _, step := range []string{"matching", "refreshing"} {
		if step == "refreshing" {
			sh.do("POST", "/v1/admin/items/"+heat+"/metadata/refresh", nil, nil, 200)
		}
		item, err := sh.s.db.PlaybackItem(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, heat)
		if err != nil || item.OriginalLanguage != "en" {
			t.Fatalf("after %s Heat the playback item's original language is %q (%v), want en", step, item.OriginalLanguage, err)
		}
	}
}
