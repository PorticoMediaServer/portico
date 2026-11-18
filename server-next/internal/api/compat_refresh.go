package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/compat"
)

// compatRefresh answers the three ways a tool says files changed: Sonarr and Radarr after an
// import (/Library/Media/Updated with the files' paths, or /Library/Refresh), Bazarr after it
// saved a subtitle beside a title (/Items/{id}/Refresh). Each becomes a scan through the
// ordinary scan route, as the caller, so the same people may ask, and an unchanged file costs
// a scan only a look. A folder is scanned where the paths name one; otherwise the library.
func (s *Server) compatRefresh(r *http.Request, c Caller, op string, values []string) error {
	if op == compat.MediaUpdated {
		var in struct{ Updates []struct{ Path string } }
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(&in); err != nil {
			return ErrInvalid("Send the changed files as Updates, each with its Path.")
		}
		paths := make([]string, len(in.Updates))
		for n, update := range in.Updates {
			paths[n] = update.Path
		}
		_, err := s.scanChanged(r, c, paths)
		return err
	}
	libraries, err := s.db.AdminLibraries(r.Context(), "")
	if err != nil {
		return err
	}
	switch op {
	case compat.ItemRefresh:
		native := compat.PorticoID(values[0])
		if !strings.HasPrefix(native, "itm_") {
			return ErrNotFound()
		}
		it, err := s.db.PlaybackItem(r.Context(), viewer(r), native) // a title the key's person may not see is not found
		if err != nil {
			return refuse(err)
		}
		for _, lib := range libraries {
			if lib.ID == it.LibraryID {
				return s.scanAs(r, c, lib.PublicID, "")
			}
		}
	case compat.LibraryRefresh:
		for _, lib := range libraries {
			if err := s.scanAs(r, c, lib.PublicID, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// ChangedPaths is a tool's report that files changed on the server's disk (Sonarr and Radarr
// after an import, or a script): full paths of files or folders.
type ChangedPaths struct {
	Paths []string `json:"paths"`
}

// ChangedScans says how many library folders the report asked to scan; a path in no library
// is passed over.
type ChangedScans struct {
	Folders int `json:"folders"`
}

// changedPaths answers POST /v1/libraries/changed (definition 13 rule 19): the folders holding
// the paths are scanned, as the caller.
func (s *Server) changedPaths(r *http.Request, req ChangedPaths) (ChangedScans, error) {
	if len(req.Paths) == 0 || len(req.Paths) > 1000 {
		return ChangedScans{}, ErrField("paths", "Name between 1 and 1,000 changed paths.")
	}
	folders, err := s.scanChanged(r, CallerFrom(r.Context()), req.Paths)
	return ChangedScans{Folders: folders}, err
}

// scanChanged scans each library folder that holds one of the changed paths, as the caller,
// and says how many folders it asked to scan. A path in no library's folder is passed over.
// The compatible change report and the assistant's report_changed_files both use it.
func (s *Server) scanChanged(r *http.Request, c Caller, paths []string) (int, error) {
	libraries, err := s.db.AdminLibraries(r.Context(), "")
	if err != nil {
		return 0, err
	}
	asked := map[[2]string]bool{}
	for _, path := range paths {
		changed := filepath.Clean(path)
		for _, lib := range libraries {
			for _, folder := range lib.Paths {
				root := filepath.Clean(folder.Path)
				if folder.RemoteID == 0 && (changed == root || strings.HasPrefix(changed, root+string(filepath.Separator))) {
					asked[[2]string{lib.PublicID, strconv.FormatInt(folder.ID, 10)}] = true
				}
			}
		}
	}
	for at := range asked {
		if err := s.scanAs(r, c, at[0], at[1]); err != nil {
			return 0, err
		}
	}
	return len(asked), nil
}

// scanAs scans a library, or one of its folders, through the ordinary scan route as the caller.
func (s *Server) scanAs(r *http.Request, c Caller, library, folder string) error {
	err := s.callAsWith(r, c, "POST", "/v1/admin/libraries/"+library+"/scan", nil, ScanRequest{Folder: folder}, nil)
	var refused *Error
	if errors.As(err, &refused) && refused.Code == "scan_in_progress" {
		return nil // another folder of the library is being scanned; watching and the next scan find the file
	}
	return err
}
