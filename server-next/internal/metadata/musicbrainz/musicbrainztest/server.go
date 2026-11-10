// Package musicbrainztest is a stand-in for MusicBrainz that answers from files, so no test
// asks the real one.
//
// The files in answers/ are written by hand in the shape MusicBrainz's web service documents
// (https://musicbrainz.org/doc/MusicBrainz_API), cut down to the fields the client reads. The
// albums are real but the ids are made up: nothing in this repository's tests, and nothing on
// a development machine, calls the live service.
package musicbrainztest

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"
)

//go:embed answers/*.json
var answers embed.FS

// Server answers like MusicBrainz. Requests counts every request it was sent.
type Server struct {
	URL      string
	Requests atomic.Int64
}

// New starts a stand-in that stops when the test ends.
func New(t *testing.T) *Server {
	s := &Server{}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

// file is the answer file for a request: "/ws/2/release/<id>" is release_<id>.json, and the
// search `release:"Abbey Road" AND artist:"The Beatles"` is
// search_release_release-abbey-road-and-artist-the-beatles.json.
func file(r *http.Request) string {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/ws/2"), "/")
	if query := r.URL.Query().Get("query"); query != "" {
		slug := strings.Map(func(c rune) rune {
			if unicode.IsLetter(c) || unicode.IsDigit(c) {
				return unicode.ToLower(c)
			}
			return '-'
		}, query)
		for strings.Contains(slug, "--") {
			slug = strings.ReplaceAll(slug, "--", "-")
		}
		return "answers/search_" + path + "_" + strings.Trim(slug, "-") + ".json"
	}
	return "answers/" + strings.ReplaceAll(path, "/", "_") + ".json"
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("User-Agent") == "" || r.URL.Query().Get("fmt") != "json" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	raw, err := answers.ReadFile(file(r))
	switch {
	case err == nil:
		w.Write(raw)
	case r.URL.Query().Get("query") != "":
		w.Write([]byte(`{"created":"2026-11-10T00:00:00.000Z","count":0,"offset":0,"releases":[],"artists":[]}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Not Found"}`))
	}
}
