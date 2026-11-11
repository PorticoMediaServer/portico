package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Restore. A backup that passed its check whole is unpacked into the data folder as a pending
// restore; nothing in use changes. At the next start, before the database opens, the pending
// files take the place of the database and key files in use, which are kept aside; if the
// restored database will not open, what was in use is put back. Pictures are added to the
// picture store (they are named by what is in them, so nothing is overwritten).

const (
	pendingDir  = "restore-pending"
	previousDir = "restore-previous"
	readyFile   = "restore.json"
)

// Pending says which backup a pending restore came from.
type Pending struct {
	Backup   string    `json:"backup"`
	StagedAt time.Time `json:"stagedAt"`
}

// Stage checks the backup at archivePath whole and unpacks it as the restore to make at the
// next start, replacing any restore already pending. name is how the backup is named.
func Stage(ctx context.Context, archivePath, dataDir, name string, now time.Time) error {
	if _, err := Inspect(ctx, archivePath, dataDir); err != nil {
		return err
	}
	work, err := os.MkdirTemp(dataDir, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return &ErrDamaged{"it is not a Portico backup"}
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return &ErrDamaged{"it is cut short or damaged"}
		}
		// Inspect has refused every name that is not the database, a key file or a picture
		// under the store's own name, so each joins to a place inside work.
		if entryKind(h.Name) == "" || h.Typeflag != tar.TypeReg {
			return &ErrDamaged{fmt.Sprintf("it holds %q", h.Name)}
		}
		dst := filepath.Join(work, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		if err = errors.Join(err, out.Sync(), out.Close()); err != nil {
			return err
		}
	}
	raw, _ := json.Marshal(Pending{Backup: name, StagedAt: now.UTC()})
	if err := os.WriteFile(filepath.Join(work, readyFile), raw, 0o600); err != nil {
		return err
	}
	pending := filepath.Join(dataDir, pendingDir)
	if err := os.RemoveAll(pending); err != nil {
		return err
	}
	return os.Rename(work, pending)
}

// PendingRestore is the restore waiting for the next start, or nil.
func PendingRestore(dataDir string) *Pending {
	raw, err := os.ReadFile(filepath.Join(dataDir, pendingDir, readyFile))
	if err != nil {
		return nil
	}
	var p Pending
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

// CancelRestore drops a pending restore.
func CancelRestore(dataDir string) error { return os.RemoveAll(filepath.Join(dataDir, pendingDir)) }

// databaseFiles are the database and the files SQLite keeps beside it.
var databaseFiles = []string{"main.db", "main.db-wal", "main.db-shm"}

// ApplyPending makes a pending restore, before the database opens. It answers which backup
// was restored ("" when none was pending) and a function that puts back what was in use, for
// when the restored database will not open. The files that were in use are kept in
// restore-previous until the next restore.
func ApplyPending(dataDir string) (restored string, undo func() error, err error) {
	pending := filepath.Join(dataDir, pendingDir)
	p := PendingRestore(dataDir)
	if p == nil {
		return "", nil, nil
	}
	previous := filepath.Join(dataDir, previousDir)
	if err := os.RemoveAll(previous); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(previous, 0o700); err != nil {
		return "", nil, err
	}
	replaced := append(append([]string{}, databaseFiles...), keyFiles...)
	var moved []string
	undo = func() error {
		var errs []error
		for _, name := range databaseFiles {
			errs = append(errs, ignoreMissing(os.Remove(filepath.Join(dataDir, name))))
		}
		for _, name := range moved {
			errs = append(errs, os.Rename(filepath.Join(previous, name), filepath.Join(dataDir, name)))
		}
		return errors.Join(errs...)
	}
	for _, name := range replaced {
		err := os.Rename(filepath.Join(dataDir, name), filepath.Join(previous, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", nil, errors.Join(err, undo())
		}
		moved = append(moved, name)
	}
	for _, name := range append([]string{"main.db"}, keyFiles...) {
		err := os.Rename(filepath.Join(pending, name), filepath.Join(dataDir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", nil, errors.Join(err, undo())
		}
	}
	// Pictures are added to the store; one already there is the same picture.
	pictures := filepath.Join(pending, "artwork")
	filepath.WalkDir(pictures, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(pending, path)
		dst := filepath.Join(dataDir, rel)
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
		os.MkdirAll(filepath.Dir(dst), 0o700)
		return os.Rename(path, dst)
	})
	return p.Backup, undo, os.RemoveAll(pending)
}
