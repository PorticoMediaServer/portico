package backup

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"runtime"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Source selects the restore source: one listed backup, or a path on the
// server. A path is a backup folder (with a manifest) or a bare .db file that
// is a Portico database. Exactly one of the two is set.
type Source struct {
	BackupID string `json:"backupId,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Validation summarises the staged restore's checks for the reply.
type Validation struct {
	SchemaVersion int `json:"schemaVersion"`
	// Integrity is "ok", or "recovered" when the database was damaged and
	// Recovery says what restore repaired or could not read.
	Integrity string                `json:"integrity"`
	Recovery  *persistence.Recovery `json:"recovery,omitempty"`
}

// StageResult answers a staged restore: restart to apply it.
type StageResult struct {
	Staged          bool       `json:"staged"`
	RestartRequired bool       `json:"restartRequired"`
	Validation      Validation `json:"validation"`
}

// restoreMarker is restore-staged/restore.json: the one staged-restore marker.
// Its states and how each resumes after a crash are described on ApplyStaged.
type restoreMarker struct {
	OperationID      string   `json:"operationId,omitempty"`
	JobID            string   `json:"jobId,omitempty"`
	State            string   `json:"state"`
	Source           Source   `json:"source"`
	SchemaVersion    int      `json:"schemaVersion"`
	Files            []string `json:"files"`
	PreRestoreSHA256 string   `json:"preRestoreSha256,omitempty"`
	ManifestSHA256   string   `json:"manifestSha256,omitempty"`
	StreamManifest   bool     `json:"streamManifest,omitempty"`
	ManifestOffset   int64    `json:"manifestOffset,omitempty"`
	ManifestLine     int64    `json:"manifestLine,omitempty"`
	// Preserved files were copied from the current state, not replaced by the
	// incoming restore. They belong to the recovery copy only.
	Preserved  []string `json:"preserved,omitempty"`
	PreRestore string   `json:"preRestore,omitempty"`
	// Recovery is what staging did to a damaged database, kept for the
	// restore's recorded outcome.
	Recovery *persistence.Recovery `json:"recovery,omitempty"`
	// Reason is why a rolling_back restore is being rolled back.
	Reason      string `json:"reason,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

const (
	markerStaged      = "staged"
	markerPreparing   = "preparing"
	markerSwitching   = "switching"
	markerApplied     = "applied"
	markerRollingBack = "rolling_back"
	markerRestored    = "restored"
	markerRolledBack  = "rolled_back"
)

// Stage validates the source and copies it into restore-staged/.incoming,
// writes the marker, and renames it into place. It answers the validation
// summary and, through the injected restart func, asks the server to restart
// so the next start applies the staged copy.
func (s *Service) Stage(ctx context.Context, source Source) (StageResult, error) {
	return s.StageWithPassphrase(ctx, source, "")
}

func (s *Service) StageWithPassphrase(ctx context.Context, source Source, passphrase string) (StageResult, error) {
	return s.stageWithPassphrase(ctx, source, passphrase, restoreIdentity{})
}

func (s *Service) stageWithPassphrase(ctx context.Context, source Source, passphrase string, work restoreIdentity) (StageResult, error) {
	var out StageResult
	ctx, leave, enterErr := s.enter(ctx)
	if enterErr != nil {
		return out, enterErr
	}
	defer leave()
	if (source.BackupID == "") == (source.Path == "") {
		return out, ErrSourceInvalid
	}
	if source.Path != "" && (!filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path) {
		return out, ErrSourceInvalid
	}
	if len(passphrase) > 1024 {
		return out, ErrInvalid
	}
	if s.restoreUnavailable {
		return out, ErrRestoreLockUnavailable
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	// Staging replaces restore-staged; never while it holds a restore that has
	// started moving files.
	if RestoreUnresolved(s.state) {
		return out, ErrRestorePending
	}
	originalSource := source
	source, liveCleanup, err := s.snapshotLiveSource(ctx, source)
	if err != nil {
		return out, err
	}
	defer liveCleanup()
	source, cleanup, err := s.decryptSource(ctx, source, passphrase)
	if err != nil {
		return out, err
	}
	defer cleanup()
	s.restoreProgress(work, "verify-files", 0, 0)
	if dir, streamed, err := s.streamSource(source); err != nil {
		return out, err
	} else if streamed {
		return s.stageStream(ctx, dir, originalSource, work)
	}
	srcDB, srcDir, files, manifest, err := s.resolveSource(source)
	if err != nil {
		return out, err
	}
	var estimate int64
	if info, err := os.Stat(srcDB); err != nil || !info.Mode().IsRegular() {
		return out, ErrSourceInvalid
	} else {
		estimate = info.Size()
	}
	if wal, err := os.Stat(srcDB + "-wal"); err == nil && wal.Mode().IsRegular() {
		estimate += wal.Size()
	}
	for _, name := range files {
		if info, err := os.Stat(filepath.Join(srcDir, filepath.FromSlash(name))); err != nil || !info.Mode().IsRegular() {
			return out, ErrSourceInvalid
		} else {
			estimate += info.Size()
		}
	}
	if err = checkFreeSpace(s.state, estimate); err != nil {
		return out, err
	}
	staged := storagepaths.Path(s.state, RestoreStagedDir)
	incoming := filepath.Join(staged, ".incoming")
	complete := storagepaths.Path(s.state, ".staged-complete")
	// A previous staged restore is superseded. The marker only ever appears
	// at restore-staged/restore.json, so a crash anywhere below leaves either
	// the previous staged copy or nothing behind.
	_ = os.RemoveAll(complete)
	_ = os.RemoveAll(staged)
	if err = os.MkdirAll(incoming, 0700); err != nil {
		return out, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(incoming)
		}
	}()
	if _, err = copyOneFileContext(ctx, srcDB, filepath.Join(incoming, DatabaseName), DatabaseName, func(done, total int64) { s.restoreProgress(work, "copy", done, total) }); err != nil {
		if errors.Is(err, ErrIntegrity) {
			return out, err
		}
		return out, ErrSourceInvalid
	}
	if wal, statErr := os.Lstat(srcDB + "-wal"); statErr == nil {
		if !wal.Mode().IsRegular() {
			return out, ErrSourceInvalid
		}
		if _, err = copyOneFileContext(ctx, srcDB+"-wal", filepath.Join(incoming, DatabaseName+"-wal"), DatabaseName+"-wal", nil); err != nil {
			return out, ErrSourceInvalid
		}
	} else if !os.IsNotExist(statErr) {
		return out, ErrSourceInvalid
	}
	for _, name := range files {
		if _, err = copyOneFile(filepath.Join(srcDir, filepath.FromSlash(name)), filepath.Join(incoming, filepath.FromSlash(name)), name); err != nil {
			return out, ErrSourceInvalid
		}
	}
	// Verify the private copy against the exact manifest captured before copying.
	// A source that changes after initial verification must not be staged. Do this
	// before SQLite inspection can checkpoint or otherwise rewrite the candidate.
	if manifest != nil {
		if err = checkSourceHashes(incoming, *manifest); err != nil {
			return out, err
		}
	}
	s.restoreProgress(work, "integrity", estimate, estimate)
	// The staged copy is private, so inspection may repair it in place.
	inspected, err := persistence.InspectCandidate(ctx, incoming, DatabaseName)
	if err != nil {
		return out, err
	}
	schemaVersion := inspected.SchemaVersion
	if err = syncRestoreDirectories(incoming, incoming, files); err != nil {
		return out, err
	}
	stagedManifest, streamErr := writeStreamTree(ctx, incoming, Manifest{Format: Format, Version: StreamVersion, SchemaVersion: schemaVersion})
	if streamErr != nil {
		err = streamErr
		return out, err
	}
	marker := restoreMarker{State: markerStaged, Source: originalSource, SchemaVersion: schemaVersion, StreamManifest: true, ManifestSHA256: stagedManifest.streamHash, JobID: work.jobID, OperationID: work.operation, Recovery: inspected.Recovery}
	if err = writeMarker(incoming, marker); err != nil {
		return out, err
	}
	if err = os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
		return out, err
	}
	if err = os.Rename(incoming, complete); err != nil {
		return out, err
	}
	// incoming is out of staged now; drop the emptied husk so the final
	// rename lands on a clean path on every platform.
	if err = os.RemoveAll(staged); err != nil {
		_ = os.RemoveAll(complete)
		return out, err
	}
	if err = os.Rename(complete, staged); err != nil {
		_ = os.RemoveAll(complete)
		return out, err
	}
	if err = syncDirectory(s.state); err != nil {
		return out, err
	}
	failed = false
	out = StageResult{Staged: true, RestartRequired: true, Validation: Validation{SchemaVersion: schemaVersion, Integrity: inspected.Integrity, Recovery: inspected.Recovery}}
	if s.onStaged != nil {
		s.onStaged()
	}
	return out, nil
}

