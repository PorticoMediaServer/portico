package api

import (
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) collectionHomeRows(r *http.Request, library, collection string) ([]HomeRow, error) {
	collections, err := s.db.HomeCollections(r.Context(), viewer(r), library, collection)
	if err != nil {
		return nil, err
	}
	rows := make([]HomeRow, 0, len(collections))
	for _, c := range collections {
		rows = append(rows, HomeRow{ID: "collection:" + c.ID, Title: c.Title, Shape: "poster", Items: []Item{}, Library: c.Library,
			Query: url.Values{"collection": {c.ID}}.Encode(), Deferred: true})
	}
	return rows, nil
}

func (s *Server) collectionHomeRow(r *http.Request, id string) (HomeRow, error) {
	rows, err := s.collectionHomeRows(r, "", strings.TrimPrefix(id, "collection:"))
	if err != nil {
		return HomeRow{}, err
	}
	if len(rows) == 0 {
		return HomeRow{}, ErrNotFound()
	}
	return rows[0], nil
}

// Explicit promotion uses the existing layout/save path and its 64-id bound. Resetting the
// order removes promoted collections; hiding one keeps its id there so it can be restored.
func (s *Server) addedCollectionHomeRows(r *http.Request) ([]HomeRow, error) {
	stored, err := s.db.Preferences(r.Context(), viewer(r).ProfileID, 0)
	if err != nil {
		return nil, err
	}
	rows := []HomeRow{}
	for _, id := range savedList(stored, "home.rowOrder") {
		if !strings.HasPrefix(id, "collection:") {
			continue
		}
		found, err := s.collectionHomeRows(r, "", strings.TrimPrefix(id, "collection:"))
		if err != nil {
			return nil, err
		}
		rows = append(rows, found...)
	}
	return rows, nil
}
