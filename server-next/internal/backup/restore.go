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

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/dirsync"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
)

// Restore. A backup is unpacked once into the data folder and the database unpacked is the
// one checked whole (db.CheckCopy); that very file is what is put in place. Nothing in use
// changes until the next start, which must come within an hour (pendingFor) or the staged
// restore is dropped, so a restart weeks later never sends the server back by surprise.
//
// At the next start, before the database opens (Prepare), the swap is made under a journal:
//
//  1. restore-swap.json is written and synced: from here a stop always ends in either the
//     backup in place or the original back, never an empty data folder;
//  2. the database (with its -wal and -shm) and the key files the backup replaces move into
//     restore-previous; the backup's move in;
//  3. restore-made.json is written and synced, then the journal and the staging folder go.
//
// restore-made.json stays until, after the database has opened, every sign-in has been ended
// (Opened): a stop at any point before that commit ends them at the next start instead. If
// the restored database will not open, PutBack moves the original back. The original stays
// in restore-previous until the next restore, which only clears it while a database is in
// place.

const (
	pendingDir  = "restore-pending"
	previousDir = "restore-previous"
	readyFile   = "restore.json"
	journalFile = "restore-swap.json"
	madeFile    = "restore-made.json"
	// pendingFor is how long a staged restore waits for a restart before it is dropped: long
	// enough to restart by hand on Windows or in a container.
	pendingFor = time.Hour
)

// Pending says which backup a staged restore came from.
type Pending struct {
	Backup   string    `json:"backup"`
	StagedAt time.Time `json:"stagedAt"`
}

// Stage unpacks the backup at archivePath into the data folder, checks the database it holds
// whole, and leaves it as the restore to make at the next start, in place of any restore
// already staged. name is how the backup is named.
func Stage(ctx context.Context, archivePath, dataDir, name string, now time.Time) error {
	work, err := os.MkdirTemp(dataDir, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := unpack(archivePath, work, dataDir); err != nil {
		return err
	}
	if err := db.CheckCopy(ctx, filepath.Join(work, "main.db")); err != nil {
		return &ErrDamaged{err.Error()}
	}
	raw, _ := json.Marshal(Pending{Backup: name, StagedAt: now.UTC()})
	if err := writeSynced(filepath.Join(work, readyFile), raw); err != nil {
		return err
	}
	pending := filepath.Join(dataDir, pendingDir)
	if err := os.RemoveAll(pending); err != nil {
		return err
	}
	if err := os.Rename(work, pending); err != nil {
		return err
	}
	return dirsync.Sync(dataDir)
}

// unpack writes a backup's files into dir, refusing anything a backup never holds and a
// database larger than the free space of the data folder's disk allows.
func unpack(archivePath, dir, dataDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return &ErrDamaged{"it cannot be opened"}
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return &ErrDamaged{"it is not a file"}
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		return &ErrDamaged{"it is not a Portico backup"}
	}
	free, _ := diskspace.Free(dataDir)
	tr := tar.NewReader(zr)
	database := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return &ErrDamaged{"it is cut short or damaged"}
		}
		kind := entryKind(h.Name)
		if kind == "" || h.Typeflag != tar.TypeReg || h.Size < 0 {
			return &ErrDamaged{fmt.Sprintf("it holds %q, which is not a file a backup holds", h.Name)}
		}
		if free > 0 && h.Size > free {
			return fmt.Errorf("the data folder's disk has too little free space to restore this backup")
		}
		if kind == "database" {
			if database {
				return &ErrDamaged{"it holds two databases"}
			}
			database = true
		}
		dst := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return &ErrDamaged{fmt.Sprintf("it holds %q twice", h.Name)}
		}
		n, err := io.Copy(out, tr)
		if err = errors.Join(err, out.Sync(), out.Close()); err != nil || n != h.Size {
			return &ErrDamaged{"it is cut short or damaged"}
		}
	}
	if !database {
		return &ErrDamaged{"it holds no database"}
	}
	return nil
}

func writeSynced(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Sync(), f.Close())
}

// PendingRestore is the restore staged for the next start, or nil.
func PendingRestore(dataDir string) *Pending {
	pending := filepath.Join(dataDir, pendingDir)
	raw, err := os.ReadFile(filepath.Join(pending, readyFile))
	if err != nil {
		return nil
	}
	var p Pending
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(pending, "main.db")); err != nil {
		return nil
	}
	return &p
}

// CancelRestore drops a staged restore. One already being made at a start cannot be dropped.
func CancelRestore(dataDir string) error {
	if exists(filepath.Join(dataDir, journalFile)) {
		return nil
	}
	return os.RemoveAll(filepath.Join(dataDir, pendingDir))
}

func exists(name string) bool { _, err := os.Stat(name); return err == nil }

// databaseFiles are the database and the files SQLite keeps beside it.
var databaseFiles = []string{"main.db", "main.db-wal", "main.db-shm"}

