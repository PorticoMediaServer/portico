package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Restore and restart. Restoring checks a backup whole and unpacks it to be made at the next
// start; the server restarts, everyone signs in again, and keys and tokens in the backup work
// again, including any revoked since it was made. A restore needs the owner's password again.

// BackupRestore names the backup to restore and proves it is the owner asking.
type BackupRestore struct {
	BackupInspect
	CurrentPassword string `json:"currentPassword"`
	// Code is the authenticator or recovery code, for an owner with two-step sign-in.
	Code string `json:"code,omitempty"`
}

// RestoreStaged says the restore is ready and waits for a restart.
type RestoreStaged struct {
	// Playing is how many streams a restart now would stop.
	Playing int `json:"playing"`
	// CanRestart is false where the server cannot start itself again: restart it by hand.
	CanRestart bool `json:"canRestart"`
}

// Restart answers how many are playing, and whether the server restarted itself.
type Restart struct {
	Playing    int  `json:"playing"`
	CanRestart bool `json:"canRestart"`
	Restarting bool `json:"restarting"`
}

// canRestart says the server can start itself again: it replaces its own process, which
// Windows cannot do.
var canRestart = runtime.GOOS != "windows"

// RestartRequested is closed when the owner asks for a restart. The server stops cleanly and
// starts itself again.
func (s *Server) RestartRequested() <-chan struct{} { return s.restart }

func (s *Server) restoreRoutes() {
	Handle(s, "POST /v1/admin/backups/restore", Owner, func(r *http.Request, req BackupRestore) (RestoreStaged, error) {
		archive, name := "", req.ID
		switch {
		case req.ID != "" && req.Path == "":
			path, err := s.backups.Path(req.ID)
			if err != nil {
				return RestoreStaged{}, ErrNotFound()
			}
			archive = path
		case req.Path != "" && req.ID == "" && filepath.IsAbs(req.Path):
			archive, name = filepath.Clean(req.Path), filepath.Base(req.Path)
		default:
			return RestoreStaged{}, ErrInvalid("Name a listed backup by id, or a backup file by its full path.")
		}
		proof, err := s.passwordProof(r, req.CurrentPassword, req.Code)
		if err != nil {
			return RestoreStaged{}, proofError(err)
		}
		// The backup is checked whole before the password is counted as used for it.
		if _, err := backup.Inspect(r.Context(), archive, s.backups.DataDir); err != nil {
			return RestoreStaged{}, backupRefusal(err)
		}
		if err := s.db.ConfirmRestore(r.Context(), proof, name, s.now()); err != nil {
			return RestoreStaged{}, proofError(err)
		}
		if err := backup.Stage(r.Context(), archive, s.backups.DataDir, name, s.now()); err != nil {
			return RestoreStaged{}, backupRefusal(err)
		}
		return RestoreStaged{Playing: len(s.playback.All()), CanRestart: canRestart}, nil
	})
	Handle(s, "DELETE /v1/admin/backups/restore", Owner, func(r *http.Request, _ None) (None, error) {
		return None{}, backup.CancelRestore(s.backups.DataDir)
	})
	// Restart says how many are playing unless confirm=true; then the server restarts.
	Handle(s, "POST /v1/admin/restart", Owner, func(r *http.Request, _ None) (Restart, error) {
		out := Restart{Playing: len(s.playback.All()), CanRestart: canRestart}
		if r.URL.Query().Get("confirm") != "true" || !canRestart {
			return out, nil
		}
		out.Restarting = true
		s.restartOnce.Do(func() { close(s.restart) })
		return out, nil
	})
}

func backupRefusal(err error) error {
	var damaged *backup.ErrDamaged
	if errors.As(err, &damaged) {
		return refusal(http.StatusUnprocessableEntity, "backup_refused", damaged.Error(), false)
	}
	if errors.Is(err, db.ErrBadCredentials) {
		return proofError(err)
	}
	return err
}
