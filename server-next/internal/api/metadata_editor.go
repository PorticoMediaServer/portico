package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The metadata editor: one title's fields, lists, cast and crew and locks, with the History of
// what people changed and Undo. Owner and administrators only, like the rest of metadata.go.

// MetadataField is one field as the editor shows it.
type MetadataField struct {
	Value any `json:"value"`
	// Source is where the value came from: provider, file or edit.
	Source string `json:"source"`
	Locked bool   `json:"locked"`
}

// MetadataList is a list as the editor shows it. Labels are the owner's own and never locked.
type MetadataList struct {
	Values []string `json:"values"`
	Source string   `json:"source"`
	Locked bool     `json:"locked"`
}

// MetadataCredit is one line of Cast & crew.
type MetadataCredit struct {
	PersonID  string `json:"personId,omitempty"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role"`
	Character string `json:"character,omitempty"`
}

// MetadataCredits is Cast & crew as the editor shows it.
type MetadataCredits struct {
	Values []MetadataCredit `json:"values"`
	Source string           `json:"source"`
	Locked bool             `json:"locked"`
}

// MetadataMatch is what the title is matched to.
type MetadataMatch struct {
	// State is unmatched, matched, chosen (a person chose it), needs_attention or kept.
	State  string            `json:"state"`
	Locked bool              `json:"locked"`
	IDs    map[string]string `json:"ids"`
}

// MetadataDocument is the editor's document. Its revision is also its ETag; an edit sends it
// back in If-Match.
type MetadataDocument struct {
	ItemID   string `json:"itemId"`
	Kind     string `json:"kind"`
	Revision string `json:"revision"`
	// Fields and Lists hold only what this kind of title has.
	Fields  map[string]MetadataField `json:"fields"`
	Lists   map[string]MetadataList  `json:"lists"`
	Credits *MetadataCredits         `json:"credits,omitempty"`
	Match   MetadataMatch            `json:"match"`
}

func (d MetadataDocument) reply(h http.Header) int { etag(h, d.Revision); return 0 }

// MetadataPatch edits a title. A field left out is left alone; a field or list sent is locked.
// A list sent replaces the whole list. Lock and Unlock name fields to lock or unlock without
// changing them; LockAll and UnlockAll do every field, list and the match.
type MetadataPatch struct {
	Title         *string           `json:"title,omitempty"`
	SortTitle     *string           `json:"sortTitle,omitempty"`
	OriginalTitle *string           `json:"originalTitle,omitempty"`
	Edition       *string           `json:"edition,omitempty"`
	Tagline       *string           `json:"tagline,omitempty"`
	Summary       *string           `json:"summary,omitempty"`
	Year          *int              `json:"year,omitempty"`
	ReleaseDate   *string           `json:"releaseDate,omitempty"`
	ContentRating *string           `json:"contentRating,omitempty"`
	Genres        *[]string         `json:"genres,omitempty"`
	Studios       *[]string         `json:"studios,omitempty"`
	Networks      *[]string         `json:"networks,omitempty"`
	Countries     *[]string         `json:"countries,omitempty"`
	Labels        *[]string         `json:"labels,omitempty"`
	Credits       *[]MetadataCredit `json:"credits,omitempty"`
	Lock          []string          `json:"lock,omitempty"`
	Unlock        []string          `json:"unlock,omitempty"`
	LockAll       bool              `json:"lockAll,omitempty"`
	UnlockAll     bool              `json:"unlockAll,omitempty"`
}

// MetadataHistoryEntry is one change a person made: fields_edited, artwork_chosen,
// match_changed or undone.
type MetadataHistoryEntry struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	At   string `json:"at"`
}

// editorFields are the fields and lists the editor offers for each kind of title.
var editorFields = map[db.ItemKind]struct {
	fields, lists []string
	credits       bool
}{
	db.KindMovie: {[]string{db.FieldTitle, db.FieldSortTitle, db.FieldOriginalTitle, db.FieldEdition, db.FieldTagline, db.FieldSummary,
		db.FieldYear, db.FieldReleaseDate, db.FieldContentRating, db.FieldRating}, []string{db.FieldGenres, db.FieldStudios, db.FieldCountries, "labels"}, true},
	db.KindShow: {[]string{db.FieldTitle, db.FieldSortTitle, db.FieldOriginalTitle, db.FieldTagline, db.FieldSummary, db.FieldYear,
		db.FieldReleaseDate, db.FieldContentRating, db.FieldRating}, []string{db.FieldGenres, db.FieldStudios, db.FieldNetworks, db.FieldCountries, "labels"}, true},
	db.KindSeason: {[]string{db.FieldTitle, db.FieldSortTitle, db.FieldSummary}, []string{"labels"}, false},
	db.KindEpisode: {[]string{db.FieldTitle, db.FieldSortTitle, db.FieldSummary, db.FieldYear, db.FieldReleaseDate, db.FieldContentRating,
		db.FieldRating}, []string{"labels"}, true},
	db.KindCollection: {[]string{db.FieldTitle, db.FieldSortTitle, db.FieldSummary}, []string{"labels"}, false},
}

// otherFields are what the editor offers for music, books and the rest until their own
// fields are built.
var otherFields = struct {
	fields, lists []string
	credits       bool
}{[]string{db.FieldTitle, db.FieldSortTitle, db.FieldSummary, db.FieldYear}, []string{db.FieldGenres, "labels"}, false}