// Prepare makes a staged restore, or finishes or undoes one a stop cut short, before the
// database opens. It answers the backup a restore was made from when one is waiting for its
// sign-ins to be ended ("" otherwise) and what it dropped and why, for the log.
func Prepare(dataDir string, now time.Time) (restored string, note string, err error) {
	journal := filepath.Join(dataDir, journalFile)
	pending := filepath.Join(dataDir, pendingDir)
	previous := filepath.Join(dataDir, previousDir)
	switch {
	case exists(journal):
		// A swap was cut short. If the backup's database has moved in, finish; if not, put
		// back whatever had moved out, and the staged restore is dropped.
		if exists(filepath.Join(pending, "main.db")) {
			if err := moveBack(dataDir, false); err != nil {
				return "", "", err
			}
			if err := os.Remove(journal); err != nil {
				return "", "", err
			}
			os.RemoveAll(pending)
			return made(dataDir), "a restore was cut short before it began and was dropped; the server is as it was", dirsync.Sync(dataDir)
		}
		if err := finish(dataDir); err != nil {
			return "", "", err
		}
	case !exists(filepath.Join(dataDir, "main.db")) && exists(filepath.Join(previous, "main.db")):
		// No database in place but the one set aside: put it back rather than start empty.
		if err := moveBack(dataDir, false); err != nil {
			return "", "", err
		}
		note = "the database set aside by a restore was put back"
	case PendingRestore(dataDir) != nil:
		p := PendingRestore(dataDir)
		if now.Sub(p.StagedAt) > pendingFor {
			return made(dataDir), "the restore of " + p.Backup + " was staged more than an hour ago and was dropped", os.RemoveAll(pending)
		}
		if err := swap(dataDir, p); err != nil {
			return "", "", err
		}
		note = "the server was restored from " + p.Backup
	default:
		// A staging folder with nothing to restore is what is left of an unfinished one.
		os.RemoveAll(pending)
	}
	return made(dataDir), note, nil
}

// made is the backup a restore was made from while its sign-ins wait to be ended.
func made(dataDir string) string {
	raw, err := os.ReadFile(filepath.Join(dataDir, madeFile))
	if err != nil {
		return ""
	}
	var p Pending
	json.Unmarshal(raw, &p)
	if p.Backup == "" {
		p.Backup = "a backup"
	}
	return p.Backup
}

// swap puts a staged restore in place under the journal.
func swap(dataDir string, p *Pending) error {
	pending := filepath.Join(dataDir, pendingDir)
	previous := filepath.Join(dataDir, previousDir)
	// The original of an earlier restore is cleared only now, with a database in place.
	if err := os.RemoveAll(previous); err != nil {
		return err
	}
	if err := os.MkdirAll(previous, 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	if err := writeSynced(filepath.Join(dataDir, journalFile), raw); err != nil {
		return err
	}
	if err := dirsync.Sync(dataDir); err != nil {
		return err
	}
	// What moves aside: the database, and each key file the backup brings its own of.
	aside := append([]string{}, databaseFiles...)
	for _, name := range keyFiles {
		if exists(filepath.Join(pending, name)) {
			aside = append(aside, name)
		}
	}
	for _, name := range aside {
		if err := os.Rename(filepath.Join(dataDir, name), filepath.Join(previous, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := dirsync.Sync(previous); err != nil {
		return err
	}
	return finish(dataDir)
}

// finish moves the backup's files in, records that its sign-ins must be ended, and clears the
// journal and the staging folder. It is safe to run again after a stop.
func finish(dataDir string) error {
	pending := filepath.Join(dataDir, pendingDir)
	raw, _ := os.ReadFile(filepath.Join(dataDir, journalFile))
	for _, name := range append([]string{"main.db"}, keyFiles...) {
		if err := os.Rename(filepath.Join(pending, name), filepath.Join(dataDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	// Pictures are added to the store; one already there is the same picture.
	filepath.WalkDir(filepath.Join(pending, "artwork"), func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(pending, path)
		dst := filepath.Join(dataDir, rel)
		if !exists(dst) {
			os.MkdirAll(filepath.Dir(dst), 0o700)
			os.Rename(path, dst)
		}
		return nil
	})
	if err := writeSynced(filepath.Join(dataDir, madeFile), raw); err != nil {
		return err
	}
	if err := dirsync.Sync(dataDir); err != nil {
		return err
	}
	os.Remove(filepath.Join(pending, readyFile))
	if err := os.Remove(filepath.Join(dataDir, journalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	os.RemoveAll(pending)
	return dirsync.Sync(dataDir)
}

// moveBack puts the files set aside in restore-previous back in the data folder. With
// replacing, the backup's database is in place and gives way; without, nothing in the data
// folder is touched and only what is missing there comes back.
func moveBack(dataDir string, replacing bool) error {
	previous := filepath.Join(dataDir, previousDir)
	if replacing && exists(filepath.Join(previous, "main.db")) {
		for _, f := range databaseFiles {
			os.Remove(filepath.Join(dataDir, f))
		}
	}
	for _, name := range append(append([]string{}, databaseFiles...), keyFiles...) {
		from, to := filepath.Join(previous, name), filepath.Join(dataDir, name)
		if !exists(from) || !replacing && exists(to) {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return dirsync.Sync(dataDir)
}

// PutBack undoes a restore whose database would not open: the original goes back and the
// restore is forgotten. It answers false when no restore was made.
func PutBack(dataDir string) (bool, error) {
	if !exists(filepath.Join(dataDir, madeFile)) || !exists(filepath.Join(dataDir, previousDir, "main.db")) {
		return false, nil
	}
	if err := moveBack(dataDir, true); err != nil {
		return false, err
	}
	return true, os.Remove(filepath.Join(dataDir, madeFile))
}

// Opened ends every sign-in after a restore, once the restored database has opened, and only
// then forgets that the restore was made: a stop before this commit ends them at the next
// start instead.
func Opened(ctx context.Context, d *db.DB, dataDir string, now time.Time) error {
	if !exists(filepath.Join(dataDir, madeFile)) {
		return nil
	}
	if err := d.EndEverySignIn(ctx, now); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dataDir, madeFile))
}
