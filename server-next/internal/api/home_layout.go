package api

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/preferences"
)

// Customize Home is saved as two of the profile's own settings, through PATCH
// /v1/preferences like any other: home.rowOrder (row ids, first first) and home.hiddenRowIds.
// Saving them that way gives Home what every setting has: kept on a device while the server is
// out of reach, the later of two devices' saves winning, and the profile's other devices told.

// alwaysOnRows can be moved but never hidden: what a person is part-way through stays on Home.
var alwaysOnRows = []string{"continue", "continue_listening", "continue_books"}

// homeLayout puts the rows in the profile's saved order and leaves out the ones it hid, or
// with withHidden keeps them in their place marked hidden and without items. A row the order
// does not name keeps its usual place after the named ones; an id that names no row is passed
// over, so a row that goes away and comes back keeps its place.
func (s *Server) homeLayout(ctx context.Context, profileID int64, rows []HomeRow, withHidden bool) ([]HomeRow, error) {
	stored, err := s.db.Preferences(ctx, profileID, 0)
	if err != nil {
		return nil, err
	}
	order, hidden := savedList(stored, "home.rowOrder"), savedList(stored, "home.hiddenRowIds")
	place := func(id string) int {
		if n := slices.Index(order, id); n >= 0 {
			return n
		}
		return len(order)
	}
	out := make([]HomeRow, 0, len(rows))
	for _, row := range rows {
		switch {
		case !slices.Contains(hidden, row.ID) || slices.Contains(alwaysOnRows, row.ID):
			out = append(out, row)
		case withHidden:
			row.Hidden, row.Items = true, []Item{}
			out = append(out, row)
		}
	}
	slices.SortStableFunc(out, func(a, b HomeRow) int { return place(a.ID) - place(b.ID) })
	return out, nil
}

// savedList is a saved list setting, or nothing when none is saved or it no longer reads.
func savedList(stored map[string]string, key string) []string {
	field, _ := preferences.Find(key)
	var raw any
	if json.Unmarshal([]byte(stored[key]), &raw) != nil {
		return nil
	}
	value, err := field.Check(raw)
	if err != nil {
		return nil
	}
	list, _ := value.([]string)
	return list
}
