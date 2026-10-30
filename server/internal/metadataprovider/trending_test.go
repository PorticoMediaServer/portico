package metadataprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Trend feeds are documented provider endpoints; these tests pin the request
// shape, the persisted identity typing, the lean decode, adult filtering and
// the failure classes through the same paced transports production uses.
func trendingOrigin(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	transport := newTransport("fixture", server.URL, "Portico/test (https://getportico.tv)")
	transport.interval = 0
	t.Cleanup(transport.client.CloseIdleConnections)
	// The adapter constructors build their own paced transports, so the tests
	// point the whole adapter at the origin instead: pacing, credential header
	// and error classification are the production ones.
	return server.URL
}

func TestTMDBTrendingMovieTVAndLeanRows(t *testing.T) {
	var routes []string
	tmdb, _ := NewTMDBAt("fixture-secret", trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("credential missing")
		}
		routes = append(routes, r.URL.RequestURI())
		switch {
		case strings.HasPrefix(r.URL.Path, "/trending/movie/"):
			if r.URL.Query().Get("page") != "1" {
				w.WriteHeader(404)
				return
			}
			// Oversized descriptive payload is fine: only identities decode.
			fmt.Fprint(w, `{"results":[`+
				`{"id":42,"title":"Movie","adult":false,"overview":"Description"},`+
				`{"id":43,"adult":true},`+
				`{"id":0,"adult":false},`+
				`{"id":44}]}`)
		case strings.HasPrefix(r.URL.Path, "/trending/tv/"):
			fmt.Fprint(w, `{"results":[{"id":7,"name":"Series","adult":false}]}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	ctx := context.Background()
	movies, e := tmdb.Trending(ctx, "movie", "en-US", 1)
	if e != nil || len(movies) != 2 || movies[0].ID != "42" || movies[0].Type != "movie" || movies[1].ID != "44" {
		t.Fatal(movies, e)
	}
	if movies[0].Adult || movies[1].Adult {
		t.Fatal("adult entries must not survive a trend snapshot")
	}
	shows, e := tmdb.Trending(ctx, "tv", "", 1)
	if e != nil || len(shows) != 1 || shows[0].ID != "7" || shows[0].Type != "show" {
		t.Fatal(shows, e)
	}
	// The transport kind and the persisted media_kind are distinct things: the
	// route uses TMDB's documented path, the entry type uses the provider's
	// entity typing, and the caller persists the discovery kind.
	if len(routes) != 2 || !strings.HasPrefix(routes[0], "/trending/movie/day") || !strings.Contains(routes[1], "/trending/tv/day") {
		t.Fatal(routes)
	}
	if _, e := tmdb.Trending(ctx, "tv", "", 4); e == nil {
		t.Fatal("unbounded page budget accepted")
	}
	if _, e := tmdb.Trending(ctx, "anime", "", 1); e == nil {
		t.Fatal("unsupported kind accepted as a documented feed")
	}
	if kind, ok := TrendingType("tv"); !ok || kind != "show" {
		t.Fatal("discovery tv kind must map to the provider show typing")
	}
}

func TestTMDBTrendingRateLimitAndOffline(t *testing.T) {
	calls := 0
	tmdb, _ := NewTMDBAt("fixture-secret", trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(500)
	}))
	ctx := context.Background()
	_, e := tmdb.Trending(ctx, "movie", "", 1)
	pe, ok := e.(*Error)
	if !ok || pe.Status != 429 || !pe.Retryable() || pe.RetryAfter != 90*time.Second {
		t.Fatalf("Retry-After not classified: %v", e)
	}
	// The shared transport defers the whole feed after a 429, exactly as item
	// matching would be deferred: the retry lands in the cooldown, not on the
	// provider.
	deferCtx, deferCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, deferErr := tmdb.Trending(deferCtx, "movie", "", 1)
	deferCancel()
	if deferErr == nil || !errors.Is(deferErr, context.DeadlineExceeded) {
		t.Fatalf("the deferred retry ran instead of waiting out Retry-After: %v", deferErr)
	}
	if calls != 1 {
		t.Fatalf("a deferred feed issued %d requests", calls)
	}
}

func TestTMDBTrendingMalformedResponse(t *testing.T) {
	tmdb, _ := NewTMDBAt("fixture-secret", trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":"not-a-list"}`)
	}))
	if _, e := tmdb.Trending(context.Background(), "movie", "", 1); e == nil {
		t.Fatal("malformed feed accepted")
	}
}

func TestAniListTrendingGraphQLAndLeanRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	requests := []map[string]any{}
	anilist := NewAniListAt(trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("not GraphQL POST")
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		requests = append(requests, body)
		query, _ := body["query"].(string)
		// Primary-schema shape: documented Page.media arguments and trend sort.
		for _, want := range []string{"Page(page:$page,perPage:25)", "media(type:ANIME,sort:TRENDING_DESC,isAdult:false)"} {
			if !strings.Contains(query, want) {
				t.Errorf("trending query missing %q", want)
			}
		}
		page := 1
		if vars, ok := body["variables"].(map[string]any); ok {
			if v, ok := vars["page"].(float64); ok {
				page = int(v)
			}
		}
		if page == 2 {
			fmt.Fprint(w, `{"data":{"Page":{"media":[]}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"Page":{"media":[`+
			`{"id":11,"type":"ANIME","isAdult":false},`+
			`{"id":12,"type":"ANIME","isAdult":true},`+
			`{"id":0,"type":"ANIME"},`+
			`{"id":13,"type":"MANGA","isAdult":false}]}}}`)
	}))
	ctx := context.Background()
	rows, e := anilist.Trending(ctx, "en-US", 2)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].Provider != "anilist" || rows[0].Type != "anime" || rows[0].ID != "11" || rows[0].Adult {
		t.Fatal(rows)
	}
	if len(requests) != 2 {
		t.Fatalf("page budget produced %d GraphQL requests", len(requests))
	}
}

func TestScreenTMDBKeywordsDeduplicatedAndCapped(t *testing.T) {
	keywords := make([]map[string]any, 0, 80)
	for i := 0; i < 80; i++ {
		keywords = append(keywords, map[string]any{"id": i%40 + 1, "name": fmt.Sprintf("Keyword %d", i%40)})
	}
	payload, _ := json.Marshal(map[string]any{"keywords": keywords})
	tmdb, _ := NewTMDBAt("fixture-secret", trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("append_to_response"), "keywords") {
			t.Error("keywords not requested with the detail document")
		}
		fmt.Fprintf(w, `{"id":42,"title":"Film","release_date":"2020-01-02",%s:%s}`, `"keywords"`, payload)
	}))
	r, e := tmdb.ScreenDetails(context.Background(), "movie", "42", "en-US", "US")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Tags) != 40 {
		t.Fatalf("keywords did not deduplicate to the typed cap: %d", len(r.Tags))
	}
	for i, tag := range r.Tags {
		if tag.ID == "" || tag.Name == "" {
			t.Fatalf("untyped keyword row %d: %#v", i, tag)
		}
	}
}

func TestScreenAniListThemesTypedAndNonSpoiler(t *testing.T) {
	anilist := NewAniListAt(trendingOrigin(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "tags { id name rank isGeneralSpoiler isMediaSpoiler isAdult }") {
			t.Error("theme fields missing from the anime document query")
		}
		fmt.Fprint(w, `{"data":{"Media":{"id":1,"type":"ANIME","title":{"romaji":"Anime"},"tags":[`+
			`{"id":1,"name":"Space","rank":60},`+
			`{"id":2,"name":"Spoiled Theme","rank":90,"isGeneralSpoiler":true},`+
			`{"id":3,"name":"Episode Spoiler","rank":80,"isMediaSpoiler":true},`+
			`{"id":4,"name":"Adult Theme","rank":85,"isAdult":true},`+
			`{"id":5,"name":"Whispered","rank":5},`+
			`{"id":1,"name":"Space","rank":55}]}}}`)
	}))
	r, e := anilist.ScreenDetails(context.Background(), "anime", "1", "en-US", "")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Tags) != 1 || r.Tags[0].Name != "Space" || r.Tags[0].ID != "1" {
		t.Fatalf("theme projection not typed, ranked and non-spoiler: %#v", r.Tags)
	}
}
