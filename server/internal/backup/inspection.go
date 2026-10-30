package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SourceInspection identifies protection from captured source bytes. It does
// not authenticate an archive or grant restore authority. Restore reopens and
// fully verifies its input before touching staged or live state.
type SourceInspection struct {
	Encrypted bool `json:"encrypted"`
}

// InspectSource is read-only: no SQLite connection, decryption scratch,
// operation receipt, restore job, marker or settings write is created. A file
// suffix is never evidence of protection.
func (s *Service) InspectSource(ctx context.Context, source Source) (SourceInspection, error) {
	var out SourceInspection
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if (source.BackupID == "") == (source.Path == "") {
		return out, ErrSourceInvalid
	}
	path := source.Path
	if source.BackupID != "" {
		var err error
		path, err = s.backupPath(source.BackupID)
		if err != nil {
			return out, ErrSourceInvalid
		}
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return out, ErrSourceInvalid
	}
	info, err := os.Lstat(path)
	if err != nil {
		return out, ErrSourceInvalid
	}
	if info.IsDir() {
		root, err := openCanonicalRoot(path)
		if err != nil {
			return out, ErrSourceInvalid
		}
		defer root.Close()
		if _, err = root.Lstat(encryptedSummary); err == nil {
			raw, err := s.inspectRootFile(ctx, root, encryptedSummary, maxEncryptedSummaryBytes, true)
			if err != nil {
				return out, err
			}
			var summary Manifest
			if json.Unmarshal(raw, &summary) != nil || summary.Format != Format || (summary.Version != Version && summary.Version != HashedVersion) || len(summary.Files) != 0 {
				return out, ErrSourceInvalid
			}
			prefix, err := s.inspectRootFile(ctx, root, encryptedPayload, 100, false)
			if err != nil || !inspectionEncryptedHeader(prefix) {
				return out, ErrSourceInvalid
			}
			out.Encrypted = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return out, ErrSourceInvalid
		} else {
			knownStream, err := s.inspectRootStream(ctx, root)
			if err != nil {
				return out, err
			}
			var identity struct {
				Format  string `json:"format"`
				Version int    `json:"version"`
			}

			if !knownStream {
				raw, err := s.inspectRootFile(ctx, root, ManifestName, maxManifestBytes, true)
				if err != nil {
					return out, err
				}
				if json.Unmarshal(raw, &identity) != nil || identity.Format != Format || (identity.Version != HashedVersion && identity.Version != LegacyVersion) {
					return out, ErrSourceInvalid
				}
			}
			prefix, err := s.inspectRootFile(ctx, root, DatabaseName, 100, false)
			if err != nil || !inspectionSQLiteHeader(prefix) {
				return out, ErrSourceInvalid
			}
		}
	} else if info.Mode().IsRegular() {
		files, err := s.sourceGuard.files()
		if err != nil {
			return out, err
		}
		if kind := liveSourceKind(info, files); kind != 0 {
			if kind == 2 || canonicalDirectory(filepath.Dir(path), false) != nil {
				return out, ErrSourceInvalid
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(info, after) {
				return out, ErrSourceInvalid
			}
			// The open live main is already known to be plaintext SQLite.
			// Reading its header through another descriptor would drop locks.
			return out, ctx.Err()
		}
		root, err := openCanonicalRoot(filepath.Dir(path))
		if err != nil {
			return out, ErrSourceInvalid
		}
		defer root.Close()
		prefix, err := s.inspectRootFile(ctx, root, filepath.Base(path), 100, false)
		if err != nil {
			return out, err
		}
		out.Encrypted = inspectionEncryptedHeader(prefix)
		if !out.Encrypted && !inspectionSQLiteHeader(prefix) {
			return out, ErrSourceInvalid
		}
	} else {
		return out, ErrSourceInvalid
	}
	// A renamed root or replaced path cannot classify a different selection.
	after, err := os.Lstat(path)
	if err != nil || !sameInspectionFile(info, after) || canonicalDirectory(filepath.Dir(path), false) != nil {
		return SourceInspection{}, ErrSourceInvalid
	}
	if err = ctx.Err(); err != nil {
		return SourceInspection{}, err
	}
	return out, nil
}

func inspectionEncryptedHeader(prefix []byte) bool {
	// The fixed salt and at least an authenticated terminal record must fit.
	return len(prefix) >= len(envelopeMagic)+kdfSaltBytes+archiveSaltBytes+4+16 && bytes.HasPrefix(prefix, envelopeMagic)
}

func inspectionSQLiteHeader(prefix []byte) bool {
	return len(prefix) >= 100 && bytes.HasPrefix(prefix, []byte("SQLite format 3\x00"))
}

func sameInspectionFile(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func (s *Service) openInspectionFile(ctx context.Context, root *os.Root, name string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || i != len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, nil, ErrSourceInvalid
		}
	}
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, ErrSourceInvalid
	}
	files, err := s.sourceGuard.files()
	if err != nil || liveSourceKind(before, files) != 0 {
		return nil, nil, ErrSourceInvalid
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, ErrSourceInvalid
	}
	opened, err := file.Stat()
	if err != nil || !sameInspectionFile(before, opened) {
		file.Close()
		return nil, nil, ErrSourceInvalid
	}
	return file, before, nil
}

func (s *Service) inspectRootFile(ctx context.Context, root *os.Root, name string, limit int64, whole bool) ([]byte, error) {
	file, before, err := s.openInspectionFile(ctx, root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if whole && before.Size() > limit {
		return nil, ErrSourceInvalid
	}

	bound := limit
	if whole {
		bound++
	}
	raw, err := io.ReadAll(io.LimitReader(file, bound))
	if err != nil || whole && int64(len(raw)) > limit {
		return nil, ErrSourceInvalid
	}
	finished, err := file.Stat()
	current, pathErr := root.Lstat(name)
	if err != nil || pathErr != nil || !sameInspectionFile(before, finished) || !sameInspectionFile(before, current) {
		return nil, ErrSourceInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}

// A corrupt/oversized opening region cannot hide later readable stream records.
func (s *Service) inspectRootStream(ctx context.Context, root *os.Root) (bool, error) {
	file, before, err := s.openInspectionFile(ctx, root, ManifestName)
	if err != nil {
		return false, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(&scrubReader{ctx: ctx, r: file}, manifestLineBytes)
	found := false
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		raw, size, oversized, readErr := nextManifestLine(reader)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, readErr
		}
		if !oversized {
			var header Manifest
			if line == 1 && json.Unmarshal(raw, &header) == nil && header.Format == Format && header.Version == StreamVersion {
				found = true
				break
			}
			var record streamRecord
			if json.Unmarshal(raw, &record) == nil && (record.File != nil || record.End != nil) {
				found = true
				break
			}
		}
		if size == 0 || errors.Is(readErr, io.EOF) {
			break
		}
	}
	finished, err := file.Stat()
	current, pathErr := root.Lstat(ManifestName)
	if err != nil || pathErr != nil || !sameInspectionFile(before, finished) || !sameInspectionFile(before, current) {
		return false, ErrSourceInvalid
	}
	return found, ctx.Err()
}
