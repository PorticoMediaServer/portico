package api

import (
	"net/http"
	"strings"
)

// TagPage is one page of tags, by kind and then name.
type TagPage struct {
	Items      []Tag  `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (s *Server) tagRoutes() {
	// The Tags on the server: for a screen that chooses among them, such as the Tags a
	// profile is kept from. The owner and admins get every Tag in use; anyone else only the
	// Tags on titles they may see, and never a Tag they are kept from.
	Handle(s, "GET /v1/tags", WithProfile, func(r *http.Request, _ None) (TagPage, error) {
		query := r.URL.Query()
		limit, err := limitParam(query)
		if err != nil {
			return TagPage{}, err
		}
		c := CallerFrom(r.Context())
		var only []string
		if query.Has("ids") {
			only = strings.Split(query.Get("ids"), ",")
			if len(only) > 200 {
				return TagPage{}, ErrField("ids", "Read up to 200 selected tags at a time.")
			}
			for _, id := range only {
				if id == "" || len(id) > 128 {
					return TagPage{}, ErrField("ids", "Choose a tag from this server.")
				}
			}
		}
		page, err := s.db.VisibleTagsMatching(r.Context(), viewer(r), c.Role == "owner" || c.Role == "admin", query.Get("kind"), query.Get("cursor"), limit, only)
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
