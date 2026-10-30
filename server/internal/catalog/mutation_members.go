package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// One exact parent/key seek. Artist removal owns an album child command,
// rather than flattening/sorting every album's songs in a UNION.
func nextMutationMemberTx(ctx context.Context, tx *sql.Tx, entity int64, kind compactcatalog.Kind, cursor int64) (id int64, mode string, err error) {
	mode = "remove"
	var query string
	switch kind {
	case compactcatalog.Album:
		query = `SELECT entity_id FROM catalog_songs INDEXED BY catalog_songs_album_entity WHERE album_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`
	case compactcatalog.Book:
		query = `SELECT entity_id FROM catalog_book_files INDEXED BY catalog_book_files_book_entity WHERE book_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`
	case compactcatalog.Show:
		query = `SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show_entity WHERE show_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`
	case compactcatalog.Season:
		query = `SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_season_entity WHERE season_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`
	case compactcatalog.Artist:
		query = `SELECT entity_id FROM catalog_albums INDEXED BY catalog_albums_artist WHERE artist_id=? AND entity_id>? ORDER BY entity_id LIMIT 1`
		mode = "members"
	default:
		return 0, mode, sql.ErrNoRows
	}
	err = tx.QueryRowContext(ctx, query, entity, cursor).Scan(&id)
	return
}
func hasTitleMembersTx(ctx context.Context, tx *sql.Tx, entity int64, kind compactcatalog.Kind) (bool, error) {
	switch kind {
	case compactcatalog.Movie, compactcatalog.Episode, compactcatalog.Track, compactcatalog.Part, compactcatalog.Extra, compactcatalog.Kind(20), compactcatalog.Kind(21):
		return true, nil
	}
	// Artist emptiness spans album keys and must use the durable zero-member
	// step. Complete-only compatibility callers reject before any write.
	if kind == compactcatalog.Artist {
		return false, compactcatalog.ErrEntityMutating
	}
	_, _, e := nextMutationMemberTx(ctx, tx, entity, kind, 0)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return e == nil, e
}
