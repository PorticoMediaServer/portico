package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func VideoNameTx(ctx context.Context, tx *sql.Tx, asset, revision string) (naming.Embedded, error) {
	var e naming.Embedded
	var season, number sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT title,show_title,year,season,number,edition FROM inventory_video_names WHERE asset_id=? AND revision=?`, asset, revision).Scan(&e.Title, &e.ShowTitle, &e.Year, &season, &number, &e.Edition)
	if err == sql.ErrNoRows {
		return e, nil
	}
	if err != nil {
		return e, err
	}
	if season.Valid {
		n := int(season.Int64)
		e.Season = &n
	}
	if number.Valid {
		n := int(number.Int64)
		e.Number = &n
	}
	rows, err := tx.QueryContext(ctx, `SELECT provider,provider_id FROM inventory_video_ids WHERE asset_id=? AND revision=? ORDER BY provider`, asset, revision)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var id naming.ProviderID
		if err = rows.Scan(&id.Provider, &id.ID); err != nil {
			return e, err
		}
		e.IDs = append(e.IDs, id)
	}
	return e, rows.Err()
}
func persistVideoNameTx(ctx context.Context, tx *sql.Tx, asset, revision string, e naming.Embedded) error {
	raw, _ := json.Marshal(e.IDs)
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	result, err := tx.ExecContext(ctx, `INSERT INTO inventory_video_names(asset_id,revision,title,show_title,year,season,number,edition,ids_digest) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(asset_id,revision) DO UPDATE SET revision=excluded.revision,title=excluded.title,show_title=excluded.show_title,year=excluded.year,season=excluded.season,number=excluded.number,edition=excluded.edition,ids_digest=excluded.ids_digest WHERE inventory_video_names.revision IS NOT excluded.revision OR inventory_video_names.title IS NOT excluded.title OR inventory_video_names.show_title IS NOT excluded.show_title OR inventory_video_names.year IS NOT excluded.year OR inventory_video_names.season IS NOT excluded.season OR inventory_video_names.number IS NOT excluded.number OR inventory_video_names.edition IS NOT excluded.edition OR inventory_video_names.ids_digest IS NOT excluded.ids_digest`, asset, revision, e.Title, e.ShowTitle, e.Year, e.Season, e.Number, e.Edition, digest)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_video_ids WHERE asset_id=? AND revision=?`, asset, revision); err != nil {
		return err
	}
	for _, id := range e.IDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_video_ids(asset_id,revision,provider,provider_id) VALUES(?,?,?,?)`, asset, revision, id.Provider, id.ID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM inventory_video_names WHERE rowid IN(SELECT n.rowid FROM inventory_video_names n WHERE n.asset_id=? AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=n.asset_id AND o.revision=n.revision AND o.retired=0) LIMIT 256)`, asset)
	return err
}
