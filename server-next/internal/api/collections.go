package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A library's collections, made by the owner and administrators: catalogue items of kind
// collection that everyone with the library sees (a person's own collections are lists). They
// are read like any title: GET /v1/items?library=…&kind=collection, GET /v1/items/{id} and
// GET /v1/items?collection={id}.

// CollectionCreate makes a library's collection. Send an Idempotency-Key so a repeat does not
// make a second one.
type CollectionCreate struct {
	Title string `json:"title"`
	// ItemIDs are the titles it holds, in order: films, shows and other top-level titles of
	// the same library.
	ItemIDs []string `json:"itemIds,omitempty"`
}

// CollectionCreated names the new collection, an item of kind collection.
type CollectionCreated struct {
	ID string `json:"id"`
}

// CollectionEdit renames a library's collection, adds and removes titles and orders them.
// Every part is optional and safe to send twice: adding a title it already holds changes
// nothing. A rename or a change of titles holds against automatic grouping.
type CollectionEdit struct {
	Title  *string  `json:"title,omitempty"`
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
	// Order puts these titles first, in this order; the others follow as they were.
	Order []string `json:"order,omitempty"`
}

func (s *Server) collectionRoutes() {
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "POST /v1/admin/libraries/{id}/collections", Admin, s.createCollection))
	library(Handle(s, "PATCH /v1/admin/collections/{id}", Admin, func(r *http.Request, req CollectionEdit) (None, error) {
		edit := db.CollectionEdit{Title: req.Title, Add: req.Add, Remove: req.Remove, Order: req.Order}
		return None{}, refuse(s.db.EditCollection(r.Context(), viewer(r), r.PathValue("id"), edit))
	}))
	library(Handle(s, "DELETE /v1/admin/collections/{id}", Admin, func(r *http.Request, _ None) (None, error) {
		err := s.db.DeleteCollection(r.Context(), viewer(r), r.PathValue("id"))
		if errors.Is(err, db.ErrNotFound) {
			return None{}, nil // already gone: a delete sent twice changes nothing
		}
		return None{}, err
	}))
}

func (s *Server) createCollection(r *http.Request, req CollectionCreate) (CollectionCreated, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return CollectionCreated{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	id, err := s.db.CreateCollection(r.Context(), db.NewCollection{Viewer: viewer(r), Library: r.PathValue("id"), Title: req.Title,
		Items: req.ItemIDs, IdempotencyKey: key, RequestHash: requestHash(req)})
	if errors.Is(err, db.ErrKeyReused) {
		return CollectionCreated{}, errKeyReused()
	}
	return CollectionCreated{ID: id}, refuse(err)
}
