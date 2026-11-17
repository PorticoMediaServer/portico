package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (s *Server) homeRowRoutes() {
	Handle(s, "GET /v1/home/rows/{id}", WithProfile, s.homeRow)
}

func (s *Server) homeRow(r *http.Request, _ None) (HomeRow, error) {
	id, values := r.PathValue("id"), r.URL.Query()
	if strings.HasPrefix(id, "because:") {
		row, err := s.becauseHomeRow(r, id)
		if err != nil {
			return row, err
		}
		q, err := itemQuery(values)
		if err != nil {
			return row, err
		}
		page, err := s.db.BecauseWatchedPage(r.Context(), viewer(r), id, q)
		return homePage(row, page, q.WithTotal), refuse(err)
	}
	row := HomeRow{ID: id, Shape: "poster"}
	switch id {
	case "continue":
		row.Title, row.Shape = "Continue Watching", "landscape"
	case "continue_listening":
		row.Title = "Continue Listening"
	case "continue_books":
		row.Title = "Continue your books"
	case "recently_released":
		row.Title = "Recently Released"
	case "popular":
		row.Title = popularHomeTitle
	case "recently_added":
		row.Title = "Recently Added"
	case "trending":
		row.Title = "Trending now"
	default:
		if strings.HasPrefix(id, "collection:") {
			var err error
			row, err = s.collectionHomeRow(r, id)
			if err != nil {
				return row, err
			}
			break
		}
		if strings.HasPrefix(id, "view:") {
			views, err := s.savedViewRows(r)
			if err != nil {
				return row, err
			}
			for _, view := range views {
				if view.ID == id {
					row = view
					break
				}
			}
			if row.Title == "" {
				return row, ErrNotFound()
			}
			query, _, err := s.openView(r, row.Query)
			if err != nil {
				return row, err
			}
			row.Query = query
			base, _ := url.ParseQuery(query)
			for name, value := range values {
				base[name] = value
			}
			base.Set("library", row.Library)
			q, err := itemQuery(base)
			if err != nil {
				return row, err
			}
			page, err := s.db.Items(r.Context(), viewer(r), q)
			return homePage(row, page, q.WithTotal), refuse(err)
		}
		mood, err := s.db.MoodRow(r.Context(), viewer(r), id)
		if err != nil {
			return row, refuse(err)
		}
		row.Title, row.Reason = moodRowTitle(mood.Label), because(mood.Because)
	}
	q, err := itemQuery(values)
	if err != nil {
		return row, err
	}
	q.HomeAt = s.now()
	page, err := s.db.HomeRowItems(r.Context(), viewer(r), id, q)
	return homePage(row, page, q.WithTotal), refuse(err)
}

func homePage(row HomeRow, page db.ItemPage, total bool) HomeRow {
	row.Items, row.NextCursor, row.Deferred = itemShapes(page.Items), page.NextCursor, false
	if total {
		row.Total = &page.Total
	}
	return row
}

// pageHomeRows gives initial rows the same page and continuation as See all.
func (s *Server) pageHomeRows(r *http.Request, rows []HomeRow, library string) ([]db.Item, error) {
	var continuing []db.Item
	for n := range rows {
		if rows[n].ID == "recently_added" {
			items, err := s.db.RecentlyAddedIn(r.Context(), viewer(r), library)
			if err != nil {
				return nil, err
			}
			rows[n].Items, rows[n].NextCursor = itemShapes(items), db.RecentlyAddedCursor(items)
			continue
		}
		page, err := s.db.HomeRowItems(r.Context(), viewer(r), rows[n].ID, db.ItemQuery{Library: library, Limit: 40, HomeAt: s.now()})
		if err != nil {
			return nil, refuse(err)
		}
		if rows[n].ID == "continue" {
			continuing = page.Items
		}
		rows[n] = homePage(rows[n], page, false)
	}
	return continuing, nil
}