// stateFileNames returns a manifest's state files: every entry except the
// database/WAL, which restore stages on its own. Version 1 ordinary backups
// omitted the database; current backups and pre-restore copies cover it too.
func stateFileNames(files []FileEntry) ([]string, bool) {
	names := make([]string, 0, len(files))
	seen := make(map[string]bool, len(files))
	for _, entry := range files {
		name := entry.Name
		if !safeRelativeFile(name) || !restorableFile(name) {
			return nil, false
		}
		key := name
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return nil, false
		}
		seen[key] = true
		if name == DatabaseName || name == DatabaseName+"-wal" {
			continue
		}
		names = append(names, name)
	}
	return names, true
}

// Only files owned by the backup format may be installed in live state.
func safeRelativeFile(name string) bool {
	if name == "" || name == "." || name == ".." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:\x00") {
		return false
	}
	return runtime.GOOS != "windows" || safeWindowsBackupName(name)
}

// Windows normalizes some names and reserves device names in every directory.
// Refuse aliases before resolving a manifest entry inside the backup tree.
func safeWindowsBackupName(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.Trim(part, " ") != part || strings.TrimRight(part, ".") != part || strings.ContainsAny(part, "<>\"\\|?*:") {
			return false
		}
		for _, r := range part {
			if r < 32 {
				return false
			}
		}
		stem, _, _ := strings.Cut(strings.ToUpper(part), ".")
		switch stem {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "COM¹", "COM²", "COM³", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "LPT¹", "LPT²", "LPT³":
			return false
		}
	}
	return true
}

