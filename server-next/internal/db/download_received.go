package db

import (
	"context"
	"database/sql"
	"time"
)

// Every copy the server makes for a download is temporary (area 9 rule 24, Justin 16 Nov): a
// device says when it has a title's whole file, and a copy goes once no download waits for it,
// or once it has been ready for 48 hours without being fetched lately.

// DownloadReceived is the state of a title whose device has its whole file. It is listed and
// counted as ready, is never prepared again, and can still be fetched while its copy lasts.
const DownloadReceived = 3

// ReceiveTitle records that the device has a download's title: a title still wanted becomes
// received. It returns the copy the title used, 0 for an original. Saying it twice changes
// nothing.
func (d *DB) ReceiveTitle(ctx context.Context, downloadID, itemID int64) (preparedID int64, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE download_items SET state = ? WHERE download_id = ? AND item_id = ? AND state = ?`,
			DownloadReceived, downloadID, itemID, DownloadWanted); err != nil {
			return err
		}
		var id sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT prepared_id FROM download_items WHERE download_id = ? AND item_id = ?`, downloadID, itemID).Scan(&id)
		preparedID = id.Int64
		return err
	})
	return preparedID, err
}

// MediaAwaited reports whether a download's title still waits for a file of this version.
func (d *DB) MediaAwaited(ctx context.Context, mediaID int64) (bool, error) {
	var waiting bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM download_items WHERE media_id = ? AND state = ?)`,
		mediaID, DownloadWanted).Scan(&waiting)
	return waiting, err
}

// FinishedPrepared pages, after the copy id after, the copies that can go: no download's
// title waits for them; they have been ready since before ready and not fetched since before
// used; or they could not be made and nobody has asked since before ready, so a download
// waiting for one reads as expired and can ask again. A copy being made is never among them.
func (d *DB) FinishedPrepared(ctx context.Context, ready, used time.Time, after int64, limit int) ([]Prepared, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media p WHERE p.id > ? AND p.state <> 'preparing'
		AND (NOT EXISTS (SELECT 1 FROM download_items x WHERE x.prepared_id = p.id AND x.state = ?)
			OR p.state = 'ready' AND ifnull(p.ready_at, p.created_at) < ? AND p.last_used_at < ?
			OR p.state = 'failed' AND p.last_used_at < ?)
		ORDER BY p.id LIMIT ?`, after, DownloadWanted, ready.UnixMilli(), used.UnixMilli(), ready.UnixMilli(), limit)
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

// RemoveUnawaitedPrepared removes a copy no download's title waits for any more, and reports
// whether it did.
func (d *DB) RemoveUnawaitedPrepared(ctx context.Context, id int64) (bool, error) {
	var removed bool
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM prepared_media WHERE id = ? AND state <> 'preparing'
			AND NOT EXISTS (SELECT 1 FROM download_items x WHERE x.prepared_id = prepared_media.id AND x.state = ?)`, id, DownloadWanted)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		removed = n > 0
		return err
	})
	return removed, err
}
