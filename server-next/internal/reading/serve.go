package reading

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
)

// ServeMedia answers one request under a book's address:
//
//	source         the file's own bytes, by range: a PDF read in the app, or with save=1 the
//	               file saved (which needs a profile allowed to download)
//	parts/{path}   one part of an EPUB, named in the book's own package document
//	pages/{n}      one page of a comic, counting from 0, no wider than w when w is given
//
// An address that was altered, has ended, or names a file the scanner no longer has is the
// same hidden "not found". A file changed since the address was given is "book changed", and
// the app opens the book again.
func (s *Service) ServeMedia(w http.ResponseWriter, r *http.Request, token, file string) {
	g, ok := s.parse(token)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "This is not available.")
		return
	}
	ctx := r.Context()
	place, err := s.db.FileOnDisk(ctx, g.fileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "This is not available.")
		return
	}
	folders, err := s.db.LibraryFolders(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	version := db.BookVersion{FileID: g.fileID, Path: place.Path, Size: place.Size, ModifiedNS: place.ModifiedNS}
	f, err := libraryfile.Open(place.Path, folders)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "This is not available.")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != place.Size || info.ModTime().UnixNano() != place.ModifiedNS ||
		string(fingerprintBytes(place.Size, place.ModifiedNS)) != string(g.fingerprint[:]) {
		writeError(w, http.StatusConflict, "book_changed", ErrChanged.Error())
		return
	}
	format := Format(place.Path)
	// Nothing served here is a page of this server's own: a part opened on its own can run no
	// script and reach nothing, and no type is guessed from the bytes.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case file == "source":
		save := r.URL.Query().Get("save") == "1"
		if (save || format != FormatPDF) && !g.save {
			writeError(w, http.StatusForbidden, "not_permitted", "This profile can't save books.")
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", fileType(format))
		if save {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(place.Path)}))
		}
		http.ServeContent(w, r, "", info.ModTime(), f)
	case strings.HasPrefix(file, "parts/") && format == FormatEPUB:
		a, err := openArchive(f, info.Size())
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "book_damaged", err.Error())
			return
		}
		book, err := s.epub(version, a)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "book_damaged", err.Error())
			return
		}
		part, ok := book.byPath[strings.TrimPrefix(file, "parts/")]
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "This is not available.")
			return
		}
		sendEntry(w, a, part.Path, part.Type)
	case strings.HasPrefix(file, "pages/") && format == FormatCBZ:
		n, err := strconv.Atoi(strings.TrimPrefix(file, "pages/"))
		width, werr := strconv.Atoi(r.URL.Query().Get("w"))
		if r.URL.Query().Get("w") == "" {
			width, werr = 0, nil
		}
		a, aerr := openArchive(f, info.Size())
		if aerr != nil {
			writeError(w, http.StatusUnprocessableEntity, "book_damaged", aerr.Error())
			return
		}
		c, cerr := readComic(a)
		if err != nil || cerr != nil || n < 0 || n >= len(c.pages) || werr != nil || width < 0 {
			writeError(w, http.StatusNotFound, "not_found", "This is not available.")
			return
		}
		s.sendPage(ctx, w, a, c.pages[n], width)
	default:
		writeError(w, http.StatusNotFound, "not_found", "This is not available.")
	}
}

// sendPage sends one comic page: as it is in the file, or made smaller when it is wider than
// the reader asked for. A page too large to prepare safely is refused, and only that page.
func (s *Service) sendPage(ctx context.Context, w http.ResponseWriter, a *archive, name string, width int) {
	entry := a.entries[name]
	if entry.UncompressedSize64 > maxPartSize {
		writeError(w, http.StatusUnprocessableEntity, "page_too_large", errPageTooLarge.Error())
		return
	}
	if width > 0 {
		out, contentType, done, err := smaller(ctx, entry.Open, width)
		switch {
		case errors.Is(err, errPageTooLarge):
			writeError(w, http.StatusUnprocessableEntity, "page_too_large", errPageTooLarge.Error())
			return
		case err != nil:
			writeError(w, http.StatusUnprocessableEntity, "page_damaged", "This page can't be shown.")
			return
		case out != nil:
			defer done()
			w.Header().Set("Cache-Control", "private, max-age=86400")
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", strconv.Itoa(len(out)))
			w.Write(out)
			return
		}
	}
	sendEntry(w, a, name, pageTypes[strings.ToLower(path.Ext(name))])
}

// sendEntry streams one entry of the archive as it unpacks; nothing is held whole. The archive
// reader stops at the size the entry declares, so a damaged or dishonest entry ends the reply
// early instead of sending more.
func sendEntry(w http.ResponseWriter, a *archive, name, contentType string) {
	entry := a.entries[name]
	rc, err := entry.Open()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "book_damaged", errDamaged.Error())
		return
	}
	defer rc.Close()
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatUint(entry.UncompressedSize64, 10))
	io.Copy(w, rc)
}

func fileType(format string) string {
	switch format {
	case FormatPDF:
		return "application/pdf"
	case FormatEPUB:
		return "application/epub+zip"
	case FormatCBZ:
		return "application/vnd.comicbook+zip"
	}
	return "application/octet-stream"
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Del("Content-Security-Policy")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retry": false}})
}
