package httpapi

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These transport fixtures seed ordinary base facts through the real point
// writers. They need no second, ungated production ingest implementation.
func seedHTTPMovie(ctx context.Context, db *sql.DB, library, path string, f assets.Facts) error {
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	return dbwork.WithWriteTxContext(ctx, db, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		lib, e := compactcatalog.LibraryTx(ctx, tx, library)
		if e != nil {
			return e
		}
		asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: f.Container, VideoCodec: f.VideoCodec, AudioCodec: f.AudioCodec, Duration: f.Duration})
		if e != nil {
			return e
		}
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		item, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: path, Title: title})
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{})
	})
}
