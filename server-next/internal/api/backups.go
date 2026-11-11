package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/backup"
)

// Storage & backups: the owner's backups. Their schedule, how many are kept, the folder and
// whether artwork is included are the `backups` settings document. See package backup.

// Backups is the backups page.
type Backups struct {
	Items []backup.Backup `json:"items"`
	// Running is the backup being made now.
	Running *backup.Progress `json:"running,omitempty"`
	// LastError says why the last scheduled backup failed, until one succeeds.
	LastError string `json:"lastError,omitempty"`
	// NextAt is when the next scheduled backup runs; absent for Never.
	NextAt *time.Time `json:"nextAt,omitempty"`
	Folder string     `json:"folder"`
	// PendingRestore is a restore staged and waiting for a restart; it is dropped if the
	// server has not restarted within an hour of staging it. Cancel is
	// DELETE /v1/admin/backups/restore.
	PendingRestore *backup.Pending `json:"pendingRestore,omitempty"`
	// Readable says that anyone with a backup file can read the working credentials inside it
	// (the server's keys, remote sources' sign-ins and the key file that opens them): no
	// backup here has a passphrase.
	Readable bool `json:"readable"`
}

// BackupStarted is a backup being made.
type BackupStarted struct {
	ID string `json:"id"`
}

func (b BackupStarted) reply(http.Header) int { return http.StatusAccepted }

// BackupInspect names a backup to check whole: a listed one by ID, or a file chosen in the
// folder explorer by its full Path.
type BackupInspect struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

// BackupInspection is what a backup holds and what restoring it would do.
type BackupInspection struct {
	Pictures    int      `json:"pictures"`
	KeyFiles    []string `json:"keyFiles"`
	Credentials []string `json:"credentials"`
	// CreatedAt is when a listed backup was made; everything changed since is undone.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// Restoring lists what restoring it does, for the page to say before it starts.
	Restoring []string `json:"restoring"`
}

// Backups are the server's backups: the server runs their schedule.
func (s *Server) Backups() *backup.Service { return s.backups }

func (s *Server) backupRoutes() {
	Handle(s, "GET /v1/admin/backups", Owner, func(r *http.Request, _ None) (Backups, error) {
		list, err := s.backups.List()
		out := Backups{Items: list, Running: s.backups.Running(), Folder: s.backups.Folder(), Readable: true,
			PendingRestore: backup.PendingRestore(s.backups.DataDir)}
		if out.Items == nil {
			out.Items = []backup.Backup{}
		}
		if failed := s.backups.LastError(); failed != nil {
			out.LastError = failed.Error()
		}
		if next := s.backups.Next(); !next.IsZero() {
			utc := next.UTC()
			out.NextAt = &utc
		}
		return out, err
	})
	Handle(s, "POST /v1/admin/backups", Owner, func(r *http.Request, _ None) (BackupStarted, error) {
		c := CallerFrom(r.Context())
		key, err := idempotencyKey(r)
		if err != nil {
			return BackupStarted{}, err
		}
		if id, err := s.db.CreatedBefore(r.Context(), c.UserID, key, "backup"); err != nil || id != "" {
			return BackupStarted{ID: id}, err
		}
		id, err := s.backups.Start(false)
		if errors.Is(err, backup.ErrRunning) {
			return BackupStarted{}, ErrConflict("backup_running", "A backup is being made. Try again when it has finished.")
		}
		if err != nil {
			return BackupStarted{}, err
		}
		if err := s.db.RecordBackup(r.Context(), c.UserID, "backup.create", id, s.now()); err != nil {
			return BackupStarted{}, err
		}
		return BackupStarted{ID: id}, s.db.RememberCreated(r.Context(), c.UserID, key, "backup", id, s.now())
	})
	Handle(s, "DELETE /v1/admin/backups/{id}", Owner, func(r *http.Request, _ None) (None, error) {
		id := r.PathValue("id")
		if err := s.backups.Delete(id); err != nil && !errors.Is(err, backup.ErrNotFound) {
			return None{}, err
		}
		return None{}, s.db.RecordBackup(r.Context(), CallerFrom(r.Context()).UserID, "backup.delete", id, s.now())
	})
	Handle(s, "POST /v1/admin/backups/inspect", Owner, s.inspectBackup)
}

func (s *Server) inspectBackup(r *http.Request, req BackupInspect) (BackupInspection, error) {
	var archive string
	var made *time.Time
	switch {
	case req.ID != "" && req.Path == "":
		path, err := s.backups.Path(req.ID)
		if err != nil {
			return BackupInspection{}, ErrNotFound()
		}
		archive = path
		if list, err := s.backups.List(); err == nil {
			for _, b := range list {
				if b.ID == req.ID {
					at := b.CreatedAt.UTC()
					made = &at
				}
			}
		}
	case req.Path != "" && req.ID == "":
		// A file the owner chose in the folder explorer. It is only read.
		if !filepath.IsAbs(req.Path) {
			return BackupInspection{}, ErrField("path", "Choose the backup file by its full path.")
		}
		if info, err := os.Stat(req.Path); err != nil || !info.Mode().IsRegular() {
			return BackupInspection{}, ErrField("path", "There is no file at this path.")
		}
		archive = filepath.Clean(req.Path)
	default:
		return BackupInspection{}, ErrInvalid("Name a listed backup by id, or a backup file by its path.")
	}
	contents, err := backup.Inspect(r.Context(), archive, s.backups.Folder())
	var damaged *backup.ErrDamaged
	if errors.As(err, &damaged) {
		return BackupInspection{}, refusal(http.StatusUnprocessableEntity, "backup_refused", damaged.Error(), false)
	}
	if err != nil {
		return BackupInspection{}, err
	}
	out := BackupInspection{Pictures: contents.Pictures, KeyFiles: contents.KeyFiles, Credentials: contents.Credentials, CreatedAt: made,
		Restoring: []string{"everything_since_undone", "removed_people_return", "server_restarts", "everyone_signs_in_again",
			"keys_in_backup_work_again"}}
	if out.KeyFiles == nil {
		out.KeyFiles = []string{}
	}
	return out, nil
}
