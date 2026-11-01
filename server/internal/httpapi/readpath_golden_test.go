package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// The read path is being rebuilt underneath these routes: counts become
// maintained counters, the browse union view becomes a materialised table, the
// letter index becomes a prefix sum, facets become bucketed rollups. Every one
// of those changes is a promise that the *answer* does not change — only where
// it comes from.
//
// This is the test that holds the promise. It drives a representative matrix of
// route × pivot × sort × restriction against a deterministic catalogue,
// normalises the handful of values that cannot be deterministic across runs (a
// session hash, an HMAC-signed cursor, a wall clock), and compares the result
// against a file recorded on the behaviour that existed before any of the read
// models were built.
//
// A case records its status code and its whole normalised body, so a change of
// membership, of ordering, of a count, of a letter index or of an error is a
// diff on a named case rather than a silent difference nobody looks at. The
// golden file is regenerated only by running with PORTICO_GOLDEN_UPDATE=1, and
// regenerating it is the thing this workstream is not allowed to do.
//
// It was recorded again on 13 October 2026, after the wire itself was changed
// on purpose, and only once every kind of difference from the earlier
// recording had been accounted for: media times in whole milliseconds
// (durationMs, progressMs, startMs) and ratings from 0 to 10; the `missing`
// flag on titles; `seeAll` on Home rows; `total` on facets; `discNumber` on
// songs; search by accent-folded prefix with Collections and Authors groups,
// cast names in a title's text, and title order by sort key; and groupings
// (genres, decades, series) paged, honouring a range, and listed by title in
// the direction asked. What did not change, and was checked case by case: the
// members and counts of every grouping, every total a restricted viewer is
// given, and every status code.
// The second combined-base pass records the grouping-sort capability document
// and its two newly supported year-sort requests (books and songs, 400 to 200).
// Apart from those responses and deterministic catalogue revision advances,
// all existing result membership, ordering and restricted totals are unchanged.
// Recorded again on 18 October 2026 for Recently Added in Plex's shape: only
// the four Recently Added TV cases changed, each show now standing for what
// arrived (its season, or for the restricted viewer the one episode it may
// see). Every other case is unchanged.

// volatileKeys are the JSON keys whose values cannot be stable across two runs
// of the same catalogue: they carry a session hash, an HMAC signature, a wall
// clock or a per-install identity. Each is replaced by a marker that still
// records whether a value was present, so "a cursor appeared" and "no cursor
// appeared" remain different results.
var volatileKeys = map[string]bool{
	"viewerFence": true, "generatedAt": true, "nextCursor": true, "serverId": true,
	"cursor": true, "expiresAt": true, "issuedAt": true, "updatedAt": true,
	"receiptId": true, "operationId": true, "etag": true,
}

// stringRevisionKeys are keys whose value is a revision *digest* (a string) that
// folds the viewer fence in, as opposed to a revision *counter* (a number) that
// does not. Only the digest form is volatile.
func normaliseValue(key string, value any) any {
	if volatileKeys[key] {
		if value == nil {
			return nil
		}
		if text, ok := value.(string); ok {
			if text == "" {
				return ""
			}
			return "<volatile>"
		}
	}
	if key == "revision" {
		if text, ok := value.(string); ok && text != "" {
			return "<volatile>"
		}
	}
	// Whole-selection carries the page's viewer-bound browse digest under
	// this key. Its raw equality to pageInfo.revision is asserted first.
	if key == "catalogRevision" {
		if text, ok := value.(string); ok && len(text) == 71 && strings.HasPrefix(text, "browse_") && digestValue.MatchString(strings.TrimPrefix(text, "browse_")) {
			return "<volatile>"
		}
	}
	return normalise(value)
}

// digestValue matches a value that is a bare cryptographic digest — a person id
// derived from a per-install salt, a viewer fence, a signed revision. Nothing in
// the fixture's own identifiers looks like one: they are all spelled
// `movie-0000000`, `fixture-music`, `collection-00000`.
var digestValue = regexp.MustCompile(`^[0-9a-f]{32}(?:[0-9a-f]{32})?$`)

// goldenNames spells every public id as its entity's source key. Public ids are
// random per install; the source key (`movie-0000000`) is what the fixture chose.
var goldenNames = strings.NewReplacer()

func normalise(value any) any {
	if text, ok := value.(string); ok && digestValue.MatchString(text) {
		return "<digest>"
	}
	if text, ok := value.(string); ok {
		return goldenNames.Replace(text)
	}
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, inner := range typed {
			out[key] = normaliseValue(key, inner)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, inner := range typed {
			out = append(out, normalise(inner))
		}
		return out
	}
	return value
}

// goldenCase is one request and the answer it produced.
type goldenCase struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body,omitempty"`
	Viewer string `json:"viewer"`
	Status int    `json:"status"`
	// Response is the normalised body, re-encoded with sorted keys.
	Response string `json:"response"`
}

// goldenFixture names the deterministic catalogue the matrix addresses.
type goldenFixture struct {
	library         fixture.Libraries
	item            string
	db              *sql.DB
	public          map[string]string
	browsePlayables map[string][]goldenPlayable
}

