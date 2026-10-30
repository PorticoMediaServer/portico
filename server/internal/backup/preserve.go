package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

// currentOnlyStateNames runs after the replaced files have moved into pre.
// The remaining required files stay live and are copied, using the same file
// policy and owner-artwork references as an ordinary backup. Downloaded,
// rebuildable artwork is omitted when the previous database can identify the
// owner-held files. A damaged previous database instead keeps all metadata:
// recovery must not depend on the broken database being readable first.
func currentOnlyStateNames(ctx context.Context, state, pre string) ([]string, error) {
	artwork, err := currentOwnerArtworkNames(ctx, pre)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Print("warn: Previous database artwork references are unavailable; preserving all current metadata in the pre-restore copy")
		return remainingStateNames(ctx, state, true)
	}
	names, err := remainingStateNames(ctx, state, false)
	if err != nil {
		return nil, err
	}
	for _, name := range artwork {
		if _, err = os.Lstat(filepath.Join(pre, filepath.FromSlash(name))); err == nil {
			continue // the incoming restore replaces it, so it already moved
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		info, err := os.Lstat(filepath.Join(state, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			// The old state may be what the owner is trying to repair. A
			// reference cannot preserve bytes that were already absent.
			log.Printf("warn: Previous artwork %s is already missing; the pre-restore copy cannot preserve it", name)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, ErrSourceInvalid
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func remainingStateNames(ctx context.Context, state string, includeArtwork bool) ([]string, error) {
	names := []string{}
	for _, rel := range stateFiles() {
		root := filepath.Join(state, filepath.FromSlash(rel))
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			name, err := filepath.Rel(state, path)
			if err != nil {
				return err
			}
			name = filepath.ToSlash(name)
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrSourceInvalid
			}
			if !includeArtwork && excludedDownloadedMetadata(name) {
				if entry.IsDir() {
					return filepath.SkipDir
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
			if !info.Mode().IsRegular() || !restorableFile(name) {
				return ErrSourceInvalid
			}
			names = append(names, name)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	return names, nil
}

func currentOwnerArtworkNames(ctx context.Context, pre string) ([]string, error) {
	// Read the previous database, including any WAL that moved with it. The
	// incoming database must never decide which recovery files are needed.
	u := url.URL{Scheme: "file", Path: filepath.Join(pre, DatabaseName), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, ownerArtworkReferences)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var digest string
		if err = rows.Scan(&digest); err != nil {
			return nil, err
		}
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != sha256.Size {
			return nil, ErrIntegrity
		}
		names = append(names, "Metadata/artwork/"+digest+".img")
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}
