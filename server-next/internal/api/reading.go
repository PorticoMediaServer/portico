package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

// BookAnswer is how to read one book: GET /v1/items/{id}/book.
type BookAnswer struct {
	ItemID string `json:"itemId"`
	// Version is the version opened; Versions are all of the book's files, for "Read a
	// different version".
	Version  string        `json:"version"`
	Versions []BookVersion `json:"versions"`
	// Format is epub, pdf, cbz or images (a comic made of a folder of pictures) for a book this
	// server opens; any other is one it cannot yet.
	Format string `json:"format"`
	// Fingerprint names this exact file. A place or a highlight made on another fingerprint was
	// made on another copy of the book, and is placed by how far through the book it was.
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size"`
	// Unreadable says why this book cannot be read here, in words to show. Nothing below it
	// but SaveURL is then given.
	Unreadable *BookProblem `json:"unreadable,omitempty"`
	// MediaURL is where the book is read from: MediaURL + "/source" is a PDF's bytes by range,
	// MediaURL + "/parts/" + a part's path is one part of an EPUB, and MediaURL + "/pages/" +
	// n (from 0), with "?w=" a width, is one comic page. The address carries its own
	// permission while this sign-in holds and this profile may see the book; when it ends (after
	// a few hours, or at once when the sign-in ends or changes) the app asks for the book again
	// and carries on at the same page.
	MediaURL string `json:"mediaUrl,omitempty"`
	// SaveURL saves the book's own file, of any size. Only a profile allowed to download is given it.
	SaveURL string `json:"saveUrl,omitempty"`
	// Direction is "rtl" for a book that reads right to left.
	Direction string `json:"direction,omitempty"`
	// Parts, Spine and Contents are an EPUB's: every part the reader may ask for, the reading
	// order (paths of parts), and the table of contents.
	Parts    []BookPart     `json:"parts,omitempty"`
	Spine    []string       `json:"spine,omitempty"`
	Contents []BookContents `json:"contents,omitempty"`
	// Pages are a comic's pages in reading order, by their names in the archive.
	Pages []string `json:"pages,omitempty"`
}

// BookVersion is one file of a book.
type BookVersion struct {
	ID     string `json:"id"`
	Format string `json:"format"`
	Name   string `json:"name,omitempty"`
}

// BookProblem is why a book cannot be read: format_not_supported, remote_storage,
// unavailable, book_changed, too_many_pages or damaged.
type BookProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BookPart struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

type BookContents struct {
	Title    string `json:"title"`
	Part     string `json:"part"`
	Fragment string `json:"fragment,omitempty"`
	Depth    int    `json:"depth"`
}

func (s *Server) readingRoutes() {
	// A book's address is tied to the sign-in that asked for it, so this takes a signed-in
	// person and not a key, as playback does.
	Handle(s, "GET /v1/items/{id}/book", WithProfile, s.openBook).Scope = ScopeSession
}

func (s *Server) openBook(r *http.Request, _ None) (BookAnswer, error) {
	book, err := s.db.Book(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return BookAnswer{}, refuse(err)
	}
	version, ok := reading.Choose(book, r.URL.Query().Get("version"))
	if !ok {
		return BookAnswer{}, ErrNotFound()
	}
	c := CallerFrom(r.Context())
	who := reading.Reader{SessionID: c.SessionID, Stamp: c.Stamp, Viewer: viewer(r)}
	opened := s.reading.Open(r.Context(), who, book, version)
	out := BookAnswer{ItemID: book.PublicID, Version: version.PublicID, Versions: []BookVersion{}, Format: opened.Format,
		Fingerprint: opened.Fingerprint, Size: version.Size, SaveURL: opened.SaveURL}
	for _, v := range book.Versions {
		out.Versions = append(out.Versions, BookVersion{ID: v.PublicID, Format: reading.VersionFormat(v), Name: v.Name})
	}
	if opened.Problem != nil {
		out.Unreadable = &BookProblem{Code: bookProblemCode(opened.Problem), Message: opened.Problem.Error()}
		return out, nil
	}
	out.MediaURL, out.Direction, out.Spine, out.Pages = opened.MediaURL, opened.Direction, opened.Spine, opened.Pages
	for _, p := range opened.Parts {
		out.Parts = append(out.Parts, BookPart{Path: p.Path, Type: p.Type, Size: p.Size})
	}
	for _, c := range opened.Contents {
		out.Contents = append(out.Contents, BookContents{Title: c.Title, Part: c.Part, Fragment: c.Fragment, Depth: c.Depth})
	}
	return out, nil
}

func bookProblemCode(err error) string {
	switch err {
	case reading.ErrFormatLater:
		return "format_not_supported"
	case reading.ErrRemote:
		return "remote_storage"
	case reading.ErrUnavailable:
		return "unavailable"
	case reading.ErrChanged:
		return "book_changed"
	case reading.ErrTooManyPages:
		return "too_many_pages"
	}
	return "damaged"
}
