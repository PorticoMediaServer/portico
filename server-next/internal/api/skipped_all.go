package api

import (
	"net/http"
	"strconv"
	"strings"
)

// The server's Files skipped list (12-console rule 72): every file a scan could not use, of
// every library, in one list, for Settings › Troubleshooting. A library's own list is
// GET /v1/admin/libraries/{id}/skipped; this is the same files with the library each is in.

// ServerSkippedFile is one file Portico could not prepare, with why, and the library it is in.
type ServerSkippedFile struct {
	LibraryID   string `json:"libraryId"`
	LibraryName string `json:"libraryName"`
	Path        string `json:"path"`
	Reason      string `json:"reason"`
	Message     string `json:"message"`
}

// serverSkippedPage is how many files one page of the server's list holds.
var serverSkippedPage = skippedPage

func (s *Server) skippedRoutes() {
	Handle(s, "GET /v1/admin/skipped", Owner, s.allSkippedFiles)
}

// allSkippedFiles answers a page of the list. The cursor is the last file's place in the list,
// so a page costs the same however far into the list it is, and a file skipped meanwhile
// further on is still reached.
func (s *Server) allSkippedFiles(r *http.Request, _ None) (Page[ServerSkippedFile], error) {
	out := Page[ServerSkippedFile]{Items: []ServerSkippedFile{}}
	var directory, file int64
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		a, b, ok := strings.Cut(cursor, ".")
		var errA, errB error
		directory, errA = strconv.ParseInt(a, 10, 64)
		file, errB = strconv.ParseInt(b, 10, 64)
		if !ok || errA != nil || errB != nil || directory < 0 || file < 0 {
			return out, refusal(http.StatusBadRequest, "cursor_expired", "Start the list again.", false)
		}
	}
	files, err := s.db.AllSkippedFiles(r.Context(), directory, file, serverSkippedPage+1)
	if err != nil {
		return out, err
	}
	more := len(files) > serverSkippedPage
	files = files[:min(len(files), serverSkippedPage)]
	for _, f := range files {
		out.Items = append(out.Items, ServerSkippedFile{LibraryID: f.LibraryID, LibraryName: f.LibraryName,
			Path: f.Path, Reason: f.Problem.String(), Message: skippedMessages[f.Problem]})
	}
	if more {
		last := files[len(files)-1]
		out.NextCursor = strconv.FormatInt(last.Directory, 10) + "." + strconv.FormatInt(last.File, 10)
	}
	return out, nil
}
