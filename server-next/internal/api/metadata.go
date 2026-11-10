package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// Metadata and matching: the owner's and administrators' routes that look titles up at the
// library's provider and put a wrong match right. Each route first checks that the caller's own
// profile may see the title, so an administrator whose profile has a limit never learns of a
// hidden title here either.

// MatchCandidate is one title at the provider a person can choose as a title's match.
type MatchCandidate struct {
	Provider      string `json:"provider"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle,omitempty"`
	Year          int    `json:"year,omitempty"`
	Overview      string `json:"overview,omitempty"`
	// Exact is true for the one title an id named.
	Exact bool `json:"exact,omitempty"`
}

// MatchCandidates are the possible matches for a title, best first.
type MatchCandidates struct {
	Items []MatchCandidate `json:"items"`
}

// UnmatchedEntry is one line of a library's Unmatched items list.
type UnmatchedEntry struct {
	ItemID string `json:"itemId"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Year   int    `json:"year,omitempty"`
	// Reason is no_results (the provider found nothing for the name), no_close_match (nothing
	// close enough to the name and year), several_titles (more than one fits equally well) or
	// id_not_found (the id in the name or an NFO file is not the provider's).
	Reason string `json:"reason"`
	// Matches are up to three closest answers, as they were when the title was looked up.
	Matches []MatchCandidate `json:"matches"`
}

// MatchRequest uses a match for a title, or keeps the title as it is. Provider and ID are as
// GET /v1/admin/items/{id}/matches answered them.
type MatchRequest struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
	// KeepAsIs takes an unmatched title off Unmatched items until its files change.
	KeepAsIs bool `json:"keepAsIs,omitempty"`
	// For a show: the episode order its files are numbered in, aired (the default), dvd or
	// absolute, and a whole number added to every episode number the files give before the
	// provider is asked (files numbered from 101 for a provider that numbers from 1: -100).
	// Both are kept with the match through every refresh.
	EpisodeOrder  string `json:"episodeOrder,omitempty"`
	EpisodeOffset int    `json:"episodeOffset,omitempty"`
}

// MergeRequest names the other title, which is merged into the one in the address.
type MergeRequest struct {
	ItemID string `json:"itemId"`
}

// MatchResult is the title an action left: the one in the address, or another when two became one
// or a version was split off.
type MatchResult struct {
	ItemID string `json:"itemId"`
}

// MetadataRefreshed says whether the title now has a match. A matched title is asked about
// again; an unmatched one is looked up again from its name and year.
type MetadataRefreshed struct {
	Matched bool `json:"matched"`
}

func (s *Server) metadataRoutes() {
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "GET /v1/admin/libraries/{id}/unmatched", Admin, s.unmatched))
	library(Handle(s, "GET /v1/admin/items/{id}/matches", Admin, s.matches))
	library(Handle(s, "PUT /v1/admin/items/{id}/match", Admin, s.setMatch))
	library(Handle(s, "POST /v1/admin/items/{id}/metadata/refresh", Admin, s.refreshMetadata))
	library(Handle(s, "POST /v1/admin/items/{id}/merge", Admin, s.merge))
	library(Handle(s, "POST /v1/admin/versions/{id}/split", Admin, s.split))
}

// metadataError turns the matcher's refusals into the API's.
func metadataError(err error) error {
	switch {
	case errors.Is(err, metadata.ErrNoProvider):
		return ErrConflict("no_provider", "This title is not looked up online: its library uses local metadata only, or it follows the title it belongs to.")
	case errors.Is(err, metadata.ErrMatchLocked):
		return ErrConflict("match_locked", "A person chose this title's match. Choose another match to change it.")
	case errors.Is(err, metadata.ErrUnknownRef), errors.Is(err, metadata.ErrNotFound):
		return refusal(http.StatusUnprocessableEntity, "unknown_ref", "The provider has no title with that id or address.", false)
	case errors.Is(err, metadata.ErrUnknownOrder):
		return ErrField("episodeOrder", "Use aired, dvd or absolute.")
	case errors.Is(err, metadata.ErrNotAShow):
		return refusal(http.StatusUnprocessableEntity, "not_a_show", "Only a show has an episode order and offset.", false)
	case errors.Is(err, metadata.ErrWrongKind):
		return refusal(http.StatusUnprocessableEntity, "wrong_kind", "That address is of a film and this is a show, or the other way round.", false)
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrInvalid):
		return refuse(err)
	case errors.Is(err, db.ErrConflict):
		return ErrConflict("conflict", "This cannot be done to this title as it is now.")
	case err != nil:
		// The rest is the provider not answering. Nothing was changed.
		return refusal(http.StatusBadGateway, "provider_unavailable", "The metadata provider did not answer. Try again later.", true)
	}
	return nil
}

func (s *Server) unmatched(r *http.Request, _ None) (Page[UnmatchedEntry], error) {
	out := Page[UnmatchedEntry]{Items: []UnmatchedEntry{}}
	l, err := s.db.Library(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return out, refuse(err)
	}
	query := r.URL.Query()
	limit, err := limitParam(query)
	if err != nil {
		return out, err
	}
	if limit == 0 || limit > 100 {
		limit = 100
	}
	var after *db.UnmatchedItem
	if cursor := query.Get("cursor"); cursor != "" {
		if after, err = db.UnmatchedAfter(cursor); err != nil {
			return out, ErrField("cursor", "This cursor does not belong to this list.")
		}
	}
	lines, err := s.db.Unmatched(r.Context(), viewer(r), l.ID, after, limit+1)
	if err != nil {
		return out, err
	}
	if len(lines) > limit {
		lines = lines[:limit]
		out.NextCursor = lines[limit-1].Cursor()
	}
	for _, line := range lines {
		entry := UnmatchedEntry{ItemID: line.PublicID, Kind: line.Kind.String(), Title: line.Title, Year: line.Year, Reason: line.Reason,
			Matches: []MatchCandidate{}}
		json.Unmarshal([]byte(line.Matches), &entry.Matches)
		out.Items = append(out.Items, entry)
	}
	return out, nil
}

// editable checks that the caller's profile may see the title in the address.
func (s *Server) editable(r *http.Request) (string, error) {
	id := r.PathValue("id")
	return id, refuse(s.db.SeesItem(r.Context(), viewer(r), id))
}

func (s *Server) matches(r *http.Request, _ None) (MatchCandidates, error) {
	out := MatchCandidates{Items: []MatchCandidate{}}
	id, err := s.editable(r)
	if err != nil {
		return out, err
	}
	query := r.URL.Query()
	year := 0
	if text := query.Get("year"); text != "" {
		if year, err = strconv.Atoi(text); err != nil || year < 0 || year > 9999 {
			return out, ErrField("year", "Use a year from 0 to 9999.")
		}
	}
	found, err := s.metadata.Matches(r.Context(), id, query.Get("q"), year, query.Get("ref"))
	if err != nil {
		return out, metadataError(err)
	}
	for _, c := range found {
		// The provider's picture addresses are not passed on: an app never contacts a provider.
		out.Items = append(out.Items, MatchCandidate{Provider: c.Ref.Provider, ID: c.Ref.ID, Title: c.Title, OriginalTitle: c.OriginalTitle,
			Year: c.Year, Overview: c.Overview, Exact: c.Exact})
	}
	return out, nil
}

func (s *Server) setMatch(r *http.Request, req MatchRequest) (MatchResult, error) {
	id, err := s.editable(r)
	if err != nil {
		return MatchResult{}, err
	}
	switch {
	case req.KeepAsIs && req.ID != "":
		return MatchResult{}, ErrInvalid("Send a match or Keep as is, not both.")
	case req.KeepAsIs:
		if err := s.db.KeepAsIs(r.Context(), id); errors.Is(err, db.ErrConflict) {
			return MatchResult{}, ErrConflict("already_matched", "This title is matched, so there is nothing to keep as it is.")
		} else if err != nil {
			return MatchResult{}, refuse(err)
		}
		return MatchResult{ItemID: id}, nil
	case req.ID == "":
		return MatchResult{}, ErrField("id", "Name the match to use, as the list of matches gave it.")
	}
	order := req.EpisodeOrder
	if order == "aired" {
		order = metadata.OrderAired
	}
	if req.EpisodeOffset < -9999 || req.EpisodeOffset > 9999 {
		return MatchResult{}, ErrField("episodeOffset", "Use a whole number from -9999 to 9999.")
	}
	remaining, err := s.metadata.IdentifyShow(r.Context(), id, metadata.Ref{Provider: req.Provider, ID: req.ID, Order: order}, req.EpisodeOffset)
	if err != nil && remaining == "" {
		return MatchResult{}, metadataError(err)
	}
	// The title's own details are in. A show's episodes that the provider did not answer for
	// keep what they had and are fetched at the next refresh.
	return MatchResult{ItemID: remaining}, nil
}

func (s *Server) refreshMetadata(r *http.Request, _ None) (MetadataRefreshed, error) {
	id, err := s.editable(r)
	if err != nil {
		return MetadataRefreshed{}, err
	}
	matched, err := s.metadata.Refresh(r.Context(), id)
	if err == nil && !matched {
		// "Look it up again" for a title with no match.
		matched, err = s.metadata.Rematch(r.Context(), id)
	}
	return MetadataRefreshed{Matched: matched}, metadataError(err)
}

func (s *Server) merge(r *http.Request, req MergeRequest) (MatchResult, error) {
	id, err := s.editable(r)
	if err != nil {
		return MatchResult{}, err
	}
	if req.ItemID == "" {
		return MatchResult{}, ErrField("itemId", "Name the title to merge into this one.")
	}
	if err := s.db.SeesItem(r.Context(), viewer(r), req.ItemID); err != nil {
		return MatchResult{}, refuse(err)
	}
	return MatchResult{ItemID: id}, refuse(s.db.MergeItems(r.Context(), req.ItemID, id))
}

func (s *Server) split(r *http.Request, _ None) (MatchResult, error) {
	if err := s.db.SeesVersion(r.Context(), viewer(r), r.PathValue("id")); err != nil {
		return MatchResult{}, refuse(err)
	}
	created, err := s.db.SplitVersion(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrConflict) {
		return MatchResult{}, ErrConflict("only_version", "This is the title's only version, so there is nothing to split it from.")
	}
	return MatchResult{ItemID: created}, refuse(err)
}
