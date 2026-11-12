package db

import (
	"context"
	"database/sql"
)

// PlayedSong is one song of a profile's recent listening that sound analysis has read, with
// what Mixes for you are made from and drawn with.
type PlayedSong struct {
	ItemID    int64
	PublicID  string
	Plays     int
	Embedding []byte
	Tags      []byte
	AlbumID   string
	AlbumHash string // the album's cover, for the mix's drawn cover; "" for none
	ArtistID  string
	Artist    string
}

// soundOn is the condition a library's songs meet for Mixes: a music library whose Sonic
// analysis is not turned off.
const soundOn = `l.kind = 'music' AND ifnull(json_extract(l.options, '$.noSoundAnalysis'), 0) = 0`

// RecentPlayedSongs reads the songs among the profile's last `plays` plays that were listened
// to (half the song, or 30 seconds of a long one; a play made while its history was paused is
// left out) that it may still see, that are available and that the model has read, each with
// how many of those plays it had. It reads at most that many rows of Play history through the
// profile's index, and one row of each table per song.
func (d *DB) RecentPlayedSongs(ctx context.Context, v Viewer, model string, plays int) ([]PlayedSong, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	// Older pre-launch folders also say version 1, but may predate sound analysis.
	// They have no analyses to make mixes from; a fresh folder always has this table.
	var hasSoundTable bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'track_embeddings')`).Scan(&hasSoundTable); err != nil {
		return nil, err
	}
	if !hasSoundTable {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `WITH recent AS (
			SELECT h.item_id, h.played_at, ifnull(h.watched_ms, 0) AS watched FROM play_history h
			WHERE h.profile_id = ? AND h.item_kind = ? AND h.hidden_from_profile = 0 AND h.item_id IS NOT NULL
			ORDER BY h.played_at DESC LIMIT ?)
		SELECT i.id, i.public_id, sum(recent.watched >= min(30000, ifnull(i.duration_ms, 0) / 2)) AS plays, e.embedding, e.tags, ifnull(a.public_id, ''),
			ifnull((SELECT g.file_hash FROM images g WHERE g.item_id = a.id AND g.role = ? AND g.file_hash IS NOT NULL ORDER BY g.selected DESC, g.position LIMIT 1), ''),
			ifnull(r.public_id, ''), ifnull(r.title, '')
		FROM recent JOIN items i ON i.id = recent.item_id
		JOIN libraries l ON l.id = i.library_id
		JOIN track_embeddings e ON e.item_id = i.id AND e.model = ? AND e.failed IS NULL AND e.embedding IS NOT NULL
		LEFT JOIN items a ON a.id = i.parent_id
		LEFT JOIN items r ON r.id = i.root_id
		WHERE i.available = 1 AND `+soundOn+` AND `+acc.visible("i")+`
		GROUP BY i.id HAVING plays > 0 ORDER BY max(recent.played_at) DESC, i.id`, v.ProfileID, KindTrack, plays, ImagePoster, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayedSong
	for rows.Next() {
		var p PlayedSong
		if err := rows.Scan(&p.ItemID, &p.PublicID, &p.Plays, &p.Embedding, &p.Tags, &p.AlbumID, &p.AlbumHash, &p.ArtistID, &p.Artist); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MusicCoverage counts the songs the profile may see in music libraries with Sonic analysis
// on, and how many of them the model has read. It reads every such song once: as large as
// the music the profile may see, so the caller keeps the answer for a while.
func (d *DB) MusicCoverage(ctx context.Context, v Viewer, model string) (songs, analysed int, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return 0, 0, err
	}
	// From the music libraries to their songs through (library_id, kind): the rest of the
	// catalogue is never read.
	err = tx.QueryRowContext(ctx, `SELECT count(*), ifnull(sum(e.item_id IS NOT NULL), 0) FROM libraries l
		CROSS JOIN items i ON i.library_id = l.id AND i.kind = ?
		LEFT JOIN track_embeddings e ON e.item_id = i.id AND e.model = ?
		WHERE i.available = 1 AND `+soundOn+` AND `+acc.visible("i"), KindTrack, model).Scan(&songs, &analysed)
	return songs, analysed, err
}
