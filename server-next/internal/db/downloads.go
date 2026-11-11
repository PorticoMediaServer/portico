package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Download is one thing a profile asked to have on one device: a title, or a show, season,
// album, artist or playlist with a selection of its titles.
type Download struct {
	ID       int64
	PublicID string
	// ProfileID and DeviceID own it; DevicePublicID is the device on the wire.
	ProfileID      int64
	DeviceID       int64
	DevicePublicID string
	// Exactly one of ItemID (with ItemPublicID, Kind and Title) and ListID (with
	// ListPublicID and Title) is set.
	ItemID       int64
	ItemPublicID string
	Kind         ItemKind
	ListID       int64
	ListPublicID string
	Title        string
	// VersionPublicID is the version the person chose, when they chose one.
	VersionPublicID string
	// Quality is "original" or a recipe. Selection is all, unwatched or next; KeepNext is
	// 1, 3 or 5 for a lasting "keep the next" rule, else 0.
	Quality   string
	Selection string
	KeepNext  int
	Paused    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// The counts of its titles, for lists: Titles in all, Ready, Waiting (being made or in
	// line), Failed (could not be prepared, expired or left out).
	Titles, Ready, Waiting, Failed int
}

// The states of one title of a download, as stored.
const (
	DownloadWanted  = 0
	DownloadLeftOut = 1
)

// DownloadTitle is one title of a download, with the copy it uses when it uses one.
type DownloadTitle struct {
	ItemID       int64
	ItemPublicID string
	Kind         ItemKind
	Title        string
	MediaID      int64
	State        int
	Reason       string
	Position     int
	// Prepared is the copy it fetches, nil for the original or when the copy has gone.
	Prepared *Prepared
}

// NewDownload is a request to store, with its titles in order.
type NewDownload struct {
	Viewer
	DeviceID                   int64
	ItemPublicID, ListPublicID string
	VersionPublicID            string
	Quality, Selection         string
	KeepNext                   int
	Titles                     []DownloadTitle
	IdempotencyKey, Hash       string
}

const downloadColumns = `d.id, d.public_id, d.profile_id, d.device_id, dv.public_id, ifnull(d.item_id, 0), ifnull(i.public_id, ''),
	ifnull(i.kind, 0), ifnull(d.list_id, 0), ifnull(l.public_id, ''), coalesce(i.title, l.name, ''), ifnull(m.public_id, ''), d.quality,
	d.selection, ifnull(d.keep_next, 0), d.paused, d.created_at, d.updated_at,
	(SELECT count(*) FROM download_items x WHERE x.download_id = d.id),
	(SELECT count(*) FROM download_items x LEFT JOIN prepared_media p ON p.id = x.prepared_id WHERE x.download_id = d.id AND x.state = 0
		AND (d.quality = 'original' OR p.state = 'ready')),
	(SELECT count(*) FROM download_items x JOIN prepared_media p ON p.id = x.prepared_id WHERE x.download_id = d.id AND x.state = 0
		AND p.state IN ('waiting', 'preparing')),
	(SELECT count(*) FROM download_items x LEFT JOIN prepared_media p ON p.id = x.prepared_id WHERE x.download_id = d.id
		AND (x.state <> 0 OR d.quality <> 'original' AND (p.id IS NULL OR p.state = 'failed')))`

const downloadFrom = ` FROM downloads d JOIN devices dv ON dv.id = d.device_id LEFT JOIN items i ON i.id = d.item_id
	LEFT JOIN lists l ON l.id = d.list_id LEFT JOIN media m ON m.id = d.media_id`

