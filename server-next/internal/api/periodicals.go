package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// periodicalViews are a Books or Comics library's Magazines and Newspapers views, each only when
// the profile may see at least one issue of that kind there: the app shows those tabs only then.
func (s *Server) periodicalViews(r *http.Request, l db.Library) []View {
	if l.Kind != db.LibraryBooks && l.Kind != db.LibraryComics {
		return nil
	}
	var out []View
	for _, p := range []int{db.PublicationMagazine, db.PublicationNewspaper} {
		page, err := s.db.Items(r.Context(), viewer(r), db.ItemQuery{Library: l.PublicID, Publication: &p, Limit: 1})
		if err == nil && len(page.Items) > 0 {
			out = append(out, View{db.PublicationName(p)})
		}
	}
	return out
}

// newIssuesRow is a reading library's New issues: its magazines' issues, newest first by their
// own dates. Empty, and so not shown, anywhere else.
func (s *Server) newIssuesRow(r *http.Request, l db.Library) HomeRow {
	row := HomeRow{ID: "new_issues", Title: "New issues", Shape: "poster"}
	if l.Kind != db.LibraryBooks && l.Kind != db.LibraryComics {
		return row
	}
	magazine := db.PublicationMagazine
	page, err := s.db.Items(r.Context(), viewer(r), db.ItemQuery{Library: l.PublicID, Publication: &magazine,
		Kinds: []db.ItemKind{db.KindBook, db.KindComic, db.KindIssue}, Sort: db.SortRelease, Desc: true, Limit: 20})
	if err == nil {
		row.Items = itemShapes(page.Items)
	}
	return row
}
