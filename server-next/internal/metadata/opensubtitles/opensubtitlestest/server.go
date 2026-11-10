// Package opensubtitlestest is a stand-in for OpenSubtitles.com that answers from files, so
// no test asks the real one.
//
// The files in answers/ are written by hand in the shape OpenSubtitles' REST API documents
// (https://opensubtitles.stoplight.io/docs/opensubtitles-api), cut down to the fields the
// client reads. The subtitle text is placeholder lines. Nothing in this repository's tests,
// and nothing on a development machine, calls the live service.
package opensubtitlestest

import (
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"
)

//go:embed answers/*
var answers embed.FS

// Server answers like OpenSubtitles. Requests counts every request; AllowanceUsed makes it
// refuse downloads as when the account's daily allowance is used.
type Server struct {
	URL           string
	Requests      atomic.Int64
	AllowanceUsed atomic.Bool
}

// New starts a stand-in that stops when the test ends.
func New(t *testing.T) *Server {
	s := &Server{}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

// slug is a search's answer file: imdb_id=133093&languages=en is
// subtitles_imdb_id-133093-languages-en.json.
func slug(query string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' {
			return unicode.ToLower(c)
		}
		return '-'
	}, query)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/api/") && (r.Header.Get("Api-Key") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "Portico v")) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	signedIn := r.Header.Get("Authorization") == "Bearer stand-in-token"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/login":
		var who struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&who)
		if who.Password != "right" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"You cannot consume this service","status":401}`))
			return
		}
		host := strings.TrimPrefix(s.URL, "http://")
		w.Write([]byte(`{"user":{"allowed_downloads":20,"level":"Sub leecher","vip":false},"base_url":"` + host + `","token":"stand-in-token","status":200}`))
	case !signedIn && strings.HasPrefix(r.URL.Path, "/api/"):
		w.WriteHeader(http.StatusUnauthorized)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/subtitles":
		raw, err := answers.ReadFile("answers/subtitles_" + slug(r.URL.RawQuery) + ".json")
		if err != nil {
			raw = []byte(`{"total_pages":0,"total_count":0,"per_page":60,"page":1,"data":[]}`)
		}
		w.Write(raw)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/download":
		if s.AllowanceUsed.Load() {
			w.WriteHeader(http.StatusNotAcceptable)
			w.Write([]byte(`{"requests":21,"remaining":0,"message":"You have downloaded your allowed 20 subtitles for 24h.","reset_time":"5 hours","reset_time_utc":"2026-11-11T00:00:00.000Z"}`))
			return
		}
		var ask struct {
			FileID int64 `json:"file_id"`
		}
		json.NewDecoder(r.Body).Decode(&ask)
		link, _ := json.Marshal(s.URL + "/download/file_" + jsonNumber(ask.FileID) + ".srt")
		w.Write([]byte(`{"link":` + string(link) + `,"file_name":"subtitle.srt","requests":1,"remaining":19,"reset_time_utc":"2026-11-11T00:00:00.000Z"}`))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/download/"):
		raw, err := answers.ReadFile("answers/" + strings.TrimPrefix(r.URL.Path, "/download/"))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-subrip")
		w.Write(raw)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
