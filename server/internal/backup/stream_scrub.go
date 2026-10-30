package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func scrubStreamPreRestore(ctx context.Context, folder string, m Manifest, scrub func(context.Context, *dbwork.Candidate) error) error {
	marker := filepath.Join(folder, signInScrubMarker)
	if m.SignInsRemoved {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	prior, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		if err = checkSourceHashes(folder, m); err != nil {
			return err
		}
		if err = publishScrubFile(folder, signInScrubMarker, []byte(m.streamHash)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if string(prior) != m.streamHash {
		return ErrIntegrity
	}
	// An interrupted scrub may rewrite only DB/WAL. Every other expected hash
	// remains the captured original; no rehash can bless changed keys or config.
	if err = visitManifest(ctx, folder, m, func(e FileEntry) error {
		if e.Name == DatabaseName || e.Name == DatabaseName+"-wal" {
			return nil
		}
		return checkManifestHashes(folder, []FileEntry{e})
	}); err != nil {
		return err
	}
	candidate, err := dbwork.OpenCandidate(folder, DatabaseName)
	if err != nil {
		return err
	}
	if err = scrub(ctx, candidate); err != nil {
		candidate.Close()
		return err
	}
	if err = candidate.Close(); err != nil {
		return err
	}
	next := filepath.Join(folder, ManifestName+".next")
	if err = os.Remove(next); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.Remove(next)
	completed := streamHeader(m)
	completed.SignInsRemoved = true
	writer, err := newStreamManifestWriter(next, completed)
	if err != nil {
		return err
	}
	database, wal := false, false
	err = visitManifest(ctx, folder, m, func(e FileEntry) error {
		if e.Name == DatabaseName || e.Name == DatabaseName+"-wal" {
			if e.Name == DatabaseName {
				database = true
			} else {
				wal = true
			}
			if _, err := os.Lstat(filepath.Join(folder, filepath.FromSlash(e.Name))); os.IsNotExist(err) && e.Name != DatabaseName {
				return nil
			} else if err != nil {
				return err
			}
			var err error
			e, err = hashFile(ctx, folder, e.Name)
			if err != nil {
				return err
			}
		} else if err := checkManifestHashes(folder, []FileEntry{e}); err != nil {
			return err
		}
		return writer.add(e)
	})
	if err == nil && !database {
		var e FileEntry
		e, err = hashFile(ctx, folder, DatabaseName)
		if err == nil {
			err = writer.add(e)
		}
	}
	if err == nil && !wal {
		if _, statErr := os.Lstat(filepath.Join(folder, DatabaseName+"-wal")); statErr == nil {
			var e FileEntry
			e, err = hashFile(ctx, folder, DatabaseName+"-wal")
			if err == nil {
				err = writer.add(e)
			}
		} else if !os.IsNotExist(statErr) {
			err = statErr
		}
	}
	if err != nil {
		writer.file.Close()
		return err
	}
	if err = writer.finish(); err != nil {
		return err
	}
	if err = os.Rename(next, filepath.Join(folder, ManifestName)); err != nil {
		return err
	}
	if err = syncDirectory(folder); err != nil {
		return err
	}
	return errors.Join(os.Remove(marker), syncDirectory(folder))
}
