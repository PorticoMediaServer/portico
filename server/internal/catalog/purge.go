package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// PurgeItemTx deletes an item by its public id; see purgeEntityTx. An unknown
// id has nothing to purge.
func PurgeItemTx(ctx context.Context, tx *sql.Tx, item string) error {
	id, err := entityid.Resolve(ctx, tx, item)
	if errors.Is(err, entityid.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return purgeEntityTx(ctx, tx, id)
}

// purgeEntityTx releases item-owned rows first, then the catalogue rows of
// files no surviving title uses (forgetAssetTx). It never touches a file on
// disk, and it does not keep anyone's state: callers that remove a title for
// good go through RemoveTitleTx, which keeps the state first.
func purgeEntityTx(ctx context.Context, tx *sql.Tx, item int64) error {
	if err := compactcatalog.RequireNoEntityMBPinsTx(ctx, tx, item); err != nil {
		return err
	}
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, item); err != nil {
		return err
	}
	type file struct {
		id    int64
		token string
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, item)
	if err != nil {
		return err
	}
	files := []file{}
	for rows.Next() {
		var f file
		if err = rows.Scan(&f.id, &f.token); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM playback_sessions WHERE item_id=?`,
		`DELETE FROM lyric_selections WHERE resource_id IN(SELECT id FROM lyric_resources WHERE item_id=?)`,
	} {
		if _, err = tx.ExecContext(ctx, query, item); err != nil {
			return err
		}
	}
	if err = compactcatalog.DeleteEntityTx(ctx, tx, item); err != nil {
		return err
	}
	for _, f := range files {
		// A file another title still uses stays that title's file.
		var linked bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?)`, f.id).Scan(&linked); err != nil {
			return err
		}
		if linked {
			continue
		}
		if err = forgetAssetTx(ctx, tx, f.id, f.token); err != nil {
			return err
		}
	}
	return nil
}
