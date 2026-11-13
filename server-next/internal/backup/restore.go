package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
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
	// keepFile is what the server asked to be put into the restored database when it first
	// opens. See StageKeeping.
	keepFile = "restore-keep.json"
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
// already staged. name is how the backup is named. A backup without this server's mark over
// the very bytes unpacked is ErrNotMadeHere unless the owner confirmed it (notMadeHereOK).
func Stage(ctx context.Context, archivePath, dataDir, name string, now time.Time, notMadeHereOK bool) error {
	return StageKeeping(ctx, archivePath, dataDir, name, now, notMadeHereOK, nil)
}

// StageKeeping is Stage for a restore that must not put everything back: keep is handed to
// the database (db.Restored) in the commit that ends every sign-in, when the restored
// database first opens. It is how a server linked to a Portico Account keeps the link it has
// now, which Portico Services holds too, in place of the backup's earlier one. keep travels
// with the staged restore: a restore that is dropped or undone drops it.
func StageKeeping(ctx context.Context, archivePath, dataDir, name string, now time.Time, notMadeHereOK bool, keep []byte) error {
	return StageKeepingWithPassphrase(ctx, archivePath, dataDir, name, now, notMadeHereOK, keep, "")
}

// StageKeepingWithPassphrase verifies and decrypts before replacing any pending restore.
func StageKeepingWithPassphrase(ctx context.Context, archivePath, dataDir, name string, now time.Time, notMadeHereOK bool, keep []byte, phrase string) error {
	work, err := os.MkdirTemp(dataDir, ".staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// Without a readable key the mark cannot be checked, and only a confirmed restore goes on.
	key, keyErr := markKey(dataDir)
	if keyErr != nil && !notMadeHereOK {
		return ErrNotMadeHere
	}
	m := newMarker(key)
	if err := unpackWithPassphrase(archivePath, work, dataDir, m, phrase); err != nil {
		return err
	}
	if stated := statedMark(archivePath); !notMadeHereOK && (stated == "" || !hmac.Equal([]byte(stated), []byte(m.mark()))) {
		return ErrNotMadeHere
	}
	if err := db.CheckCopy(ctx, filepath.Join(work, "main.db")); err != nil {
		return &ErrDamaged{err.Error()}
	}
	if len(keep) > 0 {
		if err := writeSynced(filepath.Join(work, keepFile), keep); err != nil {
			return err
		}
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
func unpack(archivePath, dir, dataDir string, m *marker) error {
	return unpackWithPassphrase(archivePath, dir, dataDir, m, "")
}

func unpackWithPassphrase(archivePath, dir, dataDir string, m *marker, phrase string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return &ErrDamaged{"it cannot be opened"}
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return &ErrDamaged{"it is not a file"}
	}
	// Every byte read is marked as it passes, so the mark checked is of the bytes unpacked.
	source := io.TeeReader(f, m)
	defer io.Copy(io.Discard, source)
	decoded, err := backupReader(source, phrase)
	if err != nil {
		return err
	}
	zr, err := gzip.NewReader(decoded)
	if err != nil {
		if errors.Is(err, ErrPassphrase) {
			return err
		}
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
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return &ErrDamaged{"it is cut short or damaged"}
	}
	if _, err := io.Copy(io.Discard, decoded); err != nil {
		return &ErrDamaged{"it is cut short or damaged"}
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

// WaitingRestore is the staged restore the next start would make, for the owner's page: none
// once its hour is up, or when it was staged "after" now (a clock was wrong), since the start
// drops it then.
func WaitingRestore(dataDir string, now time.Time) *Pending {
	p := PendingRestore(dataDir)
	if p == nil {
		return nil
	}
	if age := now.Sub(p.StagedAt); age > pendingFor || age < -pendingFor {
		return nil
	}
	return p
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
		// Staged "after" this start means a clock was wrong at one end: its age is unknown.
		if age := now.Sub(p.StagedAt); age > pendingFor || age < -pendingFor {
			os.RemoveAll(pending)
			return made(dataDir), "the restore of " + p.Backup + " was not staged within the last hour and was dropped", nil
		}
		if err := swap(dataDir, p); err != nil {
			// Not made. If the backup's database has not moved in, put back what moved out
			// and drop the restore; only then is the folder fit to open.
			if !exists(filepath.Join(pending, "main.db")) {
				return "", "", err
			}
			if back := moveBack(dataDir, false); back != nil {
				return "", "", errors.Join(err, back)
			}
			os.Remove(journal)
			os.RemoveAll(pending)
			return made(dataDir), "the restore of " + p.Backup + " could not be made and was dropped; the server is as it was: " + err.Error(), dirsync.Sync(dataDir)
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
	// What the server asked to keep goes with the database it is for.
	if err := os.Rename(filepath.Join(pending, keepFile), filepath.Join(dataDir, keepFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Pictures and subtitle text are immutable, named by their contents. Add the backup's
	// files to each store; a move that fails leaves the journal for the next start to retry.
	touched := map[string]bool{}
	for _, store := range []string{"artwork", "subtitles"} {
		root := filepath.Join(pending, store)
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if e.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(pending, path)
			dst := filepath.Join(dataDir, rel)
			if exists(dst) {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return err
			}
			if err := os.Rename(path, dst); err != nil {
				return err
			}
			touched[filepath.Dir(dst)] = true
			return nil
		})
		if err != nil {
			return err
		}
	}
	for folder := range touched {
		if err := dirsync.Sync(folder); err != nil {
			return err
		}
	}
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
	// The original already holds what was to be kept.
	os.Remove(filepath.Join(dataDir, keepFile))
	return true, os.Remove(filepath.Join(dataDir, madeFile))
}

// Opened ends every sign-in after a restore, once the restored database has opened, puts in
// what the server asked to keep in the same commit, and only then forgets that the restore
// was made: a stop before this commit does it at the next start instead.
func Opened(ctx context.Context, d *db.DB, dataDir string, now time.Time) error {
	kept := filepath.Join(dataDir, keepFile)
	if !exists(filepath.Join(dataDir, madeFile)) {
		os.Remove(kept)
		return nil
	}
	keep, err := os.ReadFile(kept)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := d.Restored(ctx, keep, now); err != nil {
		return err
	}
	os.Remove(kept)
	return os.Remove(filepath.Join(dataDir, madeFile))
}

// OpenData opens the data folder's database for a start, the one way the server does it: a
// staged restore is made first (or one a stop cut short is finished or undone); a restored
// database that will not open gives way to the original; and after a restore every sign-in is
// ended, committed, before this returns. Nothing is served before it returns, so no request
// ever meets a restored database with its old sign-ins (an access token from before a restart
// is let in while the database still agrees with it).
func OpenData(ctx context.Context, dataDir string, log *slog.Logger, now func() time.Time) (*db.DB, error) {
	restored, note, err := Prepare(dataDir, now())
	if err != nil {
		// The data folder may be mid-swap: opening it now could make a new empty server or
		// serve a restored database with its old sign-ins. The next start finishes or undoes it.
		return nil, fmt.Errorf("a restore could not be finished or undone, so the server does not start; free space or fix the data folder and start it again: %w", err)
	}
	if note != "" {
		log.Info(note)
	}
	path := filepath.Join(dataDir, "main.db")
	d, err := db.Open(ctx, path)
	if err != nil && restored != "" {
		log.Error("the restored database would not open, so the server goes back to what it had", "backup", restored, "error", err)
		if _, err := PutBack(dataDir); err != nil {
			return nil, err
		}
		d, err = db.Open(ctx, path)
	}
	if err != nil {
		return nil, err
	}
	// After a restore everyone signs in again: the restored sign-ins are from before the
	// backup. Until this has committed, every start does it again.
	if err := Opened(ctx, d, dataDir, now()); err != nil {
		d.Close()
		return nil, err
	}
	if restored != "" {
		log.Info("the server was restored from a backup; everyone signs in again", "backup", restored)
	}
	return d, nil
}
