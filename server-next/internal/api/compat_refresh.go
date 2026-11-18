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
	libraries, err := s.db.AdminLibraries(r.Context(), "")
	if err != nil {
		return err
	}
	scan := func(library, folder string) error {
		err := s.callAsWith(r, c, "POST", "/v1/admin/libraries/"+library+"/scan", nil, ScanRequest{Folder: folder}, nil)
		var refused *Error
		if errors.As(err, &refused) && refused.Code == "scan_in_progress" {
			return nil // another folder of the library is being scanned; watching and the next scan find the file
		}
		return err
	}
	switch op {
	case compat.LibraryRefresh:
		for _, lib := range libraries {
			if err := scan(lib.PublicID, ""); err != nil {
				return err
			}
		}
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
				return scan(lib.PublicID, "")
			}
		}
	case compat.MediaUpdated:
		var in struct{ Updates []struct{ Path string } }
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(&in); err != nil {
			return ErrInvalid("Send the changed files as Updates, each with its Path.")
		}
		asked := map[[2]string]bool{}
		for _, update := range in.Updates {
			changed := filepath.Clean(update.Path)
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
			if err := scan(at[0], at[1]); err != nil {
				return err
			}
		}
	}
	return nil
}
