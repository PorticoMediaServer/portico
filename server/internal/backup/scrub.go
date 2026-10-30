package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const signInScrubMarker = ".sign-in-scrub"

// ScrubPreRestoreCopy removes sign-ins without breaking the backup's manifest.
// A durable marker records the verified manifest before the database changes:
// after a crash the database scrub can resume, while every untouched state file
// still has to match its original hash. A completed copy is not rewritten on
// subsequent starts.
func ScrubPreRestoreCopy(ctx context.Context, folder string, scrub func(context.Context, *dbwork.Candidate) error) error {
	if scrub == nil {
		return errors.New("no sign-in scrubber")
	}
	manifest, err := readManifest(folder)
	if err != nil {
		return err
	}
	if manifest.Kind != KindPreRestore {
		return ErrSourceInvalid
	}
	// The scrubber opens SQLite for writing. Refuse link substitutions before
	// handing it a path, including the WAL that SQLite may replay.
	for _, name := range []string{DatabaseName, DatabaseName + "-wal"} {
		info, err := os.Lstat(filepath.Join(folder, name))
		if os.IsNotExist(err) && name != DatabaseName {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrSourceInvalid
		}
	}
	if manifest.Version == StreamVersion {
		return scrubStreamPreRestore(ctx, folder, manifest, scrub)
	}
	marker := filepath.Join(folder, signInScrubMarker)
	if manifest.SignInsRemoved {
		if err = os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if _, ok := stateFileNames(manifest.Files); !ok {
		return ErrSourceInvalid
	}
	original, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(original)
	want := hex.EncodeToString(sum[:])
	prior, err := os.ReadFile(marker)
	switch {
	case os.IsNotExist(err):
		if err = checkManifestHashes(folder, manifest.Files); err != nil {
			return err
		}
		if err = publishScrubFile(folder, signInScrubMarker, []byte(want)); err != nil {
			return err
		}
	case err != nil:
		return err
	case string(prior) != want:
		return ErrIntegrity
	}
	// The database/WAL may have changed in an interrupted scrub. Everything
	// else must still match; do not turn a changed state file into a valid one.
	untouched := make([]FileEntry, 0, len(manifest.Files))
	for _, entry := range manifest.Files {
		if entry.Name != DatabaseName && entry.Name != DatabaseName+"-wal" {
			untouched = append(untouched, entry)
		}
	}
	if err = checkManifestHashes(folder, untouched); err != nil {
		return err
	}
	// The copy is opened privately: scrubbing it is not a live write.
	candidate, err := dbwork.OpenCandidate(folder, DatabaseName)
	if err != nil {
		return err
	}
	if err = scrub(ctx, candidate); err != nil {
		candidate.Close()
		return err
	}
	// Closing the copy's only connection folds its log back into the file.
	if err = candidate.Close(); err != nil {
		return err
	}
	files := append([]FileEntry(nil), untouched...)
	for _, name := range []string{DatabaseName, DatabaseName + "-wal"} {
		path := filepath.Join(folder, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && name != DatabaseName {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrSourceInvalid
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		// The database may be larger than RAM: hash it in bounded pieces.
		size, copyErr := io.Copy(hash, &scrubReader{ctx: ctx, r: file})
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files = append(files, FileEntry{Name: name, Bytes: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	manifest.Files, manifest.SignInsRemoved = files, true
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	// Publish the new hashes atomically, keeping the old manifest and marker
	// available until the replacement is durable.
	if err = publishScrubFile(folder, ManifestName, append(raw, '\n')); err != nil {
		return err
	}
	if err = os.Remove(marker); err != nil {
		return err
	}
	return syncDirectory(folder)
}

type scrubReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *scrubReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Both the intent marker and final manifest are durable before any later step.
// A truncated metadata write never replaces the previous complete file.
func publishScrubFile(folder, name string, raw []byte) error {
	file, err := os.CreateTemp(folder, name+".scrub-*")
	if err != nil {
		return err
	}
	next := file.Name()
	defer os.Remove(next)
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(next, filepath.Join(folder, name)); err != nil {
		return err
	}
	return syncDirectory(folder)
}
