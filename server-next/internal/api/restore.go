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
// again, including any revoked since it was made. A restore needs the owner's password again,
// or, on a server linked to a Portico Account, where the owner has no password, a fresh
// approval from that account.

// BackupRestore names the backup to restore and proves it is the owner asking.
type BackupRestore struct {
	BackupInspect
	CurrentPassword string `json:"currentPassword"`
	// Code is the authenticator or recovery code, for an owner with two-step sign-in.
	Code string `json:"code,omitempty"`
	// Approval, on a server that uses Portico Accounts, takes the password's place: a fresh
	// approval by the Portico Account that owns the server, started with
	// POST /v1/admin/portico/link and brought back here instead of to its finish.
	Approval string `json:"approval,omitempty"`
	// NotMadeHereConfirmed says the owner was warned that this backup was not made by this
	// server, or was changed since, and typed the confirmation. Without it such a backup is
	// refused with 409 backup_not_made_here and the warning.
	NotMadeHereConfirmed bool `json:"notMadeHereConfirmed,omitempty"`
}

// notMadeHere is the warning for a backup without this server's mark. Where this server's
// key cannot be read, it says that instead: the restore still goes on once confirmed.
func (s *Server) notMadeHere() *Error {
	if problem := backup.MarkKeyProblem(s.backups.DataDir); problem != "" {
		s.log.Warn("a restore cannot check the backup's mark", "error", problem)
		return ErrConflict("backup_not_made_here", "This server's backup key can't be read, so it can't tell whether this backup was made here "+
			"or has been changed since. Restoring it replaces every account, including yours, with what is in the file.")
	}
	return ErrConflict("backup_not_made_here", "This backup wasn't made by this server, or has been changed since. "+
		"Restoring it replaces every account, including yours, with what is in the file.")
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
		// A backup this server did not make is warned of before anything else is asked.
		if !req.NotMadeHereConfirmed && !backup.MadeHere(s.backups.DataDir, archive) {
			return RestoreStaged{}, s.notMadeHere()
		}
		if _, linked, err := s.db.Link(r.Context()); err != nil {
			return RestoreStaged{}, err
		} else if linked {
			// The owner of a Portico Accounts server has no password here to ask for.
			if err := s.porticoOwnerApproved(r, req.Approval); err != nil {
				return RestoreStaged{}, err
			}
			if err := s.db.RecordRestore(r.Context(), CallerFrom(r.Context()).UserID, name, s.now()); err != nil {
				return RestoreStaged{}, err
			}
		} else {
			proof, err := s.passwordProof(r, req.CurrentPassword, req.Code)
			if err != nil {
				return RestoreStaged{}, proofError(err)
			}
			if err := s.db.ConfirmRestore(r.Context(), proof, name, s.now()); err != nil {
				return RestoreStaged{}, proofError(err)
			}
		}
		// The approval made a new link, here and at Portico Services. It is carried across
		// the restore, so the restored server starts on the link Portico Services holds.
		keep, err := s.db.KeptLink(r.Context())
		if err != nil {
			return RestoreStaged{}, err
		}
		// Unpacked once; the database that will be put in place is the one checked, and its
		// mark is checked over the very bytes unpacked.
		err = backup.StageKeepingWithPassphrase(r.Context(), archive, s.backups.DataDir, name, s.now(), req.NotMadeHereConfirmed, keep, req.Passphrase)
		if errors.Is(err, backup.ErrNotMadeHere) {
			return RestoreStaged{}, s.notMadeHere()
		}
		if err != nil {
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
	if errors.Is(err, backup.ErrPassphrase) {
		return ErrField("passphrase", "Enter the passphrase used to make this backup.")
	}
	var damaged *backup.ErrDamaged
	if errors.As(err, &damaged) {
		return refusal(http.StatusUnprocessableEntity, "backup_refused", damaged.Error(), false)
	}
	if errors.Is(err, db.ErrBadCredentials) {
		return proofError(err)
	}
	return err
}
