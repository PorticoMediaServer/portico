package api

import (
	"errors"
	"net/http"
	"os/user"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/datafolder"
)

// Who may open the data folder (12-console rules 10 and 75). While accounts other than the
// folder's owner can open it, the Dashboard has an alert, and Troubleshooting says the same.
// Its one action is Fix permissions where a fix can be made; where it cannot (a drive that
// does not keep who may open a folder, a folder the server's account does not own) the alert
// stays, because it is true, and says why instead. The alert looks at the folder's own mode
// only: see datafolder.Look. It is not a notice: like a library folder that cannot be read, it
// stays on the Dashboard.

// DataFolderPermissions is whether other accounts on this computer can open the data folder.
type DataFolderPermissions struct {
	// Path is the data folder. Owner is the account that owns it: after Fix permissions only
	// that account can open it. Account is the account the server runs as.
	Path    string `json:"path"`
	Owner   string `json:"owner"`
	Account string `json:"account"`
	// ReadableByOthers is true while other accounts can open it.
	ReadableByOthers bool `json:"readableByOthers"`
	// Fixable is whether Fix permissions is offered, while ReadableByOthers. Where it is not,
	// Reason says why (not_kept_here, not_folder_owner, read_only_file_system) and Message
	// says it in English.
	Fixable bool   `json:"fixable"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// Checked is false where a folder's mode bits do not say who may open it (Windows). There
	// is never an alert there.
	Checked bool `json:"checked"`
}

// The reasons a fix cannot be made, or was refused, each saying why.
var dataFolderRefusals = []struct {
	err  error
	code string
}{
	{datafolder.ErrNotKept, "not_kept_here"},
	{datafolder.ErrNotOwner, "not_folder_owner"},
	{datafolder.ErrReadOnly, "read_only_file_system"},
	{datafolder.ErrChanged, "folder_changed"},
	{datafolder.ErrNotChecked, "not_checked_here"},
}

func dataFolderReason(err error) (code, message string) {
	for _, known := range dataFolderRefusals {
		if errors.Is(err, known.err) {
			return known.code, known.err.Error() + "."
		}
	}
	return "", ""
}

// The data folder's look and fix; tests stand in for a drive that cannot keep the change.
var (
	lookDataFolder    = datafolder.Look
	makeFolderPrivate = datafolder.MakePrivate
)

func (s *Server) dataFolderRoutes() {
	Handle(s, "GET /v1/admin/data-folder/permissions", Owner, func(r *http.Request, _ None) (DataFolderPermissions, error) {
		return s.dataFolderPermissions(), nil
	})
	// Safe to send again: a folder already private has nothing to change, and the answer is
	// the same.
	Handle(s, "POST /v1/admin/data-folder/permissions/fix", Owner, s.fixDataFolderPermissions)
}

func (s *Server) dataFolderPermissions() DataFolderPermissions {
	st, err := lookDataFolder(s.dataDir)
	out := DataFolderPermissions{Path: s.dataDir, Owner: st.Owner, Account: serverAccount(), Checked: err == nil}
	if err == nil && st.ReadableByOthers {
		out.ReadableByOthers, out.Fixable = true, st.Fixable
		out.Reason, out.Message = dataFolderReason(st.Why)
	}
	return out
}

func (s *Server) fixDataFolderPermissions(r *http.Request, _ None) (DataFolderPermissions, error) {
	before := s.dataFolderPermissions()
	if err := makeFolderPrivate(s.dataDir); err != nil {
		if code, message := dataFolderReason(err); code != "" {
			return before, refusal(http.StatusConflict, code, message, code == "folder_changed")
		}
		return before, err
	}
	// MakePrivate looked again after the change: a change the drive did not keep was refused.
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