func restorableFile(name string) bool {
	return name == DatabaseName || name == DatabaseName+"-wal" || name == "portico.toml" || strings.HasPrefix(name, "Metadata/") || strings.HasPrefix(name, "Keys/")
}

// resolveSource maps a restore source to its database file, the directory its
// state files live in, and those files' relative names.
func (s *Service) resolveSource(source Source) (db, dir string, files []string, expected *Manifest, err error) {
	if source.BackupID != "" {
		if err = checkID(source.BackupID); err != nil {
			return "", "", nil, nil, ErrSourceInvalid
		}
		folder, err := s.backupPath(source.BackupID)
		if err != nil {
			return "", "", nil, nil, ErrSourceInvalid
		}
		if err = s.sourceFilesSafe(filepath.Join(folder, ManifestName)); err != nil {
			return "", "", nil, nil, err
		}
		manifest, err := readManifest(folder)
		if err != nil {
			return "", "", nil, nil, ErrSourceInvalid
		}
		names, ok := stateFileNames(manifest.Files)
		if !ok {
			return "", "", nil, nil, ErrSourceInvalid
		}
		if err = s.sourceManifestSafe(folder, manifest); err != nil {
			return "", "", nil, nil, err
		}
		if err = checkSourceHashes(folder, manifest); err != nil {
			return "", "", nil, nil, err
		}
		return filepath.Join(folder, DatabaseName), folder, names, &manifest, nil
	}
	path := source.Path
	if path == "" || !filepath.IsAbs(path) || path != filepath.Clean(path) {
		return "", "", nil, nil, ErrSourceInvalid
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", nil, nil, ErrSourceInvalid
	}
	if info.IsDir() {
		if err = s.sourceFilesSafe(filepath.Join(path, ManifestName)); err != nil {
			return "", "", nil, nil, err
		}
		manifest, err := readManifest(path)
		if err != nil {
			return "", "", nil, nil, ErrSourceInvalid
		}
		names, ok := stateFileNames(manifest.Files)
		if !ok {
			return "", "", nil, nil, ErrSourceInvalid
		}
		if err = s.sourceManifestSafe(path, manifest); err != nil {
			return "", "", nil, nil, err
		}
		if err = checkSourceHashes(path, manifest); err != nil {
			return "", "", nil, nil, err
		}
		return filepath.Join(path, DatabaseName), path, names, &manifest, nil
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, nil, ErrSourceInvalid
	}
	return path, "", nil, nil, nil
}
