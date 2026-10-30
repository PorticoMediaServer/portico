package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// VerifyFunc checks the restored database at path: it opens it with
// persistence.Open (running the ordinary migrations), verifies it, retires
// the restored playback state, and closes it. A nil VerifyFunc selects
// VerifyRestored; tests inject failures through this seam.
type VerifyFunc func(ctx context.Context, path string) error

// VerifyRestored is the default verification for a database moved into place:
// quick_check plus RetireRestored in one gated transaction.
func VerifyRestored(ctx context.Context, path string) error {
	db, err := persistence.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	rows, err := tx.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return err
	}
	lines := []string{}
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, line)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil || len(lines) != 1 || lines[0] != "ok" {
		return errors.New("backup: restored database failed quick_check")
	}
	if err = playbackv1.RetireRestored(ctx, tx, time.Now()); err != nil {
		return err
	}
	return gated.Commit()
}

// ApplyStaged applies a staged restore at the next start, before the database
// is opened: the current database and replaced files move into a pre-restore
// backup, the staged copy moves into place, and verification runs the ordinary
// migrations. On failure the pre-restore copy moves back automatically and the
// outcome is recorded for the console. Nothing staged is a no-op. The server
// starts afterwards only when the journal is resolved; an unresolved restore
// must finish before serving the database.
//
// The current database is never deleted and never left anywhere a cleanup
// could remove it (O3 review of be/backups). The marker names the pre-restore
// folder before anything moves, and every state it can be in resumes safely
// after a crash:
//
//	staged     validated and waiting; nothing has moved.
//	preparing  the current files are moving into backups/<preRestore>/. Resume
//	           moves them back and starts again from staged.
//	switching  the pre-restore backup is complete; the staged files are moving
//	           into place. Resume finishes the move.
//	applied    the staged files are in place; verification is pending.
//	rolling_back  verification (or the move into place) failed; the previous
//	           files are moving back. Resume finishes the rollback (it is
//	           idempotent) and records rolled_back; it never verifies again.
//
// The pre-restore folder has its final name from the start (no .partial-
// prefix), so CleanupPartials never touches it; it has no manifest until it is
// complete, so List and Prune ignore it until then.
func ApplyStaged(ctx context.Context, state string, verify VerifyFunc) error {
	if verify == nil {
		verify = VerifyRestored
	}
	staged := storagepaths.Path(state, RestoreStagedDir)
	raw, err := os.ReadFile(filepath.Join(staged, RestoreMarkerFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var marker restoreMarker
	if err = json.Unmarshal(raw, &marker); err != nil {
		return err
	}
	if marker.StreamManifest {
		return applyStreamStaged(ctx, state, staged, marker, verify)
	}
	// A journal can be interrupted or damaged independently of staging. Never
	// allow it to name live database files, parent paths or recovery directories.
	entries := make([]FileEntry, len(marker.Files))
	for i, name := range marker.Files {
		if name == DatabaseName || name == DatabaseName+"-wal" {
			return ErrSourceInvalid
		}
		entries[i].Name = name
	}
	if _, ok := stateFileNames(entries); !ok {
		return ErrSourceInvalid
	}
	for _, name := range marker.Preserved {
		if name == DatabaseName || name == DatabaseName+"-wal" {
			return ErrSourceInvalid
		}
		entries = append(entries, FileEntry{Name: name})
	}
	if _, ok := stateFileNames(entries); !ok {
		return ErrSourceInvalid
	}
	if marker.PreRestore != "" && (checkID(marker.PreRestore) != nil || !strings.HasSuffix(marker.PreRestore, "-pre-restore")) {
		return ErrSourceInvalid
	}
	switch marker.State {
	case markerRestored, markerRolledBack:
		return finishCompleted(state, staged, marker)
	case markerRollingBack:
		if err = finishRollback(state, staged, marker); err != nil {
			return err
		}
		return errors.New("backup: the restore was rolled back: " + marker.Reason)
	case markerApplied:
		return finishApplied(ctx, state, staged, marker, verify)
	case markerSwitching:
		if err = moveStagedIntoPlace(staged, state, marker.Files); err != nil {
			return failSwitch(state, staged, marker, err)
		}
		marker.State = markerApplied
		if err = writeMarker(staged, marker); err != nil {
			return err
		}
		return finishApplied(ctx, state, staged, marker, verify)
	case markerPreparing:
		if err = undoPreparing(state, marker); err != nil {
			// Leave everything where it is: the next start tries again, and no
			// file is lost.
			return err
		}
		marker.State, marker.PreRestore, marker.Preserved = markerStaged, "", nil
		if err = writeMarker(staged, marker); err != nil {
			return err
		}
		return applyMarker(ctx, state, staged, marker, verify)
	case markerStaged:
		return applyMarker(ctx, state, staged, marker, verify)
	default:
		return errors.New("backup: unknown restore marker state")
	}
}

// applyStep is a test seam: a non-nil error at a named step stops ApplyStaged
// there exactly as a crash would (nothing is undone).
var applyStep = func(string) error { return nil }

func applyMarker(ctx context.Context, state, staged string, marker restoreMarker, verify VerifyFunc) error {
	serverDB := storagepaths.Path(state, "server.sqlite")
	stagedDB := filepath.Join(staged, DatabaseName)
	if _, err := os.Lstat(stagedDB); err != nil {
		return poisoned(state, staged, "staged database is missing")
	}
	now := time.Now().UTC()
	// A state with no database yet restores by moving the staged copy in;
	// there is nothing to preserve.
	_, currentErr := os.Lstat(serverDB)
	if os.IsNotExist(currentErr) {
		marker.State = markerSwitching
		if err := writeMarker(staged, marker); err != nil {
			return err
		}
		if err := moveStagedIntoPlace(staged, state, marker.Files); err != nil {
			return failSwitch(state, staged, marker, err)
		}
		marker.State = markerApplied
		if err := writeMarker(staged, marker); err != nil {
			return err
		}
		return finishApplied(ctx, state, staged, marker, verify)
	}
	if currentErr != nil {
		return currentErr
	}
	checkpointFile(serverDB)
	dir, err := backupsDir(state)
	if err != nil {
		return err
	}
	preID := now.Format(timestampLayout) + "-pre-restore"
	for n := 2; ; n++ {
		if _, err = os.Lstat(filepath.Join(dir, preID)); os.IsNotExist(err) {
			break
		} else if err != nil {
			return err
		}
		preID = now.Format(timestampLayout) + "-" + itoa(n) + "-pre-restore"
	}
	pre := filepath.Join(dir, preID)
	// 1. Name the pre-restore folder in the marker before anything moves.
	marker.State, marker.PreRestore = markerPreparing, preID
	if err = writeMarker(staged, marker); err != nil {
		return err
	}
	if err = os.MkdirAll(pre, 0700); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	if err = syncDirectory(dir); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	// 2. Move the current database and the files the source replaces into it.
	for _, name := range preparingNames(marker) {
		if err = applyStep("preparing:" + name); err != nil {
			return err
		}
		from, to := storagepaths.Path(state, filepath.FromSlash(name)), filepath.Join(pre, filepath.FromSlash(name))
		if name == DatabaseName || name == DatabaseName+"-wal" {
			from = storagepaths.Path(state, "server.sqlite"+strings.TrimPrefix(name, DatabaseName))
		}
		if err = moveIfPresent(from, to); err != nil {
			return failPreparing(state, staged, marker, err)
		}
	}
	_ = os.Remove(serverDB + "-shm")
	// The old database also references files absent from the incoming backup.
	// Keep those bytes independently of the live tree (where later GC may
	// remove them). Journal the copy-only names before creating any copies so
	// a preparing rollback never mistakes them for files that moved out.
	marker.Preserved, err = currentOnlyStateNames(ctx, state, pre)
	if err != nil {
		return failPreparing(state, staged, marker, err)
	}
	if err = writeMarker(staged, marker); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	for _, name := range marker.Preserved {
		if err = applyStep("preserving:" + name); err != nil {
			return err
		}
		entry, copyErr := copyOneFileContext(ctx, storagepaths.Path(state, filepath.FromSlash(name)), filepath.Join(pre, filepath.FromSlash(name)), name, nil)
		if copyErr != nil {
			return failPreparing(state, staged, marker, copyErr)
		}
		if strings.HasPrefix(name, "Metadata/artwork/") {
			digest := strings.TrimSuffix(strings.TrimPrefix(name, "Metadata/artwork/"), ".img")
			if raw, err := hex.DecodeString(digest); err == nil && len(raw) == sha256.Size && !strings.EqualFold(entry.SHA256, digest) {
				// Preserve the actual previous bytes, including pre-existing
				// damage, without preventing a valid incoming repair restore.
				log.Printf("warn: Previous artwork %s does not match its digest; its existing bytes were preserved in the pre-restore copy", name)
			}
		}
	}
	// Finish every SQLite read before hashing. A read-only WAL connection may
	// leave a rebuildable shared-memory file; it is not part of the backup.
	schemaVersion := storedVersion(filepath.Join(pre, DatabaseName))
	if err = removeRecoverySharedMemory(pre); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	hashed, err := hashTree(pre)
	if err != nil {
		return failPreparing(state, staged, marker, err)
	}
	manifest := Manifest{
		Format: Format, Version: Version,
		SchemaVersion: schemaVersion, CreatedAt: now.Format(time.RFC3339),
		Kind: KindPreRestore, Files: hashed,
	}
	if err = writeManifest(pre, manifest); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	if err = checkManifestHashes(pre, manifest.Files); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	if err = syncRestoreDirectories(state, pre, append(append([]string(nil), marker.Files...), marker.Preserved...)); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	// 3. The pre-restore backup is complete: switch.
	marker.State = markerSwitching
	if err = writeMarker(staged, marker); err != nil {
		return failPreparing(state, staged, marker, err)
	}
	if err = applyStep("switching"); err != nil {
		return err
	}
	if err = moveStagedIntoPlace(staged, state, marker.Files); err != nil {
		return failSwitch(state, staged, marker, err)
	}
	marker.State = markerApplied
	if err = writeMarker(staged, marker); err != nil {
		return err
	}
	if err = applyStep("applied"); err != nil {
		return err
	}
	return finishApplied(ctx, state, staged, marker, verify)
}

// preparingNames are the names moved out of the state folder, in order: the
// database, its WAL, then the replaced state files.
func preparingNames(marker restoreMarker) []string {
	return append([]string{DatabaseName, DatabaseName + "-wal"}, marker.Files...)
}

// undoPreparing moves whatever a preparing restore already moved out back
// into the state folder. It never overwrites a file that is there: in the
// preparing state nothing staged has moved in, so a present file is the
// current one.
func undoPreparing(state string, marker restoreMarker) error {
	if marker.PreRestore == "" {
		return nil
	}
	pre := storagepaths.Path(state, BackupsDir, marker.PreRestore)
	if _, err := os.Lstat(pre); os.IsNotExist(err) {
		return nil
	}
	for _, name := range preparingNames(marker) {
		from, to := filepath.Join(pre, filepath.FromSlash(name)), storagepaths.Path(state, filepath.FromSlash(name))
		if name == DatabaseName || name == DatabaseName+"-wal" {
			to = storagepaths.Path(state, "server.sqlite"+strings.TrimPrefix(name, DatabaseName))
		}
		if _, err := os.Lstat(from); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		if err := moveIfPresent(from, to); err != nil {
			return err
		}
	}
	if err := removePreservedCopies(pre, marker.Preserved); err != nil {
		return err
	}
	if err := removeRecoverySharedMemory(pre); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(pre, ManifestName))
	return removeEmptyTree(pre)
}

// failPreparing puts back what already moved and leaves the restore staged
// for the next start. The current state is exactly as it was.
func failPreparing(state, staged string, marker restoreMarker, cause error) error {
	if err := undoPreparing(state, marker); err != nil {
		return errors.Join(cause, err)
	}
	marker.State, marker.PreRestore, marker.Preserved = markerStaged, "", nil
	_ = writeMarker(staged, marker)
	return cause
}

// failSwitch: verification failed, or the staged files could not all move
// into place. The marker turns rolling_back before anything moves back, so a
// crash during the rollback resumes it (never "applied", which would verify
// whatever database happened to be in place and could record "restored").
func failSwitch(state, staged string, marker restoreMarker, cause error) error {
	if marker.PreRestore != "" {
		if _, err := os.Lstat(storagepaths.Path(state, BackupsDir, marker.PreRestore, DatabaseName)); err != nil {
			// Verification refused this state. Without the recovery database, keep the
			// journal unresolved instead of claiming a rollback that never happened.
			return errors.Join(cause, fmt.Errorf("backup: previous database unavailable: %w", err))
		}
	}
	marker.State, marker.Reason = markerRollingBack, cause.Error()
	if err := writeMarker(staged, marker); err != nil {
		return errors.Join(cause, err)
	}
	if err := finishRollback(state, staged, marker); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// finishRollback runs (or resumes) the rollback and records it. When a file
// can't move back, the marker stays rolling_back and the pre-restore folder is
// kept, so the next start tries again and nothing is lost.
func finishRollback(state, staged string, marker restoreMarker) error {
	if err := applyStep("rolling_back"); err != nil {
		return err
	}
	if err := rollback(state, marker); err != nil {
		writeResult(state, "rolled_back", marker.Reason+"; not every file moved back yet (the previous state is kept in backups/"+marker.PreRestore+"), the next start continues")
		return err
	}
	marker.State, marker.CompletedAt = markerRolledBack, time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeMarker(staged, marker); err != nil {
		return err
	}
	return finishCompleted(state, staged, marker)
}

// finishApplied verifies the database in place. Success records "restored";
// failure moves the pre-restore copy back. Completion journals the result before
// cleanup, which can be resumed independently.
func finishApplied(ctx context.Context, state, staged string, marker restoreMarker, verify VerifyFunc) error {
	serverDB := storagepaths.Path(state, "server.sqlite")
	if err := verify(ctx, serverDB); err != nil {
		return failSwitch(state, staged, marker, err)
	}
	marker.State, marker.CompletedAt = markerRestored, time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeMarker(staged, marker); err != nil {
		return err
	}
	return finishCompleted(state, staged, marker)
}

// Completion is journaled before cleanup. Replays publish the same result and
// never re-verify or roll back a database that has since accepted new writes.
func finishCompleted(state, staged string, marker restoreMarker) error {
	if _, err := time.Parse(time.RFC3339Nano, marker.CompletedAt); err != nil {
		return err
	}
	if err := writeResult(state, marker.State, marker.Reason, marker); err != nil {
		return err
	}
	if err := applyStep("completed:" + marker.State); err != nil {
		return err
	}
	if marker.State == markerRolledBack && marker.PreRestore != "" {
		pre := storagepaths.Path(state, BackupsDir, marker.PreRestore)
		if err := removePreservedCopies(pre, marker.Preserved); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(pre, ManifestName)); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Lstat(pre); err == nil {
			if err = removeEmptyTree(pre); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := syncDirectory(storagepaths.Path(state, BackupsDir)); err != nil {
			return err
		}
	}
	if marker.State == markerRestored {
		local := New(state, nil, "", "", 0)
		if err := local.pruneRecoveryCopies(); err != nil {
			return err
		}
		if err := syncDirectory(storagepaths.Path(state, BackupsDir)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	return syncDirectory(state)
}

// These names were copied, never moved out of live state. Removing them after
// rollback leaves the original bytes in place and lets the recovery directory
// be removed only once every moved file has also returned.
func removePreservedCopies(pre string, names []string) error {
	for _, name := range names {
		if err := os.Remove(filepath.Join(pre, filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func removeRecoverySharedMemory(pre string) error {
	err := os.Remove(filepath.Join(pre, DatabaseName+"-shm"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// File bytes were already flushed. Persist both rename directory entries
// before any durable phase change can authorize removal of the recovery copy.
func renameRestoreFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(to)); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(from))
}

func syncRestoreDirectories(first, second string, files []string) error {
	first, second = filepath.Clean(first), filepath.Clean(second)
	dirs := map[string]bool{first: true, second: true}
	for _, root := range []string{first, second} {
		for _, name := range files {
			dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(name)))
			for dir != root {
				dirs[dir] = true
				parent := filepath.Dir(dir)
				if parent == dir {
					return ErrSourceInvalid
				}
				dir = parent
			}
		}
	}
	for dir := range dirs {
		if err := syncDirectory(dir); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// rollback puts the pre-restore copy back over the restored files. It is
// idempotent, so a resumed rollback finishes what a crash interrupted:
//   - the database: the restored WAL and shared-memory files are removed
//     first, then the previous database is renamed over the restored one, then
//     its WAL (the pair moves together; after the checkpoint the WAL is empty,
//     but if the checkpoint failed it isn't). A crash can never leave the
//     previous database beside the restored database's WAL.
//   - replaced state files: the pre-restore manifest says which the previous
//     state had. One still in the pre-restore folder is renamed back over the
//     restored one; one the previous state had that is no longer in the folder
//     has already moved back and is left alone; one the previous state didn't
//     have was added by the restore and is removed.
//
// The pre-restore folder is removed only once everything is back. A state
// that had no database before the restore (no PreRestore) is cleared of the
// restored files.
func rollback(state string, marker restoreMarker) error {
	serverDB := storagepaths.Path(state, "server.sqlite")
	if marker.PreRestore == "" {
		var failed error
		for _, name := range append([]string{"server.sqlite-wal", "server.sqlite-shm", "server.sqlite"}, marker.Files...) {
			failed = errors.Join(failed, os.RemoveAll(storagepaths.Path(state, filepath.FromSlash(name))))
		}
		return errors.Join(failed, syncRestoreDirectories(state, state, marker.Files))
	}
	pre := storagepaths.Path(state, BackupsDir, marker.PreRestore)
	manifest, err := readManifest(pre)
	if err != nil {
		return errors.New("backup: the pre-restore manifest is unreadable; nothing was moved back")
	}
	listed := func(name string) bool {
		for _, entry := range manifest.Files {
			if entry.Name == name || strings.HasPrefix(entry.Name, name+"/") {
				return true
			}
		}
		return false
	}
	if _, err = os.Lstat(filepath.Join(pre, DatabaseName)); err == nil {
		for _, extra := range []string{serverDB + "-wal", serverDB + "-shm"} {
			if err = os.Remove(extra); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err = syncDirectory(state); err != nil {
			return err
		}
		if err = applyStep("rollback:wal-removed"); err != nil {
			return err
		}
		if err = renameRestoreFile(filepath.Join(pre, DatabaseName), serverDB); err != nil {
			return err
		}
		if err = applyStep("rollback:database"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		info, statErr := os.Lstat(serverDB)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() {
			return ErrSourceInvalid
		}
	}
	if _, err = os.Lstat(filepath.Join(pre, DatabaseName+"-wal")); err == nil {
		if err = renameRestoreFile(filepath.Join(pre, DatabaseName+"-wal"), serverDB+"-wal"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	var failed error
	for _, name := range marker.Files {
		from, to := filepath.Join(pre, filepath.FromSlash(name)), storagepaths.Path(state, filepath.FromSlash(name))
		if _, err := os.Lstat(from); err == nil {
			if err = os.RemoveAll(to); err != nil {
				failed = errors.Join(failed, err)
				continue
			}
			if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
				failed = errors.Join(failed, err)
				continue
			}
			failed = errors.Join(failed, renameRestoreFile(from, to))
			continue
		} else if !os.IsNotExist(err) {
			failed = errors.Join(failed, err)
			continue
		}
		if !listed(name) {
			failed = errors.Join(failed, os.RemoveAll(to))
		}
	}
	if failed != nil {
		return failed
	}
	// Keep the manifest until the terminal journal and result are durable.
	return syncRestoreDirectories(state, pre, marker.Files)
}

// moveStagedIntoPlace renames the staged database and state files over the
// live state. A name already moved (absent from staged) is skipped, so a
// resumed switch finishes the rest.
func moveStagedIntoPlace(staged, state string, files []string) error {
	if err := moveIfPresent(filepath.Join(staged, DatabaseName), storagepaths.Path(state, "server.sqlite")); err != nil {
		return err
	}
	if err := moveIfPresent(filepath.Join(staged, DatabaseName+"-wal"), storagepaths.Path(state, "server.sqlite-wal")); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(staged, DatabaseName+"-shm"))
	for _, name := range files {
		if err := moveIfPresent(filepath.Join(staged, filepath.FromSlash(name)), storagepaths.Path(state, filepath.FromSlash(name))); err != nil {
			return err
		}
	}
	return syncRestoreDirectories(state, staged, files)
}

// moveIfPresent renames from to to, creating to's parent; a missing from is
// not an error.
func moveIfPresent(from, to string) error {
	if _, err := os.Lstat(from); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	return renameRestoreFile(from, to)
}

// removeEmptyTree removes a folder whose files have all moved away; it refuses
// (keeps the folder) when any regular file is still inside.
func removeEmptyTree(root string) error {
	left := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			left = true
		}
		return nil
	})
	if left {
		return errors.New("backup: files are still in " + root)
	}
	return os.RemoveAll(root)
}

// poisoned records a staged restore that can never apply (tampered files) and
// clears it so the server starts clean.
func poisoned(state, staged, reason string) error {
	writeResult(state, "rolled_back", reason)
	_ = os.RemoveAll(staged)
	return errors.New("backup: " + reason)
}

// writeMarker replaces the marker atomically and durably: a new file is
// written and synced beside it, renamed over it, and the folder is synced. A
// crash leaves either the old marker or the new one, never a torn one, and the
// marker is on disk before the files it describes start moving.
func writeMarker(staged string, marker restoreMarker) error {
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(staged, RestoreMarkerFile)
	tmp := final + ".next"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDirectory(staged)
}

// RestoreUnresolved reports whether a restore stopped part-way: its marker is
// still there and says files may already have moved (or can't be read). The
// server must not start then, or it would serve a half-restored state folder
// (possibly an empty new database). A restore that is only staged has moved
// nothing, so the current state is intact and the server may start.
func RestoreUnresolved(state string) bool {
	raw, err := os.ReadFile(storagepaths.Path(state, RestoreStagedDir, RestoreMarkerFile))
	if os.IsNotExist(err) {
		return false
	}
	var marker restoreMarker
	if err != nil || json.Unmarshal(raw, &marker) != nil {
		return true
	}
	return marker.State != markerStaged
}

func writeResult(state, outcome, reason string, markers ...restoreMarker) error {
	result := RestoreResult{At: time.Now().UTC().Format(time.RFC3339), Outcome: outcome, Reason: reason}
	if len(markers) > 0 {
		if markers[0].CompletedAt != "" {
			result.At = markers[0].CompletedAt
		}
		result.JobID = markers[0].JobID
		result.OperationID = markers[0].OperationID
		result.SourceDigest = restoreSourceDigest(markers[0].Source)
		result.SchemaVersion = markers[0].SchemaVersion
		result.Recovery = markers[0].Recovery
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	final := storagepaths.Path(state, RestoreResultFile)
	if err = durableFile(final+".next", append(raw, '\n')); err != nil {
		return err
	}
	if err = os.Rename(final+".next", final); err != nil {
		return err
	}
	return syncDirectory(state)
}

// checkpointFile merges the WAL back into the database so the pre-restore
// move carries everything. Errors are ignored: the move that follows carries
// the -wal file too when one is there.
func checkpointFile(path string) {
	db, err := dbwork.OpenHandle(path, dbwork.DefaultPolicy())
	if err != nil {
		return
	}
	defer db.Close()
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

func backupsDir(state string) (string, error) {
	dir := storagepaths.Path(state, BackupsDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// storedVersion reads the current database's recorded schema version for the
// pre-restore manifest. It opens the file read-only and never migrates.
func storedVersion(serverDB string) int {
	db, err := dbwork.OpenHandle(serverDB, dbwork.DefaultPolicy())
	if err != nil {
		return 0
	}
	defer db.Close()
	var raw string
	if err = db.QueryRow(`SELECT value FROM configuration WHERE key='schema_version'`).Scan(&raw); err != nil {
		return 0
	}
	var version int
	_, _ = parseInt(raw, &version)
	return version
}

func parseInt(raw string, out *int) (bool, error) {
	var n int
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return true, nil
}

// hashTree lists a staging tree's files with their sizes and hashes.
func hashTree(root string) ([]FileEntry, error) {
	entries := []FileEntry{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			file.Close()
			return err
		}
		file.Close()
		sum := hash.Sum(nil)
		entries = append(entries, FileEntry{Name: filepath.ToSlash(name), Bytes: info.Size(), SHA256: hex.EncodeToString(sum)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
