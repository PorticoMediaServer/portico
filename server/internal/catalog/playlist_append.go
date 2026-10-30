package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// AppendPlaylistItemsTx appends items, in order, at the end of a playlist the
// actor owns, inside the caller's write transaction, and returns the playlist's
// new revision. It is the write half of saving a queue that doesn't fit one
// synchronous playlist write (NEW-37): the caller has already checked that the
// actor may play every item, and sends at most MaxPlaylistEntries per call so
// each transaction stays short. Only playable leaves are appended; anything
// else is skipped and not counted. A deleted or foreign playlist is
// sql.ErrNoRows.
func AppendPlaylistItemsTx(ctx context.Context, tx *sql.Tx, a ResourceActor, playlist string, items []string) (revision int64, appended int, err error) {
	if len(items) > MaxPlaylistEntries {
		return 0, 0, ErrPlaylistCapacity
	}
	role, _, deleted, err := playlistRole(tx, playlist, a)
	if err != nil {
		return 0, 0, err
	}
	if role != "owner" || deleted {
		return 0, 0, sql.ErrNoRows
	}
	var position int64
	var last string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(e.position),0) FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=?`, playlist).Scan(&position); err != nil {
		return 0, 0, err
	}
	err = tx.QueryRowContext(ctx, `SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=p.id WHERE p.token=? ORDER BY e.order_key DESC,e.id DESC LIMIT 1`, playlist).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	allowed := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT v.value,k.playable=1 AND i.kind<>11 FROM json_each(?) v JOIN catalog_entities i ON i.public_id=pid_blob(v.value) JOIN catalog_kinds k ON k.id=i.kind`, idsJSON(items))
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var id string
		var ok bool
		if err = rows.Scan(&id, &ok); err != nil {
			rows.Close()
			return 0, 0, err
		}
		allowed[id] = ok
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, err
	}
	for _, item := range items {
		if !allowed[item] {
			continue
		}
		position++
		last = playlistEndKey(last)
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,p.id,i.id,?,? FROM catalog_playlists p,catalog_entities i WHERE p.token=? AND i.public_id=pid_blob(?)`, identity.Token(), position, last, playlist, item); err != nil {
			return 0, 0, err
		}
		appended++
	}
	// The revision moves on every call, as it always has; the playlist's own last change
	// moves only when an item really landed.
	if appended > 0 {
		err = tx.QueryRowContext(ctx, `UPDATE catalog_playlists SET revision=revision+1,updated_at=? WHERE token=? RETURNING revision`, time.Now().UTC().Format(time.RFC3339), playlist).Scan(&revision)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE catalog_playlists SET revision=revision+1 WHERE token=? RETURNING revision`, playlist).Scan(&revision)
	}
	if err != nil {
		return 0, 0, err
	}
	return revision, appended, nil
}