func scanDownload(row interface{ Scan(...any) error }) (Download, error) {
	var d Download
	var created, updated int64
	err := row.Scan(&d.ID, &d.PublicID, &d.ProfileID, &d.DeviceID, &d.DevicePublicID, &d.ItemID, &d.ItemPublicID, &d.Kind, &d.ListID,
		&d.ListPublicID, &d.Title, &d.VersionPublicID, &d.Quality, &d.Selection, &d.KeepNext, &d.Paused, &created, &updated,
		&d.Titles, &d.Ready, &d.Waiting, &d.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	d.CreatedAt, d.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
	return d, err
}

// DownloadsAllowed says whether the viewer may download at all: the account and the profile
// both allow it, and the account is active.
func (d *DB) DownloadsAllowed(ctx context.Context, v Viewer) (bool, error) {
	var allowed bool
	err := d.read.QueryRowContext(ctx, `SELECT u.allow_downloads AND p.allow_downloads AND u.state = 'active' AND u.disabled_at IS NULL
		FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.id = ? AND u.id = ?`, v.ProfileID, v.UserID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return allowed, err
}

// DownloadSources returns titles by id with their versions, for choosing what to download.
// The caller has already found them through the viewer's own access.
func (d *DB) DownloadSources(ctx context.Context, itemIDs []int64) (map[int64]DownloadSource, error) {
	out := map[int64]DownloadSource{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	list, err := queryAll(ctx, tx, `SELECT id, public_id, kind, title FROM items WHERE id IN `+intList(itemIDs), nil,
		func(s *DownloadSource) []any { return []any{&s.ID, &s.PublicID, &s.Kind, &s.Title} })
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if s.Versions, err = versions(ctx, tx, s.ID); err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, nil
}

// DownloadSource is a title that can be downloaded, with its versions.
type DownloadSource struct {
	ID       int64
	PublicID string
	Kind     ItemKind
	Title    string
	Versions []Version
}

// VisibleItem returns a title's id and kind if the viewer may see it.
func (d *DB) VisibleItem(ctx context.Context, v Viewer, publicID string) (int64, ItemKind, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return 0, 0, err
	}
	var id int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = @id AND `+acc.visible("i"),
		append(v.args(), sql.Named("id", publicID))...).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return id, kind, err
}

// Watched returns which of the titles the profile has watched.
func (d *DB) Watched(ctx context.Context, profileID int64, itemIDs []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := d.read.QueryContext(ctx, `SELECT item_id FROM user_item_state WHERE profile_id = ? AND played = 1 AND item_id IN `+intList(itemIDs), profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CreateDownload stores a request and its titles. The same target at the same quality on the
// same device asked for again is the stored one, with created false; so is a repeat of the
// same keyed request.
func (d *DB) CreateDownload(ctx context.Context, n NewDownload, now time.Time) (out Download, created bool, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, n.UserID, n.IdempotencyKey, n.Hash)
		if err != nil {
			return err
		}
		if ref == "" {
			err = tx.QueryRowContext(ctx, `SELECT d.public_id FROM downloads d LEFT JOIN items i ON i.id = d.item_id LEFT JOIN lists l ON l.id = d.list_id
				WHERE d.profile_id = ? AND d.device_id = ? AND ifnull(i.public_id, '') = ? AND ifnull(l.public_id, '') = ? AND d.quality = ?`,
				n.ProfileID, n.DeviceID, n.ItemPublicID, n.ListPublicID, n.Quality).Scan(&ref)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if ref != "" {
			out, err = scanDownload(tx.QueryRowContext(ctx, `SELECT `+downloadColumns+downloadFrom+` WHERE d.public_id = ?`, ref))
			return err
		}
		created = true
		publicID := NewID("dl")
		res, err := tx.ExecContext(ctx, `INSERT INTO downloads (public_id, profile_id, device_id, item_id, list_id, media_id, quality, selection,
			keep_next, created_at, updated_at) VALUES (?, ?, ?, (SELECT id FROM items WHERE public_id = nullif(?, '')),
			(SELECT id FROM lists WHERE public_id = nullif(?, '')), (SELECT id FROM media WHERE public_id = nullif(?, '')), ?, ?, nullif(?, 0), ?, ?)`,
			publicID, n.ProfileID, n.DeviceID, n.ItemPublicID, n.ListPublicID, n.VersionPublicID, n.Quality, n.Selection, n.KeepNext,
			now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		if err := insertDownloadTitles(ctx, tx, id, n.Titles); err != nil {
			return err
		}
		if err := rememberCreated(tx, n.UserID, n.IdempotencyKey, n.Hash, publicID, now); err != nil {
			return err
		}
		out, err = scanDownload(tx.QueryRowContext(ctx, `SELECT `+downloadColumns+downloadFrom+` WHERE d.id = ?`, id))
		return err
	})
	return out, created, err
}

func insertDownloadTitles(ctx context.Context, tx *sql.Tx, downloadID int64, titles []DownloadTitle) error {
	for _, t := range titles {
		var prepared *int64
		if t.Prepared != nil {
			prepared = &t.Prepared.ID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO download_items (download_id, item_id, media_id, prepared_id, state, reason, position)
			VALUES (?, ?, nullif(?, 0), ?, ?, nullif(?, ''), ?) ON CONFLICT DO NOTHING`,
			downloadID, t.ItemID, t.MediaID, prepared, t.State, t.Reason, t.Position); err != nil {
			return err
		}
	}
	return nil
}

