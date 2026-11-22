package api

import (
	"net/http"
)

// SelectionCounts is the answer of GET /v1/items/selection: what "select all" over a list
// stands for. Kinds holds, for each kind in the list, how many titles the viewer sees and how
// many of them are available (none of their files missing), counted under the same condition
// as the list's pages. A title's own page offers Play by whether it is available; every other
// action a selection offers depends only on the kind and on the person and app, so an app
// works out each action's count from these with the same rule it uses for one title.
type SelectionCounts struct {
	Total int                  `json:"total"`
	Kinds []SelectionKindCount `json:"kinds"`
}

// SelectionKindCount is one kind's count.
type SelectionKindCount struct {
	Kind      string `json:"kind"`
	Count     int    `json:"count"`
	Available int    `json:"available"`
}

func (s *Server) selectionCountRoutes() {
	// The same list parameters as GET /v1/items; sort, cursor and limit are ignored. It reads
	// every title of the list, so an app asks when someone selects, not with every page.
	Handle(s, "GET /v1/items/selection", WithProfile, func(r *http.Request, _ None) (SelectionCounts, error) {
		q, err := itemQuery(r.URL.Query())
		if err != nil {
			return SelectionCounts{}, err
		}
		counts, err := s.db.SelectionCounts(r.Context(), viewer(r), q)
		if err != nil {
			return SelectionCounts{}, refuse(err)
		}
		out := SelectionCounts{Kinds: make([]SelectionKindCount, len(counts))}
		for n, c := range counts {
			out.Kinds[n] = SelectionKindCount{Kind: c.Kind.String(), Count: c.Count, Available: c.Available}
			out.Total += c.Count
		}
		return out, nil
	})
}
