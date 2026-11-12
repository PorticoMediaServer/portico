package db

import (
	"context"
	"database/sql"
	"errors"
)

// SongSound is a song's place in the sound model's space, with its artist (the song's root),
// for radio by sound.
type SongSound struct {
	ItemID    int64
	Artist    int64
	Embedding []byte
}

const songSoundColumns = `i.id, ifnull(i.root_id, 0), e.embedding`

// RadioSeedSounds reads the analysed songs a radio starts from: the song itself, an album's
// songs or an artist's, at most 500, that the profile may see and that are available, with the
// library they are in. None when the model has read none of them, or the seed is not music
// the profile may see.
func (d *DB) RadioSeedSounds(ctx context.Context, v Viewer, seedPublicID, model string) (library int64, songs []SongSound, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return 0, nil, err
	}
	var seed int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind, i.library_id FROM items i JOIN libraries l ON l.id = i.library_id
		WHERE i.public_id = ? AND i.kind IN (?, ?, ?) AND `+soundOn+` AND `+acc.visible("i"), seedPublicID, KindArtist, KindAlbum, KindTrack).
		Scan(&seed, &kind, &library)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	which := `i.id = ?`
	switch kind {
	case KindAlbum:
		which = `i.parent_id = ?`
	case KindArtist:
		which = `i.root_id = ?`
	}
	songs, err = readSongSounds(ctx, tx, `SELECT `+songSoundColumns+` FROM items i
		JOIN track_embeddings e ON e.item_id = i.id AND e.model = ? AND e.embedding IS NOT NULL
		WHERE `+which+` AND i.kind = ? AND i.available = 1 AND `+acc.visible("i")+` LIMIT 500`, model, seed, KindTrack)
	return library, songs, err
}

// RadioCandidates reads up to limit analysed songs of a library the profile may see and that
// are available, in id order from after: the pool radio by sound ranks. A library's analyses
// are read through their own key, so the pool costs its size, not the library's.
func (d *DB) RadioCandidates(ctx context.Context, v Viewer, library int64, model string, after int64, limit int) ([]SongSound, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	return readSongSounds(ctx, tx, `SELECT `+songSoundColumns+` FROM track_embeddings e
		CROSS JOIN items i ON i.id = e.item_id
		WHERE e.item_id > ? AND e.model = ? AND e.embedding IS NOT NULL AND i.library_id = ? AND i.kind = ? AND i.available = 1
			AND `+acc.visible("i")+` ORDER BY e.item_id LIMIT ?`, after, model, library, KindTrack, limit)
}

// LastAnalysedSong is the highest id sound analysis has kept, where a pool may start from.
func (d *DB) LastAnalysedSong(ctx context.Context) (int64, error) {
	var id int64
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(max(item_id), 0) FROM track_embeddings`).Scan(&id)
	return id, err
}

// RecentlyPlayedSongs are the songs of the profile's last n plays, for a radio to leave out.
func (d *DB) RecentlyPlayedSongs(ctx context.Context, profileID int64, n int) ([]int64, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT DISTINCT item_id FROM (SELECT item_id FROM play_history
		WHERE profile_id = ? AND item_kind = ? AND item_id IS NOT NULL ORDER BY played_at DESC LIMIT ?)`, profileID, KindTrack, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func readSongSounds(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]SongSound, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SongSound
	for rows.Next() {
		var s SongSound
		if err := rows.Scan(&s.ItemID, &s.Artist, &s.Embedding); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