// Downloads lists a profile's requests on one device, or on every device with deviceID 0,
// newest first. A few rows per device.
func (d *DB) Downloads(ctx context.Context, profileID, deviceID int64) ([]Download, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+downloadColumns+downloadFrom+` WHERE d.profile_id = ?1 AND (?2 = 0 OR d.device_id = ?2)
		ORDER BY d.created_at DESC, d.id DESC`, profileID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Download{}
	for rows.Next() {
		dl, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// DownloadOf returns one of the profile's requests with its titles in order. Another
// profile's is ErrNotFound.
func (d *DB) DownloadOf(ctx context.Context, profileID int64, publicID string) (Download, []DownloadTitle, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Download{}, nil, err
	}
	defer tx.Rollback()
	dl, err := scanDownload(tx.QueryRowContext(ctx, `SELECT `+downloadColumns+downloadFrom+` WHERE d.public_id = ? AND d.profile_id = ?`,
		publicID, profileID))
	if err != nil {
		return dl, nil, err
	}
	titles, err := downloadTitles(ctx, tx, dl.ID)
	return dl, titles, err
}

func downloadTitles(ctx context.Context, tx *sql.Tx, downloadID int64) ([]DownloadTitle, error) {
	rows, err := tx.QueryContext(ctx, `SELECT x.item_id, i.public_id, i.kind, i.title, ifnull(x.media_id, 0), x.state, ifnull(x.reason, ''),
		x.position, p.id IS NOT NULL, p.id, p.public_id, p.media_id, p.recipe, p.state, p.error, p.container, p.size, p.content_hash,
		p.created_at, p.last_used_at FROM download_items x JOIN items i ON i.id = x.item_id
		LEFT JOIN prepared_media p ON p.id = x.prepared_id WHERE x.download_id = ? ORDER BY x.position, x.item_id`, downloadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DownloadTitle
	for rows.Next() {
		var t DownloadTitle
		var has bool
		var p Prepared
		var pid, media, size sql.NullInt64
		var public, recipe, state, reason, container, hash sql.NullString
		var created, used sql.NullInt64
		if err := rows.Scan(&t.ItemID, &t.ItemPublicID, &t.Kind, &t.Title, &t.MediaID, &t.State, &t.Reason, &t.Position, &has,
			&pid, &public, &media, &recipe, &state, &reason, &container, &size, &hash, &created, &used); err != nil {
			return nil, err
		}
		if has {
			p = Prepared{ID: pid.Int64, PublicID: public.String, MediaID: media.Int64, Recipe: recipe.String, State: state.String,
				Error: reason.String, Container: container.String, Size: size.Int64, ContentHash: hash.String,
				CreatedAt: time.UnixMilli(created.Int64), LastUsedAt: time.UnixMilli(used.Int64)}
			t.Prepared = &p
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetDownload pauses or resumes a request, and sets or removes its "keep the next" count
// (keepNext 0 removes it; nil leaves it). It returns the copies the request uses, so the
// caller can stop making those nobody else wants now.
func (d *DB) SetDownload(ctx context.Context, profileID int64, publicID string, paused *bool, keepNext *int, now time.Time) ([]int64, error) {
	var copies []int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE downloads SET paused = coalesce(?, paused), keep_next = CASE WHEN ? THEN nullif(?, 0) ELSE keep_next END,
			updated_at = ? WHERE public_id = ? AND profile_id = ?`, paused, keepNext != nil, ptrValue(keepNext), now.UnixMilli(), publicID, profileID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		copies, err = ids(ctx, tx, `SELECT DISTINCT x.prepared_id FROM download_items x JOIN downloads d ON d.id = x.download_id
			WHERE d.public_id = ? AND x.prepared_id IS NOT NULL`, publicID)
		return err
	})
	return copies, err
}

func ptrValue(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// UseCopy points one title of a request at a copy: after the old one expired, or to try
// again.
func (d *DB) UseCopy(ctx context.Context, downloadID, itemID, preparedID int64) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE download_items SET prepared_id = ? WHERE download_id = ? AND item_id = ?`, preparedID, downloadID, itemID)
		return err
	})
}

// DeleteDownload removes a profile's request. It returns the copies it used. Removing one
// that is not there succeeds.
func (d *DB) DeleteDownload(ctx context.Context, profileID int64, publicID string) ([]int64, error) {
	var copies []int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var err error
		copies, err = ids(ctx, tx, `SELECT DISTINCT x.prepared_id FROM download_items x JOIN downloads d ON d.id = x.download_id
			WHERE d.public_id = ? AND d.profile_id = ? AND x.prepared_id IS NOT NULL`, publicID, profileID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM downloads WHERE public_id = ? AND profile_id = ?`, publicID, profileID)
		return err
	})
	return copies, err
}

// CopyWanted says whether a request that is not paused still waits for a copy.
func (d *DB) CopyWanted(ctx context.Context, preparedID int64) (bool, error) {
	var wanted bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM download_items x JOIN downloads d ON d.id = x.download_id
		WHERE x.prepared_id = ? AND x.state = 0 AND d.paused = 0)`, preparedID).Scan(&wanted)
	return wanted, err
}

// ProfileCopiesSpace is the room the copies a profile's requests use take, each copy once.
func (d *DB) ProfileCopiesSpace(ctx context.Context, profileID int64) (int64, error) {
	var used int64
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(sum(size), 0) FROM prepared_media WHERE id IN (SELECT x.prepared_id
		FROM download_items x JOIN downloads d ON d.id = x.download_id WHERE d.profile_id = ?)`, profileID).Scan(&used)
	return used, err
}

// DownloadedTitle is one title of a profile's request, for fetching it.
func (d *DB) DownloadedTitle(ctx context.Context, profileID int64, downloadPublicID, itemPublicID string) (Download, DownloadTitle, error) {
	dl, titles, err := d.DownloadOf(ctx, profileID, downloadPublicID)
	if err != nil {
		return dl, DownloadTitle{}, err
	}
	for _, t := range titles {
		if t.ItemPublicID == itemPublicID {
			return dl, t, nil
		}
	}
	return dl, DownloadTitle{}, ErrNotFound
}
