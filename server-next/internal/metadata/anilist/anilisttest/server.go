// Package anilisttest serves hand-written GraphQL answers; it never contacts AniList.
package anilisttest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Work is original fixture text in the fields the adapter reads.
const Work = `{"id":42,"format":"TV","title":{"english":"The Quiet Sky","romaji":"Shizuka na Sora","native":"静かな空"},
"description":"A <b>quiet</b> journey &amp; a return.","startDate":{"year":2024,"month":4,"day":7},"status":"FINISHED",
"averageScore":82,"genres":["Adventure"],"countryOfOrigin":"JP","coverImage":{"extraLarge":"ORIGIN/poster.png"},
"bannerImage":"ORIGIN/banner.png","staff":{"edges":[{"role":"Director","node":{"id":8,"name":{"full":"Mina Writer"}}}]}}`

type Server struct {
	URL      string
	Requests atomic.Int64
	Fail     atomic.Bool
}

func New(t *testing.T) *Server {
	s := &Server{}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" {
			t.Errorf("AniList must be asked by an unauthenticated POST")
			w.WriteHeader(400)
			return
		}
		if s.Fail.Load() {
			w.Write([]byte(`{"data":null,"errors":[{"status":503,"message":"Unavailable"}]}`))
			return
		}
		var request struct{ Variables map[string]any }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		work := strings.ReplaceAll(Work, "ORIGIN", s.URL)
		if id, hasID := request.Variables["id"]; hasID {
			if id != float64(42) {
				work = "null"
			}
			w.Write([]byte(`{"data":{"Media":` + work + `}}`))
		} else {
			adult := `{"id":43,"format":"TV","isAdult":true,"title":{"english":"Adults only"}}`
			w.Write([]byte(`{"data":{"Page":{"media":[` + work + `,` + adult + `]}}}`))
		}
	}))
	s.URL = h.URL
	t.Cleanup(h.Close)
	return s
}
