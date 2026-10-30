package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// readingFormat is deliberately separate from audio Book/Part and timed video.
// An extension admits inventory only; the rooted reading runtime validates the
// actual package/PDF/image bytes before exposing a readable representation.
func readingFormat(path, kind string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if kind == "book" && (ext == ".epub" || ext == ".pdf" || ext == ".mobi" || ext == ".azw3") {
		return ext[1:]
	}
	if kind == "comic" && (ext == ".cbz" || ext == ".pdf" || ext == ".cb7" && readingcap.CB7() || ext == ".cbr" && readingcap.CBR()) {
		return ext[1:]
	}
	return ""
}

func attachReadingTx(ctx context.Context, tx *sql.Tx, library, libraryKind, asset, path string) (int64, error) {
	format := readingFormat(path, libraryKind)
	if format == "" {
		return 0, ErrAdminQuery
	}
	kindName := "ebook"
	if libraryKind == "comic" {
		kindName = "comic"
	}
	// The steward owns kind registration; absent canonical registration fails
	// closed rather than accidentally attaching the publication as a movie.
	kind, err := compactcatalog.ParseKind(kindName)
	if err != nil {
		return 0, err
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return 0, err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil {
		return 0, err
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return 0, err
	}
	name := compactcatalog.ReadingFilenameTitle(filepath.Base(path))
	var item int64
	err = tx.QueryRowContext(ctx, `SELECT p.item_id FROM reading_publications p JOIN catalog_entities e ON e.id=p.item_id WHERE p.asset_id=? AND e.library_id=? AND e.kind=?`, assetID, handle, int(kind)).Scan(&item)
	newPublication := err == sql.ErrNoRows
	if err == sql.ErrNoRows {
		// Same stem/directory in different admitted formats is one title with
		// real alternative publications. Preserve an existing title's identity,
		// owner edits and resume when a later format arrives.
		stem := strings.TrimSuffix(path, filepath.Ext(path))
		err = tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_assets a JOIN reading_publications p ON p.asset_id=a.id JOIN catalog_entities e ON e.id=p.item_id
 WHERE e.library_id=? AND e.kind=? AND e.retired=0 AND a.path>=? AND a.path<?
 AND substr(a.path,1,length(a.path)-length(p.format)-1)=? ORDER BY e.id LIMIT 1`, handle, int(kind), stem+".", stem+"/", stem).Scan(&item)
		if err == sql.ErrNoRows {
			item, _, err = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: kind, Key: "reading:" + kindName + ":" + compactcatalog.ItemKey(root, stem, 0), Title: name, Added: catalogedNow()})
		}
	}
	if err != nil {
		return 0, err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, item, assetID, compactcatalog.Link{}); err != nil {
		return 0, err
	}
	if newPublication {
		_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, item, assetID, format)
	} else {
		// An unchanged attachment must not run the BEFORE INSERT trigger.
		_, err = tx.ExecContext(ctx, `UPDATE reading_publications SET format=? WHERE item_id=? AND asset_id=? AND format IS NOT ?`, format, item, assetID, format)
	}
	if err != nil {
		return 0, err
	}
	if newPublication {
		err = restoreRetainedTx(ctx, tx, item)
	}
	return item, err
}
