package api

import (
	"errors"
	"net/http"
	"os"
)

// DvrFolderStatus names the effective destination, including the server's default.
// Not made is distinct from a folder that exists but cannot be written to.
type DvrFolderStatus struct {
	Folder  string `json:"folder"`
	State   string `json:"state"` // ready, not_made or unwritable
	Problem string `json:"problem,omitempty"`
}

func (s *Server) readDvrFolder(r *http.Request, _ None) (DvrFolderStatus, error) {
	out := DvrFolderStatus{Folder: s.dvrOptions().Folder, State: "ready"}
	look := &s.folderLook
	look.mu.Lock()
	problem := look.check(out.Folder, func() error {
		if _, err := os.Stat(out.Folder); os.IsNotExist(err) {
			return errRecordingFolderNotMade
		} else if err != nil {
			return err
		}
		return writable(out.Folder)
	})
	look.mu.Unlock()
	if errors.Is(problem, errRecordingFolderNotMade) {
		out.State = "not_made"
	} else if problem != nil {
		out.State, out.Problem = "unwritable", problem.Error()
	}
	return out, nil
}
