package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// GenreList is the answer of GET /v1/genres: the genres of the films and shows the profile may
// see, across every library it may see, by name, at most 200. Search shows them before
// anything is typed; opening one is GET /v1/items?tag={value}.
type GenreList struct {
	Genres []GenreEntry `json:"genres"`
}

// GenreEntry is one genre. Cover is the backdrop of one of its titles the profile may see, the
// same one on every read until the library changes; a genre whose titles have none has no
// cover. Count, only with counts=true, is how many films and shows the profile may see carry
// it. Counting grows with the library, so an app draws the genres from the plain read at once
// and fills the counts from a second read with counts=true when it returns.
type GenreEntry struct {
	Value string            `json:"value"`
	Label string            `json:"label"`
	Cover map[string]string `json:"cover,omitempty"`
	Count *int              `json:"count,omitempty"`
	// Posters are up to two pictures, each of a different title the profile may see that
	// carries the genre, the same two on every read until the library changes: a film's or
	// show's poster, or for one library of music or books its titles' covers. A phone's genre
	// tile draws them. Always present, empty when there are none.
	Posters []map[string]string `json:"posters"`
}

func (s *Server) genreRoutes() {
	Handle(s, "GET /v1/genres", WithProfile, func(r *http.Request, _ None) (GenreList, error) {
		counts, err := boolParam(r.URL.Query(), "counts")
		if err != nil {
			return GenreList{}, err
		}
		withCounts := counts != nil && *counts
		// With library, that one library's genres, whatever its kind: a music library's Genres tab.
		genres, err := s.db.Genres(r.Context(), viewer(r), r.URL.Query().Get("library"), withCounts)
		if err != nil {
			return GenreList{}, refuse(err)
		}
		out := GenreList{Genres: make([]GenreEntry, len(genres))}
		for n, g := range genres {
			out.Genres[n] = GenreEntry{Value: g.PublicID, Label: g.Name, Posters: []map[string]string{}}
			for _, hash := range g.Posters {
				out.Genres[n].Posters = append(out.Genres[n].Posters, imageMap([]db.Image{{Role: g.PosterRole, FileHash: hash}}))
			}
			if g.CoverHash != "" {
				out.Genres[n].Cover = imageMap([]db.Image{{Role: g.CoverRole, FileHash: g.CoverHash}})
			}
			// One library's genres come with their counts whether asked or not: the count is how
			// they are found.
			if withCounts || r.URL.Query().Get("library") != "" {
				out.Genres[n].Count = &g.Count
			}
		}
		return out, nil
	})
}
