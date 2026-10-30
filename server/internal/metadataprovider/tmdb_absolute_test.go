package metadataprovider

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestTMDBAbsoluteOrdersAndOffsets(t *testing.T) {
	p, _ := NewTMDB("fixture")
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/7":
			fmt.Fprint(w, `{"id":7,"name":"Series","seasons":[{"season_number":2,"episode_count":2},{"season_number":0,"episode_count":10},{"season_number":1,"episode_count":2}],"episode_groups":{"results":[{"id":"dvd","name":"DVD","type":3}]}}`)
		case "/tv/episode_group/dvd":
			fmt.Fprint(w, `{"id":"dvd","groups":[{"order":1,"episodes":[{"order":0,"season_number":2,"episode_number":2}]},{"order":0,"episodes":[{"order":1,"season_number":1,"episode_number":1},{"order":0,"season_number":2,"episode_number":1}]}]}`)
		case "/tv/7/season/2/episode/1":
			fmt.Fprint(w, `{"id":21,"name":"Third","season_number":2,"episode_number":1,"still_path":"/third.jpg"}`)
		case "/tv/7/season/1/episode/1":
			fmt.Fprint(w, `{"id":11,"name":"First","season_number":1,"episode_number":1}`)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for _, tc := range []struct {
		local           int
		order, id       string
		season, episode int
	}{
		{3, "official", "21", 2, 1}, {15, "tmdb-absolute:official:-12", "21", 2, 1},
		{1, "tmdb-group:dvd", "21", 2, 1}, {14, "tmdb-absolute:tmdb-group:dvd:-12", "11", 1, 1},
	} {
		r, err := p.ScreenAbsoluteEpisode(context.Background(), "7", tc.local, tc.order, "en-US")
		if err != nil || r.Identity.ID != tc.id || r.Coordinates == nil {
			t.Fatal(r, err)
		}
		c := r.Coordinates
		if c.Season != nil || *c.Absolute != tc.local || c.Episode != tc.local || c.Order != tc.order || *c.OriginalSeason != tc.season || *c.OriginalEpisode != tc.episode {
			t.Fatal(c)
		}
	}
	for _, order := range []string{"tmdb-absolute:official:+1", "tmdb-absolute:official:1000001", "tmdb-absolute:official:-20", "tmdb-group:foreign"} {
		if _, err := p.ScreenAbsoluteEpisode(context.Background(), "7", 1, order, "en-US"); err == nil {
			t.Fatal("accepted", order)
		}
	}
}

func TestTMDBConditionalEpisodeKeepsLocalCoordinates(t *testing.T) {
	p, _ := NewTMDB("fixture")
	p.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/7/season/2/episode/1" || r.Header.Get("If-None-Match") != "episode-tag" {
			t.Error(r.URL, r.Header)
		}
		w.Header().Set("ETag", "episode-tag")
		w.WriteHeader(http.StatusNotModified)
	})
	season, ep, abs := 2, 1, 15
	previous := ScreenRecord{Identity: ScreenID{"tmdb", "episode", "21"}, Title: "Third", Coordinates: &ScreenCoordinates{ShowID: "7", Episode: abs, Absolute: &abs, Order: "tmdb-absolute:official:-12", OriginalSeason: &season, OriginalEpisode: &ep}}
	_, out, err := p.ScreenEpisodeConditional(context.Background(), previous, "en-US", Conditional{ETag: "episode-tag"})
	if err != ErrNotModified || out.ETag != "episode-tag" {
		t.Fatal(out, err)
	}
}
