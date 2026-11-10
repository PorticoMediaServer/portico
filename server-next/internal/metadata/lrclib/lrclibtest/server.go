// Package lrclibtest is a stand-in for LRCLIB that answers from files, so no test asks the
// real one.
//
// The files in answers/ are written by hand in the shape LRCLIB's API documents
// (https://lrclib.net/docs), cut down to the fields the client reads. The lyrics in them are
// placeholder lines, not any song's words. Nothing in this repository's tests, and nothing on
// a development machine, calls the live service.
package lrclibtest

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

// Server answers like LRCLIB. Requests counts every request it was sent.
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

// file is the answer file for a request: /api/get for the song "Come Together" is
// get_come-together.json.
func file(r *http.Request) string {
	slug := strings.Map(func(c rune) rune {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			return unicode.ToLower(c)
		}
		return '-'
	}, r.URL.Query().Get("track_name"))
	return "answers/" + strings.TrimPrefix(r.URL.Path, "/api/") + "_" + strings.Trim(slug, "-") + ".json"
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	raw, err := answers.ReadFile(file(r))
	switch {
	case err == nil:
		w.Write(raw)
	case r.URL.Path == "/api/search":
		w.Write([]byte(`[]`))
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":404,"name":"TrackNotFound","message":"Failed to find specified track"}`))
	}
}
