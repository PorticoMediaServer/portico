package catalog

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const SearchGenresMaximumLimit = 50

// SearchGenreArt references selected, ready bytes on an already authorized title.
// Path is a relative metadata route, never a provider or filesystem location.
type SearchGenreArt struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type SearchGenre struct {
	Value    string           `json:"value"`
	Label    string           `json:"label"`
	Count    int              `json:"count"`
	Backdrop *SearchGenreArt  `json:"backdrop,omitempty"`
	Covers   []SearchGenreArt `json:"covers"`
}

type SearchGenrePage struct {
	QueryID        string        `json:"queryId"`
	PreviousCursor string        `json:"previousCursor,omitempty"`
	ReloadCursor   string        `json:"reloadCursor,omitempty"`
	HasGenres      *bool         `json:"hasGenres,omitempty"`
	Revision       string        `json:"revision"`
	Genres         []SearchGenre `json:"genres"`
	NextCursor     string        `json:"nextCursor,omitempty"`
	release        func()
}

type SearchGenreRequest struct {
	Preview      string
	LimitPresent bool
	Limit        int
	Cursor       string
}

// Release retains hydrated delivery memory until the compatibility caller has
// finished serializing the page. New HTTP uses SearchGenreResult.Release.
func (p SearchGenrePage) Release() {
	if p.release != nil {
		p.release()
	}
}

type searchGenreCallerKey struct{}

// WithSearchGenreCaller supplies the authenticated private caller for the old
// Service signature. A profile identifier alone is not a caller identity.
func WithSearchGenreCaller(ctx context.Context, caller readprep.Caller) context.Context {
	return context.WithValue(ctx, searchGenreCallerKey{}, caller)
}

// SearchGenres delegates to the explicitly installed maintained runtime. Cold
// reads return a typed preparation; composition owns the shared worker/store.
func (s *Service) SearchGenres(viewer Viewer, request SearchGenreRequest) (SearchGenrePage, error) {
	empty := SearchGenrePage{Genres: []SearchGenre{}}
	if request.Limit < 0 || request.Limit > SearchGenresMaximumLimit {
		return empty, ErrFacetBaseInput
	}
	r := s.GenreRuntime()
	caller, _ := s.Context().Value(searchGenreCallerKey{}).(readprep.Caller)
	if r == nil || caller == "" {
		return empty, ErrFacetBaseUnavailable
	}
	out, err := r.Initial(s.Context(), caller, viewer, request)
	if err != nil {
		out.Release()
		return empty, err
	}
	if out.Page != nil {
		page := *out.Page
		page.release = out.release
		return page, nil
	}
	defer out.Release()
	if out.Preparing != nil {
		return empty, &SearchGenrePreparingError{Preparation: *out.Preparing}
	}
	if out.Fallback != nil {
		return empty, &SearchGenrePreparingError{Preparation: out.Fallback.Preparation}
	}
	return empty, ErrFacetBaseUnavailable
}
