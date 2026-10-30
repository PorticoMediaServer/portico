package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
)

// stateFiles are the state files a backup carries besides the database, as
// paths relative to the state directory. Directories are carried recursively,
// except managed-storage, which carries only rclone's plain config files.
// Anything absent is skipped; rebuildable data (transcodes, caches, artwork,
// logs, prepared media) and the backups and restore-staged folders themselves
// are never carried.
func stateFiles() []string { return []string{"portico.toml", "Metadata", "Keys"} }

func copyStateFilesForBackup(ctx context.Context, state, partial string, includeArtwork bool) ([]FileEntry, error) {
	for _, rel := range stateFiles() {
		src := storagepaths.Path(state, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			entry, err := copyOneFile(src, filepath.Join(partial, filepath.FromSlash(rel)), rel)
			if err != nil {
				return nil, err
			}
			_ = entry
			continue
		}
		if rel == "managed-storage" {
			// Only the plain configs: caches, candidates and anything else
			// the mount service keeps here are rebuildable or transient.
			names, err := os.ReadDir(src)
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				if name.IsDir() || !strings.HasSuffix(name.Name(), ".conf") {
					continue
				}
				fileEntry, err := copyOneFile(filepath.Join(src, name.Name()), filepath.Join(partial, rel, name.Name()), rel+"/"+name.Name())
				if err != nil {
					return nil, err
				}
				_ = fileEntry
			}
			continue
		}
		err = walkStateTree(ctx, state, rel, func(name string, entry fs.DirEntry) error {
			path := filepath.Join(state, filepath.FromSlash(name))
			if !includeArtwork && excludedDownloadedMetadata(name) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrSourceInvalid
			}
			if entry.IsDir() {
				return os.MkdirAll(filepath.Join(partial, filepath.FromSlash(name)), 0700)
			}
			if rebuildable(name) && (strings.Contains(name, artworkTombMarker) || strings.HasPrefix(name, "Metadata/guide-images/") && strings.Contains(name, ".tomb-")) {
				return nil // a collector's file in transit, carried by its own name if needed
			}
			info, err := entry.Info()
			if err != nil {
				if rebuildable(name) && errors.Is(err, fs.ErrNotExist) {
					return nil // collected while the walk ran; it can be fetched again
				}
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrSourceInvalid
			}
			fileEntry, err := copyOneFileContext(ctx, path, filepath.Join(partial, filepath.FromSlash(name)), name, nil)
			if err != nil {
				if rebuildable(name) && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			_ = fileEntry
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	_, err := copyOwnerArtwork(ctx, state, partial, nil)
	if err == nil && includeArtwork {
		err = copyGuideImages(ctx, state, partial)
	}
	return nil, err
}

// copyOneFile copies one regular file, refusing symlinks, and hashes it.
func copyOneFile(src, dst, name string) (FileEntry, error) {
	return copyOneFileContext(context.Background(), src, dst, name, nil)
}

func copyOneFileContext(ctx context.Context, src, dst, name string, progress func(int64, int64)) (FileEntry, error) {
	entry := FileEntry{Name: name}
	info, err := os.Lstat(src)
	if err != nil {
		return entry, err
	}
	if !info.Mode().IsRegular() {
		return entry, ErrSourceInvalid
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return entry, err
	}
	in, err := os.Open(src)
	if err != nil {
		return entry, err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return entry, ErrSourceInvalid
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return entry, err
	}
	hash := sha256.New()
	var written int64
	buffer := make([]byte, 128<<10)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		n, readErr := in.Read(buffer)
		if n > 0 {
			count, writeErr := out.Write(buffer[:n])
			hash.Write(buffer[:count])
			written += int64(count)
			if progress != nil {
				progress(written, info.Size())
			}
			if writeErr != nil {
				err = writeErr
				break
			}
			if count != n {
				err = io.ErrShortWrite
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			err = readErr
			break
		}
	}
	if syncErr := out.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return entry, err
	}
	after, statErr := in.Stat()
	current, pathErr := os.Lstat(src)
	if statErr != nil || pathErr != nil || !os.SameFile(info, current) || !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || written != info.Size() {
		return entry, ErrIntegrity
	}
	entry.Bytes = written
	entry.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return entry, nil
}

// writeManifest stores manifest.json in a finished staging folder.
const maxManifestBytes = 64 << 20

func writeManifest(partial string, manifest Manifest) error {
	if manifest.Version == StreamVersion {
		next := filepath.Join(partial, ManifestName+".next")
		_ = os.Remove(next)
		defer os.Remove(next)
		w, err := newStreamManifestWriter(next, manifest)
		if err != nil {
			return err
		}
		for _, e := range manifest.Files {
			if err = w.add(e); err != nil {
				w.file.Close()
				return err
			}
		}
		if err = w.finish(); err != nil {
			return err
		}
		if err = os.Rename(next, filepath.Join(partial, ManifestName)); err != nil {
			return err
		}
		return syncDirectory(partial)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > maxManifestBytes {
		return ErrInvalid
	}
	raw = append(raw, '\n')
	if err = durableFile(filepath.Join(partial, ManifestName), raw); err != nil {
		return err
	}
	return nil
}

// readManifest parses a backup folder's manifest, checking its identity.
func readManifest(dir string) (Manifest, error) {
	var manifest Manifest
	if err := regularManifestFile(dir, ManifestName); err != nil {
		return manifest, err
	}
	if isStreamManifest(dir) {
		return readStreamHeader(dir)
	}
	info, err := os.Stat(filepath.Join(dir, ManifestName))
	if err != nil || info.Size() > maxManifestBytes {
		return manifest, ErrSourceInvalid
	}
	file, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return manifest, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	file.Close()
	if err != nil {
		return manifest, err
	}
	if len(raw) > maxManifestBytes {
		return manifest, ErrSourceInvalid
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != Format || (manifest.Version != HashedVersion && manifest.Version != LegacyVersion) {
		return manifest, ErrInvalid
	}
	if _, ok := stateFileNames(manifest.Files); !ok {
		return manifest, ErrSourceInvalid
	}
	database := false
	for _, entry := range manifest.Files {
		digest, err := hex.DecodeString(entry.SHA256)
		if entry.Bytes < 0 || err != nil || len(digest) != sha256.Size {
			return manifest, ErrSourceInvalid
		}
		database = database || entry.Name == DatabaseName
	}
	// Version 1 ordinary backups omitted the database. Keep that deliberate
	// recovery path, with SQLite/schema validation still mandatory in Stage.
	if manifest.Version != LegacyVersion && !database {
		return manifest, ErrSourceInvalid
	}
	return manifest, nil
}

// estimate sizes the coming backup: the database, its WAL, and the state
// files. The free-space check multiplies this by 1.1.
func (s *Service) estimate(ctx context.Context, includeArtwork bool) (int64, error) {
	var total int64
	for _, name := range []string{"Database/server.sqlite", "Database/server.sqlite-wal"} {
		if info, err := os.Stat(storagepaths.Path(s.state, name)); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	for _, rel := range stateFiles() {
		src := storagepaths.Path(s.state, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() && !info.IsDir() {
			continue
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			continue
		}
		err = walkStateTree(ctx, s.state, rel, func(name string, entry fs.DirEntry) error {
			if !includeArtwork && excludedDownloadedMetadata(name) {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrSourceInvalid
			}
			if info.Size() > int64(^uint64(0)>>1)-total {
				return ErrInsufficientDisk
			}
			total += info.Size()
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	if !includeArtwork {
		var ownerBytes int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(bytes),0) FROM artwork_blobs WHERE digest IN (`+ownerArtworkReferences+`)`).Scan(&ownerBytes); err != nil {
			return 0, err
		}
		total += ownerBytes
	}
	if total < 1<<20 {
		total = 1 << 20
	}
	return total, nil
}

// checkManifestHashes verifies every listed file's sha256 against the tree.
func checkManifestHashes(dir string, files []FileEntry) error {
	for _, entry := range files {
		if !safeRelativeFile(entry.Name) {
			return ErrSourceInvalid
		}
		if err := regularManifestFile(dir, entry.Name); err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.FromSlash(entry.Name))
		// Streamed: a pre-restore backup lists the database itself, which can be
		// larger than memory.
		file, err := os.Open(path)
		if err != nil {
			return ErrSourceInvalid
		}
		hash := sha256.New()
		read, err := io.Copy(hash, file)
		file.Close()
		if err != nil {
			return ErrSourceInvalid
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 || read != entry.Bytes {
			return ErrIntegrity
		}
	}
	return nil
}

func durableFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Refuse links within a backup and special files before opening them. The owner
// may choose a mounted or linked destination; its contents must be plain files.
func regularManifestFile(root, name string) error {
	parts := strings.Split(name, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return ErrSourceInvalid
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return ErrSourceInvalid
			}
		} else if !info.IsDir() {
			return ErrSourceInvalid
		}
	}
	return nil
}

// checkSourceHashes also refuses unverified WAL frames: SQLite would replay
// them over an otherwise verified database during candidate inspection. Empty
// unlisted WALs are harmless and can be created by a concurrent read-only open.
func checkSourceHashes(dir string, manifest Manifest) error {
	wal := false
	err := visitManifest(context.Background(), dir, manifest, func(entry FileEntry) error {
		wal = wal || entry.Name == DatabaseName+"-wal"
		return checkManifestHashes(dir, []FileEntry{entry})
	})
	if err != nil {
		return err
	}
	if wal {
		return nil
	}
	info, err := os.Lstat(filepath.Join(dir, DatabaseName+"-wal"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrSourceInvalid
	}
	if info.Size() != 0 {
		return ErrIntegrity
	}
	return nil
}

// hashFile hashes a finalized regular payload without loading it into memory.
func hashFile(ctx context.Context, folder, name string) (FileEntry, error) {
	entry := FileEntry{Name: name}
	if err := regularManifestFile(folder, name); err != nil {
		return entry, err
	}
	path := filepath.Join(folder, filepath.FromSlash(name))
	before, err := os.Lstat(path)
	if err != nil {
		return entry, err
	}
	file, err := os.Open(path)
	if err != nil {
		return entry, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		file.Close()
		return entry, ErrIntegrity
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, &scrubReader{ctx: ctx, r: file})
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	closeErr := file.Close()
	if statErr != nil || pathErr != nil || !os.SameFile(before, current) || !os.SameFile(before, after) || size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return entry, ErrIntegrity
	}
	if copyErr != nil {
		return entry, copyErr
	}
	if closeErr != nil {
		return entry, closeErr
	}
	entry.Bytes, entry.SHA256 = size, hex.EncodeToString(hash.Sum(nil))
	return entry, nil
}

// Owner references come from the exact finalized SQLite snapshot, including undo
// history. Remote images are excluded unless explicitly selected/edited by an owner.
func copyOwnerArtwork(ctx context.Context, state, partial string, copied map[string]string) ([]FileEntry, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(partial, DatabaseName), RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	rows, e := db.QueryContext(ctx, ownerArtworkReferences)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		if e = rows.Scan(&digest); e != nil {
			return nil, e
		}
		raw, e := hex.DecodeString(digest)
		if e != nil || len(raw) != sha256.Size {
			return nil, ErrIntegrity
		}
		name := "Metadata/artwork/" + digest + ".img"
		if _, statErr := os.Lstat(filepath.Join(partial, filepath.FromSlash(name))); statErr == nil {
			existing, e := hashFile(ctx, partial, name)
			if e != nil {
				return nil, e
			}
			hash := existing.SHA256
			// Already carried by the whole-folder walk: its bytes must still
			// be the ones the database names.
			if hash != digest {
				return nil, ErrIntegrity
			}
			continue
		}
		entry, e := copyArtworkBlob(ctx, state, partial, name)
		if e != nil {
			return nil, e
		}
		if entry.SHA256 != digest {
			return nil, ErrIntegrity
		}
	}
	return nil, rows.Err()
}

// artworkTombMarker is the artwork collector's name for a file it has moved
// aside while it checks whether the file is still named
// (metadata/artwork_lifecycle.go): <digest>.img.tomb-<token>.
const artworkTombMarker = ".img.tomb-"

// copyArtworkBlob copies one artwork file. A collector that marked the file
// before this backup's hold may have moved it aside for a moment; it puts it
// back when it finds the file named, but the backup does not wait for that:
// the moved-aside file has the same bytes (the name is their digest, and the
// caller checks it).
func copyArtworkBlob(ctx context.Context, state, partial, name string) (FileEntry, error) {
	return copyBlobWithTombs(ctx, state, partial, name)
}

// rebuildable is a file under a folder of downloaded metadata, which can be
// fetched again.
func rebuildable(name string) bool {
	for _, dir := range []string{"Metadata/artwork/", "Metadata/people/", "Metadata/guide-images/", "Metadata/analysis-artifacts/"} {
		if strings.HasPrefix(name, dir) {
			return true
		}
	}
	return false
}

// Observed external drift is an additional refusal signal, not an ABA proof.
// Path-bound digests combine without retaining or sorting directory entries.
func authorityFingerprint(ctx context.Context, state string) (string, error) {
	var combined [sha256.Size]byte
	for _, root := range []string{"portico.toml", "Keys"} {
		err := walkStateTree(ctx, state, root, func(name string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return ErrSourceInvalid
			}
			entry, err := hashFile(ctx, state, name)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(entry)
			digest := sha256.Sum256(raw)
			for i := range combined {
				combined[i] ^= digest[i]
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(combined[:]), nil
}

// sameCarriedAuthority compares both path sets incrementally. Stable hashes bind
// each file to its open inode and closing size/mtime; the process fence separately
// detects every cooperating application mutation, including completed ABA.
// Non-cooperating writers cannot be given a cross-file global-instant guarantee.
func sameCarriedAuthority(ctx context.Context, live, carried string) error {
	for _, root := range []string{"portico.toml", "Keys"} {
		if err := walkStateTree(ctx, live, root, func(name string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return ErrSourceInvalid
			}
			current, err := hashFile(ctx, live, name)
			if err != nil {
				return err
			}
			copied, err := hashFile(ctx, carried, name)
			if err != nil {
				return err
			}
			if current.Bytes != copied.Bytes || current.SHA256 != copied.SHA256 {
				return ErrIntegrity
			}
			return nil
		}); err != nil {
			return err
		}
		if err := walkStateTree(ctx, carried, root, func(name string, d fs.DirEntry) error {
			if d.IsDir() {
				return nil
			}
			return regularManifestFile(live, name)
		}); err != nil {
			return err
		}
	}
	return nil
}

func excludedDownloadedMetadata(name string) bool {
	return name == "Metadata/artwork" || name == "Metadata/people" || name == "Metadata/guide-images" || name == "Metadata/analysis-artifacts"
}

// ownerArtworkReferences is the backup's artwork closure: every blob of every
// source an owner made irreplaceable (uploads, locked or owner-chosen
// selections, and undo history pins). Each source carries both slots and its
// ambient effect; current embedded-audio fallback roots are also retained,
// even while a sidecar hides them. A shared blob is listed once. Provider
// art is re-fetched after a restore and is not carried.
const ownerArtworkReferences = `WITH roots(digest) AS (
 SELECT digest FROM artwork_uploads
 UNION SELECT digest FROM artwork_selections WHERE locked=1 OR actor<>''
 UNION ` + artworkref.AudioFallbackKeys + `
 UNION SELECT j.value FROM metadata_owner_history h,json_tree(h.before_json) j WHERE j.type='text' AND length(j.value)=64
 UNION SELECT j.value FROM metadata_owner_history h,json_tree(h.after_json) j WHERE j.type='text' AND length(j.value)=64
 ), sources AS MATERIALIZED (SELECT s.small_blob,s.large_blob,s.ambient_blob FROM roots r JOIN artwork_sources s ON s.digest=r.digest)
 SELECT small_blob FROM sources UNION SELECT large_blob FROM sources UNION SELECT ambient_blob FROM sources`
