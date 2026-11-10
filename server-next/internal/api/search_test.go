package api

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// An app can search every kind at once, page through one kind, read suggestions and keep its
// recent searches; a profile limited by rating is never shown what it may not see; and a
// search is refused, in the one error format, when it is malformed or the index is not ready.
func TestSearch(t *testing.T) {
	s, token, limited := seeded(t)
	libraries := get[Libraries](t, s, token, "/v1/libraries")
	list := "/v1/items?library=" + libraries.Items[0].ID + "&kind=movie&limit=200"
	var hidden Item
	for _, film := range get[ItemPage](t, s, token, list).Items {
		if w := call(t, s, "GET", "/v1/items/"+film.ID, limited, nil, nil); w.Code == 404 {
			hidden = film
			break
		}
	}
	if hidden.ID == "" {
		t.Fatal("no film is hidden from the limited profile")
	}
	search := "/v1/search?q=" + url.QueryEscape(hidden.Title)

	// Before the index exists a search says so and asks to be tried again.
	var refused errorBody
	if w := call(t, s, "GET", search, token, nil, &refused); w.Code != 503 || refused.Error.Code != "search_unavailable" || !refused.Error.Retry ||
		w.Header().Get("Retry-After") == "" {
		t.Fatalf("a search with no index answered %d %+v", w.Code, refused.Error)
	}
	s.db.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.db.SearchCaughtUp(context.Background()); err != nil {
		t.Fatal(err)
	}

	shown := func(token, path string) (films []string, total int) {
		t.Helper()
		results := get[SearchResults](t, s, token, path)
		if len(results.Groups) != 9 || results.Groups[0].Kind != "movie" || results.Groups[3].Kind != "person" {
			t.Fatalf("groups: %+v", results.Groups)
		}
		for _, item := range results.Groups[0].Items {
			films = append(films, item.ID)
		}
		return films, *results.Groups[0].Total
	}
	films, total := shown(token, search+"&record=true")
	if len(films) == 0 || films[0] != hidden.ID || total < 1 {
		t.Fatalf("searching for %q found %v", hidden.Title, films)
	}
	if fewer, fewerTotal := shown(limited, search); strings.Contains(strings.Join(fewer, " "), hidden.ID) || fewerTotal >= total {
		t.Fatalf("the limited profile was shown %v (%d of %d)", fewer, fewerTotal, total)
	}

	// One kind, a result at a time, to its end; an episode says which show it is from.
	word := strings.Fields(hidden.Title)[0]
	page := "/v1/search?kind=episode&limit=1&q=" + url.QueryEscape(word[:2])
	first := get[SearchResults](t, s, token, page)
	group := first.Groups[2]
	if len(first.Groups) != 9 || len(group.Items) != 1 || group.Items[0].Root == nil || group.Items[0].Root.Kind != "show" || group.NextCursor == "" {
		t.Fatalf("the first episode page: %+v", group)
	}
	seen := 1
	for next := group.NextCursor; next != ""; {
		more := get[SearchResults](t, s, token, page+"&cursor="+next)
		if len(more.Groups) != 1 || more.Groups[0].Total != nil {
			t.Fatalf("a later page: %+v", more.Groups)
		}
		seen += len(more.Groups[0].Items)
		next = more.Groups[0].NextCursor
	}
	if seen != *group.Total {
		t.Fatalf("paged through %d episodes of %d", seen, *group.Total)
	}

	suggestions := get[SearchSuggestions](t, s, token, "/v1/search/suggestions?q="+url.QueryEscape(hidden.Title))
	if len(suggestions.Items) == 0 || suggestions.Items[0].ID != hidden.ID || suggestions.Items[0].Kind != "movie" {
		t.Fatalf("suggestions: %+v", suggestions.Items)
	}
	if got := get[SearchSuggestions](t, s, limited, "/v1/search/suggestions?q="+url.QueryEscape(hidden.Title)); len(got.Items) > 0 && got.Items[0].ID == hidden.ID {
		t.Fatal("the limited profile was suggested a film it may not see")
	}

	// Only the search sent with record=true was kept, and only for the profile that sent it.
	if history := get[SearchHistory](t, s, token, "/v1/search/history"); len(history.Items) != 1 || history.Items[0].Query != hidden.Title {
		t.Fatalf("history: %+v", history.Items)
	}
	if history := get[SearchHistory](t, s, limited, "/v1/search/history"); len(history.Items) != 0 {
		t.Fatalf("another profile's history: %+v", history.Items)
	}
	if w := call(t, s, "DELETE", "/v1/search/history", token, nil, nil); w.Code != 204 {
		t.Fatalf("clearing history answered %d", w.Code)
	}
	if history := get[SearchHistory](t, s, token, "/v1/search/history"); len(history.Items) != 0 {
		t.Fatalf("history after clearing: %+v", history.Items)
	}

	for _, c := range []struct {
		path   string
		token  string
		status int
	}{
		{search, "", 401},
		{"/v1/search", token, 400},
		{"/v1/search?q=" + strings.Repeat("a", 129), token, 400},
		{"/v1/search?q=a+b+c+d+e+f+g+h+i", token, 400},
		{search + "&kind=film", token, 400},
		{search + "&sort=title", token, 400}, // a sort needs a kind
		{search + "&kind=person&sort=year", token, 400},
		{search + "&kind=movie&cursor=xyz", token, 400},
		{search + "&order=up", token, 400},
		{"/v1/search/suggestions", token, 400},
	} {
		if w := call(t, s, "GET", c.path, c.token, nil, nil); w.Code != c.status {
			t.Errorf("GET %s answered %d, want %d: %s", c.path, w.Code, c.status, w.Body)
		}
	}
}
