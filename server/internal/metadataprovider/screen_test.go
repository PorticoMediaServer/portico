package metadataprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScreenTypedIdentitiesAndEvidenceBounds(t *testing.T) {
	for _, id := range []ScreenID{{"tmdb", "movie", "01"}, {"tmdb", "movie", "-1"}, {"anilist", "episode", "1"}, {"tvdb", "show", "https://example.org"}, {"tmdb", "movie", "9007199254740992"}} {
		if ValidScreenID(id) {
			t.Fatalf("accepted untyped/noncanonical identity: %#v", id)
		}
	}
	r := ScreenRecord{Identity: ScreenID{"tmdb", "movie", "42"}, Title: "Fixture", Overview: strings.Repeat("x", 65537)}
	if ValidateScreenRecord(r) == nil {
		t.Fatal("oversized evidence accepted")
	}
	r.Overview = "Description"
	if e := ValidateScreenRecord(r); e != nil {
		t.Fatal(e)
	}
}
func TestScreenTMDBSearchDetailsCacheAndOrdering(t *testing.T) {
	var calls atomic.Int32
	provider, _ := NewTMDB("fixture-secret")
	provider.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("credential missing")
		}
		switch r.URL.Path {
		case "/search/movie":
			if r.URL.Query().Get("query") != "Fixture" || r.URL.Query().Get("year") != "2020" {
				t.Error("query not encoded")
			}
			fmt.Fprint(w, `{"results":[{"id":42,"title":"Fixture","release_date":"2020-01-02"}]}`)
		case "/movie/42":
			fmt.Fprint(w, `{"id":42,"title":"Fixture","release_date":"2020-01-02","runtime":120,"overview":"Film description","external_ids":{"tvdb_id":71},"genres":[{"id":18,"name":"Drama"}],"belongs_to_collection":{"id":9,"name":"Saga"}}`)
		case "/tv/7":
			fmt.Fprint(w, `{"id":7,"name":"Series","first_air_date":"2020-01-02","episode_groups":{"results":[{"id":"abcdef1234","name":"DVD","type":3}]}}`)
		case "/tv/episode_group/abcdef1234":
			fmt.Fprint(w, `{"id":"abcdef1234","groups":[{"order":1,"episodes":[{"order":0,"id":99,"season_number":2,"episode_number":3}]}]}`)
		case "/tv/7/season/2/episode/3":
			fmt.Fprint(w, `{"id":99,"name":"Mapped episode","season_number":2,"episode_number":3,"air_date":"2020-01-02","runtime":40}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	rows, e := provider.SearchScreen(ctx, "movie", "Fixture", 2020, "en-US", "CA")
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	r, e := provider.ScreenDetails(ctx, "movie", "42", "en-US", "CA")
	if e != nil || r.AdvisoryRuntimeMinutes == nil || *r.AdvisoryRuntimeMinutes != 120 || len(r.Crosswalk) != 1 {
		t.Fatal(r, e)
	}
	_, e = provider.ScreenDetails(ctx, "movie", "42", "en-US", "CA")
	if e != nil || calls.Load() != 2 {
		t.Fatal("equivalent call not cached", calls.Load(), e)
	}
	ep, e := provider.ScreenEpisode(ctx, "7", 1, 1, "tmdb-group:abcdef1234", "en-US")
	if e != nil || ep.Identity.ID != "99" || ep.Coordinates == nil || *ep.Coordinates.OriginalSeason != 2 || *ep.Coordinates.Season != 1 || *ep.Coordinates.OriginalEpisode != 3 {
		t.Fatal(ep, e)
	}
	if _, e = provider.ScreenEpisode(ctx, "7", 1, 1, "tmdb-group:unknown1234", "en-US"); e == nil {
		t.Fatal("foreign episode group accepted")
	}
}
func TestScreenTMDbAggregateCreditsAndEpisodeStill(t *testing.T) {
	provider, _ := NewTMDB("fixture-secret")
	provider.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/7/aggregate_credits":
			fmt.Fprint(w, `{"id":7,"cast":[{"id":11,"name":"Ada Actor","order":0,"roles":[{"character":"Captain","episode_count":10}]},{"id":0,"name":"","order":1,"roles":[]}],"crew":[{"id":12,"name":"Dan Director","jobs":[{"job":"Director","department":"Directing"}]}]}`)
		case "/tv/7/season/1/episode/2":
			fmt.Fprint(w, `{"id":99,"name":"Second","season_number":1,"episode_number":2,"air_date":"2020-01-09","still_path":"/still.jpg"}`)
		case "/tv/8/aggregate_credits":
			fmt.Fprint(w, `{"id":7,"cast":[],"crew":[]}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	credits, e := provider.ScreenShowAggregateCredits(ctx, "7", "en-US")
	if e != nil || len(credits) != 2 || credits[0] != (ScreenCredit{ID: "11", Name: "Ada Actor", Role: "Captain", Department: "Acting", Ordinal: 0}) || credits[1].ID != "12" || credits[1].Department != "Directing" || credits[1].Role != "Director" {
		t.Fatal(credits, e)
	}
	if _, e = provider.ScreenShowAggregateCredits(ctx, "7x", "en-US"); e == nil {
		t.Fatal("invalid show accepted")
	}
	if _, e = provider.ScreenShowAggregateCredits(ctx, "8", "en-US"); e == nil {
		t.Fatal("mismatched aggregate accepted")
	}
	ep, e := provider.ScreenEpisode(ctx, "7", 1, 2, "official", "en-US")
	if e != nil || ep.StillPath != "/still.jpg" {
		t.Fatal(ep, e)
	}
	r := ep
	r.Title = ""
	if ValidateScreenRecord(r) == nil {
		t.Fatal("empty title accepted")
	}
}
func TestScreenTVDBMovieCompaniesTranslationsAndCrosswalk(t *testing.T) {
	p, _ := NewTVDB("key")
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			fmt.Fprint(w, `{"status":"success","data":{"token":"fixture-token"}}`)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"id":42,"name":"Original","year":"2020","averageRuntime":90,"first_release":{"date":"2020-02-03"},"companies":{"studio":[{"id":8,"name":"Studio"}]},"remoteIds":[{"id":"9","sourceName":"TheMovieDB.com"}],"translations":{"nameTranslations":[{"name":"Nom","language":"fra"}],"overviewTranslations":[{"overview":"Résumé","language":"fra"}]}}}`)
	})
	r, e := p.ScreenDetails(context.Background(), "movie", "42", "fr-CA", "CA")
	if e != nil || r.Title != "Nom" || r.Overview != "Résumé" || r.Date != "2020-02-03" || len(r.Relations) != 1 || len(r.Crosswalk) != 1 || r.AdvisoryRuntimeMinutes == nil {
		t.Fatal(r, e)
	}
	var companies tvdbScreenCompanies
	if e = json.Unmarshal([]byte(`[{"id":1,"name":"Network"}]`), &companies); e != nil || len(companies) != 1 {
		t.Fatal(companies, e)
	}
}
func TestScreenAniListWorkRelationshipsAndNoSyntheticEpisode(t *testing.T) {
	p := NewAniList()
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("not GraphQL POST")
		}
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body.Variables["id"] != float64(1) {
			t.Error("wrong typed id")
		}
		fmt.Fprint(w, `{"data":{"Media":{"id":1,"type":"ANIME","title":{"romaji":"Anime","english":"Show","native":"アニメ"},"description":"<b>Story</b>","format":"TV","seasonYear":2020,"startDate":{"year":2020,"month":1},"episodes":12,"isAdult":false,"relations":{"edges":[{"relationType":"SEQUEL","node":{"id":2,"type":"ANIME","title":{"english":"Next"}}},{"relationType":"ADAPTATION","node":{"id":3,"type":"MANGA","title":{"english":"Book"}}}]}}}}`)
	})
	r, e := p.ScreenDetails(context.Background(), "anime", "1", "en-US", "")
	if e != nil || r.Title != "Show" || r.Overview != "Story" || r.Identity.Type != "anime" || r.Coordinates != nil || r.AdvisoryRuntimeMinutes != nil || len(r.Relations) != 1 || r.Relations[0].Target.Type != "anime" {
		t.Fatal(r, e)
	}
	if _, e = p.ScreenDetails(context.Background(), "episode", "1", "en-US", ""); e == nil {
		t.Fatal("synthetic AniList episode accepted")
	}
}
func TestScreenCacheSingleFlightAndCancellation(t *testing.T) {
	var c screenCache
	var calls atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	done := make(chan struct{})
	fetch := func() (any, error) {
		if calls.Add(1) == 1 {
			close(start)
		}
		<-done
		return map[string]int{"id": 42}, nil
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out map[string]int
			if e := c.get(context.Background(), "same", &out, fetch); e != nil || out["id"] != 42 {
				t.Error(out, e)
			}
		}()
	}
	<-start
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var out any
	if e := c.get(ctx, "same", &out, fetch); e == nil {
		t.Fatal("waiter ignored cancellation")
	}
	close(done)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("singleflight", calls.Load())
	}
}

// TheTVDB lists a show's companies with a type each and groups a film's by
// type. The studio and the network keep their kind so they can be published.
func TestScreenTVDBCompaniesKeepTheirKind(t *testing.T) {
	kinds := func(body string, kind string) map[string]string {
		p, _ := NewTVDB("key")
		p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				fmt.Fprint(w, `{"status":"success","data":{"token":"fixture-token"}}`)
				return
			}
			fmt.Fprint(w, body)
		})
		r, e := p.ScreenDetails(context.Background(), kind, "42", "en-US", "US")
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]string{}
		for _, relation := range r.Relations {
			out[relation.Name] = relation.Kind
		}
		return out
	}
	show := kinds(`{"status":"success","data":{"id":42,"name":"Show","year":"2020","companies":[
 {"id":1,"name":"Channel","companyType":{"companyTypeId":1,"companyTypeName":"Network"}},
 {"id":2,"name":"Producer","companyType":{"companyTypeId":2,"companyTypeName":"Production Company"}},
 {"id":3,"name":"Animator","companyType":{"companyTypeId":3,"companyTypeName":"Studio"}},
 {"id":4,"name":"Seller","companyType":{"companyTypeId":4,"companyTypeName":"Distributor"}},
 {"id":5,"name":"Untyped"}]}}`, "show")
	want := map[string]string{"Channel": "network", "Producer": "production_company", "Animator": "studio", "Seller": "company", "Untyped": "company"}
	for name, kind := range want {
		if show[name] != kind {
			t.Errorf("show company %s: got %q, want %q", name, show[name], kind)
		}
	}
	film := kinds(`{"status":"success","data":{"id":42,"name":"Film","year":"2020","companies":{"studio":[{"id":8,"name":"Studio"}],"network":[{"id":9,"name":"Channel"}],"production":[{"id":10,"name":"Producer"}],"distributor":[{"id":11,"name":"Seller"}],"special_effects":[{"id":12,"name":"Effects"}]}}}`, "movie")
	want = map[string]string{"Studio": "studio", "Channel": "network", "Producer": "production_company", "Seller": "company", "Effects": "company"}
	for name, kind := range want {
		if film[name] != kind {
			t.Errorf("film company %s: got %q, want %q", name, film[name], kind)
		}
	}
}
