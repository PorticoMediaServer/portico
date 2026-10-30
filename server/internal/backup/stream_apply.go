package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// Stream restores retain the same durable phases as the original journal.
// The manifest is the file registry; only its byte/line cursor is journaled.
func applyStreamStaged(ctx context.Context, state, staged string, marker restoreMarker, verify VerifyFunc) error {
	if marker.ManifestOffset < 0 || marker.ManifestLine < 0 || len(marker.Files) > 0 || len(marker.Preserved) > 0 {
		return ErrSourceInvalid
	}
	if marker.PreRestore != "" && (checkID(marker.PreRestore) != nil || !strings.HasSuffix(marker.PreRestore, "-pre-restore")) {
		return ErrSourceInvalid
	}
	if marker.State != markerStaged && marker.PreRestore == "" {
		return ErrSourceInvalid
	}
	if marker.State == markerRestored || marker.State == markerRolledBack {
		return finishStreamRestore(state, staged, marker)
	}
	m, err := readManifest(staged)
	if err != nil || m.Version != StreamVersion || m.streamHash != marker.ManifestSHA256 {
		return ErrSourceInvalid
	}
	// Validate the entire captured registry before any active file replacement.
	if err = visitManifest(ctx, staged, m, func(FileEntry) error { return nil }); err != nil {
		return err
	}
	if marker.State == markerRollingBack {
		if err = rollbackStream(ctx, state, staged, &marker, m); err != nil {
			return err
		}
		return errors.New("backup: restore rolled back: " + marker.Reason)
	}
	if marker.State == markerStaged {
		id := time.Now().UTC().Format(timestampLayout) + "-pre-restore"
		for n := 2; ; n++ {
			if _, err = os.Lstat(storagepaths.Path(state, BackupsDir, id)); os.IsNotExist(err) {
				break
			} else if err != nil {
				return err
			}
			id = time.Now().UTC().Format(timestampLayout) + "-" + itoa(n) + "-pre-restore"
		}
		marker.PreRestore = id
		marker.State = markerPreparing
		marker.ManifestOffset = 0
		marker.ManifestLine = 0
		if err = writeMarker(staged, marker); err != nil {
			return err
		}
	}
	pre := storagepaths.Path(state, BackupsDir, marker.PreRestore)
	if marker.State == markerPreparing {
		if err = prepareStreamRecovery(ctx, state, pre, marker); err != nil {
			return err
		}
		// Remove old SQLite sidecars exactly before the switching phase is
		// published; a resumed switch must retain already installed new WAL.
		for _, name := range []string{DatabaseName + "-wal", DatabaseName + "-shm"} {
			if err = os.Remove(storagepaths.Path(state, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		previousManifest, readErr := readManifest(pre)
		if readErr != nil {
			return readErr
		}
		marker.PreRestoreSHA256 = previousManifest.streamHash
		marker.State = markerSwitching
		marker.ManifestOffset = 0
		marker.ManifestLine = 0
		if err = writeMarker(staged, marker); err != nil {
			return err
		}
		if err = applyStep("switching"); err != nil {
			return err
		}
	}
	if marker.State == markerSwitching {
		interrupted := false
		resume, resumeLine := marker.ManifestOffset, marker.ManifestLine
		resumeSeen := resume == 0
		err = scanStream(ctx, m, func(e FileEntry, pos streamPosition) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if pos.End == resume {
				if pos.Line != resumeLine {
					return ErrSourceInvalid
				}
				resumeSeen = true
			}
			if pos.Start < resume && pos.End > resume {
				return ErrSourceInvalid
			}
			if pos.End <= resume {
				actual, err := hashFile(ctx, state, e.Name)
				if err != nil {
					return err
				}
				if actual.SHA256 != e.SHA256 || actual.Bytes != e.Bytes {
					return ErrIntegrity
				}
				return nil
			}
			from := filepath.Join(staged, filepath.FromSlash(e.Name))
			to := storagepaths.Path(state, filepath.FromSlash(e.Name))
			if _, err := os.Lstat(from); err == nil {
				if err = regularManifestFile(staged, e.Name); err != nil {
					return err
				}
				if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
					return err
				}
				if err = renameRestoreFile(from, to); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			// Even a damaged journal cursor cannot silently skip a missing/replaced file.
			actual, err := hashFile(ctx, state, e.Name)
			if err != nil {
				return err
			}
			if actual.SHA256 != e.SHA256 || actual.Bytes != e.Bytes {
				return ErrIntegrity
			}
			marker.ManifestOffset = pos.End
			marker.ManifestLine = pos.Line
			if err = writeMarker(staged, marker); err != nil {
				return err
			}
			err = applyStep("stream:switch:" + e.Name)
			interrupted = err != nil
			return err
		}, nil)
		if err == nil && !resumeSeen {
			err = ErrSourceInvalid
		}
		if err != nil {
			if interrupted {
				return err
			}
			return failStreamSwitch(ctx, state, staged, &marker, m, err)
		}
		marker.State = markerApplied
		marker.ManifestOffset = 0
		marker.ManifestLine = 0
		if err = writeMarker(staged, marker); err != nil {
			return err
		}
		if err = applyStep("applied"); err != nil {
			return err
		}
	}
	if marker.State != markerApplied {
		return ErrSourceInvalid
	}
	if err = verify(ctx, storagepaths.Path(state, DatabaseName)); err != nil {
		return failStreamSwitch(ctx, state, staged, &marker, m, err)
	}
	marker.State = markerRestored
	marker.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err = writeMarker(staged, marker); err != nil {
		return err
	}
	return finishStreamRestore(state, staged, marker)
}

func prepareStreamRecovery(ctx context.Context, state, pre string, marker restoreMarker) error {
	if err := os.MkdirAll(pre, 0700); err != nil {
		return err
	}
	if existing, err := readManifest(pre); err == nil && existing.Kind == KindPreRestore {
		return checkSourceHashes(pre, existing)
	}
	// Nothing live has moved. Discard an incomplete private copy so a retry
	// cannot retain stale files from an interrupted traversal.
	if err := os.RemoveAll(pre); err != nil {
		return err
	}
	if err := os.MkdirAll(pre, 0700); err != nil {
		return err
	}
	// Nothing live has moved during preparing. Re-copying an interrupted private
	// pre-restore copy is safe and never consumes the previous files.
	for _, rel := range append([]string{DatabaseName, DatabaseName + "-wal"}, stateFiles()...) {
		err := walkStateTree(ctx, state, rel, func(name string, e fs.DirEntry) error {
			if e.IsDir() {
				return nil
			}
			if !e.Type().IsRegular() {
				return ErrSourceInvalid
			}
			_, err := copyOneFileContext(ctx, storagepaths.Path(state, filepath.FromSlash(name)), filepath.Join(pre, filepath.FromSlash(name)), name, nil)
			if err != nil {
				return err
			}
			return applyStep("preparing:" + name)
		})
		if err != nil {
			return err
		}
		if rel == DatabaseName+"-wal" {
			if err = applyStep("preparing:" + rel); err != nil {
				return err
			}
		}
	}
	if err := checkPreviousArtworkPaths(ctx, state, pre); err != nil {
		return err
	}
	// storedVersion may fold private WAL; close it before final file hashes.
	version := storedVersion(filepath.Join(pre, DatabaseName))
	if err := removeRecoverySharedMemory(pre); err != nil {
		return err
	}
	m := Manifest{Format: Format, Version: StreamVersion, Kind: KindPreRestore, BackupID: marker.PreRestore, SchemaVersion: version, BaselineDigest: persistence.BaselineDigest(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	m, err := writeStreamTree(ctx, pre, m)
	if err != nil {
		return err
	}
	if err = checkSourceHashes(pre, m); err != nil {
		return err
	}
	return syncManifestDirectories(ctx, pre, m)
}

func failStreamSwitch(ctx context.Context, state, staged string, marker *restoreMarker, m Manifest, cause error) error {
	marker.State = markerRollingBack
	marker.Reason = cause.Error()
	marker.ManifestOffset = 0
	marker.ManifestLine = 0
	if err := writeMarker(staged, *marker); err != nil {
		return errors.Join(cause, err)
	}
	if err := applyStep("rolling_back"); err != nil {
		return err
	}
	if err := rollbackStream(ctx, state, staged, marker, m); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("backup: restore rolled back: %w", cause)
}

func rollbackStream(ctx context.Context, state, staged string, marker *restoreMarker, incoming Manifest) error {
	pre := storagepaths.Path(state, BackupsDir, marker.PreRestore)
	previous, err := readManifest(pre)
	if err != nil {
		return err
	}
	if previous.streamHash != marker.PreRestoreSHA256 {
		return ErrIntegrity
	}
	if err = checkSourceHashes(pre, previous); err != nil {
		return err
	}
	// The pre-restore copy remains intact throughout rollback. Remove candidate
	// SQLite sidecars first, including ones produced by failed verification.
	if marker.ManifestOffset == 0 {
		for _, name := range []string{DatabaseName + "-wal", DatabaseName + "-shm"} {
			if err = os.Remove(storagepaths.Path(state, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err = applyStep("rollback:wal-removed"); err != nil {
			return err
		}
	}
	// Remove only new incoming paths that had no predecessor. Their original
	// registry remains in staged after the payload itself moved away.
	if err = visitManifest(ctx, staged, incoming, func(e FileEntry) error {
		if _, err := os.Lstat(filepath.Join(pre, filepath.FromSlash(e.Name))); os.IsNotExist(err) {
			if err = os.Remove(storagepaths.Path(state, filepath.FromSlash(e.Name))); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	resume, resumeLine := marker.ManifestOffset, marker.ManifestLine
	resumeSeen := resume == 0
	err = scanStream(ctx, previous, func(e FileEntry, pos streamPosition) error {
		// Replay is idempotent; the cursor records exact durable progress, while
		// every replay still checks the full immutable pre-restore registry.
		if pos.End == resume {
			if pos.Line != resumeLine {
				return ErrSourceInvalid
			}
			resumeSeen = true
		}
		if pos.Start < resume && pos.End > resume {
			return ErrSourceInvalid
		}
		if pos.End <= resume {
			actual, err := hashFile(ctx, state, e.Name)
			if err != nil {
				return err
			}
			if actual.SHA256 != e.SHA256 || actual.Bytes != e.Bytes {
				return ErrIntegrity
			}
			return nil
		}
		dst := storagepaths.Path(state, filepath.FromSlash(e.Name))
		copied, err := copyOneFileContext(ctx, filepath.Join(pre, filepath.FromSlash(e.Name)), dst, e.Name, nil)
		if err != nil {
			return err
		}
		if copied.SHA256 != e.SHA256 || copied.Bytes != e.Bytes {
			return ErrIntegrity
		}
		if err = syncDirectory(filepath.Dir(dst)); err != nil {
			return err
		}
		marker.ManifestOffset = pos.End
		marker.ManifestLine = pos.Line
		if err = writeMarker(staged, *marker); err != nil {
			return err
		}
		if e.Name == DatabaseName {
			if err = applyStep("rollback:database"); err != nil {
				return err
			}
		}
		return applyStep("stream:rollback:" + e.Name)
	}, nil)
	if err != nil {
		return err
	}
	if !resumeSeen {
		return ErrSourceInvalid
	}
	marker.State = markerRolledBack
	marker.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	marker.ManifestOffset = 0
	marker.ManifestLine = 0
	if err = writeMarker(staged, *marker); err != nil {
		return err
	}
	return finishStreamRestore(state, staged, *marker)
}

func finishStreamRestore(state, staged string, marker restoreMarker) error {
	if _, err := time.Parse(time.RFC3339Nano, marker.CompletedAt); err != nil {
		return err
	}
	if err := writeResult(state, marker.State, marker.Reason, marker); err != nil {
		return err
	}
	if err := applyStep("completed:" + marker.State); err != nil {
		return err
	}
	// The previous copy is consumed only after a completed rollback has restored
	// every file and durably recorded its terminal outcome, matching original
	// restore retention. Successful restores retain it as recovery evidence.
	if marker.State == markerRestored {
		local := New(state, nil, "", "", 0)
		defer local.stop()
		if err := local.pruneRecoveryCopies(); err != nil {
			return err
		}
	}
	if marker.State == markerRolledBack {
		if err := os.RemoveAll(storagepaths.Path(state, BackupsDir, marker.PreRestore)); err != nil {
			return err
		}
		if err := syncDirectory(storagepaths.Path(state, BackupsDir)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(staged))
}

// Preserve the established refusal of a directory/symlink at an owner-artwork
// file path. The old database's reference registry is streamed, never retained.
func checkPreviousArtworkPaths(ctx context.Context, state, pre string) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(pre, DatabaseName), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, ownerArtworkReferences)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Print("warn: Previous database artwork references are unavailable; preserving all current metadata in the pre-restore copy")
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return err
		}
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != sha256.Size {
			return ErrIntegrity
		}
		info, err := os.Lstat(filepath.Join(state, "Metadata", "artwork", digest+".img"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrSourceInvalid
		}
	}
	return rows.Err()
}
