package backup

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func (s *Service) streamSource(source Source) (string, bool, error) {
	dir := source.Path
	if source.BackupID != "" {
		var err error
		dir, err = s.backupPath(source.BackupID)
		if err != nil {
			return "", false, ErrSourceInvalid
		}
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", false, nil
	}
	if err = s.sourceFilesSafe(filepath.Join(dir, ManifestName)); err != nil {
		return "", false, err
	}
	if err = regularManifestFile(dir, ManifestName); err != nil {
		return "", false, err
	}
	return dir, isStreamManifest(dir), nil
}

func addManifestDamage(report *persistence.ManifestRecovery, d ManifestDamage) {
	report.Count++
	if len(report.Regions) < 20 {
		report.Regions = append(report.Regions, d)
	} else {
		report.RegionsTruncated = true
	}
}

func (s *Service) stageStream(ctx context.Context, dir string, original Source, work restoreIdentity) (StageResult, error) {
	var out StageResult
	captured, err := readManifest(dir)
	if err != nil {
		return out, err
	}
	staged := storagepaths.Path(s.state, RestoreStagedDir)
	incoming, err := s.privateScratch("backup-snapshot-restore-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(incoming)
	complete := storagepaths.Path(s.state, ".staged-complete")
	if err = os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
		return out, err
	}
	_ = os.RemoveAll(complete)
	s.restoreProgress(work, "copy", 0, 0)
	// Freeze the selected manifest before any payload copy or SQLite repair.
	e, err := copyOneFileContext(ctx, filepath.Join(dir, ManifestName), filepath.Join(incoming, ManifestName), ManifestName, nil)
	if err != nil {
		return out, err
	}
	if e.SHA256 != captured.streamHash {
		return out, ErrIntegrity
	}
	frozen, err := readManifest(incoming)
	if err != nil {
		return out, err
	}
	report := &persistence.ManifestRecovery{Regions: []ManifestDamage{}}
	guard, err := s.sourceGuard.files()
	if err != nil {
		return out, err
	}
	var copiedBytes int64
	database := false
	wal := false
	eof := streamPosition{Line: 1}
	err = scanStream(ctx, frozen, func(entry FileEntry, pos streamPosition) error {
		src := filepath.Join(dir, filepath.FromSlash(entry.Name))
		dst := filepath.Join(incoming, filepath.FromSlash(entry.Name))
		damage := func(reason string) {
			addManifestDamage(report, ManifestDamage{FirstLine: pos.Line, LastLine: pos.Line, StartByte: pos.Start, EndByte: pos.End, Reason: reason})
		}
		info, err := os.Lstat(src)
		if err == nil && liveSourceKind(info, guard) != 0 {
			return ErrSourceInvalid
		}
		if err != nil {
			if os.IsNotExist(err) {
				report.MissingFiles++
				damage("missing_file")
				return nil
			}
			return err
		}
		if _, err = os.Lstat(dst); err == nil {
			damage("duplicate_file")
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = regularManifestFile(dir, entry.Name); err != nil {
			return ErrSourceInvalid
		}
		sourceBytes, err := hashFile(ctx, dir, entry.Name)
		if err != nil {
			return err
		}
		copied, err := copyOneFileContext(ctx, src, dst, entry.Name, nil)
		if err != nil {
			return err
		}
		copiedBytes += copied.Bytes
		s.restoreProgress(work, "copy", copiedBytes, 0)
		if copied.SHA256 != sourceBytes.SHA256 || copied.Bytes != sourceBytes.Bytes {
			return ErrIntegrity
		}
		if copied.SHA256 != entry.SHA256 || copied.Bytes != entry.Bytes {
			if entry.Name == DatabaseName || entry.Name == DatabaseName+"-wal" {
				report.UnverifiedDatabase = true
				damage("database_hash_mismatch")
			} else {
				if err = os.Remove(dst); err != nil {
					return err
				}
				report.MissingFiles++
				damage("payload_hash_mismatch")
				return nil
			}
		}
		if entry.Name == DatabaseName {
			database = true
		}
		if entry.Name == DatabaseName+"-wal" {
			wal = true
		}
		return nil
	}, func(d ManifestDamage) { addManifestDamage(report, d) }, func(pos streamPosition) { eof = pos })
	if err != nil {
		return out, err
	}
	if !database {
		// #21 also tries a readable database whose damaged manifest record was lost.
		if err = s.sourceFilesSafe(filepath.Join(dir, DatabaseName), filepath.Join(dir, DatabaseName+"-wal")); err != nil {
			return out, err
		}
		if _, err = copyOneFileContext(ctx, filepath.Join(dir, DatabaseName), filepath.Join(incoming, DatabaseName), DatabaseName, nil); err != nil {
			return out, ErrSourceInvalid
		}
		report.UnverifiedDatabase = true
		addManifestDamage(report, ManifestDamage{FirstLine: eof.Line, LastLine: eof.Line, StartByte: eof.Start, EndByte: eof.End, Reason: "database_entry_missing"})
	}
	if !wal {
		sourceWal := filepath.Join(dir, DatabaseName+"-wal")
		if info, statErr := os.Lstat(sourceWal); statErr == nil && info.Size() > 0 {
			if err = s.sourceFilesSafe(sourceWal); err != nil {
				return out, err
			}
			if err = regularManifestFile(dir, DatabaseName+"-wal"); err != nil {
				return out, err
			}
			if _, err = copyOneFileContext(ctx, sourceWal, filepath.Join(incoming, DatabaseName+"-wal"), DatabaseName+"-wal", nil); err != nil {
				return out, err
			}
			report.UnverifiedDatabase = true
			addManifestDamage(report, ManifestDamage{FirstLine: eof.Line, LastLine: eof.Line, StartByte: eof.Start, EndByte: eof.End, Reason: "database_entry_missing"})
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return out, statErr
		}
	}
	s.restoreProgress(work, "integrity", copiedBytes, copiedBytes)
	inspected, err := persistence.InspectCandidate(ctx, incoming, DatabaseName)
	if err != nil {
		return out, err
	}
	if report.Count > 0 {
		if inspected.Recovery == nil {
			inspected.Recovery = &persistence.Recovery{}
		}
		inspected.Recovery.ManifestDamage = report
		inspected.Integrity = "recovered"
	}
	// Database recovery may rewrite it. Only accepted/copied payloads become the
	// staged registry; rejected Keys/config bytes never get a new trusted hash.
	frozen.SchemaVersion = inspected.SchemaVersion
	frozen.BaselineDigest = persistence.BaselineDigest()
	frozen.Files = nil
	frozen.BackupID = ""
	frozen.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	stagedManifest, streamErr := writeStreamTree(ctx, incoming, frozen)
	if streamErr != nil {
		err = streamErr
		return out, err
	}
	marker := restoreMarker{State: markerStaged, Source: original, SchemaVersion: inspected.SchemaVersion, JobID: work.jobID, OperationID: work.operation, Recovery: inspected.Recovery, StreamManifest: true, ManifestSHA256: stagedManifest.streamHash}
	if err = writeMarker(incoming, marker); err != nil {
		return out, err
	}
	if err = os.Rename(incoming, complete); err != nil {
		return out, err
	}
	if err = os.RemoveAll(staged); err != nil {
		return out, err
	}
	if err = os.Rename(complete, staged); err != nil {
		return out, err
	}
	if err = syncDirectory(filepath.Dir(staged)); err != nil {
		return out, err
	}
	out = StageResult{Staged: true, RestartRequired: true, Validation: Validation{SchemaVersion: inspected.SchemaVersion, Integrity: inspected.Integrity, Recovery: inspected.Recovery}}
	if s.onStaged != nil {
		s.onStaged()
	}
	return out, nil
}
