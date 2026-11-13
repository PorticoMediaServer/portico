package db

import (
	"context"
	"database/sql"
)

// PersonDownloadCopies is the prepared storage named by one person’s download requests.
func (d *DB) PersonDownloadCopies(ctx context.Context, publicID string) ([]Prepared, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media WHERE id IN
  (SELECT x.prepared_id FROM download_items x JOIN downloads d ON d.id=x.download_id JOIN profiles p ON p.id=d.profile_id JOIN users u ON u.id=p.user_id WHERE u.public_id=?)`, publicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Prepared
	for rows.Next() {
		p, err := scanPrepared(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ExpirePersonDownloadCopies preserves the requests (and device-held downloads). Only
// observed copies are detached; a concurrent Retry's newer choice stays alone.
// Copies still named by another request remain stored for that request.
func (d *DB) ExpirePersonDownloadCopies(ctx context.Context, publicID string, observed []int64) ([]int64, error) {
	if len(observed) == 0 {
		return nil, nil
	}
	var removed []int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE download_items SET prepared_id=NULL WHERE download_id IN
   (SELECT d.id FROM downloads d JOIN profiles p ON p.id=d.profile_id JOIN users u ON u.id=p.user_id WHERE u.public_id=?) AND prepared_id IN `+intList(observed), publicID)
		if err != nil {
			return err
		}
		removed, err = ids(ctx, tx, `SELECT id FROM prepared_media WHERE id IN `+intList(observed)+`
   AND NOT EXISTS(SELECT 1 FROM download_items x WHERE x.prepared_id=prepared_media.id)`)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM prepared_media WHERE id IN `+intList(removed))
		return err
	})
	return removed, err
}
