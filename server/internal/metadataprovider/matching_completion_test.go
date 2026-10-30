package metadataprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTMDBAbsoluteCoordinatesAndSeriesAggregateCredits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/5":
			fmt.Fprint(w, `{"id":5,"name":"Anime","seasons":[{"season_number":0,"episode_count":3},{"season_number":1,"episode_count":12},{"season_number":2,"episode_count":10}],"aggregate_credits":{"cast":[{"id":9,"name":"Earlier cast member","roles":[{"character":"Lead"}]}]}}`)
		case "/tv/5/season/2/episode/2":
			fmt.Fprint(w, `{"id":14,"name":"Episode Fourteen","season_number":2,"episode_number":2,"still_path":"/still.jpg"}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p, e := NewTMDBAt("fixture", server.URL)
	if e != nil {
		t.Fatal(e)
	}
	p.http.interval = 0
	show, e := p.ScreenDetails(context.Background(), "show", "5", "en", "")
	if e != nil || len(show.Credits) != 1 || show.Credits[0].Name != "Earlier cast member" {
		t.Fatal(show, e)
	}
	episode, e := p.ScreenAbsoluteEpisode(context.Background(), "5", 14, "official", "en")
	if e != nil || episode.Coordinates == nil || episode.Coordinates.Season != nil || *episode.Coordinates.Absolute != 14 || *episode.Coordinates.OriginalSeason != 2 || *episode.Coordinates.OriginalEpisode != 2 || episode.Title != "Episode Fourteen" {
		t.Fatal(episode, e)
	}
}
func TestOptionalProviderListsAreTrimmedPerEntry(t *testing.T) {
	r := ScreenRecord{Identity: ScreenID{"tmdb", "movie", "1"}, Title: "Film"}
	for i := 0; i < 300; i++ {
		r.Aliases = append(r.Aliases, fmt.Sprintf("Title %d", i))
	}
	r.Genres = []ScreenName{{"1", "Drama"}, {"2", string([]byte{0})}}
	r = BoundScreenRecord(r)
	if len(r.Aliases) != 256 || len(r.Genres) != 1 || ValidateScreenRecord(r) != nil {
		t.Fatal(r)
	}
}
func TestProviderConcurrentBodiesRemainBounded(t *testing.T) {
	var running, peak atomic.Int32
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			var out any
			if e := tr.request(context.Background(), "GET", "/body", "", nil, &out); e != nil {
				t.Error(e)
			}
		}()
	}
	group.Wait()
	if peak.Load() < 2 || peak.Load() > 4 {
		t.Fatal("concurrency was not bounded", peak.Load())
	}
}

func TestTypedHintsNeverFetchTheSuppliedURL(t *testing.T) {
	for _, raw := range []string{"tt0133093", "imdb:tt0133093", "https://www.imdb.com/title/tt0133093/", "tmdb:603", "https://www.themoviedb.org/movie/603-the-matrix", "https://anilist.co/anime/1"} {
		if _, ok := ParseScreenHint(raw, "movie"); !ok {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"https://localhost/title/tt0133093/", "https://imdb.com.evil.test/title/tt0133093/", "https://user:password@imdb.com/title/tt0133093/", "/media/Film.mkv", "tmdb:0"} {
		if _, ok := ParseScreenHint(raw, "movie"); ok {
			t.Fatal("accepted untyped input", raw)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find/tt0133093" || r.URL.Query().Get("external_source") != "imdb_id" {
			t.Fatal(r.URL)
		}
		fmt.Fprint(w, `{"movie_results":[{"id":603}],"tv_results":[]}`)
	}))
	defer server.Close()
	p, e := NewTMDBAt("fixture", server.URL)
	if e != nil {
		t.Fatal(e)
	}
	p.http.interval = 0
	id, e := p.ResolveScreenID(context.Background(), ScreenID{"imdb", "title", "tt0133093"}, "movie", "en", "")
	if e != nil || id.ID != "603" || id.Provider != "tmdb" {
		t.Fatal(id, e)
	}
}

func TestForcedEpisodeRefreshBypassesTheShortProviderCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		fmt.Fprintf(w, `{"id":9,"name":"Title %d","season_number":1,"episode_number":1}`, n)
	}))
	defer server.Close()
	p, e := NewTMDBAt("fixture", server.URL)
	if e != nil {
		t.Fatal(e)
	}
	p.http.interval = 0
	first, e := p.ScreenEpisode(context.Background(), "5", 1, 1, "official", "en")
	if e != nil {
		t.Fatal(e)
	}
	held, e := p.ScreenEpisode(context.Background(), "5", 1, 1, "official", "en")
	if e != nil || held.Title != first.Title || calls.Load() != 1 {
		t.Fatal(held, e)
	}
	fresh, e := p.ScreenEpisodeFresh(context.Background(), "5", 1, 1, "official", "en")
	if e != nil || fresh.Title == first.Title || calls.Load() != 2 {
		t.Fatal(fresh, e)
	}
}

func TestCredentialCacheInvalidationPreservesNewInflightWork(t *testing.T) {
	var cache screenCache
	oldStarted, releaseOld, oldDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(oldDone)
		var out any
		_ = cache.get(context.Background(), "same-route", &out, func() (any, error) { close(oldStarted); <-releaseOld; return nil, fmt.Errorf("old credential refused") })
	}()
	<-oldStarted
	cache.invalidate()
	newStarted, releaseNew, newDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(newDone)
		var out any
		if e := cache.get(context.Background(), "same-route", &out, func() (any, error) { close(newStarted); <-releaseNew; return map[string]bool{"ok": true}, nil }); e != nil {
			t.Error(e)
		}
	}()
	<-newStarted
	close(releaseOld)
	<-oldDone
	close(releaseNew)
	<-newDone
	var out any
	if e := cache.get(context.Background(), "same-route", &out, func() (any, error) { return nil, fmt.Errorf("new cached work was discarded") }); e != nil {
		t.Fatal(e)
	}
}
