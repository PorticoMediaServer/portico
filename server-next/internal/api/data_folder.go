package api

import (
	"errors"
	"net/http"
	"os/user"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/datafolder"
)

// Who may open the data folder (12-console rules 10 and 75). While accounts other than the
// server's own can open it, the Dashboard has an alert whose one action is Fix permissions,
// and Troubleshooting says the same. The alert looks at the folder's own mode only: see
// datafolder.ReadableByOthers. It is not a notice: like a library folder that cannot be read,
// it stays on the Dashboard.

// DataFolderPermissions is whether other accounts on this computer can open the data folder.
type DataFolderPermissions struct {
	// Path is the data folder, and Account the account the server runs as: after Fix
	// permissions only that account can open it.
	Path    string `json:"path"`
	Account string `json:"account"`
	// ReadableByOthers is true while other accounts can open it: Fix permissions is offered.
	ReadableByOthers bool `json:"readableByOthers"`
	// Checked is false where a folder's mode bits do not say who may open it (Windows). There
	// is never an alert there.
	Checked bool `json:"checked"`
}

// The refusals of Fix permissions, each saying why in words the owner can act on.
var dataFolderRefusals = []struct {
	err  error
	code string
}{
	{datafolder.ErrNotOwner, "not_folder_owner"},
	{datafolder.ErrReadOnly, "read_only_file_system"},
	{datafolder.ErrChanged, "folder_changed"},
	{datafolder.ErrNotChecked, "not_checked_here"},
}

func (s *Server) dataFolderRoutes() {
	Handle(s, "GET /v1/admin/data-folder/permissions", Owner, func(r *http.Request, _ None) (DataFolderPermissions, error) {
		return s.dataFolderPermissions(), nil
	})
	// Safe to send again: a folder already private has nothing to change, and the answer is
	// the same.
	Handle(s, "POST /v1/admin/data-folder/permissions/fix", Owner, s.fixDataFolderPermissions)
}

func (s *Server) dataFolderPermissions() DataFolderPermissions {
	open, err := datafolder.ReadableByOthers(s.dataDir)
	return DataFolderPermissions{Path: s.dataDir, Account: serverAccount(), ReadableByOthers: open && err == nil, Checked: err == nil}
}

func (s *Server) fixDataFolderPermissions(r *http.Request, _ None) (DataFolderPermissions, error) {
	before := s.dataFolderPermissions()
	if err := datafolder.MakePrivate(s.dataDir); err != nil {
		for _, known := range dataFolderRefusals {
			if errors.Is(err, known.err) {
				return before, refusal(http.StatusConflict, known.code, known.err.Error()+".", known.err == datafolder.ErrChanged)
			}
		}
		return before, err
	}
	if before.ReadableByOthers {
		if err := s.db.Record(r.Context(), CallerFrom(r.Context()).UserID, "data_folder.private", "", "", s.now()); err != nil {
			return before, err
		}
	}
	return s.dataFolderPermissions(), nil
}

// serverAccount is the name of the account the server runs as, read once.
var serverAccount = sync.OnceValue(func() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
})
