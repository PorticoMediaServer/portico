package metadataprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Similar-title lists and the IMDb crosswalk: parsed from the detail documents
// the adapters already read, bounded and validated like every other field.

func TestScreenSimilarAndIMDbValidation(t *testing.T) {
	for _, id := range []ScreenID{{"imdb", "title", "tt0113277"}, {"imdb", "title", "tt10872600"}} {
		if !ValidScreenID(id) {
			t.Fatalf("valid IMDb id refused: %#v", id)
		}
	}
	for _, id := range []ScreenID{{"imdb", "movie", "tt0113277"}, {"imdb", "title", "0113277"}, {"imdb", "title", "tt123"}, {"imdb", "title", "tt01132x7"}, {"imdb", "title", "nm0000158"}, {"imdb", "title", "tt0113277 "}} {
		if ValidScreenID(id) {
			t.Fatalf("malformed IMDb id accepted: %#v", id)
		}
	}
	base := ScreenRecord{Identity: ScreenID{"tmdb", "movie", "42"}, Title: "Film", Crosswalk: []ScreenID{{"imdb", "title", "tt0113277"}}}
	base.Similar = []ScreenID{{"tmdb", "movie", "1"}, {"tmdb", "show", "2"}}
	if e := ValidateScreenRecord(base); e != nil {
		t.Fatal(e)
	}
	for name, similar := range map[string][]ScreenID{
		"self":             {{"tmdb", "movie", "42"}},
		"duplicate":        {{"tmdb", "movie", "1"}, {"tmdb", "movie", "1"}},
		"foreign provider": {{"anilist", "anime", "1"}},
		"not a work":       {{"tmdb", "person", "1"}},
		"malformed":        {{"tmdb", "movie", "01"}},
	} {
		r := base
		r.Similar = similar
		if ValidateScreenRecord(r) == nil {
			t.Fatalf("%s similar target accepted", name)
		}
	}
	r := base
	r.Similar = nil
	for i := 1; i <= MaxScreenSimilar+1; i++ {
		r.Similar = append(r.Similar, ScreenID{"tmdb", "movie", strconv.Itoa(i + 100)})
	}
	if ValidateScreenRecord(r) == nil {
		t.Fatal("oversized similar list accepted")
	}
	// The parsers' cap drops the excess instead of the record.
	var capped []ScreenID
	for _, id := range r.Similar {
		capped = appendSimilar(capped, r.Identity, id)
	}
	if len(capped) != MaxScreenSimilar || capped[0] != r.Similar[0] {
		t.Fatal(len(capped))
	}
}