func (s *Server) editorRoutes() {
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "GET /v1/admin/items/{id}/metadata", Admin, func(r *http.Request, _ None) (MetadataDocument, error) {
		id, err := s.editable(r)
		if err != nil {
			return MetadataDocument{}, err
		}
		return s.document(r, id)
	}))
	library(Handle(s, "PATCH /v1/admin/items/{id}/metadata", Admin, s.editMetadata))
	library(Handle(s, "GET /v1/admin/items/{id}/metadata/history", Admin, func(r *http.Request, _ None) (Page[MetadataHistoryEntry], error) {
		out := Page[MetadataHistoryEntry]{Items: []MetadataHistoryEntry{}}
		id, err := s.editable(r)
		if err != nil {
			return out, err
		}
		entries, err := s.db.MetadataHistory(r.Context(), id)
		for _, e := range entries {
			out.Items = append(out.Items, MetadataHistoryEntry{ID: e.ID, Kind: e.Kind.String(), At: time.UnixMilli(e.At).UTC().Format(time.RFC3339)})
		}
		return out, refuse(err)
	}))
	library(Handle(s, "POST /v1/admin/items/{id}/metadata/history/{entryId}/undo", Admin, func(r *http.Request, _ None) (MetadataDocument, error) {
		id, err := s.editable(r)
		if err != nil {
			return MetadataDocument{}, err
		}
		if _, err := s.db.UndoMetadata(r.Context(), id, r.PathValue("entryId")); err != nil {
			return MetadataDocument{}, refuse(err)
		}
		return s.document(r, id)
	}))
}

func (s *Server) document(r *http.Request, id string) (MetadataDocument, error) {
	doc, err := s.db.MetadataDocument(r.Context(), id)
	if err != nil {
		return MetadataDocument{}, refuse(err)
	}
	locked := map[string]bool{}
	for _, f := range doc.Locked {
		locked[f] = true
	}
	// Where a value came from: a person's edit, the title's files, or the provider once the
	// title is matched. A field can be locked whatever its source.
	source := func(field string) string {
		switch {
		case doc.Sources[field] == db.SourceEdit:
			return "edit"
		case doc.Sources[field] == db.SourceFile:
			return "file"
		case doc.Match == db.MatchFound || doc.Match == db.MatchChosen:
			return "provider"
		}
		return "file"
	}
	offer, ok := editorFields[doc.Kind]
	if !ok {
		offer = otherFields
	}
	out := MetadataDocument{ItemID: id, Kind: doc.Kind.String(), Revision: strconv.FormatInt(doc.Revision, 10),
		Fields: map[string]MetadataField{}, Lists: map[string]MetadataList{},
		Match: MetadataMatch{State: doc.Match.String(), Locked: locked[db.FieldMatch] || doc.Match == db.MatchChosen, IDs: map[string]string{}}}
	for _, f := range offer.fields {
		out.Fields[f] = MetadataField{Value: doc.Fields[f], Source: source(f), Locked: locked[f]}
	}
	for _, l := range offer.lists {
		list := MetadataList{Values: doc.Lists[l], Source: source(l), Locked: locked[l]}
		if l == "labels" {
			list.Source = "edit"
		}
		out.Lists[l] = list
	}
	if offer.credits {
		out.Credits = &MetadataCredits{Values: []MetadataCredit{}, Source: source(db.FieldCredits), Locked: locked[db.FieldCredits]}
		for _, c := range doc.Credits {
			out.Credits.Values = append(out.Credits.Values, MetadataCredit{PersonID: c.Person, Name: c.Name, Role: c.Role.String(), Character: c.Character})
		}
	}
	for _, ext := range doc.ExternalIDs {
		out.Match.IDs[ext.Provider] = ext.ID
	}
	return out, nil
}

func (s *Server) editMetadata(r *http.Request, req MetadataPatch) (MetadataDocument, error) {
	id, err := s.editable(r)
	if err != nil {
		return MetadataDocument{}, err
	}
	header := strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`)
	if header == "" {
		return MetadataDocument{}, refusal(http.StatusPreconditionRequired, "if_match_required", "Send the title's revision in If-Match.", false)
	}
	revision, _ := strconv.ParseInt(header, 10, 64)
	edit := db.MetadataEdit{Title: req.Title, SortTitle: req.SortTitle, OriginalTitle: req.OriginalTitle, Edition: req.Edition, Tagline: req.Tagline,
		Summary: req.Summary, Year: req.Year, ReleaseDate: req.ReleaseDate, ContentRating: req.ContentRating, Genres: req.Genres, Studios: req.Studios,
		Networks: req.Networks, Countries: req.Countries, Labels: req.Labels, Lock: req.Lock, Unlock: req.Unlock, LockAll: req.LockAll, UnlockAll: req.UnlockAll}
	if req.Credits != nil {
		credits := make([]db.CreditEdit, len(*req.Credits))
		for i, c := range *req.Credits {
			credits[i] = db.CreditEdit{Person: c.PersonID, Name: c.Name, Role: c.Role, Character: c.Character}
		}
		edit.Credits = &credits
	}
	_, err = s.db.EditMetadata(r.Context(), id, revision, edit)
	switch {
	case errors.Is(err, db.ErrStale):
		return MetadataDocument{}, refusal(http.StatusPreconditionFailed, "stale", "This title changed. Reload and try again.", false)
	case errors.Is(err, db.ErrInvalid):
		// The data layer names the field and its limit: "year: 0 to 9999".
		message := strings.TrimPrefix(err.Error(), db.ErrInvalid.Error()+": ")
		field, limit, _ := strings.Cut(message, ": ")
		e := refusal(http.StatusUnprocessableEntity, "invalid_value", "Check the highlighted field.", false)
		e.Fields = []Field{{Path: field, Code: "invalid", Message: strings.TrimSpace(limit)}}
		return MetadataDocument{}, e
	case err != nil:
		return MetadataDocument{}, refuse(err)
	}
	return s.document(r, id)
}
