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

func testTVDB(t *testing.T, handler http.HandlerFunc) *TVDB {
	t.Helper()
	s, e := NewTVDB("test-project-secret")
	if e != nil {
		t.Fatal(e)
	}
	s.http = fixtureTransport(t, handler)
	return s
}
func TestTVDBLoginSingleflightAndConcurrent401(t *testing.T) {
	var logins atomic.Int32
	var gets atomic.Int32
	s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			var body map[string]string
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil || body["apikey"] != "test-project-secret" {
				t.Error(body, e)
			}
			n := logins.Add(1)
			time.Sleep(5 * time.Millisecond)
			fmt.Fprintf(w, `{"status":"success","data":{"token":"token-value-%d"}}`, n)
			return
		}
		gets.Add(1)
		if r.Header.Get("Authorization") == "Bearer token-value-1" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token-value-2" {
			t.Error("wrong token")
		}
		w.Write([]byte(`{"status":"success","data":[]}`))
	})
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.SearchSeries(context.Background(), "Title & name", 2020); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if logins.Load() != 2 || gets.Load() < 12 {
		t.Fatal(logins.Load(), gets.Load())
	}
}
func TestTVDB401RetriesOnlyOnceAndSanitizedLogin(t *testing.T) {
	var calls atomic.Int32
	s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"status":"success","data":{"token":"valid-token"}}`))
			return
		}
		w.WriteHeader(401)
		w.Write([]byte("secret body"))
	})
	_, e := s.SearchSeries(context.Background(), "Name", 0)
	errorCode(t, e, "request_rejected")
	if calls.Load() != 4 || strings.Contains(e.Error(), "secret") {
		t.Fatal(calls.Load(), e)
	}
	bad := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"token":"short"}}`))
	})
	_, e = bad.SearchSeries(context.Background(), "Name", 0)
	errorCode(t, e, "invalid_login")
}
func TestTVDBEpisodeValidationAndPaging(t *testing.T) {
	valid := `{"status":"success","data":{"series":{"id":10},"episodes":[{"id":100,"seriesId":10,"name":"Special","seasonNumber":0,"number":1}]},"links":{"next":"?page=1"}}`
	for _, tc := range []struct{ name, body, code string }{{"valid", valid, ""}, {"wrong-series", strings.Replace(valid, `"seriesId":10`, `"seriesId":11`, 1), "invalid_episodes"}, {"wrong-envelope", strings.Replace(valid, `"id":10}`, `"id":11}`, 1), "invalid_episodes"}, {"negative-season", strings.Replace(valid, `"seasonNumber":0`, `"seasonNumber":-1`, 1), "invalid_episodes"}, {"duplicate-page-param", strings.Replace(valid, `?page=1`, `?page=1&page=2`, 1), "invalid_pagination"}, {"skipped-page", strings.Replace(valid, `?page=1`, `?page=2`, 1), "invalid_pagination"}, {"foreign-host", strings.Replace(valid, `?page=1`, `https://evil.invalid/series/10/episodes/official?page=1`, 1), "invalid_pagination"}, {"wrong-path", strings.Replace(valid, `?page=1`, `/other?page=1`, 1), "invalid_pagination"}} {
		t.Run(tc.name, func(t *testing.T) {
			s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					w.Write([]byte(`{"status":"success","data":{"token":"valid-token"}}`))
					return
				}
				if r.URL.Path != "/series/10/episodes/official" || r.URL.Query().Get("page") != "0" {
					t.Error(r.URL)
				}
				w.Write([]byte(tc.body))
			})
			page, e := s.Episodes(context.Background(), 10, Official, 0)
			if tc.code != "" {
				errorCode(t, e, tc.code)
			} else if e != nil || page.NextPage == nil || *page.NextPage != 1 || len(page.Episodes) != 1 || *page.Episodes[0].SeasonNumber != 0 {
				t.Fatal(page, e)
			}
		})
	}
}
func TestTVDBQueryBoundsAndCancellation(t *testing.T) {
	var requests atomic.Int32
	s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte(`{}`)) })
	if _, e := s.Episodes(context.Background(), 1, EpisodeOrder("../login"), 0); e == nil {
		t.Fatal("invalid order accepted")
	}
	if _, e := s.SearchSeries(context.Background(), "", 0); e == nil {
		t.Fatal("empty title")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid query fetched")
	}
	s.auth <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := s.access(ctx, ""); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	<-s.auth
}
func TestTVDBDuplicateEpisodesAndInvalidToken(t *testing.T) {
	s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.Write([]byte(`{"status":"success","data":{"token":"valid-token"}}`))
			return
		}
		w.Write([]byte(`{"status":"success","data":{"series":{"id":1},"episodes":[{"id":2,"seriesId":1},{"id":2,"seriesId":1}]},"links":{"next":null}}`))
	})
	_, e := s.Episodes(context.Background(), 1, Official, 0)
	errorCode(t, e, "invalid_episodes")
	s = testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"token":"invalid\nheader-token"}}`))
	})
	_, e = s.access(context.Background(), "")
	errorCode(t, e, "invalid_login")
}
func TestTVDBEpisodeCharactersValidation(t *testing.T) {
	valid := `{"status":"success","data":{"id":77,"seriesId":10,"name":"Pilot","characters":[{"peopleId":1,"personName":"Ada Actor","name":"Captain","peopleType":"Actor","sort":0},{"peopleId":2,"personName":"Gus Guest","name":"Stranger","peopleType":"Guest Star","sort":1},{"peopleId":0,"personName":"","name":"Nobody","peopleType":"Actor","sort":2}]},"links":{}}`
	for _, tc := range []struct{ name, body, code string }{{"valid", valid, ""}, {"wrong-episode", strings.Replace(valid, `"id":77,`, `"id":78,`, 1), "invalid_episode"}, {"wrong-envelope", strings.Replace(valid, `"status":"success"`, `"status":"failure"`, 1), "invalid_episode"}, {"null-characters", `{"status":"success","data":{"id":77,"seriesId":10,"name":"Pilot","characters":null},"links":{}}`, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			s := testTVDB(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					w.Write([]byte(`{"status":"success","data":{"token":"valid-token"}}`))
					return
				}
				if r.URL.Path != "/episodes/77/extended" {
					t.Error(r.URL)
				}
				w.Write([]byte(tc.body))
			})
			chars, e := s.EpisodeCharacters(context.Background(), 77)
			if tc.code != "" {
				errorCode(t, e, tc.code)
			} else if e != nil {
				t.Fatal(e)
			} else if tc.name == "null-characters" {
				if len(chars) != 0 {
					t.Fatal(chars)
				}
			} else if len(chars) != 2 || chars[0].PersonID != 1 || chars[0].PersonName != "Ada Actor" || chars[0].Character != "Captain" || chars[0].Type != "Actor" || chars[1].PersonID != 2 || chars[1].Type != "Guest Star" {
				t.Fatal(chars)
			}
		})
	}
	if _, e := testTVDB(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }).EpisodeCharacters(context.Background(), 0); e == nil {
		t.Fatal("invalid episode accepted")
	}
}
