package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// The states of a prepared copy.
const (
	PreparedWaiting   = "waiting"
	PreparedPreparing = "preparing"
	PreparedReady     = "ready"
	PreparedFailed    = "failed"
)

// Prepared is a smaller copy of one version that the server made, or is making, for
// downloads. The file is in the prepared folder, named by PublicID.
type Prepared struct {
	ID       int64
	PublicID string
	MediaID  int64
	// Recipe is the quality it is made at, as one word: a step of the video ladder
	// ("720p-high") or a music bitrate ("320").
	Recipe string
	State  string
	// Error is why it could not be made, in words a person can read.
	Error     string
	Container string
	// Size is the bytes on disk once ready; ContentHash is the SHA-256 of the file, or of
	// each part's file in order, separated by spaces, for a version in several files.
	Size        int64
	ContentHash string
	CreatedAt   time.Time
	LastUsedAt  time.Time
}

const preparedColumns = `id, public_id, media_id, recipe, state, ifnull(error, ''), ifnull(container, ''), ifnull(size, 0),
	ifnull(content_hash, ''), created_at, last_used_at`

func scanPrepared(row interface{ Scan(...any) error }) (Prepared, error) {
	var p Prepared
	var created, used int64
	err := row.Scan(&p.ID, &p.PublicID, &p.MediaID, &p.Recipe, &p.State, &p.Error, &p.Container, &p.Size, &p.ContentHash, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt, p.LastUsedAt = time.UnixMilli(created), time.UnixMilli(used)
	return p, err
}

// WantPrepared returns the prepared copy of a version at a recipe, making the row (waiting)
// when there is none. Asking again for one that exists counts as using it, so it is kept
// longer. Asking is one read of the unique index and one small write.
//
// estimate is about how big the copy will be. It stands as the copy's size until the copy is
// made, so copies still waiting count against the owner's download space.
func (d *DB) WantPrepared(ctx context.Context, mediaID int64, recipe string, estimate int64, now time.Time) (p Prepared, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO prepared_media (public_id, media_id, recipe, size, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (media_id, recipe) DO UPDATE SET last_used_at = excluded.last_used_at`,
			NewID("prp"), mediaID, recipe, estimate, now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
		p, err = scanPrepared(tx.QueryRowContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media WHERE media_id = ? AND recipe = ?`, mediaID, recipe))
		return err
	})
	return p, err
}

// FindPrepared returns the copy of a version at a recipe, or ErrNotFound.
func (d *DB) FindPrepared(ctx context.Context, mediaID int64, recipe string) (Prepared, error) {
	return scanPrepared(d.read.QueryRowContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media WHERE media_id = ? AND recipe = ?`, mediaID, recipe))
}

// PreparedByID returns one prepared copy.
func (d *DB) PreparedByID(ctx context.Context, id int64) (Prepared, error) {
	return scanPrepared(d.read.QueryRowContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media WHERE id = ?`, id))
}

// NextPrepared returns the copy that has waited longest, leaving out those in skip (being
// made already). ErrNotFound when nothing waits.
func (d *DB) NextPrepared(ctx context.Context, skip []int64) (Prepared, error) {
	return scanPrepared(d.read.QueryRowContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media
		WHERE state IN ('waiting', 'preparing') AND id NOT IN `+intList(skip)+` ORDER BY created_at, id LIMIT 1`))
}

// SetPreparedState records that a copy is being made, or why it could not be.
func (d *DB) SetPreparedState(ctx context.Context, id int64, state, reason string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE prepared_media SET state = ?, error = nullif(?, '') WHERE id = ?`, state, reason, id)
		return err
	})
}

// FinishPrepared records a copy that is ready. The keeping time starts again from now.
func (d *DB) FinishPrepared(ctx context.Context, id int64, container string, size int64, hash string, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE prepared_media SET state = 'ready', error = NULL, container = ?, size = ?, content_hash = ?,
			ready_at = ?, last_used_at = ? WHERE id = ?`, container, size, hash, now.UnixMilli(), now.UnixMilli(), id)
		return err
	})
}

// RetryPrepared puts a copy that could not be made back in the queue.
func (d *DB) RetryPrepared(ctx context.Context, id int64, now time.Time) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE prepared_media SET state = 'waiting', error = NULL, last_used_at = ? WHERE id = ? AND state = 'failed'`,
			now.UnixMilli(), id)
		return err
	})
}

// UsePrepared moves a copy's keeping time on: someone asked for it or fetched it.
func (d *DB) UsePrepared(ctx context.Context, id int64, now time.Time) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE prepared_media SET last_used_at = max(last_used_at, ?) WHERE id = ?`, now.UnixMilli(), id)
		return err
	})
}

// PreparedSpace is the room the server's copies take, each copy once however many people
// asked for it: a copy made at its size, one waiting or being made at its estimate. A copy
// that could not be made takes none. Thousands of rows at most.
func (d *DB) PreparedSpace(ctx context.Context) (int64, error) {
	var used int64
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(sum(size), 0) FROM prepared_media WHERE state <> 'failed'`).Scan(&used)
	return used, err
}

// UnusedPrepared returns up to limit copies nobody has asked for or fetched since before.
// A copy being made is never among them.
func (d *DB) UnusedPrepared(ctx context.Context, before time.Time, limit int) ([]Prepared, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+preparedColumns+` FROM prepared_media
		WHERE last_used_at < ? AND state <> 'preparing' ORDER BY last_used_at LIMIT ?`, before.UnixMilli(), limit)
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

// RemovePrepared deletes copies' rows. A download that waited for one reads as expired.
func (d *DB) RemovePrepared(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM prepared_media WHERE id IN `+intList(ids))
		return err
	})
}

// PreparedNames lists the public id of every prepared copy, so files no row names can be
// removed. Thousands at most.
func (d *DB) PreparedNames(ctx context.Context) (map[string]bool, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT public_id FROM prepared_media`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// PreparedVersion returns the version a copy is made from, with its parts and their tracks,
// and the kind of its title. It reads as the server: whoever asked for the copy was checked
// then. A version with a part gone missing is ErrNotFound.
func (d *DB) PreparedVersion(ctx context.Context, mediaID int64) (Version, ItemKind, error) {
	var kind ItemKind
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Version{}, kind, err
	}
	defer tx.Rollback()
	var itemID int64
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM media m JOIN items i ON i.id = m.item_id WHERE m.id = ?`, mediaID).Scan(&itemID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, kind, ErrNotFound
	}
	if err != nil {
		return Version{}, kind, err
	}
	list, err := versions(ctx, tx, itemID)
	if err != nil {
		return Version{}, kind, err
	}
	for _, v := range list {
		if v.id == mediaID {
			return v, kind, nil
		}
	}
	return Version{}, kind, ErrNotFound
}
