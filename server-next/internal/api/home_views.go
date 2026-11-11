package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Saved views on Home. Customize Home adds one with the setting home.savedViews (saved-view
// ids, at most 20), saved with PATCH /v1/preferences like the rest of the layout. Each becomes
// a row "view:<id>", named after the view, placed and hidden by home.rowOrder and
// home.hiddenRowIds like any row.
//
// A row's titles cost one read of the view's list, and some views are slow to read (a random
// order over a whole library is), so Home fills only the first filledViewRows of them, in the
// profile's order, and stops when those reads have taken viewRowsBudget: the read under way
// is stopped. The rest are sent deferred, with no items: the app reads them with
// GET /v1/items?library=…&{query} when they come near the screen, and leaves one out that
// turns out empty. Whatever a profile puts on Home, it costs at most the budget more.
const (
	filledViewRows = 4
	viewRowLength  = 20
)

var viewRowsBudget = 40 * time.Millisecond

// savedViewRows are the profile's saved-view rows in the order it put them on Home, without
// items. An id that names no saved view the profile may read gives no row and no name.
func (s *Server) savedViewRows(r *http.Request) ([]HomeRow, error) {
	stored, err := s.db.Preferences(r.Context(), viewer(r).ProfileID, 0)
	if err != nil {
		return nil, err
	}
	ids := savedList(stored, "home.savedViews")
	views, err := s.db.HomeViews(r.Context(), viewer(r), ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]db.HomeView{}
	for _, view := range views {
		byID[view.PublicID] = view
	}
	var rows []HomeRow
	for _, id := range ids {
		if view, ok := byID[id]; ok {
			rows = append(rows, HomeRow{ID: "view:" + id, Title: view.Name, Shape: "poster", Items: []Item{},
				Library: view.Library, Query: view.Query, Deferred: true})
		}
	}
	return rows, nil
}

// fillViewRows reads the titles of the first saved-view rows Home shows, in place.
func (s *Server) fillViewRows(r *http.Request, rows []HomeRow) error {
	ctx, cancel := context.WithTimeout(r.Context(), viewRowsBudget)
	defer cancel()
	filled := 0
	for n := range rows {
		row := &rows[n]
		if row.Hidden {
			row.Deferred = false // the editor names a hidden row; it reads no titles for it
		}
		if !row.Deferred || filled == filledViewRows {
			continue
		}
		filled++
		items, err := s.viewRowItems(ctx, r, row.Library, row.Query)
		if ctx.Err() != nil && r.Context().Err() == nil {
			filled = filledViewRows // out of time: this row and the rest stay deferred
			continue
		}
		if err != nil {
			return err
		}
		row.Items, row.Deferred = items, false
	}
	return nil
}

// viewRowItems reads the first titles of a saved view. When that finds nothing, the view is
// read again as it opens (see openView): a filter naming something that no longer exists is
// left out, as it is when the person opens the view.
func (s *Server) viewRowItems(ctx context.Context, r *http.Request, library, query string) ([]Item, error) {
	read := func(query string) ([]Item, error) {
		values, err := url.ParseQuery(query)
		if err != nil {
			return nil, db.ErrInvalid
		}
		q, err := itemQuery(values)
		if err != nil {
			return nil, db.ErrInvalid
		}
		q.Library, q.Limit, q.WithTotal = library, viewRowLength, false
		page, err := s.db.Items(ctx, viewer(r), q)
		if errors.Is(err, db.ErrNotFound) || errors.Is(err, db.ErrInvalid) {
			return []Item{}, nil
		}
		return itemShapes(page.Items), err
	}
	items, err := read(query)
	if err != nil || len(items) > 0 {
		return items, err
	}
	opened, dropped, err := s.openView(r, query)
	if err != nil || len(dropped) == 0 {
		return items, err
	}
	return read(opened)
}
