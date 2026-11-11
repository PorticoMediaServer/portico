// Package tmdbtest is a stand-in for TMDB that answers from files, so no test asks the real one.
//
// The files in answers/ are written by hand in the shape TMDB's API documents, cut down to
// the fields the client reads, for a handful of real titles. They were not captured from the
// live service: nothing in this repository's tests, and nothing on a development machine,
// calls it.
package tmdbtest

import (
	"bytes"
	"embed"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"
)

//go:embed answers/*.json
var answers embed.FS

// Picture is what the stand-in answers for every picture address: a 4 by 6 PNG.
var Picture = func() []byte {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 6)))
	return buf.Bytes()
}()

// Server answers like TMDB. Requests counts every request it was sent. Down makes it answer
// 503 to everything, as TMDB does when it is unwell.
type Server struct {
	URL      string
	Requests atomic.Int64
	Down     atomic.Bool
}

// New starts a stand-in that stops when the test ends.
func New(t *testing.T) *Server {
	s := &Server{}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

// file is the answer file for a request: "/3/movie/603" is movie_603.json, and a search for
// "The Matrix" among films is search_movie_the-matrix.json.
func file(r *http.Request) string {
	name := strings.ReplaceAll(strings.Trim(strings.TrimPrefix(r.URL.Path, "/3"), "/"), "/", "_")
	if query := r.URL.Query().Get("query"); query != "" {
		slug := strings.Map(func(c rune) rune {
			if unicode.IsLetter(c) || unicode.IsDigit(c) {
				return unicode.ToLower(c)
			}
			return '-'
		}, query)
		name += "_" + strings.Trim(slug, "-")
	}
	return "answers/" + name + ".json"
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/t/p/") {
		// A picture, which TMDB serves without a token: the same small one for every address.
		w.Header().Set("Content-Type", "image/png")
		w.Write(Picture)
		return
	}
	switch {
	case s.Down.Load():
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	case !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false,"status_code":7,"status_message":"Invalid API key: You must be granted a valid key."}`))
		return
	}
	raw, err := answers.ReadFile(file(r))
	switch {
	case err == nil:
		w.Write(raw)
	case strings.HasPrefix(r.URL.Path, "/3/search/"):
		w.Write([]byte(`{"page":1,"results":[],"total_pages":1,"total_results":0}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"success":false,"status_code":34,"status_message":"The resource you requested could not be found."}`))
	}
}
