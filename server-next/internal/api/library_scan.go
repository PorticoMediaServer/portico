package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func (s *Server) startLibraryScan(r *http.Request, lib db.AdminLibrary, req ScanRequest) (Job, error) {
	owner := CallerFrom(r.Context()).UserID
	var job db.Job
	var err error
	if req.Folder == "" {
		job, err = s.scans.Scan(r.Context(), lib.PublicID, owner, req.AllFiles)
	} else {
		var folder *db.LibraryPath
		for _, path := range lib.Paths {
			if strconv.FormatInt(path.ID, 10) == req.Folder {
				folder = &path
				break
			}
		}
		if folder == nil {
			return Job{}, ErrField("folder", "Choose a folder of this library.")
		}
		job, err = s.scans.ScanFolder(r.Context(), lib.PublicID, owner, req.AllFiles, *folder)
	}
	if errors.Is(err, scan.ErrScanBusy) {
		return Job{}, ErrConflict("scan_in_progress", "Another folder is being scanned. Let it finish or cancel it, then try again.")
	}
	if err != nil {
		return Job{}, libraryError(err)
	}
	return *jobOut(r, job), nil
}
