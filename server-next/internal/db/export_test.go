package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SetRareTag lets a test send tag-filtered lists down the path for common tags or the path
// for rare ones, whatever the size of its library.
func SetRareTag(uses int) { rareTag = uses }

// SetChangesKeepRows lets a test prune the changes table without writing 200,000 rows first.
func SetChangesKeepRows(rows int) { changesKeepRows = rows }

// WatchedMarks compares every profile's stored watched mark on every holder with what is
// under the holder, and returns the marks as stored and a line for each that is wrong.
func WatchedMarks(d *DB) (stored map[[2]int64]bool, wrong []string, err error) {
	stored = map[[2]int64]bool{}
	err = d.write(context.Background(), Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		profiles, err := queryInts(tx, `SELECT id FROM profiles`)
		if err != nil {
			return err
		}
		holders, err := queryInts(tx, `SELECT id FROM items WHERE kind NOT IN `+playableKindsSQL)
		if err != nil {
			return err
		}
		for _, profile := range profiles {
			acc, err := profileAccess(b, profile)
			if err != nil {
				return err
			}
			for _, holder := range holders {
				// Counted here in full, apart from the statement the server uses.
				var leaves, watched int
				err := b.queryRow(`SELECT count(*), count(played) FROM (`+underHolder(`SELECT c.id, w.item_id AS played FROM items c
					LEFT JOIN user_item_state w ON w.profile_id = @viewer_profile AND w.item_id = c.id AND w.played = 1
					WHERE {under} AND `+acc.visible("c"), "@id", " UNION ")+`)`,
					sql.Named("viewer_profile", profile), sql.Named("id", holder)).Scan(&leaves, &watched)
				if err != nil {
					return err
				}
				var mark bool
				err = b.queryRow(`SELECT EXISTS (SELECT 1 FROM user_item_state WHERE profile_id = ? AND item_id = ? AND played = 1)`, profile, holder).Scan(&mark)
				if err != nil {
					return err
				}
				if mark {
					stored[[2]int64{profile, holder}] = true
				}
				if want := leaves > 0 && watched == leaves; want != mark {
					wrong = append(wrong, fmt.Sprintf("profile %d, holder %d: stored %v, but %d of the %d it may see are watched", profile, holder, mark, watched, leaves))
				}
			}
		}
		return nil
	})
	return stored, wrong, err
}

// RemoveItem removes one item the way emptying the trash does.
func RemoveItem(d *DB, publicID string) error {
	return d.write(context.Background(), Background, func(tx *sql.Tx) (err error) {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		defer func() {
			if err == nil {
				err = b.settleWatchedForAll(now)
			}
		}()
		var id, library int64
		if err := b.queryRow(`SELECT id, library_id FROM items WHERE public_id = ?`, publicID).Scan(&id, &library); err != nil {
			return err
		}
		return removeItem(b, library, id, now)
	})
}

// DeviceForTest registers a device, for a test that reports playback.
func DeviceForTest(d *DB) (int64, error) {
	var id int64
	now := time.Now().UnixMilli()
	err := d.write(context.Background(), Background, func(tx *sql.Tx) error {
		return tx.QueryRow(`INSERT INTO devices (public_id, client_id, name, created_at, last_seen_at) VALUES (?, 'test-device', 'test', ?, ?)
			RETURNING id`, NewID("dev"), now, now).Scan(&id)
	})
	return id, err
}