func TestScreenTMDBRecommendationsAndIMDbCrosswalk(t *testing.T) {
	provider, _ := NewTMDB("fixture-secret")
	provider.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		parts := r.URL.Query().Get("append_to_response")
		if !strings.Contains(parts, "recommendations") || !strings.Contains(parts, "keywords") {
			t.Errorf("%s detail read without recommendations and keywords: %q", r.URL.Path, parts)
		}
		switch r.URL.Path {
		case "/movie/42":
			// Top-level imdb_id only; TMDB order is the rank; a person, the
			// title itself, a repeat and a malformed id are each dropped alone.
			fmt.Fprint(w, `{"id":42,"title":"Film","imdb_id":"tt0113277","external_ids":{"tvdb_id":71,"imdb_id":null},"recommendations":{"page":1,"results":[{"id":949,"media_type":"movie"},{"id":7,"media_type":"tv"},{"id":5,"media_type":"person"},{"id":42,"media_type":"movie"},{"id":949,"media_type":"movie"},{"id":0,"media_type":"movie"},{"id":11,"media_type":"movie"}],"total_pages":3}}`)
		case "/tv/7":
			fmt.Fprint(w, `{"id":7,"name":"Series","first_air_date":"2020-01-02","external_ids":{"tvdb_id":81189,"imdb_id":"tt0903747"},"recommendations":{"results":[{"id":1396,"media_type":"tv"}]}}`)
		case "/movie/43":
			fmt.Fprint(w, `{"id":43,"title":"Plain","imdb_id":"","external_ids":{"imdb_id":"not-an-id"}}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	movie, e := provider.ScreenDetails(ctx, "movie", "42", "en-US", "CA")
	if e != nil {
		t.Fatal(e)
	}
	if want := []ScreenID{{"tmdb", "movie", "949"}, {"tmdb", "show", "7"}, {"tmdb", "movie", "11"}}; !reflect.DeepEqual(movie.Similar, want) {
		t.Fatalf("movie recommendations: %v", movie.Similar)
	}
	if want := []ScreenID{{"tvdb", "movie", "71"}, {"imdb", "title", "tt0113277"}}; !reflect.DeepEqual(movie.Crosswalk, want) {
		t.Fatalf("movie crosswalk: %v", movie.Crosswalk)
	}
	show, e := provider.ScreenDetails(ctx, "show", "7", "en-US", "CA")
	if e != nil || !reflect.DeepEqual(show.Similar, []ScreenID{{"tmdb", "show", "1396"}}) || !reflect.DeepEqual(show.Crosswalk, []ScreenID{{"tvdb", "show", "81189"}, {"imdb", "title", "tt0903747"}}) {
		t.Fatal(show.Similar, show.Crosswalk, e)
	}
	plain, e := provider.ScreenDetails(ctx, "movie", "43", "", "")
	if e != nil || len(plain.Similar) != 0 || len(plain.Crosswalk) != 0 {
		t.Fatal(plain, e)
	}
	// The conditional refresh reads the same document as the first read.
	conditional, _, e := provider.ScreenDetailsConditional(ctx, "movie", "42", "en-US", "CA", Conditional{})
	if e != nil || !reflect.DeepEqual(conditional.Similar, movie.Similar) || !reflect.DeepEqual(conditional.Crosswalk, movie.Crosswalk) {
		t.Fatal(conditional, e)
	}
}

func TestScreenTVDBIMDbCrosswalk(t *testing.T) {
	p, _ := NewTVDB("key")
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			fmt.Fprint(w, `{"status":"success","data":{"token":"fixture-token"}}`)
			return
		}
		fmt.Fprint(w, `{"status":"success","data":{"id":81189,"name":"Series","year":"2008","remoteIds":[{"id":"tt0903747","sourceName":"IMDB"},{"id":"1396","sourceName":"TheMovieDB.com"},{"id":"1397","sourceName":"TheMovieDB.com"},{"id":"123","sourceName":"TV.com"},{"id":"bad","sourceName":"IMDB"}]}}`)
	})
	r, e := p.ScreenDetails(context.Background(), "show", "81189", "en-US", "")
	if e != nil || !reflect.DeepEqual(r.Crosswalk, []ScreenID{{"imdb", "title", "tt0903747"}, {"tmdb", "show", "1396"}}) || len(r.Similar) != 0 {
		t.Fatal(r.Crosswalk, r.Similar, e)
	}
}

func TestScreenAniListRecommendations(t *testing.T) {
	p := NewAniList()
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(body.Query, "Page(") {
			if strings.Contains(body.Query, "recommendations") {
				t.Error("search asks for recommendations")
			}
			fmt.Fprint(w, `{"data":{"Page":{"media":[{"id":1,"type":"ANIME","title":{"romaji":"Anime"}}]}}}`)
			return
		}
		if !strings.Contains(body.Query, "recommendations(sort:[RATING_DESC,ID],perPage:25)") {
			t.Error("detail read without recommendations", body.Query)
		}
		fmt.Fprint(w, `{"data":{"Media":{"id":1,"type":"ANIME","title":{"romaji":"Anime"},"recommendations":{"nodes":[
{"rating":120,"mediaRecommendation":{"id":20,"type":"ANIME"}},
{"rating":80,"mediaRecommendation":{"id":30,"type":"MANGA"}},
{"rating":60,"mediaRecommendation":null},
{"rating":40,"mediaRecommendation":{"id":1,"type":"ANIME"}},
{"rating":30,"mediaRecommendation":{"id":21,"type":"ANIME"}},
{"rating":0,"mediaRecommendation":{"id":22,"type":"ANIME"}},
{"rating":-3,"mediaRecommendation":{"id":23,"type":"ANIME"}}]}}}}`)
	})
	ctx := context.Background()
	r, e := p.ScreenDetails(ctx, "anime", "1", "en-US", "")
	if e != nil || !reflect.DeepEqual(r.Similar, []ScreenID{{"anilist", "anime", "20"}, {"anilist", "anime", "21"}}) {
		t.Fatal(r.Similar, e)
	}
	rows, e := p.SearchScreen(ctx, "anime", "Anime", 0, "en-US", "")
	if e != nil || len(rows) != 1 || len(rows[0].Similar) != 0 {
		t.Fatal(rows, e)
	}
}
