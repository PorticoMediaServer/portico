package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func (s *Server) prepareHomeQueue(r *http.Request, source *playqueue.QueueSource) error {
	row := source.HomeRow
	if row == nil {
		return nil
	}
	values := url.Values{}
	for name, value := range map[string]string{"library": row.Library, "sort": row.Sort, "order": row.Order} {
		if value != "" {
			values.Set(name, value)
		}
	}
	for name, value := range map[string]*bool{"watched": row.Watched, "inProgress": row.InProgress} {
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
			query, _, err := s.openView(r, view.Query)
			if err != nil {
				return err
			}
			base, _ := url.ParseQuery(query)
			for name, value := range values {
				base[name] = value
			}
			base.Set("library", view.Library)
			values, found = base, true
			break
		}
		if !found {
			return ErrNotFound()
		}
		id = ""
	} else if id == "" {
		return ErrField("source.homeRow.id", "Choose a Home row.")
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
	return nil
}
