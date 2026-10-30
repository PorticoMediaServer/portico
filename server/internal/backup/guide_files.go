package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// copyBlobWithTombs handles a collector already between mark/rename and final
// backup-hold check. Directory reads stay bounded even after repeated crashes.
func copyBlobWithTombs(ctx context.Context, state, partial, name string) (FileEntry, error) {
	source := filepath.Join(state, filepath.FromSlash(name))
	target := filepath.Join(partial, filepath.FromSlash(name))
	entry, err := copyOneFileContext(ctx, source, target, name, nil)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return entry, err
	}
	directory, openErr := os.Open(filepath.Dir(source))
	if openErr != nil {
		return entry, err
	}
	defer directory.Close()
	prefix := filepath.Base(source) + ".tomb-"
	for {
		batch, readErr := directory.ReadDir(128)
		for _, e := range batch {
			if !strings.HasPrefix(e.Name(), prefix) || !e.Type().IsRegular() {
				continue
			}
			if moved, copyErr := copyOneFileContext(ctx, filepath.Join(filepath.Dir(source), e.Name()), target, name, nil); copyErr == nil {
				return moved, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return entry, readErr
		}
	}
	return entry, err
}

// A plain metadata walk can miss a guide image that was moved aside before the
// hold. Reconcile the finalized candidate's references in 128-row url windows,
// exactly as owner artwork reconciles its own roots after the optional walk.
func copyGuideImages(ctx context.Context, state, partial string) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(partial, DatabaseName), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	after := ""
	type reference struct{ url, small, large, mime string }
	for {
		rows, err := db.QueryContext(ctx, `SELECT url,small_digest,large_digest,media_type FROM live_programme_images WHERE url>? ORDER BY url LIMIT 128`, after)
		if err != nil {
			return err
		}
		batch := make([]reference, 0, 128)
		for rows.Next() {
			var r reference
			if err = rows.Scan(&r.url, &r.small, &r.large, &r.mime); err != nil {
				break
			}
			batch = append(batch, r)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		for _, r := range batch {
			ext := ""
			switch r.mime {
			case "image/png":
				ext = ".png"
			case "image/jpeg":
				ext = ".jpg"
			case "image/webp":
				ext = ".webp"
			default:
				return ErrIntegrity
			}
			for _, digest := range []string{r.small, r.large} {
				raw, err := hex.DecodeString(digest)
				if err != nil || len(raw) != sha256.Size {
					return ErrIntegrity
				}
				name := "Metadata/guide-images/" + digest + ext
				var entry FileEntry
				if _, err = os.Lstat(filepath.Join(partial, filepath.FromSlash(name))); err == nil {
					entry, err = hashFile(ctx, partial, name)
				} else if os.IsNotExist(err) {
					entry, err = copyBlobWithTombs(ctx, state, partial, name)
				}
				if err != nil {
					return err
				}
				if entry.SHA256 != digest {
					return ErrIntegrity
				}
			}
			after = r.url
		}
		if len(batch) < 128 {
			return nil
		}
	}
}
