package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func (s *Server) prepareQueueSource(r *http.Request, source *playqueue.QueueSource) error {
	row := source.HomeRow
	var viewQuery string
	if row == nil {
		if source.List == "" {
			return nil
		}
		views, err := s.db.HomeViews(r.Context(), viewer(r), []string{source.List})
		if err != nil || len(views) == 0 {
			return err // a playlist or collection still uses its stored entries
		}
		row = &playqueue.HomeRowSource{Library: views[0].Library}
		viewQuery = views[0].Query
		source.SavedView, source.List = source.List, ""
	} else if row.ID == "" {
		return ErrField("source.homeRow.id", "Choose a Home row.")
	}
	values := url.Values{}
	for name, value := range map[string]string{"library": row.Library, "sort": row.Sort, "order": row.Order, "genre": row.Genre, "year": row.Year} {
		if value != "" {
			values.Set(name, value)
		}
	}
	for name, value := range map[string]*bool{"watched": row.Watched, "inProgress": row.InProgress, "favorite": row.Favorite, "inList": row.InList} {
		if value != nil {
			values.Set(name, strconv.FormatBool(*value))
		}
	}
	id := row.ID
	if strings.HasPrefix(id, "view:") {
		rows, err := s.savedViewRows(r)
		if err != nil {
			return err
		}
		found := false
		for _, view := range rows {
			if view.ID != id {
				continue
			}
			viewQuery, found = view.Query, true
			values.Set("library", view.Library)
			break
		}
		if !found {
			return ErrNotFound()
		}
		id = ""
	}
	if id == "" {
		query, _, err := s.openView(r, viewQuery)
		if err != nil {
			return err
		}
		base, _ := url.ParseQuery(query)
		for name, value := range values {
			base[name] = value
		}
		values = base
	}
	q, err := itemQuery(values)
	if err != nil {
		return err
	}
	q.Cursor, q.At, q.Limit, q.WithTotal = "", 0, 0, false
	q.HomeAt = s.now()
	prepared, err := s.db.HomeQueueSource(r.Context(), viewer(r), id, q)
	if err != nil {
		return refuse(err)
	}
	row.Prepare(prepared)
	source.HomeRow = row
	return nil
}
