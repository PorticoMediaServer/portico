package api

import (
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (s *Server) becauseHomeRows(r *http.Request, named string) ([]HomeRow, error) {
	seeds, err := s.db.BecauseSeeds(r.Context(), viewer(r), named)
	if err != nil {
		return nil, err
	}
	rows := []HomeRow{}
	for _, seed := range seeds {
		row := HomeRow{ID: "because:" + seed.PublicID, Title: "Because you watched " + seed.Title, Shape: "poster"}
		if named == "" {
			page, err := s.db.BecauseWatchedPage(r.Context(), viewer(r), row.ID, db.ItemQuery{Limit: 40})
			if err != nil {
				return nil, refuse(err)
			}
			row = homePage(row, page, false)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Server) becauseHomeRow(r *http.Request, id string) (HomeRow, error) {
	rows, err := s.becauseHomeRows(r, strings.TrimPrefix(id, "because:"))
	if err != nil {
		return HomeRow{}, err
	}
	if len(rows) == 0 {
		return HomeRow{}, ErrNotFound()
	}
	return rows[0], nil
}
