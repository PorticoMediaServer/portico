package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SeparateFileInfoVersion is the exact-occurrence version of
// SeparateInventoryVersion. It acts on the link named by the accepted FileInfo
// proof instead of guessing the first title that shares the asset. Everything
// else is the existing owner action: the file keeps its facts, the title it
// leaves keeps its other files and everyone's state, download claims move with
// it, and the decision is remembered in inventory_version_links so a later scan
// does not group the file again.
//
// A file that is only known through a manual catalogue link has no inventory
// object. There is then no source to be busy and no inventory event to write;
// the exact catalogue link and its kept-apart marker are still handled. When a
// source does hold the asset, every such source is checked with the existing
// playback guard before the transaction mutates anything.
func (s *Service) SeparateFileInfoVersion(ctx context.Context, target FileInfoActionTarget, authorize func(context.Context, *sql.Tx) error) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrAdminQuery
	}
	if authorize == nil || target.Item <= 0 || target.Asset <= 0 || target.ItemID == "" || target.AssetID == "" || target.LibraryID == "" {
		return "", ErrAdminQuery
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return "", err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = authorize(ctx, tx); err != nil {
		return "", err
	}

	// Re-check the exact selected link in this transaction. The proof's public
	// item and token must still name the same numeric row; a relinked or reused
	// asset is stale rather than a different title's file.
	var current int64
	var kindNumber compactcatalog.Kind
	var title, libraryKind, kind string
	var year, others int
	err = tx.QueryRowContext(ctx, `SELECT e.id,e.kind,e.title,e.year,l.kind,k.name,
 (EXISTS(SELECT 1 FROM catalog_asset_links x WHERE x.entity_id=e.id AND x.asset_id<al.asset_id) OR EXISTS(SELECT 1 FROM catalog_asset_links x WHERE x.entity_id=e.id AND x.asset_id>al.asset_id))
 FROM catalog_entities e
 JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN libraries l ON l.id=cl.library_id
 JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1
 JOIN catalog_asset_links al ON al.entity_id=e.id AND al.asset_id=?
 JOIN catalog_assets a ON a.id=al.asset_id
 WHERE e.id=? AND e.retired=0 AND cl.retired=0 AND cl.library_id=? AND a.token=? AND e.public_id=pid_blob(?)`,
		target.Asset, target.Item, target.LibraryID, target.AssetID, target.ItemID).Scan(&current, &kindNumber, &title, &year, &libraryKind, &kind, &others)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStaleContinuation
	}
	if err != nil {
		return "", err
	}
	if libraryKind != "movie" && libraryKind != "music" && libraryKind != "audiobook" {
		// An episode is placed by assigning it to a show, season and number.
		return "", ErrAdminQuery
	}
	if target.Kind != "" && target.Kind != kind {
		return "", ErrStaleContinuation
	}
	if others == 0 {
		// The only file of its title is already on its own.
		return "", ErrAdminQuery
	}

	// Indexed custody check of the active inventory objects for this exact asset in this
	// library. This is not an inventory walk: the asset index bounds it to the
	// sources that hold this one token. The first raw object is the
	// one whose event is recorded; it never decides which link is separated.
	object, hasObject, err := fileInfoSeparateObjects(ctx, tx, target)
	if err != nil {
		return "", err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, target.AssetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStaleContinuation
	}
	if err != nil {
		return "", err
	}
	if assetID != target.Asset {
		return "", ErrStaleContinuation
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, target.LibraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrStaleContinuation
	}
	if err != nil {
		return "", err
	}
	if current != target.Item {
		return "", ErrStaleContinuation
	}
	root, err := compactcatalog.LibraryRootTx(ctx, tx, handle)
	if err != nil {
		return "", err
	}
	var path string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM catalog_assets WHERE id=?`, assetID).Scan(&path); err != nil {
		return "", err
	}

	link := compactcatalog.Link{}
	var end sql.NullFloat64
	if err = tx.QueryRowContext(ctx, `SELECT part_index,start_seconds,end_seconds,edition FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, current, assetID).Scan(&link.Part, &link.Start, &end, &link.Edition); err != nil {
		return "", err
	}
	if end.Valid {
		link.End = &end.Float64
	}
	if err = compactcatalog.UnlinkAssetTx(ctx, tx, current, assetID); err != nil {
		return "", err
	}
	// The new title is known by its place alone, so no name it shares with the
	// title it left can pull it back.
	key := compactcatalog.ItemKey(root, path, 0) + "#separate"
	separate, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: kindNumber, Key: key, Title: title, Year: year, Added: catalogedNow()})
	if err != nil {
		return "", err
	}
	if kind == "song" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_songs(entity_id,album_id,disc_number,track_number) SELECT ?,album_id,disc_number,track_number FROM catalog_songs WHERE entity_id=?`, separate, current); err != nil {
			return "", err
		}
	}
	if kind == "audiobook_file" {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_book_files(entity_id,book_id,disc_number,part_number) SELECT ?,book_id,disc_number,part_number FROM catalog_book_files WHERE entity_id=?`, separate, current); err != nil {
			return "", err
		}
	}
	if err = moveDownloadClaimsTx(ctx, tx, current, separate, target.AssetID); err != nil {
		return "", err
	}
	if err = compactcatalog.LinkAssetTx(ctx, tx, separate, assetID, link); err != nil {
		return "", err
	}
	if err = compactcatalog.TouchEntityTx(ctx, tx, separate); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_version_links(library_id,asset_id,item_id) VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET item_id=excluded.item_id`, target.LibraryID, target.AssetID, separate); err != nil {
		return "", err
	}
	if hasObject {
		if err = inventoryEvent(ctx, tx, object.source, "owner:"+identity.Token(), object.object, target.AssetID, object.revision, "version_separated"); err != nil {
			return "", err
		}
	}
	var public string
	if err = tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, separate).Scan(&public); err != nil {
		return "", err
	}
	return public, gated.Commit()
}

type fileInfoSeparateObject struct {
	object, source, revision string
}

// Stream the selected asset's existing physical-custody checks. Never retain
// an all-observations slice/map or sort the catalogue to identify the action.
// These remain the established source playback refusals, independent of the
// exact link selected for the mutation.
func fileInfoSeparateObjects(ctx context.Context, tx *sql.Tx, target FileInfoActionTarget) (fileInfoSeparateObject, bool, error) {
	rows, e := tx.QueryContext(ctx, `SELECT o.id,o.source_id,o.revision FROM inventory_objects o INDEXED BY inventory_objects_asset JOIN library_sources s ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0`, target.AssetID, target.LibraryID)
	if e != nil {
		return fileInfoSeparateObject{}, false, e
	}
	defer rows.Close()
	var first fileInfoSeparateObject
	found := false
	for rows.Next() {
		var object fileInfoSeparateObject
		if e = rows.Scan(&object.object, &object.source, &object.revision); e != nil {
			return first, false, e
		}
		if e = sourcePlaybackBusy(ctx, tx, object.source); e != nil {
			return first, false, e
		}
		if !found {
			first = object
			found = true
		}
	}
	return first, found, rows.Err()
}
