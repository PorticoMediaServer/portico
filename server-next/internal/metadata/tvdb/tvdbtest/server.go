// Package tvdbtest serves hand-written TheTVDB answers without reaching the provider.
package tvdbtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type Server struct {
	URL                     string
	Requests, Logins, Pages atomic.Int64
	Key                     atomic.Pointer[string]
	Expire, Reject, Endless atomic.Bool
}

func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{}
	h := httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = h.URL + "/v4"
	t.Cleanup(h.Close)
	return s
}
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v4/login" && r.Method == "POST" {
		s.Logins.Add(1)
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["apikey"] == "" {
			http.Error(w, "a project key alone is required", 400)
			return
		}
		key := body["apikey"]
		s.Key.Store(&key)
		fmt.Fprint(w, `{"status":"success","data":{"token":"fixture-bearer-token"}}`)
		return
	}
	if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-bearer-token" || s.Reject.Load() || s.Expire.Swap(false) {
		http.Error(w, "key or token refused", 401)
		return
	}
	var data any
	next := ""
	switch r.URL.Path {
	case "/v4/search":
		if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("type") != "series" {
			http.Error(w, "wrong search", 400)
			return
		}
		if r.URL.Query().Get("remote_id") != "" && r.URL.Query().Get("remote_id") != "tt1234567" {
			data = []any{}
		} else {
			data = []any{map[string]any{"tvdb_id": "42", "type": "series", "name": "Quiet Harbour", "year": "2024",
				"image_url": "/banners/poster.jpg", "translations": map[string]string{"fra": "Le port tranquille"}}}
		}
	case "/v4/series/slug/quiet-harbour", "/v4/movies/slug/quiet-harbour":
		data = map[string]int{"id": 42}
	case "/v4/series/42/extended", "/v4/movies/42/extended":
		if r.URL.Query().Get("meta") != "translations" {
			http.Error(w, "translations missing", 400)
			return
		}
		data = json.RawMessage(`{
   "id":42,"name":"Quiet Harbour","overview":"A coastal town.","year":"2024","firstAired":"2024-01-02",
   "originalCountry":"can","image":"/banners/poster.jpg","status":{"name":"Continuing"},
   "genres":[{"name":"Drama"}],"originalNetwork":{"name":"Harbour TV"},"companies":[],
   "contentRatings":[{"name":"TV-14","country":"usa"},{"name":"14+","country":"can"}],
   "translations":{"nameTranslations":[{"language":"fra","name":"Le port tranquille"}],
    "overviewTranslations":[{"language":"fra","overview":"Une ville côtière."}]},
   "characters":[{"peopleId":9,"personName":"Alex Shore","name":"Pat","peopleType":"Actor","sort":0}],
   "remoteIds":[{"id":"tt1234567","sourceName":"IMDB"}],"seasons":[{"number":1}],
   "artworks":[{"type":3,"image":"/banners/backdrop.jpg","language":"fra","width":1920,"height":1080}]
  }`)
	case "/v4/artwork/types":
		data = []any{map[string]any{"id": 3, "name": "Series - Background"}}
	default:
		if !strings.HasPrefix(r.URL.Path, "/v4/series/42/episodes/") {
			http.NotFound(w, r)
			return
		}
		s.Pages.Add(1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if strings.Contains(r.URL.Path, "/dvd/") {
			data = map[string]any{"series": map[string]int{"id": 42}, "episodes": []any{}}
			break
		}
		if !strings.HasSuffix(r.URL.Path, "/fra") || r.URL.Query().Get("season") != "1" {
			http.Error(w, "wrong season or language", 400)
			return
		}
		if page == 0 || s.Endless.Load() {
			next = "https://untrusted.invalid/not-followed"
		}
		data = map[string]any{"series": map[string]int{"id": 42}, "episodes": []any{
			map[string]any{"id": 10000 + page, "seriesId": 42, "name": "Another season", "number": 7, "seasonNumber": 2},
			map[string]any{"id": 100 + page, "seriesId": 42, "name": fmt.Sprintf("Chapter %d", page+1), "number": page + 1, "seasonNumber": 1,
				"aired": "2024-01-02", "image": "/banners/episode.jpg"},
		}}
	}
	json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data, "links": map[string]any{"next": next}})
}
