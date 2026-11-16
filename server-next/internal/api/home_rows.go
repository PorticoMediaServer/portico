package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (s *Server) homeRowRoutes() {
	Handle(s, "GET /v1/home/rows/{id}", WithProfile, s.homeRow)
	Handle(s, "GET /v1/items/{id}/related/more-like-this", WithProfile, s.moreLikeThisPage)
}

func (s *Server) homeRow(r *http.Request, _ None) (HomeRow, error) {
	id, values := r.PathValue("id"), r.URL.Query()
	row := HomeRow{ID: id, Shape: "poster"}
	switch id {
	case "continue":
		row.Title, row.Shape = "Continue Watching", "landscape"
	case "continue_listening":
		row.Title = "Continue Listening"
	case "continue_books":
		row.Title = "Continue your books"
	case "recently_added":
		row.Title = "Recently Added"
	default:
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
		moods, err := s.db.MoodRows(r.Context(), viewer(r))
		if err != nil {
			return row, err
		}
		for _, mood := range moods {
			if mood.Key == id {
				row.Title, row.Reason = moodRowTitle(mood.Label), because(mood.Because)
				break
			}
		}
		if row.Title == "" {
			return row, ErrNotFound()
		}
	}
	q, err := itemQuery(values)
	if err != nil {
		return row, err
	}
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

func (s *Server) moreLikeThisPage(r *http.Request, _ None) (ItemPage, error) {
	values := r.URL.Query()
	offset := 0
	if cursor := values.Get("cursor"); cursor != "" {
		var err error
		offset, err = strconv.Atoi(cursor)
		if err != nil || offset < 0 {
			return ItemPage{}, errCursorExpired()
		}
	}
	limit, err := limitParam(values)
	if err != nil {
		return ItemPage{}, err
	}
	if limit == 0 {
		limit = 12
	}
	limit = min(limit, 200)
	items, err := s.db.MoreLikeThis(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return ItemPage{}, refuse(err)
	}
	return relatedPage(items, offset, limit), nil
}

func relatedPage(items []db.Item, offset, limit int) ItemPage {
	total := len(items)
	start, end := min(offset, total), min(total, min(offset, total)+limit)
	out := ItemPage{Items: itemShapes(items[start:end]), Total: &total}
	if end < total {
		out.NextCursor = strconv.Itoa(end)
	}
	return out
}

// pageHomeRows gives initial rows the same page and continuation as See all.
func (s *Server) pageHomeRows(r *http.Request, rows []HomeRow, library string) ([]db.Item, error) {
	var continuing []db.Item
	for n := range rows {
		page, err := s.db.HomeRowItems(r.Context(), viewer(r), rows[n].ID, db.ItemQuery{Library: library, Limit: 40})
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
