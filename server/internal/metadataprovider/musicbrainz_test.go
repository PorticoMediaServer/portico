package metadataprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const idA = "11111111-1111-1111-1111-111111111111"
const idB = "22222222-2222-2222-2222-222222222222"
const idC = "33333333-3333-3333-3333-333333333333"

func testMB(t *testing.T, h http.HandlerFunc) *MusicBrainz {
	s := NewMusicBrainz()
	s.http = fixtureTransport(t, h)
	s.http.base += "/ws/2"
	return s
}
func TestMusicBrainzMergeRetainsRequestedAndVerifiesFinalIdentity(t *testing.T) {
	var calls atomic.Int32
	s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("fmt") != "json" || r.URL.Query().Get("inc") != "artist-credits+isrcs+work-rels+artist-rels" {
			t.Error("request contract", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, idA) {
			w.Header().Set("Location", "/ws/2/recording/"+idB+"?ignored=provider-query")
			w.WriteHeader(301)
			return
		}
		json.NewEncoder(w).Encode(Recording{ID: idB, Title: "Canonical recording"})
	})
	result, e := s.Recording(context.Background(), idA)
	if e != nil || result.RequestedID != idA || result.Recording.ID != idB || calls.Load() != 2 {
		t.Fatal(result, e, calls.Load())
	}
	mismatch := testMB(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Recording{ID: idC, Title: "Other recording"})
	})
	_, e = mismatch.Recording(context.Background(), idA)
	errorCode(t, e, "identity_mismatch")
}
func TestMusicBrainzRejectsUnsafeAndEndlessRedirects(t *testing.T) {
	for _, target := range []string{"https://evil.invalid/ws/2/recording/" + idB, "/ws/2/release/" + idB, "/ws/2/recording/" + idB + "/extra", "/ws/2/recording/" + idB + "#fragment"} {
		t.Run(target, func(t *testing.T) {
			var calls atomic.Int32
			s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", target)
				w.WriteHeader(302)
			})
			_, e := s.Recording(context.Background(), idA)
			errorCode(t, e, "invalid_redirect")
			if calls.Load() != 1 {
				t.Fatal("unsafe destination fetched")
			}
		})
	}
	var calls atomic.Int32
	s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/ws/2/recording/"+idA)
		w.WriteHeader(308)
	})
	_, e := s.Recording(context.Background(), idA)
	errorCode(t, e, "redirect_limit")
	if calls.Load() != 4 {
		t.Fatal(calls.Load())
	}
}
func TestMusicBrainzReleaseValidation(t *testing.T) {
	valid := Release{ID: idA, Title: "Release", ReleaseGroup: ReleaseGroup{ID: idB, Title: "Group"}, Media: []Medium{{Position: 1, Tracks: []ReleaseTrack{{ID: idC, Title: "Track", Position: 1, Recording: Recording{ID: idB, Title: "Recording"}}}}}}
	for _, tc := range []struct {
		name   string
		change func(*Release)
		code   string
	}{{"valid", func(*Release) {}, ""}, {"duplicate-medium", func(r *Release) { r.Media = append(r.Media, r.Media[0]) }, "invalid_medium"}, {"negative-length", func(r *Release) { n := -1; r.Media[0].Tracks[0].LengthMillis = &n }, "invalid_track"}, {"duplicate-track", func(r *Release) { r.Media[0].Tracks = append(r.Media[0].Tracks, r.Media[0].Tracks[0]) }, "invalid_track"}, {"invalid-group", func(r *Release) { r.ReleaseGroup.ID = "bad" }, "invalid_release"}, {"genres", func(r *Release) { r.Genres = []MBGenre{{Name: "Rock", Count: 3}} }, ""}, {"bad-genre", func(r *Release) { r.Genres = []MBGenre{{Name: ""}} }, "invalid_release"}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var value Release
			json.Unmarshal(raw, &value)
			tc.change(&value)
			s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("inc") != "recordings+artist-credits+release-groups+labels+genres+isrcs" {
					t.Error(r.URL)
				}
				json.NewEncoder(w).Encode(value)
			})
			out, e := s.Release(context.Background(), idA)
			if tc.code != "" {
				errorCode(t, e, tc.code)
			} else if e != nil || out.Release.Media[0].Tracks[0].Recording.ID != idB {
				t.Fatal(out, e)
			} else if tc.name == "genres" && (len(out.Release.Genres) != 1 || out.Release.Genres[0].Name != "Rock") {
				t.Fatal(out.Release.Genres)
			}
		})
	}
}
func TestMusicBrainzLiteralSearchAndNoInventedEmptyResponse(t *testing.T) {
	s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, `\" OR \*\:\*`) || r.URL.Query().Get("limit") != "25" {
			t.Error("unescaped query", query)
		}
		w.Write([]byte(`{"recordings":[]}`))
	})
	rows, e := s.SearchRecordings(context.Background(), `Name" OR *:*`, "Artist")
	if e != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	s = testMB(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	_, e = s.SearchRecordings(context.Background(), "Name", "Artist")
	errorCode(t, e, "invalid_search")
}
func TestMusicBrainzReleaseSearchPreservesEditionCandidates(t *testing.T) {
	s := testMB(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/release" || r.URL.Query().Get("limit") != "25" || !strings.Contains(r.URL.Query().Get("query"), `release:"Album"`) {
			t.Error(r.URL)
		}
		w.Write([]byte(`{"releases":[{"id":"` + idA + `","title":"Album","country":"CA","date":"2020"},{"id":"` + idB + `","title":"Album","country":"US","date":"2020"}]}`))
	})
	rows, e := s.SearchReleases(context.Background(), "Album", "Artist")
	if e != nil || len(rows) != 2 || rows[0].Country == rows[1].Country {
		t.Fatal(rows, e)
	}
	if NewMusicBrainz().http != NewMusicBrainz().http {
		t.Fatal("MusicBrainz limiter not process-wide")
	}
}
