package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// List is a playlist, a collection or a saved view. A saved view holds no entries: it is a
// library with a sort and filters, opened as GET /v1/items?library={library}&{query}, and its
// count is that list's total. A playlist's or collection's entries are read with
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
	// Everyone says a collection is visible to everyone on the server, not only its maker.
	// Others can open and play it and cannot change it.
	Everyone bool `json:"everyone,omitempty"`
	// Library and Query are a saved view's library and its sort and filters, as the query
	// string of GET /v1/items.
	Library string `json:"library,omitempty"`
	Query   string `json:"query,omitempty"`
	// Dropped, on GET /v1/lists/{id} only, names the parameters of a saved view left out of
	// Query because what they named no longer exists: the view opens with the rest and says so.
	Dropped []string `json:"dropped,omitempty"`
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
	// Everyone makes a collection visible to everyone on the server.
	Everyone bool `json:"everyone,omitempty"`
	// Library and Query make a saved view: the library and the item list's query string with
	// its sort and filters ("kind=movie&genre=Drama&sort=year").
	Library string `json:"library,omitempty"`
	Query   string `json:"query,omitempty"`
}

// ListCreated is a new list; a repeat of the same Idempotency-Key answers the same list.
type ListCreated struct{ List }

func (l ListCreated) reply(h http.Header) int { etag(h, l.Revision); return http.StatusCreated }

// ListEdit renames or describes a list, or changes who sees a collection. Only its maker may.
type ListEdit struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Everyone    *bool   `json:"everyone,omitempty"`
	// Query saves a view's sort and filters again.
	Query *string `json:"query,omitempty"`
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

func (s *Server) listRoutes() {
	Handle(s, "GET /v1/lists", WithProfile, s.lists)
	Handle(s, "POST /v1/lists", WithProfile, s.createList)
	Handle(s, "GET /v1/lists/{id}", WithProfile, func(r *http.Request, _ None) (List, error) {
		l, err := s.db.List(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return List{}, listError(err)
		}
		out := listShape(l)
		if l.Kind == db.ListSavedView {
			out.Query, out.Dropped, err = s.openView(r, l.Query)
		}
		return out, err
	})
	Handle(s, "PATCH /v1/lists/{id}", WithProfile, s.editList)
	Handle(s, "DELETE /v1/lists/{id}", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, listError(s.db.DeleteList(r.Context(), viewer(r), r.PathValue("id")))
	})
	Handle(s, "POST /v1/lists/{id}/entries", WithProfile, s.addToList)
	Handle(s, "PATCH /v1/lists/{id}/entries/{entryId}", WithProfile, s.moveInList)
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
		Role: role, Count: l.Count, Everyone: l.Everyone, Library: l.LibraryPublicID, Query: l.Query, Covers: []map[string]string{}, Revision: strconv.FormatInt(l.Revision, 10),
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
	switch req.Kind {
	case db.ListPlaylist, db.ListCollection:
		if req.Library != "" || req.Query != "" {
			return ListCreated{}, ErrField("query", "Only a saved view has a library, a sort and filters.")
		}
	case db.ListSavedView:
		if req.Library == "" {
			return ListCreated{}, ErrField("library", "A saved view is of one library.")
		}
		if len(req.Items) > 0 {
			return ListCreated{}, ErrField("items", "A saved view holds no titles of its own.")
		}
		query, err := viewQuery(req.Query)
		if err != nil {
			return ListCreated{}, err
		}
		req.Query = query
	default:
		return ListCreated{}, ErrField("kind", "Use playlist, collection or saved_view.")
	}
	if req.Everyone && req.Kind != db.ListCollection {
		return ListCreated{}, ErrField("everyone", "Only a collection is made visible to everyone.")
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
		Description: req.Description, Items: req.Items, Everyone: req.Everyone, Library: req.Library, Query: req.Query}, key, requestHash(req), s.now())
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
	if req.Query != nil {
		query, err := viewQuery(*req.Query)
		if err != nil {
			return List{}, err
		}
		req.Query = &query
	}
	change := db.ListChange{Name: req.Name, Description: req.Description, Everyone: req.Everyone, Query: req.Query}
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

// viewQuery checks a saved view's sort and filters as the item list reads them, and keeps them
// without where a page starts, how long it is, and whether to count: each reading says those.
func viewQuery(text string) (string, error) {
	values, err := url.ParseQuery(text)
	if err != nil || len(text) > 8000 {
		return "", ErrField("query", "A saved view's query is the query string of the item list.")
	}
	for _, key := range []string{"library", "list", "cursor", "limit", "total"} {
		values.Del(key)
	}
	if _, err := itemQuery(values); err != nil {
		return "", err
	}
	return values.Encode(), nil
}

// openView is a saved view's query as it opens now: a parameter the item list no longer takes,
// or one naming a title, person or tag that no longer exists or that the profile may not see,
// is left out, and named in dropped.
func (s *Server) openView(r *http.Request, query string) (string, []string, error) {
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", []string{"query"}, nil
	}
	var dropped []string
	drop := func(key string) { values.Del(key); dropped = append(dropped, key) }
	for range len(values) {
		_, err := itemQuery(values)
		var refusal *Error
		if err == nil || !errors.As(err, &refusal) || len(refusal.Fields) == 0 || !values.Has(refusal.Fields[0].Path) {
			break
		}
		drop(refusal.Fields[0].Path)
	}
	ctx, v := r.Context(), viewer(r)
	for _, key := range []string{"parent", "collection", "person", "tag", "genre"} {
		value := values.Get(key)
		if value == "" {
			continue
		}
		var err error
		known := true
		switch key {
		case "parent", "collection":
			_, err = s.db.Item(ctx, v, value)
		case "person":
			_, err = s.db.Person(ctx, v, value)
		case "tag":
			known, err = s.db.TagKnown(ctx, value, "")
		case "genre":
			known, err = s.db.TagKnown(ctx, "", value)
		}
		if errors.Is(err, db.ErrNotFound) {
			known, err = false, nil
		}
		if err != nil {
			return "", nil, err
		}
		if !known {
			drop(key)
		}
	}
	return values.Encode(), dropped, nil
}
