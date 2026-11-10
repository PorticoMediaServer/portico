package api

import (
	"encoding/json"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Home is the Home screen, or a library's landing page, composed by the server for the
// profile. An app draws the rows it is given, in the order given. A row with nothing in it
// is not sent.
type Home struct {
	Rows []HomeRow `json:"rows"`
}

// HomeRow is one row. Shape says how to draw its cards: "landscape", "poster" or "square".
// NextCursor is present when the row has more: GET /v1/home/rows/{id}?cursor= reads on, and
// a row ends only when its source does.
type HomeRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Shape      string `json:"shape"`
	Items      []Item `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// HomeLayout is every row the profile's Home can show, in the profile's order, hidden ones
// included. PUT /v1/home/layout takes the same shape: the rows in the order wanted, each with
// whether it is hidden; only id and hidden are read. A row left out keeps its usual place
// after the ones named, a row that is always on cannot be hidden, and an empty list goes
// back to the default. The later save wins.
type HomeLayout struct {
	Rows []HomeLayoutRow `json:"rows"`
}

// HomeLayoutRow is one row of the layout. AlwaysOn rows can be moved but not hidden.
type HomeLayoutRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Hidden   bool   `json:"hidden"`
	AlwaysOn bool   `json:"alwaysOn"`
}

// homeRowSize is how many cards of a row GET /v1/home sends before the row is read on.
const homeRowSize = 20

// maxLayoutRows bounds a saved layout.
const maxLayoutRows = 200

const recentRow = "recently_added."

// homeRow is a row the server can compose: a Continue row (kinds) or a library's newest titles.
type homeRow struct {
	HomeLayoutRow
	shape   string
	kinds   []db.ItemKind
	library string
}

// homeRows lists the rows of the profile's Home in its own order.
func (s *Server) homeRows(r *http.Request) ([]homeRow, error) {
	rows := []homeRow{
		{HomeLayoutRow{ID: "continue", Title: "Continue Watching", AlwaysOn: true}, "landscape", db.ContinueWatching, ""},
		{HomeLayoutRow{ID: "continue_listening", Title: "Continue Listening", AlwaysOn: true}, "square", db.ContinueListening, ""},
		{HomeLayoutRow{ID: "continue_books", Title: "Continue your books", AlwaysOn: true}, "poster", db.ContinueReading, ""},
	}
	libraries, err := s.db.Libraries(r.Context(), viewer(r))
	if err != nil {
		return nil, err
	}
	for _, l := range libraries {
		rows = append(rows, recentIn(l))
	}
	saved, err := s.db.HomeLayout(r.Context(), viewer(r).ProfileID)
	if err != nil {
		return nil, err
	}
	var layout []HomeLayoutRow
	json.Unmarshal([]byte(saved), &layout) // nothing saved, or unreadable: the default order
	byID := map[string]*homeRow{}
	for n := range rows {
		byID[rows[n].ID] = &rows[n]
	}
	// The rows the profile placed come first, in its order; then any it never placed, such as
	// a library added since. A saved row that no longer exists is passed over.
	var out []homeRow
	for _, placed := range layout {
		if row := byID[placed.ID]; row != nil {
			row.Hidden = placed.Hidden && !row.AlwaysOn
			out = append(out, *row)
			delete(byID, placed.ID)
		}
	}
	for _, row := range rows {
		if byID[row.ID] != nil {
			out = append(out, row)
		}
	}
	return out, nil
}

func recentIn(l db.Library) homeRow {
	shape := "poster"
	if l.Kind == db.LibraryMusic || l.Kind == db.LibraryAudiobooks {
		shape = "square"
	}
	return homeRow{HomeLayoutRow{ID: recentRow + l.PublicID, Title: "Recently Added in " + l.Name}, shape, nil, l.PublicID}
}

// fill reads the start of a row.
func (s *Server) fill(r *http.Request, row homeRow, library, cursor string, limit int) (HomeRow, error) {
	out := HomeRow{ID: row.ID, Title: row.Title, Shape: row.shape}
	if row.kinds != nil {
		// A Continue row is short and is sent whole.
		items, err := s.db.Continue(r.Context(), viewer(r), row.kinds, library)
		out.Items = itemShapes(items)
		return out, err
	}
	page, err := s.db.RecentlyAdded(r.Context(), viewer(r), row.library, cursor, limit)
	out.Items, out.NextCursor = itemShapes(page.Items), page.NextCursor
	return out, refuse(err)
}

func (s *Server) homeRoutes() {
	Handle(s, "GET /v1/home", WithProfile, func(r *http.Request, _ None) (Home, error) {
		rows, err := s.homeRows(r)
		if err != nil {
			return Home{}, err
		}
		out := Home{Rows: []HomeRow{}}
		for _, row := range rows {
			if row.Hidden {
				continue
			}
			filled, err := s.fill(r, row, "", "", homeRowSize)
			if err != nil {
				return Home{}, err
			}
			if len(filled.Items) > 0 {
				out.Rows = append(out.Rows, filled)
			}
		}
		return out, nil
	})
	Handle(s, "GET /v1/home/rows/{id}", WithProfile, func(r *http.Request, _ None) (ItemPage, error) {
		limit, err := limitParam(r.URL.Query())
		if err != nil {
			return ItemPage{}, err
		}
		rows, err := s.homeRows(r)
		if err != nil {
			return ItemPage{}, err
		}
		// A hidden row can still be opened: See all does not depend on what Home shows.
		for _, row := range rows {
			if row.ID == r.PathValue("id") {
				filled, err := s.fill(r, row, "", r.URL.Query().Get("cursor"), limit)
				return ItemPage{Items: filled.Items, NextCursor: filled.NextCursor}, err
			}
		}
		return ItemPage{}, ErrNotFound()
	})
	Handle(s, "GET /v1/home/layout", WithProfile, s.homeLayout)
	Handle(s, "PUT /v1/home/layout", WithProfile, func(r *http.Request, req HomeLayout) (HomeLayout, error) {
		if len(req.Rows) > maxLayoutRows {
			return HomeLayout{}, ErrField("rows", "That is too many rows.")
		}
		keep := make([]HomeLayoutRow, 0, len(req.Rows))
		seen := map[string]bool{}
		for _, row := range req.Rows {
			if row.ID == "" || len(row.ID) > 100 || seen[row.ID] {
				return HomeLayout{}, ErrField("rows", "Name each row once, by its id.")
			}
			seen[row.ID] = true
			keep = append(keep, HomeLayoutRow{ID: row.ID, Hidden: row.Hidden})
		}
		layout := ""
		if len(keep) > 0 {
			raw, _ := json.Marshal(keep)
			layout = string(raw)
		}
		if err := s.db.SetHomeLayout(r.Context(), viewer(r).ProfileID, layout, s.now()); err != nil {
			return HomeLayout{}, err
		}
		return s.homeLayout(r, None{})
	})
	Handle(s, "GET /v1/libraries/{id}/landing", WithProfile, func(r *http.Request, _ None) (Home, error) {
		l, err := s.db.Library(r.Context(), viewer(r), r.PathValue("id"))
		if err != nil {
			return Home{}, refuse(err)
		}
		// What the profile is part-way through in this library, then what is new in it.
		proceed := homeRow{HomeLayoutRow{ID: "continue", Title: "Continue Watching"}, "landscape", db.ContinueWatching, ""}
		switch l.Kind {
		case db.LibraryMusic, db.LibraryAudiobooks:
			proceed = homeRow{HomeLayoutRow{ID: "continue_listening", Title: "Continue Listening"}, "square", db.ContinueListening, ""}
		case db.LibraryBooks:
			proceed = homeRow{HomeLayoutRow{ID: "continue_books", Title: "Continue your books"}, "poster", db.ContinueReading, ""}
		}
		recent := recentIn(l)
		recent.Title = "Recently Added"
		out := Home{Rows: []HomeRow{}}
		for _, row := range []homeRow{proceed, recent} {
			filled, err := s.fill(r, row, l.PublicID, "", homeRowSize)
			if err != nil {
				return Home{}, err
			}
			if len(filled.Items) > 0 {
				out.Rows = append(out.Rows, filled)
			}
		}
		return out, nil
	})
}

func (s *Server) homeLayout(r *http.Request, _ None) (HomeLayout, error) {
	rows, err := s.homeRows(r)
	out := HomeLayout{Rows: make([]HomeLayoutRow, len(rows))}
	for n, row := range rows {
		out.Rows[n] = row.HomeLayoutRow
	}
	return out, err
}
