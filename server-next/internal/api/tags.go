package api

import "net/http"

// TagPage is one page of tags, by kind and then name.
type TagPage struct {
	Items      []Tag  `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (s *Server) tagRoutes() {
	// The tags on the server: for a screen that chooses among them, such as the labels a
	// profile is kept from. The owner and admins get every tag in use; anyone else only the
	// tags on titles they may see, and never a label they are kept from.
	Handle(s, "GET /v1/tags", WithProfile, func(r *http.Request, _ None) (TagPage, error) {
		query := r.URL.Query()
		limit, err := limitParam(query)
		if err != nil {
			return TagPage{}, err
		}
		c := CallerFrom(r.Context())
		page, err := s.db.VisibleTags(r.Context(), viewer(r), c.Role == "owner" || c.Role == "admin", query.Get("kind"), query.Get("cursor"), limit)
		if err != nil {
			return TagPage{}, refuse(err)
		}
		out := TagPage{Items: make([]Tag, len(page.Tags)), NextCursor: page.NextCursor}
		for n, t := range page.Tags {
			out.Items[n] = Tag{ID: t.PublicID, Kind: t.Kind, Name: t.Name}
		}
		return out, nil
	})
}
