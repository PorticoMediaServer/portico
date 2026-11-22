package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

// BookSearchPage is the answer of GET /v1/reading/search: one page of the places in the
// viewer's books where what was typed stands. Hits are in the index's own order, a book's
// sections in reading order. IndexedOnly says only books already read into the index are
// searched; IndexRevision changes whenever the index does.
type BookSearchPage struct {
	IndexedOnly   bool             `json:"indexedOnly"`
	IndexRevision int64            `json:"indexRevision"`
	Anchor        BookSearchAnchor `json:"anchor"`
	Hits          []BookSearchHit  `json:"hits"`
	Next          string           `json:"next,omitempty"`
}

// BookSearchAnchor names how a hit's place is counted: in the section's text normalised as
// the reader normalises it (reader-core indexed-text.ts), in UTF-16 units.
type BookSearchAnchor struct {
	Normalization string `json:"normalization"`
	OffsetUnit    string `json:"offsetUnit"`
	MatchUnit     string `json:"matchUnit"`
}

// BookSearchHit is one place in one book. SourceID is the book's version and SourceRevision
// its file's fingerprint, as GET /v1/items/{id}/book gives them. UTF16Offset is where the
// match begins in the section's normalised text; MatchStart is where it begins in Excerpt.
type BookSearchHit struct {
	LibraryID      string `json:"libraryId"`
	ItemID         string `json:"itemId"`
	SourceID       string `json:"sourceId"`
	SourceRevision string `json:"sourceRevision"`
	Title          string `json:"title,omitempty"`
	CoverURL       string `json:"coverUrl,omitempty"`
	SectionPath    string `json:"sectionPath"`
	SectionIndex   int    `json:"sectionIndex"`
	UTF16Offset    int    `json:"utf16Offset"`
	Excerpt        string `json:"excerpt"`
	MatchStart     int    `json:"matchStart"`
	MatchLength    int    `json:"matchLength"`
}

var bookSearchAnchor = BookSearchAnchor{Normalization: "xhtml-body-blockspace-fields-v1", OffsetUnit: "utf16", MatchUnit: "utf16"}

func (s *Server) readingSearchRoutes() {
	// Search all books: q is what was typed, found as those words in that order; libraryId
	// keeps to one library; limit is 20 by default and at most 50; cursor is a page's next.
	Handle(s, "GET /v1/reading/search", WithProfile, s.searchBooks)
}

func (s *Server) searchBooks(r *http.Request, _ None) (BookSearchPage, error) {
	query := r.URL.Query()
	q := query.Get("q")
	if !bookSearchable(q) {
		return BookSearchPage{}, ErrField("q", "Type a word to search for.")
	}
	limit := 20
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			return BookSearchPage{}, ErrField("limit", "Use a whole number from 1 to 50.")
		}
		limit = n
	}
	found, err := s.bookText.Search(r.Context(), s.db, viewer(r), q, query.Get("libraryId"), query.Get("cursor"), limit)
	switch {
	case errors.Is(err, reading.ErrNoWords):
		return BookSearchPage{}, ErrField("q", "Type a word to search for.")
	case errors.Is(err, reading.ErrBadCursor):
		return BookSearchPage{}, ErrField("cursor", err.Error())
	case err != nil:
		return BookSearchPage{}, err
	}
	out := BookSearchPage{IndexedOnly: true, IndexRevision: found.Revision, Anchor: bookSearchAnchor, Hits: []BookSearchHit{}, Next: found.Next}
	for _, h := range found.Hits {
		out.Hits = append(out.Hits, BookSearchHit{LibraryID: h.Owner.LibraryID, ItemID: h.Owner.ItemID, SourceID: h.Owner.VersionID,
			SourceRevision: h.Revision, Title: h.Owner.Title, CoverURL: imageAddress(h.Owner.CoverHash), SectionPath: h.SectionPath,
			SectionIndex: h.SectionIndex, UTF16Offset: h.Offset, Excerpt: h.Excerpt, MatchStart: h.MatchStart, MatchLength: h.MatchLength})
	}
	return out, nil
}

// bookSearchable is what the apps send: not blank, at most 1024 UTF-16 units and 4096 bytes,
// and at least one letter, number or mark.
func bookSearchable(q string) bool {
	if strings.TrimSpace(q) == "" || len(q) > 4096 || !utf8.ValidString(q) || len(utf16.Encode([]rune(q))) > 1024 {
		return false
	}
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			return true
		}
	}
	return false
}