func buildGoldenFixture(t *testing.T) (*goldenFixture, func(method, path, body, viewer string) (int, string)) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "db")
	libraries, err := fixture.Build(context.Background(), fixture.Tiny(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stablePublicIDs(t, db)
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.Verify(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	// The production classifier runs after catalogue publication. Settle the
	// fixture's pending ratings before comparing stable restricted projections.
	if _, err = cat.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	cat.Clock = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
	host, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: ident, Catalog: cat, Hosted: host, Playback: playback.New(db)}
	facets := composeHTTPFacetFixture(t, &d)
	handler := New(d)

	all := fmt.Sprintf(`["%s","%s","%s","%s"]`, libraries.Movies, libraries.Shows, libraries.Music, libraries.Books)
	restriction := fixture.Restriction(libraries)
	narrowed, _ := json.Marshal(restriction.Libraries)
	blocked, _ := json.Marshal(restriction.BlockedLabels)

	// Fixed identities: a golden file cannot depend on a random account id.
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('golden-owner','golden-owner',x'00','golden-owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	owner, err := ident.Issue("golden-owner", "golden-owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	tokens["owner"] = owner.AccessToken
	for _, viewer := range []struct {
		name, account, profile, libraries string
		restricted                        bool
	}{
		{"open", "golden-open", "golden-open-profile", all, false},
		{"restricted", "golden-limited", "golden-limited-profile", string(narrowed), true},
	} {
		if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, viewer.account, viewer.account, viewer.profile); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO direct_memberships(account_id,role,allowed_libraries,revision,disabled) VALUES(?,'member',?,1,0)
 ON CONFLICT(account_id) DO UPDATE SET role='member',allowed_libraries=excluded.allowed_libraries,disabled=0`, viewer.account, all); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position,allowed_libraries) VALUES(?,?,?,1,0,?)
 ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,is_primary=1,deleted=0,allowed_libraries=excluded.allowed_libraries`,
			viewer.profile, viewer.account, viewer.name, viewer.libraries); err != nil {
			t.Fatal(err)
		}
		if viewer.restricted {
			if _, err = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,blocked_labels,allow_unrated,revision)
 VALUES(?,?,?,0,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=excluded.maximum_age,blocked_labels=excluded.blocked_labels,allow_unrated=0`,
				viewer.profile, restriction.MaximumAge, string(blocked)); err != nil {
				t.Fatal(err)
			}
		}
		envelope, issueErr := ident.Issue(viewer.account, viewer.profile, "local", "member", 1)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		tokens[viewer.name] = envelope.AccessToken
	}

	// Stable restricted collection/category reads use a completed policy class;
	// the production worker builds it outside request transactions.
	settleCompactCatalogue(t, db)
	for _, library := range restriction.Libraries {
		if err = cat.RebuildVisibilityClass(context.Background(), library, identity.ContentRestrictions{MaximumAge: &restriction.MaximumAge, BlockUnrated: true, BlockedLabels: restriction.BlockedLabels}); err != nil {
			t.Fatal(err)
		}
	}
	settleCompactCatalogue(t, db)
	facets.Start()
	call := func(method, path, body, viewer string) (int, string) {
		rawRequest := func(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+tokens[viewer])
			if body != "" {
				r.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		var w *httptest.ResponseRecorder
		if isHTTPFixtureFacetPath(method, path) {
			w = readyHTTPFacetFixture(t, path, rawRequest, facets.WaitPrepared)
		} else {
			w = rawRequest(context.Background(), method, path, body)
		}
		return w.Code, w.Body.String()
	}
	rows, err := db.Query(`SELECT pid(public_id),source_key FROM catalog_identities UNION ALL SELECT token,'asset-'||replace(replace(path,'/fixture/',''),'.mp4','') FROM catalog_assets`)
	if err != nil {
		t.Fatal(err)
	}
	var pairs []string
	public := map[string]string{}
	item := ""
	for rows.Next() {
		var public, source string
		if err = rows.Scan(&public, &source); err != nil {
			t.Fatal(err)
		}
		// `file:/fixture/movie-0000000.mp4` and `show:show-000` name what the fixture called them.
		if rest, ok := strings.CutPrefix(source, "file:/fixture/"); ok {
			source = strings.TrimSuffix(rest, ".mp4")
		} else if _, rest, ok := strings.Cut(source, ":"); ok {
			source = rest
		}
		pairs = append(pairs, public, source)
		if source == "movie-0000000" {
			item = public
		}
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(pairs); i += 2 {
		public[pairs[i+1]] = pairs[i]
	}
	if item == "" {
		t.Fatal("the fixture has no movie-0000000")
	}
	goldenNames = strings.NewReplacer(pairs...)
	return &goldenFixture{library: libraries, item: item, db: db, public: public}, call
}

// goldenMatrix is the representative set: every browsable pivot of every library
// kind, every sort the pivot publishes, the aggregate pivots, the paging shapes
// (first page, offset range, seek prefix), the facet fields, search, people,
// detail, the composite pages and every home row — each as an unrestricted and
// as a restricted viewer.
func goldenMatrix(libraries fixture.Libraries, item string) []goldenCase {
	cases := []goldenCase{}
	add := func(name, method, path, body string) {
		for _, viewer := range []string{"open", "restricted"} {
			cases = append(cases, goldenCase{Name: name + "/" + viewer, Method: method, Path: path, Body: body, Viewer: viewer})
		}
	}

	add("home", "GET", "/v1/home", "")
	add("content", "GET", "/v1/content?limit=10", "")
	add("suggestions", "GET", "/v1/suggestions?limit=10", "")
	for _, row := range []string{"continue", "continue_listening", "ondeck", "recommended", "trending",
		"recent_" + libraries.Movies, "recent_" + libraries.Shows, "recent_" + libraries.Music, "recent_" + libraries.Books} {
		add("row:"+row, "GET", "/v1/home/rows/"+row+"?limit=6", "")
		add("row:"+row+":page2", "GET", "/v1/home/rows/"+row+"?limit=6&start=6", "")
	}

	type pivotCase struct{ library, pivot string }
	pivots := []pivotCase{
		{libraries.Movies, "movies"}, {libraries.Movies, "collections"}, {libraries.Movies, "categories"},
		{libraries.Shows, "shows"}, {libraries.Shows, "episodes"}, {libraries.Shows, "collections"}, {libraries.Shows, "categories"},
		{libraries.Music, "artists"}, {libraries.Music, "albums"}, {libraries.Music, "songs"}, {libraries.Music, "genres"},
		{libraries.Books, "authors"}, {libraries.Books, "books"}, {libraries.Books, "collections"}, {libraries.Books, "series"},
	}
	sorts := []struct{ field, direction string }{
		{"title", "asc"}, {"title", "desc"}, {"added", "desc"}, {"year", "desc"},
		{"duration", "desc"}, {"communityRating", "desc"}, {"personalRating", "desc"}, {"lastPlayed", "desc"},
	}
	for _, pivot := range pivots {
		// The name carries the library as well as the pivot: three library kinds
		// publish a `collections` pivot and they are different sets.
		name := "browse:" + pivot.library + ":" + pivot.pivot
		for _, sort := range sorts {
			body := fmt.Sprintf(`{"pivot":%q,"sort":[{"field":%q,"direction":%q}],"limit":8}`, pivot.pivot, sort.field, sort.direction)
			add(name+":"+sort.field+":"+sort.direction, "POST", "/v1/libraries/"+pivot.library+"/browse", body)
		}
		// Paging shapes over the default sort: a direct range (the offset path)
		// and a seek prefix (the letter index path).
		add(name+":range", "POST", "/v1/libraries/"+pivot.library+"/browse",
			fmt.Sprintf(`{"pivot":%q,"limit":8,"range":{"start":5}}`, pivot.pivot))
		add(name+":seek", "POST", "/v1/libraries/"+pivot.library+"/browse",
			fmt.Sprintf(`{"pivot":%q,"limit":8,"seek":{"prefix":"W"}}`, pivot.pivot))
	}
	// Expression predicates, which are what the materialised row must keep
	// answering identically: an entity-scoped field, an item-scoped join, a
	// personal-state join and the availability rollup.
	for _, query := range []struct{ name, expression string }{
		{"genre", `{"variant":"predicate","field":"genre","operator":"contains","value":"Drama"}`},
		{"year", `{"variant":"predicate","field":"year","operator":"at-least","value":2000}`},
		{"playState", `{"variant":"predicate","field":"playState","operator":"equals","value":"unplayed"}`},
		{"availability", `{"variant":"predicate","field":"availability","operator":"equals","value":"available"}`},
		{"contentRating", `{"variant":"predicate","field":"contentRating","operator":"in","value":["G","PG"]}`},
		{"label", `{"variant":"predicate","field":"label","operator":"contains-any","value":["violence"]}`},
		{"title", `{"variant":"predicate","field":"title","operator":"starts-with","value":"W"}`},
	} {
		add("browse:query:"+query.name, "POST", "/v1/libraries/"+libraries.Movies+"/browse",
			fmt.Sprintf(`{"pivot":"movies","query":%s,"limit":8}`, query.expression))
	}

	for _, field := range catalog.BrowseFacetFields() {
		add("facets:"+field, "GET", "/v1/libraries/"+libraries.Movies+"/facets?field="+field, "")
	}
	add("facets:genre:music", "GET", "/v1/libraries/"+libraries.Music+"/facets?field=genre", "")
	add("facets:genre:q", "GET", "/v1/libraries/"+libraries.Movies+"/facets?field=genre&q=ram", "")

	for _, term := range []string{"Word001", "Word002", "Volume", "Release", "Person"} {
		add("search:"+term, "GET", "/v1/search?q="+term+"&limit=8", "")
	}
	add("people", "GET", "/v1/people?q=Person&limit=10", "")
	add("detail", "GET", "/v1/items/"+item+"/detail", "")
	add("item", "GET", "/v1/items/"+item, "")
	add("items", "GET", "/v1/items?libraryId="+libraries.Movies+"&limit=10", "")
	add("libraries", "GET", "/v1/libraries", "")
	for _, view := range []string{"discover", "browse", "collections", "categories"} {
		add("content:"+view, "GET", "/v1/libraries/"+libraries.Movies+"/content?view="+view+"&limit=8", "")
		add("content:music:"+view, "GET", "/v1/libraries/"+libraries.Music+"/content?view="+view+"&limit=8", "")
	}
	add("capabilities", "GET", "/v1/libraries/"+libraries.Movies+"/browse-capabilities?pivot=movies", "")
	return cases
}

// canonical re-encodes a body with map keys sorted and volatile values replaced,
// so two runs of the same catalogue produce byte-identical text.
func canonical(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	encoded, err := json.Marshal(normalise(decoded))
	if err != nil {
		return trimmed
	}
	return string(encoded)
}

const goldenPath = "testdata/readpath_golden.json"

func TestReadPathGoldenEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, call := buildGoldenFixture(t)
	cases := goldenMatrix(f.library, f.item)
	for index := range cases {
		status, body := call(cases[index].Method, cases[index].Path, cases[index].Body, cases[index].Viewer)
		if status == 200 && strings.HasPrefix(cases[index].Name, "browse:") {
			var doc map[string]any
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatal(err)
			}
			if aggregate, ok := doc["wholeSelection"].(map[string]any); ok {
				page, pageOK := doc["pageInfo"].(map[string]any)
				revision, revisionOK := page["revision"].(string)
				if !pageOK || !revisionOK || revision == "" || aggregate["catalogRevision"] != revision {
					t.Errorf("%s: whole selection is not bound to this page's raw revision", cases[index].Name)
				}
			}
		}
		cases[index].Status = status
		if status == 200 && strings.HasPrefix(cases[index].Name, "facets:") {
			var envelope struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(body), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Kind == "simple-keyset" {
				u, err := url.ParseRequestURI(cases[index].Path)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := projectGoldenSimpleFacet([]byte(body), u.Query().Get("field"))
				if err != nil {
					t.Fatalf("%s: %v", cases[index].Name, err)
				}
				body = string(projected)
			}
		}
		cases[index].Response = canonical(body)
	}
	sort.SliceStable(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	seen := map[string]bool{}
	for _, entry := range cases {
		if seen[entry.Name] {
			t.Fatalf("two matrix cases are named %q; a golden file cannot hold both", entry.Name)
		}
		seen[entry.Name] = true
	}

	if os.Getenv("PORTICO_GOLDEN_UPDATE") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.MarshalIndent(cases, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(goldenPath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %d golden cases", len(cases))
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v: record it with PORTICO_GOLDEN_UPDATE=1", err)
	}
	var expected []goldenCase
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	byName := map[string]goldenCase{}
	for _, entry := range expected {
		byName[entry.Name] = entry
	}
	// Four newly counted fields, for both current viewers, have independent
	// fixture-derived expectations instead of a new recorded snapshot.
	for _, c := range cases {
		if entry, ok := goldenNewBrowseFacets(t, f, c); ok {
			byName[c.Name] = entry
		}
	}
	if len(byName) != len(cases) {
		t.Errorf("the matrix has %d cases and the golden file has %d", len(cases), len(byName))
	}
	differences := 0
	for _, actual := range cases {
		want, known := byName[actual.Name]
		if !known {
			t.Errorf("%s is not in the golden file", actual.Name)
			continue
		}
		// GP-08 adds library summaries. Spell out the deterministic Tiny fixture
		// facts independently; keep the recorded library membership/order and
		// all pre-existing fields exact, including the narrowed viewer scope.
		if strings.HasPrefix(actual.Name, "libraries/") {
			var rewritten map[string]any
			if err := json.Unmarshal([]byte(want.Response), &rewritten); err != nil {
				t.Fatal(err)
			}
			for _, raw := range rewritten["items"].([]any) {
				library := raw.(map[string]any)
				summary := catalog.LibrarySummary{CoverURLs: []string{}, EmptyKnown: true}
				switch library["kind"] {
				case "movie":
					count, covers := 40, []int{39, 38, 37}
					if actual.Viewer == "restricted" {
						count, covers = 13, []int{38, 37, 35}
					}
					summary.Counts = []catalog.LibraryKindCount{{Kind: "movie", Count: count}}
					for _, index := range covers {
						summary.CoverURLs = append(summary.CoverURLs, fmt.Sprintf("/v1/items/movie-%07d/art/poster", index))
					}
					summary.LatestArtwork = fmt.Sprintf("/v1/items/movie-%07d/art/backdrop", covers[0])
				case "tv":
					shows, episodes := 2, 6
					if actual.Viewer == "restricted" {
						shows, episodes = 1, 1
					}
					summary.Counts = []catalog.LibraryKindCount{{Kind: "show", Count: shows}, {Kind: "episode", Count: episodes}}
				case "music":
					summary.Counts = []catalog.LibraryKindCount{{Kind: "artist", Count: 2}, {Kind: "album", Count: 2}, {Kind: "song", Count: 6}}
				case "audiobook":
					summary.Counts = []catalog.LibraryKindCount{{Kind: "book", Count: 2}, {Kind: "author", Count: 2}}
				default:
					t.Fatalf("%s: specify the summary for golden library kind %v", actual.Name, library["kind"])
				}
				library["summary"] = summary
			}
			encoded, err := json.Marshal(rewritten)
			if err != nil {
				t.Fatal(err)
			}
			want.Response = canonical(string(encoded))
		}
		// DP-2/DP-11 intentionally replace on-page files with language and
		// visible collection summaries. Keep the rest of the recorded detail
		// exact, including membership, actions and all personal state.
		if strings.HasPrefix(actual.Name, "detail/") {
			var rewritten map[string]any
			if err := json.Unmarshal([]byte(want.Response), &rewritten); err != nil {
				t.Fatal(err)
			}
			delete(rewritten, "files")
			delete(rewritten["item"].(map[string]any), "sources")
			rewritten["languages"] = map[string]any{"audio": []string{}, "subtitles": []string{}}
			members, yearTo := 5, 1954
			posters := []string{"/v1/items/movie-0000000/art/poster", "/v1/items/movie-0000001/art/poster", "/v1/items/movie-0000002/art/poster", "/v1/items/movie-0000003/art/poster"}
			if actual.Viewer == "restricted" {
				members, yearTo = 2, 1953
				posters = []string{posters[0], posters[3]}
			}
			rewritten["collections"] = []any{map[string]any{"id": "collection 00000", "name": "Collection 00000", "memberCount": members, "position": 1, "yearFrom": 1950, "yearTo": yearTo, "posterUrls": posters}}
			encoded, _ := json.Marshal(rewritten)
			want.Response = string(encoded)
		}
		// Home's approved composition replaces the old per-library/single-hero policy.
		// Keep unrelated read-path goldens exact; assert the new manifest and retired routes explicitly.
		if strings.HasPrefix(actual.Name, "home/") {
			if actual.Status != 200 {
				t.Errorf("%s: %d", actual.Name, actual.Status)
				continue
			}
			var doc catalog.HomeDocument
			if err := json.Unmarshal([]byte(actual.Response), &doc); err != nil {
				t.Fatal(err)
			}
			// Home's default rows (mock-up frame 23): the personal and trending rows show by default.
			wantIDs := []string{"continue", "new_on_server", "because_you_watched", "featured_collection", "listen_next", "continue_books", "recommended", "for_you", "trending_now"}
			ids := []string{}
			for _, row := range doc.Rows {
				ids = append(ids, row.ID)
				if !row.Deferred || len(row.Entries) != 0 || row.SeeAll == nil {
					t.Errorf("%s: invalid descriptor %+v", actual.Name, row)
				}
			}
			if !reflect.DeepEqual(ids, wantIDs) || len(doc.Spotlight) != 5 {
				t.Errorf("%s: Home composition rows %v spotlight %d", actual.Name, ids, len(doc.Spotlight))
			}
			if strings.Contains(actual.Response, `"hero"`) || doc.Rows[0].ArtworkShape != "landscape" {
				t.Errorf("%s: retired Home policy survived", actual.Name)
			}
			for _, slide := range doc.Spotlight {
				if slide.Entry.ID == "" || slide.Reason.Text.Code == "" || len([]rune(slide.Entry.Overview)) > 300 {
					t.Errorf("%s: invalid spotlight %+v", actual.Name, slide)
				}
			}
			continue
		}
		if strings.HasPrefix(actual.Name, "row:") {
			rowID := strings.Split(strings.TrimPrefix(actual.Name, "row:"), "/")[0]
			rowID = strings.TrimSuffix(rowID, ":page2")
			if rowID != "continue" && rowID != "recommended" {
				if actual.Status != 404 || !strings.Contains(actual.Response, `"code":"not_found"`) {
					t.Errorf("%s: retired route answered %d %s", actual.Name, actual.Status, actual.Response)
				}
				continue
			}
			if rowID == "recommended" {
				var rewritten map[string]any
				if err := json.Unmarshal([]byte(want.Response), &rewritten); err != nil {
					t.Fatal(err)
				}
				rewritten["priority"] = 90
				// RH-7/collections f46: this fixture has no activity for the golden
				// viewer, so the explanation must not invent seed titles.
				rewritten["why"] = map[string]any{"code": "next.home.whyRecommended", "fallback": "Chosen from your viewing activity and available libraries."}
				// Home now projects command offers onto each returned title. Keep
				// the recorded membership, order, counts, paging and entry facts
				// exact; add only independently specified offers for this member
				// fixture (no bulk scheduler, owner tools or personal state).
				for _, raw := range rewritten["entries"].([]any) {
					entry := raw.(map[string]any)
					actions := catalog.HomeActions{
						Play: true, Queue: true, MyList: true, Favorite: true, Watched: true, Details: true,
						Selection: catalog.HomeSelectionActions{Play: true, Queue: true},
					}
					switch entry["kind"] {
					case "movie":
						actions.Playlist, actions.Collection, actions.Rating = true, true, true
						// Not-interested is a supported personal command for a movie.
						actions.NotInterested = true
					case "show":
						// The restricted page-two fixture returns show-000000 itself,
						// not a season/episode redirect. Its existing personal command
						// supports Not interested on that exact displayed show.
						actions.NotInterested = true
						actions.Selection.Favorite, actions.Selection.Watched = true, true
					default:
						t.Fatalf("%s: specify the member offers for golden kind %v", actual.Name, entry["kind"])
					}
					entry["homeActions"] = actions
				}
				encoded, _ := json.Marshal(rewritten)
				want.Response = canonical(string(encoded))
			}
			if rowID == "continue" {
				var rewritten map[string]any
				if err := json.Unmarshal([]byte(want.Response), &rewritten); err != nil {
					t.Fatal(err)
				}
				rewritten["artworkShape"] = "landscape"
				rewritten["titleText"] = map[string]any{"code": "next.home.continueWatching", "fallback": "Continue Watching"}
				encoded, _ := json.Marshal(rewritten)
				want.Response = string(encoded)
			}
		}
		if strings.HasPrefix(actual.Name, "browse:") {
			want.Response = goldenHomeCoverDecoration(t, f, actual, want.Response)
		}
		want.Response = goldenSearchReadingGroup(t, f, actual, want.Response)
		want.Response = goldenSearchOffers(t, f, actual, want.Response)
		want.Response = goldenUnselectedDetailArtwork(t, f, actual, want.Response)
		want.Response = goldenBrowseExtension(t, f, actual, want.Response)
		want.Response = goldenListeningExtension(t, f, actual, want.Response)
		// Both extensions run after the ones that compose the page, so a
		// generated section carries the same published card facts.
		want.Response = goldenAggregateCardFacts(t, f, actual, want.Response)
		want.Response = goldenSearchEpisodeFacts(t, f, actual, want.Response)
		// Offers decorate every container the pages above composed.
		want.Response = goldenBrowseOffers(t, f, actual, want.Response)
		if want.Status != actual.Status {
			differences++
			t.Errorf("%s: status %d, golden %d\n  body: %s", actual.Name, actual.Status, want.Status, truncate(actual.Response))
			continue
		}
		if want.Response != actual.Response {
			differences++
			// Name every changed case with its changed paths, not only the
			// first few: the bodies stay a bounded preview, the diff does not.
			t.Logf("%s: changed JSON paths: %s", actual.Name, goldenChangedPaths(actual.Response, want.Response))
			if differences <= 8 {
				t.Errorf("%s: the answer changed\n  now:    %s\n  golden: %s", actual.Name, truncate(actual.Response), truncate(want.Response))
			} else {
				t.Errorf("%s: the answer changed", actual.Name)
			}
		}
	}
	if differences > 8 {
		t.Errorf("%d cases differ in total", differences)
	}
}

// Report the differing fields even when long entry arrays exhaust truncate's
// preview before the changed field. This does not change the exact comparison.
func goldenChangedPaths(actual, expected string) string {
	var a, b any
	if json.Unmarshal([]byte(actual), &a) != nil || json.Unmarshal([]byte(expected), &b) != nil {
		return "invalid JSON"
	}
	var changes []string
	var walk func(any, any, string)
	walk = func(a, b any, path string) {
		if len(changes) >= 24 || reflect.DeepEqual(a, b) {
			return
		}
		if am, ok := a.(map[string]any); ok {
			if bm, ok := b.(map[string]any); ok {
				keys := map[string]bool{}
				for k := range am {
					keys[k] = true
				}
				for k := range bm {
					keys[k] = true
				}
				order := []string{}
				for k := range keys {
					order = append(order, k)
				}
				sort.Strings(order)
				for _, k := range order {
					av, aok := am[k]
					bv, bok := bm[k]
					if !aok || !bok {
						changes = append(changes, path+"."+k+" presence changed")
					} else {
						walk(av, bv, path+"."+k)
					}
				}
				return
			}
		}
		if aa, ok := a.([]any); ok {
			if ba, ok := b.([]any); ok && len(aa) == len(ba) {
				for i := range aa {
					walk(aa[i], ba[i], fmt.Sprintf("%s[%d]", path, i))
				}
				return
			}
		}
		changes = append(changes, fmt.Sprintf("%s now=%v golden=%v", path, a, b))
	}
	walk(a, b, "$")
	return strings.Join(changes, "; ")
}

func truncate(text string) string {
	if len(text) <= 900 {
		return text
	}
	return text[:900] + "…"
}

// stablePublicIDs replaces the fixture's random public ids with ones derived
// from each entity's source key. Recommendations rotate ties by a hash of the
// public id (a fresh order per install), so only fixed ids give a fixed answer.
func stablePublicIDs(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT public_id,root,source_key FROM catalog_identities`)
	if err != nil {
		t.Fatal(err)
	}
	type change struct{ from, to []byte }
	var changes []change
	for rows.Next() {
		var public []byte
		var root, key string
		if err = rows.Scan(&public, &root, &key); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(root + "\x00" + key))
		changes = append(changes, change{public, sum[:16]})
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		for _, statement := range []string{`UPDATE catalog_entities SET public_id=?2 WHERE public_id=?1`, `UPDATE catalog_identities SET public_id=?2 WHERE public_id=?1`} {
			if _, err = db.Exec(statement, c.from, c.to); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// CB-14/15/16 in design d610 intentionally add bounded cover samples and
// collection facts. Keep the recorded golden file unchanged and extend its
// expected answer from the independently seeded Tiny fixture, never from the
// actual response or the production enrichment function.
func goldenHomeCoverDecoration(t *testing.T, f *goldenFixture, actual goldenCase, want string) string {
	t.Helper()
	var body map[string]any
	if json.Unmarshal([]byte(want), &body) != nil {
		return want
	}
	raw, ok := body["entries"].([]any)
	if !ok {
		return want
	}
	restriction := fixture.Restriction(f.library)
	viewer := catalog.Viewer{Profile: "golden-open-profile", Libraries: []string{f.library.Movies, f.library.Shows, f.library.Music, f.library.Books}}
	if strings.HasSuffix(actual.Name, "/restricted") {
		viewer.Libraries = restriction.Libraries
		viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &restriction.MaximumAge, BlockUnrated: true, BlockedLabels: restriction.BlockedLabels}
	}
	permitted := func(key string) bool {
		public := f.public[key]
		return public != "" && catalog.New(f.db).VisibleItem(context.Background(), viewer, public) == nil
	}
	cover := func(key string) string { return "/v1/items/" + key + "/art/poster" }
	for _, value := range raw {
		entry := value.(map[string]any)
		kind, _ := entry["kind"].(string)
		id, _ := entry["id"].(string)
		paths := []string{}
		switch kind {
		case "artist":
			for i := 0; i < fixture.Tiny().SongsPerAlbum; i++ {
				key := fmt.Sprintf("%s-al000-t%03d", id, i)
				if permitted(key) {
					paths = append(paths, cover(key))
					break
				}
			}
		case "author":
			var index int
			if _, err := fmt.Sscanf(id, "author %d", &index); err != nil {
				t.Fatal(id, err)
			}
			for i := 0; i < fixture.Tiny().FilesPerBook; i++ {
				key := fmt.Sprintf("book-%06d-p%03d", index, i)
				if permitted(key) {
					paths = append(paths, cover(key))
					break
				}
			}
		case "collection":
			var index int
			if _, err := fmt.Sscanf(entry["title"].(string), "Collection %d", &index); err != nil {
				t.Fatal(id, err)
			}
			first, last := 0, 0
			duration := float64(0)
			backdrop := ""
			for i := 0; i < fixture.Tiny().ItemsPerCollection; i++ {
				key := fmt.Sprintf("movie-%07d", index*fixture.Tiny().ItemsPerCollection+i)
				if !permitted(key) {
					continue
				}
				var year int
				var seconds float64
				if err := f.db.QueryRow(`SELECT e.year,m.duration FROM catalog_entities e JOIN catalog_browse_item_metrics m ON m.item_id=e.id WHERE e.public_id=pid_blob(?)`, f.public[key]).Scan(&year, &seconds); err != nil {
					t.Fatal(err)
				}
				if first == 0 || year < first {
					first = year
				}
				if year > last {
					last = year
				}
				duration += seconds
				if len(paths) < 4 {
					paths = append(paths, cover(key))
				}
				if backdrop == "" {
					backdrop = "/v1/items/" + key + "/art/backdrop"
				}
			}
			if first > 0 {
				entry["yearMin"] = first
			}
			if last > 0 {
				entry["yearMax"] = last
			}
			entry["durationMs"] = int64(duration * 1000)
			entry["watchedCount"] = 0
			entry["origin"] = "owner"
			if entry["backdropUrl"] == nil || entry["backdropUrl"] == "" {
				if backdrop != "" {
					entry["backdropUrl"] = backdrop
				}
			}
		default:
			continue
		}
		if len(paths) > 0 {
			entry["artworkPaths"] = paths
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// 209d71b9 deliberately changed detail to advertise only immutable, ready
// selected artwork (held by catalog.TestDetailArtworkUsesReadySelectedDescriptors).
// The original frozen fixture has local hints only. Account for exactly those
// two obsolete URLs, with an independent producer-fact guard; never re-record
// the matrix or copy an observed answer into its expected value.
func goldenUnselectedDetailArtwork(t *testing.T, f *goldenFixture, actual goldenCase, frozen string) string {
	t.Helper()
	if actual.Name != "detail/open" && actual.Name != "detail/restricted" {
		return frozen
	}
	var poster, backdrop string
	var selected int
	err := f.db.QueryRow(`SELECT d.poster_url,d.backdrop_url,(SELECT count(*) FROM artwork_selections a WHERE a.kind='item' AND a.entity_id=e.id) FROM catalog_entities e JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`, f.item).Scan(&poster, &backdrop, &selected)
	if err != nil || poster != "local:poster/movie-0000000" || backdrop != "local:backdrop/movie-0000000" || selected != 0 {
		t.Fatalf("review changed golden artwork producer facts: poster=%q backdrop=%q selected=%d err=%v", poster, backdrop, selected, err)
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(frozen), &document); err != nil {
		t.Fatal(err)
	}
	item, ok := document["item"].(map[string]any)
	if !ok || item["id"] != "movie-0000000" || item["posterUrl"] != "/v1/items/movie-0000000/art/poster" || item["backdropUrl"] != "/v1/items/movie-0000000/art/backdrop" {
		t.Fatal("review changed frozen detail artwork expectation")
	}
	delete(item, "posterUrl")
	delete(item, "backdropUrl")
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// fb1946430 publishes an admitted episode's season and episode numbers (and
// their numbering form) on indexed Search results. Derive those from the
// fixture's own episode rows: the recorded group membership, order, counts and
// every other field stay exact, and a recorded number the fixture contradicts
// is an error rather than something to overwrite.
type goldenEpisodeFacts struct {
	season, episode *int
	numbering       string
}

func goldenSearchEpisodeFacts(t *testing.T, f *goldenFixture, c goldenCase, want string) string {
	t.Helper()
	if c.Status != 200 || !strings.HasPrefix(c.Name, "search:") {
		return want
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(want), &doc); err != nil {
		t.Fatal(err)
	}
	groups, ok := doc["groups"].([]any)
	if !ok {
		return want
	}
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		items, ok := group["items"].([]any)
		if !ok {
			continue
		}
		for _, rawItem := range items {
			entry, ok := rawItem.(map[string]any)
			if !ok {
				continue
			}
			if kind, _ := entry["kind"].(string); kind != "episode" {
				continue
			}
			id, _ := entry["id"].(string)
			public := f.public[id]
			if public == "" {
				t.Fatalf("%s: %q is not a fixture identity", c.Name, id)
			}
			var season, episode sql.NullInt64
			var numbering string
			err := f.db.QueryRow(`SELECT s.number,ep.number,COALESCE(ep.numbering,'')
 FROM catalog_episodes ep JOIN catalog_entities e ON e.id=ep.entity_id
 JOIN catalog_entities show ON show.id=ep.show_id AND show.library_id=e.library_id
 LEFT JOIN catalog_seasons s ON s.entity_id=ep.season_id AND s.show_id=ep.show_id
 WHERE e.public_id=pid_blob(?)`, public).Scan(&season, &episode, &numbering)
			if err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
			facts := goldenEpisodeFacts{numbering: numbering}
			if season.Valid {
				number := int(season.Int64)
				facts.season = &number
			}
			if episode.Valid {
				number := int(episode.Int64)
				facts.episode = &number
			}
			if err := applySearchEpisodeFacts(entry, facts); err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(encoded))
}

// applySearchEpisodeFacts states exactly the episode facts the fixture holds.
// A fact the fixture lacks stays absent: ContentEntry publishes special and
// dateBased as omitempty booleans and the numbers as omitempty pointers, so a
// recorded value there contradicts the answer the server can produce, null
// included. A non-episode entry is untouched.
func applySearchEpisodeFacts(entry map[string]any, facts goldenEpisodeFacts) error {
	if kind, _ := entry["kind"].(string); kind != "episode" {
		return nil
	}
	special := facts.season != nil && *facts.season == 0
	absolute := (*int)(nil)
	if facts.episode != nil && facts.numbering == "absolute" {
		absolute = facts.episode
	}
	for _, derived := range []struct {
		key  string
		want *int
	}{
		{"seasonNumber", facts.season},
		{"episodeNumber", facts.episode},
		{"absoluteNumber", absolute},
	} {
		if err := setGoldenDerivedNumber(entry, derived.key, derived.want); err != nil {
			return err
		}
	}
	for _, derived := range []struct {
		key  string
		want bool
	}{
		{"special", special},
		{"dateBased", facts.numbering == "date"},
	} {
		if err := setGoldenDerivedBool(entry, derived.key, derived.want); err != nil {
			return err
		}
	}
	return nil
}

func setGoldenDerivedNumber(entry map[string]any, key string, want *int) error {
	existing, present := entry[key]
	if want == nil {
		if present {
			return fmt.Errorf("%s is %v but the fixture holds no such fact", key, existing)
		}
		return nil
	}
	if present {
		switch number := existing.(type) {
		case float64:
			if number != float64(*want) {
				return fmt.Errorf("%s is %v, not the fixture's %d", key, existing, *want)
			}
		case int:
			if number != *want {
				return fmt.Errorf("%s is %v, not the fixture's %d", key, existing, *want)
			}
		default:
			return fmt.Errorf("%s is a malformed %v", key, existing)
		}
		return nil
	}
	entry[key] = *want
	return nil
}

func setGoldenDerivedBool(entry map[string]any, key string, want bool) error {
	existing, present := entry[key]
	if want {
		if present {
			value, ok := existing.(bool)
			if !ok || !value {
				return fmt.Errorf("%s is %v, not the fixture's true", key, existing)
			}
			return nil
		}
		entry[key] = true
		return nil
	}
	// ContentEntry publishes special and dateBased as omitempty booleans
	// (content_types.go), so the false fact is an absent field and never a
	// recorded value: an explicit false, a null, a non-bool or a true the
	// fixture contradicts are all changes to specify, as is any other shape.
	if present {
		return fmt.Errorf("%s is %v but the fixture publishes no such fact", key, existing)
	}
	return nil
}

// The extension is bounded: it fills absent facts, accepts the facts it
// derives, and refuses anything the fixture contradicts. These controls run
// without a catalogue.
func TestGoldenSearchEpisodeFactsBoundaries(t *testing.T) {
	number := func(value int) *int { return &value }
	episode := func() map[string]any { return map[string]any{"id": "show-000000-s01-e001", "kind": "episode"} }
	entry := episode()
	if err := applySearchEpisodeFacts(entry, goldenEpisodeFacts{season: number(1), episode: number(1), numbering: "seasonal"}); err != nil {
		t.Fatalf("absent facts: %v", err)
	}
	if entry["seasonNumber"] != 1 || entry["episodeNumber"] != 1 {
		t.Fatalf("facts were not published: %v", entry)
	}
	for _, absent := range []string{"absoluteNumber", "special", "dateBased"} {
		if _, present := entry[absent]; present {
			t.Fatalf("%s was invented: %v", absent, entry)
		}
	}
	recorded := episode()
	recorded["seasonNumber"], recorded["episodeNumber"] = float64(1), float64(1)
	if err := applySearchEpisodeFacts(recorded, goldenEpisodeFacts{season: number(1), episode: number(1)}); err != nil || recorded["seasonNumber"] != float64(1) {
		t.Fatalf("agreeing facts: %v %v", err, recorded["seasonNumber"])
	}
	conflict := episode()
	conflict["seasonNumber"] = float64(2)
	if err := applySearchEpisodeFacts(conflict, goldenEpisodeFacts{season: number(1), episode: number(1)}); err == nil {
		t.Fatal("conflicting season number was accepted")
	}
	invented := episode()
	invented["episodeNumber"] = float64(1)
	if err := applySearchEpisodeFacts(invented, goldenEpisodeFacts{numbering: "date"}); err == nil {
		t.Fatal("a recorded fact the fixture lacks was accepted")
	}
	null := episode()
	null["seasonNumber"] = nil
	if err := applySearchEpisodeFacts(null, goldenEpisodeFacts{numbering: "date"}); err == nil {
		t.Fatal("an explicitly null season number was accepted")
	}
	malformed := episode()
	malformed["seasonNumber"] = "1"
	if err := applySearchEpisodeFacts(malformed, goldenEpisodeFacts{season: number(1)}); err == nil {
		t.Fatal("malformed season number was accepted")
	}
	special := episode()
	if err := applySearchEpisodeFacts(special, goldenEpisodeFacts{season: number(0), episode: number(1)}); err != nil || special["special"] != true {
		t.Fatalf("special card: %v %v", err, special["special"])
	}
	absolute := episode()
	if err := applySearchEpisodeFacts(absolute, goldenEpisodeFacts{season: number(1), episode: number(1043), numbering: "absolute"}); err != nil {
		t.Fatalf("absolute numbering: %v", err)
	}
	if absolute["absoluteNumber"] != 1043 {
		t.Fatalf("absolute numbering was not published: %v", absolute)
	}
	date := episode()
	if err := applySearchEpisodeFacts(date, goldenEpisodeFacts{season: number(2023), episode: number(14), numbering: "date"}); err != nil || date["dateBased"] != true {
		t.Fatalf("date numbering: %v %v", err, date["dateBased"])
	}
	// special and dateBased are omitempty: the false fact is absent from the
	// published entry, so every recorded shape of it is a change to specify.
	for _, recorded := range []struct {
		name  string
		value any
	}{
		{"explicit false", false},
		{"null", nil},
		{"true", true},
		{"non-bool", 0},
		{"string", "false"},
	} {
		special := episode()
		special["special"] = recorded.value
		if err := applySearchEpisodeFacts(special, goldenEpisodeFacts{season: number(1)}); err == nil {
			t.Fatalf("a recorded %s special was accepted when the fixture publishes none", recorded.name)
		}
		dateBased := episode()
		dateBased["dateBased"] = recorded.value
		if err := applySearchEpisodeFacts(dateBased, goldenEpisodeFacts{numbering: "seasonal"}); err == nil {
			t.Fatalf("a recorded %s dateBased was accepted when the fixture publishes none", recorded.name)
		}
	}
	keptAbsent := episode()
	if err := applySearchEpisodeFacts(keptAbsent, goldenEpisodeFacts{season: number(1), episode: number(1), numbering: "seasonal"}); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"special", "dateBased", "absoluteNumber"} {
		if _, present := keptAbsent[absent]; present {
			t.Fatalf("a false/absent %s was recorded: %v", absent, keptAbsent)
		}
	}
	// A true fact the fixture holds is the only accepted recorded truth.
	trueSpecial := episode()
	trueSpecial["special"] = true
	if err := applySearchEpisodeFacts(trueSpecial, goldenEpisodeFacts{season: number(0), episode: number(1)}); err != nil || trueSpecial["special"] != true {
		t.Fatalf("agreeing special: %v %v", err, trueSpecial["special"])
	}
	nullTrue := episode()
	nullTrue["dateBased"] = nil
	if err := applySearchEpisodeFacts(nullTrue, goldenEpisodeFacts{numbering: "date"}); err == nil {
		t.Fatal("a null dateBased was accepted where the fixture publishes true")
	}
	// Ground the absent-false rule in the published schema rather than in an
	// observation of the current answer: both fields are omitempty booleans, so
	// marshalling a false one publishes no key at all.
	falseJSON, err := json.Marshal(catalog.ContentEntry{ID: "show-000000-s01-e001", Kind: "episode"})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"special":`, `"dateBased":`} {
		if strings.Contains(string(falseJSON), absent) {
			t.Fatalf("ContentEntry no longer omits a false %s: %s", absent, falseJSON)
		}
	}
	if err := applySearchEpisodeFacts(map[string]any{"id": "movie-0000000", "kind": "movie"}, goldenEpisodeFacts{season: number(1)}); err != nil {
		t.Fatal("a title result must be left alone:", err)
	}
}
