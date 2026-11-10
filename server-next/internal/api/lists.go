package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// List is a playlist, a collection or a saved view. Its entries are read with
// GET /v1/items?list={id}: in the list's own order (the default, or sort=order) each item
// carries its entryId, which moving and removing name. Revision is also the list's ETag; an
// edit may send it back in If-Match, and one made on an out-of-date copy is refused with 412.
type List struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Owner is the profile that made the list.
	Owner ListOwner `json:"owner"`
	// Role is owner, editor (may change the entries) or viewer.
	Role string `json:"role"`
	// Count is how many entries the asking profile may see.
	Count int `json:"count"`
	// Covers are the pictures of the first entries, up to four, for the list's tile.
	Covers    []map[string]string `json:"covers"`
	Revision  string              `json:"revision"`
	CreatedAt string              `json:"createdAt"`
	UpdatedAt string              `json:"updatedAt"`
}

func (l List) reply(h http.Header) int { etag(h, l.Revision); return 0 }

// ListOwner names the profile a list belongs to.
type ListOwner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListCreate makes a list. Items are the titles it starts with: a show, season, album or
// artist put in a playlist puts in what plays under it.
type ListCreate struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Items       []string `json:"items,omitempty"`
}

// ListCreated is a new list; a repeat of the same Idempotency-Key answers the same list.
type ListCreated struct{ List }

func (l ListCreated) reply(h http.Header) int { etag(h, l.Revision); return http.StatusCreated }

// ListEdit renames or describes a list. Only its maker may.
type ListEdit struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ListAdd adds titles, at the end or just after the entry After. Putting back a removed
// entry (Undo) is adding its title after the entry that was in front of it.
type ListAdd struct {
	Items []string `json:"items"`
	After string   `json:"after,omitempty"`
}

// ListAdded is the list after an addition. Already counts titles a collection held already,
// which were not added again.
type ListAdded struct {
	List    List `json:"list"`
	Added   int  `json:"added"`
	Already int  `json:"already"`
}

func (l ListAdded) reply(h http.Header) int { etag(h, l.List.Revision); return 0 }

// ListMove moves an entry to just before or just after another.
type ListMove struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// ListShare is a profile a playlist is shared with, or could be. Person is the name of the
// main profile of the account it belongs to, so two profiles called "Kids" can be told apart.
type ListShare struct {
	ProfileID string `json:"profileId"`
	Name      string `json:"name,omitempty"`
	Person    string `json:"person,omitempty"`
	// CanEdit lets them add, move and remove entries. Renaming, sharing and deleting stay
	// with the maker.
	CanEdit bool `json:"canEdit"`
}

// ListShares is who a playlist is shared with, and People everyone else on the server it
// could be shared with.
type ListShares struct {
	Shares []ListShare `json:"shares"`
	People []ListShare `json:"people"`
}

// ListSharesSet is everyone a playlist is to be shared with, at most 100. Profiles left out
// stop seeing it.
type ListSharesSet struct {
	Shares []ListShare `json:"shares"`
}

