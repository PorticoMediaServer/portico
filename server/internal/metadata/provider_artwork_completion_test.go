package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShowArtworkUsesLocaleSeasonsAndSmallerPortraits(t *testing.T) {
	s, _, _ := screenFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/42" || r.URL.Query().Get("include_image_language") != "fr,null,en" || r.URL.Query().Get("append_to_response") != "images,aggregate_credits" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"id":42,"images":{"logos":[{"file_path":"/spanish.png","iso_639_1":"es","vote_average":10},{"file_path":"/french.png","iso_639_1":"fr","vote_average":5}]},"seasons":[{"season_number":1,"poster_path":"/season.jpg"}],"aggregate_credits":{"cast":[{"id":9,"profile_path":"/person.jpg"}]}}`)
	}))
	defer server.Close()
	s.base = server.URL
	s.client = server.Client()
	images, e := s.discoverArtworkFor(context.Background(), "tmdb", "42", "show", "fr-FR")
	if e != nil || len(images) != 4 {
		t.Fatal(images, e)
	}
	if images[0].role != "logo" || images[0].locale != "fr" {
		t.Fatal("foreign logo ranked ahead", images)
	}
	foundSeason, foundPortrait := false, false
	for _, im := range images {
		if im.season != nil && im.season.number == 1 {
			foundSeason = true
		}
		if im.role == "portrait" && strings.Contains(im.origin, "/h632/") {
			foundPortrait = true
		}
	}
	if !foundSeason || !foundPortrait {
		t.Fatal(images)
	}
}
