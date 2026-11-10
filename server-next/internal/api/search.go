package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The shapes and routes of search. A search matches whole words and the beginnings of words
// in titles and names, ignoring case, accents and punctuation; it does not correct typing
// mistakes. Nothing the caller's profile may not see is in a result or in a count.

// SearchResults is the answer of GET /v1/search: the groups in the order to show them.
type SearchResults struct {
	Groups []SearchGroup `json:"groups"`
}

// SearchGroup is the results of one kind: "movie", "show", "episode", "person", "artist",
// "album", "track", "audiobook" or "book". People is filled for "person" and Items for every
// other kind; an item carries Parent and Root, so an episode says which show it is from.
//
// Total is how many results of this kind there are. It is present on a first page, for every
// kind, and absent on later pages. It is exact unless TotalIsMinimum is true: for a profile
// with restrictions the server stops checking a very wide search at 2,000 matches of a kind
// and says "at least this many". NextCursor is present while the kind has more results; send
// it back with the same q, kind, sort and order.
type SearchGroup struct {
	Kind           string   `json:"kind"`
	Total          *int     `json:"total,omitempty"`
	TotalIsMinimum bool     `json:"totalIsMinimum,omitempty"`
	Items          []Item   `json:"items,omitempty"`
	People         []Person `json:"people,omitempty"`
	NextCursor     string   `json:"nextCursor,omitempty"`
}

// SearchSuggestions is the answer of GET /v1/search/suggestions.
type SearchSuggestions struct {
	Items []SearchSuggestion `json:"items"`
}

// SearchSuggestion is a title or a name to offer under the search field. Kind is an item
// kind or "person"; ID opens it.
type SearchSuggestion struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

// SearchHistory is the answer of GET /v1/search/history: the profile's recent searches,
// newest first, or none when the profile has turned remembering them off.
type SearchHistory struct {
	Items []SearchHistoryEntry `json:"items"`
}

// SearchHistoryEntry is one recent search.
type SearchHistoryEntry struct {
	Query string `json:"query"`
}

// searchRefusal is the answer while the index is being built: try again shortly.
func searchRefusal(err error) error {
	if errors.Is(err, db.ErrSearchUnavailable) {
		e := refusal(http.StatusServiceUnavailable, "search_unavailable", "Search is getting ready. Try again in a moment.", true)
		e.RetryAfterMs = (2 * time.Second).Milliseconds()
		return e
	}
	return refuse(err)
}

func (s *Server) searchRoutes() {
	// q is what was typed. With no kind, every group comes with its first results (limit, 12
	// by default, at most 40). With kind, that group comes a page at a time (cursor, limit),
	// best match first or by sort (title; for titles also year or added) and order (asc,
	// desc). record=true, sent when the person presses Enter or opens a result, also puts the
	// search at the top of the profile's recent searches.
	Handle(s, "GET /v1/search", WithProfile, func(r *http.Request, _ None) (SearchResults, error) {
		query := r.URL.Query()
		q := db.SearchQuery{Text: query.Get("q"), Kind: query.Get("kind"), Sort: query.Get("sort"), Cursor: query.Get("cursor")}
		var err error
		if q.Limit, err = limitParam(query); err != nil {
			return SearchResults{}, err
		}
		switch query.Get("order") {
		case "":
			q.Desc = defaultOrder[q.Sort]
		case "desc":
			q.Desc = true
		case "asc":
		default:
			return SearchResults{}, ErrField("order", "Use asc or desc.")
		}
		record, err := boolParam(query, "record")
		if err != nil {
			return SearchResults{}, err
		}
		found, err := s.db.Search(r.Context(), viewer(r), q)
		if err != nil {
			return SearchResults{}, searchRefusal(err)
		}
		if record != nil && *record && q.Cursor == "" {
			if err := s.db.RememberSearch(r.Context(), viewer(r), q.Text); err != nil {
				return SearchResults{}, err
			}
		}
		out := SearchResults{Groups: make([]SearchGroup, len(found.Groups))}
		for n, g := range found.Groups {
			group := SearchGroup{Kind: g.Kind, TotalIsMinimum: g.TotalIsMinimum, NextCursor: g.NextCursor}
			if g.Counted {
				group.Total = &g.Total
			}
			for _, item := range g.Items {
				shape := itemShape(item)
				shape.Parent, shape.Root = refShape(found.Above, item.ParentPublicID), refShape(found.Above, item.RootPublicID)
				group.Items = append(group.Items, shape)
			}
			for _, p := range g.People {
				group.People = append(group.People, Person{ID: p.PublicID, Name: p.Name, Images: imageMap(p.Images)})
			}
			out.Groups[n] = group
		}
		return out, nil
	})
	// The few best titles and names for what has been typed so far (q), matching in the
	// title or name only. limit is 8 by default and at most 20.
	Handle(s, "GET /v1/search/suggestions", WithProfile, func(r *http.Request, _ None) (SearchSuggestions, error) {
		limit, err := limitParam(r.URL.Query())
		if err != nil {
			return SearchSuggestions{}, err
		}
		found, err := s.db.SearchSuggestions(r.Context(), viewer(r), r.URL.Query().Get("q"), limit)
		if err != nil {
			return SearchSuggestions{}, searchRefusal(err)
		}
		out := SearchSuggestions{Items: make([]SearchSuggestion, len(found))}
		for n, sug := range found {
			out.Items[n] = SearchSuggestion{ID: sug.PublicID, Kind: sug.Kind, Title: sug.Title}
		}
		return out, nil
	})
	Handle(s, "GET /v1/search/history", WithProfile, func(r *http.Request, _ None) (SearchHistory, error) {
		history, err := s.db.SearchHistory(r.Context(), viewer(r))
		out := SearchHistory{Items: make([]SearchHistoryEntry, len(history))}
		for n, query := range history {
			out.Items[n] = SearchHistoryEntry{Query: query}
		}
		return out, err
	})
	// Clears the recent searches, or with query=<text> removes that one.
	Handle(s, "DELETE /v1/search/history", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, s.db.ForgetSearches(r.Context(), viewer(r), r.URL.Query().Get("query"))
	})
}

func refShape(above map[string]db.ItemRef, id string) *ItemRef {
	ref, ok := above[id]
	if !ok {
		return nil
	}
	return &ItemRef{ID: ref.PublicID, Kind: ref.Kind.String(), Title: ref.Title, Index: ref.Index}
}