func (s *Server) listRoutes() {
	Handle(s, "GET /v1/lists", WithProfile, s.lists)
	Handle(s, "POST /v1/lists", WithProfile, s.createList)
	Handle(s, "GET /v1/lists/{id}", WithProfile, func(r *http.Request, _ None) (List, error) {
		l, err := s.db.List(r.Context(), viewer(r), r.PathValue("id"))
		return listShape(l), listError(err)
	})
	Handle(s, "PATCH /v1/lists/{id}", WithProfile, s.editList)
	Handle(s, "DELETE /v1/lists/{id}", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, listError(s.db.DeleteList(r.Context(), viewer(r), r.PathValue("id")))
	})
	Handle(s, "POST /v1/lists/{id}/entries", WithProfile, s.addToList)
	Handle(s, "PATCH /v1/lists/{id}/entries/{entryId}", WithProfile, s.moveInList)
	Handle(s, "GET /v1/lists/{id}/shares", WithProfile, func(r *http.Request, _ None) (ListShares, error) {
		shares, people, err := s.db.ListShares(r.Context(), viewer(r), r.PathValue("id"))
		return ListShares{Shares: listShareShapes(shares), People: listShareShapes(people)}, listError(err)
	})
	Handle(s, "PUT /v1/lists/{id}/shares", WithProfile, func(r *http.Request, req ListSharesSet) (List, error) {
		if len(req.Shares) > db.MaxListShares {
			return List{}, ErrField("shares", "A playlist is shared with at most 100 people.")
		}
		revision, err := listIfMatch(r)
		if err != nil {
			return List{}, err
		}
		shares := make([]db.ListShare, len(req.Shares))
		for n, share := range req.Shares {
			shares[n] = db.ListShare{ProfilePublicID: share.ProfileID, CanEdit: share.CanEdit}
		}
		l, err := s.db.SetListShares(r.Context(), viewer(r), r.PathValue("id"), revision, shares, s.now())
		return listShape(l), listError(err)
	})
	Handle(s, "DELETE /v1/lists/{id}/entries/{entryId}", WithProfile, func(r *http.Request, _ None) (List, error) {
		revision, err := listIfMatch(r)
		if err != nil {
			return List{}, err
		}
		l, err := s.db.RemoveFromList(r.Context(), viewer(r), r.PathValue("id"), revision, db.ParseEntryID(r.PathValue("entryId")), s.now())
		return listShape(l), listError(err)
	})
}

func listError(err error) error {
	switch {
	case errors.Is(err, db.ErrStale):
		return refusal(http.StatusPreconditionFailed, "list_changed", "This list changed. Reload and try again.", false)
	case errors.Is(err, db.ErrNotAllowed):
		return refusal(http.StatusForbidden, "forbidden", "You can't change this list.", false)
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, db.ErrQueueFull):
		return refusal(http.StatusUnprocessableEntity, "too_many_titles", "That is more than 10,000 titles at once.", false)
	}
	return refuse(err)
}

func listShape(l db.List) List {
	role := "viewer"
	switch {
	case l.Owned:
		role = "owner"
	case l.CanEdit:
		role = "editor"
	}
	out := List{ID: l.PublicID, Kind: l.Kind, Name: l.Name, Description: l.Description, Owner: ListOwner{ID: l.OwnerPublicID, Name: l.OwnerName},
		Role: role, Count: l.Count, Covers: []map[string]string{}, Revision: strconv.FormatInt(l.Revision, 10),
		CreatedAt: timeString(l.CreatedAt), UpdatedAt: timeString(l.UpdatedAt)}
	for _, cover := range l.Covers {
		out.Covers = append(out.Covers, imageMap(cover.Images))
	}
	return out
}

// listIfMatch reads the revision an edit was made on, or 0 when the edit names none. Adding
// from a title's page names none: an addition at the end cannot land in the wrong place.
func listIfMatch(r *http.Request) (int64, error) {
	header := strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`)
	if header == "" || header == "*" {
		return 0, nil
	}
	revision, err := strconv.ParseInt(header, 10, 64)
	if err != nil || revision <= 0 {
		return 0, listError(db.ErrStale)
	}
	return revision, nil
}

func idempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return "", ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	return key, nil
}

// checkName checks a list's name and description: a name of 1 to 200 characters and a
// description of up to 4,000.
func checkName(name *string, description *string) error {
	if name != nil {
		*name = strings.TrimSpace(*name)
		if *name == "" || utf8.RuneCountInString(*name) > 200 {
			return ErrField("name", "A name has 1 to 200 characters.")
		}
	}
	if description != nil {
		*description = strings.TrimSpace(*description)
		if utf8.RuneCountInString(*description) > 4000 {
			return ErrField("description", "A description has at most 4,000 characters.")
		}
	}
	return nil
}

func (s *Server) lists(r *http.Request, _ None) (Page[List], error) {
	query := r.URL.Query()
	kind := query.Get("kind")
	switch kind {
	case "", db.ListPlaylist, db.ListCollection, db.ListSavedView:
	default:
		return Page[List]{}, ErrField("kind", "Use playlist, collection or saved_view.")
	}
	limit, err := limitParam(query)
	if err != nil {
		return Page[List]{}, err
	}
	lists, next, err := s.db.Lists(r.Context(), viewer(r), kind, query.Get("cursor"), limit)
	out := Page[List]{Items: make([]List, len(lists)), NextCursor: next}
	for n, l := range lists {
		out.Items[n] = listShape(l)
	}
	return out, listError(err)
}

func (s *Server) createList(r *http.Request, req ListCreate) (ListCreated, error) {
	if req.Kind != db.ListPlaylist && req.Kind != db.ListCollection {
		return ListCreated{}, ErrField("kind", "Use playlist or collection.")
	}
	if err := checkName(&req.Name, &req.Description); err != nil {
		return ListCreated{}, err
	}
	if len(req.Items) > db.MaxListAdd {
		return ListCreated{}, ErrField("items", "Add at most 500 titles at once.")
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return ListCreated{}, err
	}
	l, err := s.db.CreateList(r.Context(), viewer(r), db.NewList{Kind: req.Kind, Name: req.Name, SortName: sorttext.Key(req.Name, "", ""),
		Description: req.Description, Items: req.Items}, key, requestHash(req), s.now())
	return ListCreated{listShape(l)}, listError(err)
}

func (s *Server) editList(r *http.Request, req ListEdit) (List, error) {
	if err := checkName(req.Name, req.Description); err != nil {
		return List{}, err
	}
	revision, err := listIfMatch(r)
	if err != nil {
		return List{}, err
	}
	change := db.ListChange{Name: req.Name, Description: req.Description}
	if req.Name != nil {
		change.SortName = sorttext.Key(*req.Name, "", "")
	}
	l, err := s.db.EditList(r.Context(), viewer(r), r.PathValue("id"), revision, change, s.now())
	return listShape(l), listError(err)
}

func (s *Server) addToList(r *http.Request, req ListAdd) (ListAdded, error) {
	if len(req.Items) == 0 || len(req.Items) > db.MaxListAdd {
		return ListAdded{}, ErrField("items", "Add 1 to 500 titles at once.")
	}
	revision, err := listIfMatch(r)
	if err != nil {
		return ListAdded{}, err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return ListAdded{}, err
	}
	var after int64
	if req.After != "" {
		if after = db.ParseEntryID(req.After); after <= 0 {
			return ListAdded{}, ErrField("after", "This entry is not in this list.")
		}
	}
	added, err := s.db.AddToList(r.Context(), viewer(r), r.PathValue("id"), revision, req.Items, after, key,
		requestHash(struct {
			List string
			ListAdd
		}{r.PathValue("id"), req}), s.now())
	return ListAdded{List: listShape(added.List), Added: added.Added, Already: added.Already}, listError(err)
}

func (s *Server) moveInList(r *http.Request, req ListMove) (List, error) {
	if (req.Before == "") == (req.After == "") {
		return List{}, ErrInvalid("Say which entry to move it before, or which to move it after.")
	}
	revision, err := listIfMatch(r)
	if err != nil {
		return List{}, err
	}
	before, after := db.ParseEntryID(req.Before), db.ParseEntryID(req.After)
	if req.Before == "" {
		before = 0
	} else if before <= 0 {
		return List{}, ErrField("before", "This entry is not in this list.")
	}
	if req.After == "" {
		after = 0
	} else if after <= 0 {
		return List{}, ErrField("after", "This entry is not in this list.")
	}
	l, err := s.db.MoveInList(r.Context(), viewer(r), r.PathValue("id"), revision, db.ParseEntryID(r.PathValue("entryId")), before, after, s.now())
	return listShape(l), listError(err)
}

func listShareShapes(shares []db.ListShare) []ListShare {
	out := make([]ListShare, len(shares))
	for n, share := range shares {
		out[n] = ListShare{ProfileID: share.ProfilePublicID, Name: share.Name, Person: share.AccountName, CanEdit: share.CanEdit}
	}
	return out
}
