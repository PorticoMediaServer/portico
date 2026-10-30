package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// Two albums, two books or two artists that turn out to be one become one,
// as two titles do. The children of the one that goes move to the one that
// stays: a track, part or album the stayer already has at the same place
// (and by the same name) absorbs the incoming one as another version of
// itself; the rest move as they are. Everyone's state for the container
// (favourites, finished marks, plays, a book's resume point, collections and
// lists it is in) is combined on the stayer, and every name the absorbed
// container was known by now finds the stayer, so the next scan of its files
// lands them where the owner put them.
//
// Children move the way a scan moves a file between albums or books (the
// parent link and the side-table fact), so every structure kept in step with
// album and book membership follows on its own.

// ErrMergeTooLarge: the merge would move more children than one step may.
var ErrMergeTooLarge = requestinput.New("these are too large to merge in one step")

// containerMergeLimit bounds the children (songs, files, albums, credited
// songs) one merge moves, so the write stays short.
const containerMergeLimit = 5000

type containerChild struct {
	id     int64
	disc   sql.NullInt64
	number sql.NullInt64
	title  string
}

func readContainerChildren(ctx context.Context, tx *sql.Tx, query string, parent int64) ([]containerChild, error) {
	rows, err := tx.QueryContext(ctx, query, parent, containerMergeLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []containerChild
	for rows.Next() {
		var c containerChild
		if err = rows.Scan(&c.id, &c.disc, &c.number, &c.title); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > containerMergeLimit {
		return nil, ErrMergeTooLarge
	}
	return out, nil
}

// samePlaceTx finds the stayer's child at the same disc and number with the
// same name, or 0. Same number alone is not enough: track 5 of a deluxe
// edition is not track 5 of the standard one.
func samePlaceTx(ctx context.Context, tx *sql.Tx, query string, into int64, c containerChild) (int64, error) {
	var disc, number any
	if c.disc.Valid {
		disc = c.disc.Int64
	}
	if c.number.Valid {
		number = c.number.Int64
	}
	rows, err := tx.QueryContext(ctx, query, into, disc, number)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	name := compactcatalog.IdentityText(c.title)
	for rows.Next() {
		var id int64
		var title string
		if err = rows.Scan(&id, &title); err != nil {
			return 0, err
		}
		if name != "" && compactcatalog.IdentityText(title) == name {
			return id, rows.Err()
		}
	}
	return 0, rows.Err()
}

// mergeAlbumChildrenTx moves an album's songs to another album.
func mergeAlbumChildrenTx(ctx context.Context, tx *sql.Tx, from, into int64) error {
	songs, err := readContainerChildren(ctx, tx, `SELECT x.entity_id,x.disc_number,x.track_number,e.title FROM catalog_songs x INDEXED BY catalog_songs_album_order JOIN catalog_entities e ON e.id=x.entity_id
 WHERE x.album_id=? ORDER BY x.disc_number,x.track_number,x.entity_id LIMIT ?`, from)
	if err != nil {
		return err
	}
	for _, s := range songs {
		same, err := samePlaceTx(ctx, tx, `SELECT x.entity_id,e.title FROM catalog_songs x INDEXED BY catalog_songs_listening_order JOIN catalog_entities e ON e.id=x.entity_id
 WHERE x.album_id=? AND COALESCE(x.disc_number,1)=COALESCE(?,1) AND COALESCE(x.track_number,0)=COALESCE(?,0) ORDER BY x.entity_id LIMIT 16`, into, s)
		if err != nil {
			return err
		}
		if same != 0 && same != s.id {
			if err = MergeTitlesTx(ctx, tx, s.id, same); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, into, s.id, into); err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, s.id, map[string]any{"album_id": into}); err != nil {
			return err
		}
	}
	return nil
}

// mergeBookChildrenTx moves a book's files to another book, with the book's
// resume points and the series and groups it was in.
func mergeBookChildrenTx(ctx context.Context, tx *sql.Tx, from, into int64) error {
	files, err := readContainerChildren(ctx, tx, `SELECT x.entity_id,x.disc_number,x.part_number,e.title FROM catalog_book_files x INDEXED BY catalog_book_files_order JOIN catalog_entities e ON e.id=x.entity_id
 WHERE x.book_id=? ORDER BY x.disc_number,x.part_number,x.entity_id LIMIT ?`, from)
	if err != nil {
		return err
	}
	for _, f := range files {
		same, err := samePlaceTx(ctx, tx, `SELECT x.entity_id,e.title FROM catalog_book_files x INDEXED BY catalog_book_files_listening_order JOIN catalog_entities e ON e.id=x.entity_id
 WHERE x.book_id=? AND COALESCE(x.disc_number,1)=COALESCE(?,1) AND COALESCE(x.part_number,0)=COALESCE(?,0) ORDER BY x.entity_id LIMIT 16`, into, f)
		if err != nil {
			return err
		}
		if same != 0 && same != f.id {
			if err = MergeTitlesTx(ctx, tx, f.id, same); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, into, f.id, into); err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, f.id, map[string]any{"book_id": into}); err != nil {
			return err
		}
	}
	for _, q := range []string{
		// The stayer's own resume point wins; the other's is kept where the stayer has none.
		`INSERT OR IGNORE INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) SELECT profile_id,?2,item_id,playback_id,position,unit,completed FROM book_resume WHERE book_id=?1`,
		`DELETE FROM book_resume WHERE book_id=?1`,
		`INSERT OR IGNORE INTO catalog_book_group_members(group_id,book_id) SELECT group_id,?2 FROM catalog_book_group_members INDEXED BY catalog_book_group_members_book WHERE book_id=?1`,
		`DELETE FROM catalog_book_group_members WHERE book_id=?1`,
	} {
		if _, err = tx.ExecContext(ctx, q, from, into); err != nil {
			return err
		}
	}
	return nil
}

// mergeArtistChildrenTx moves an artist's albums and song credits to another
// artist. An album the stayer already has under the same name and year (and a
// compatible edition) is the same album: the two merge in turn.
func mergeArtistChildrenTx(ctx context.Context, tx *sql.Tx, from, into int64) error {
	type album struct {
		id      int64
		title   string
		year    int
		edition string
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.entity_id,e.title,e.year,a.edition_key FROM catalog_albums a INDEXED BY catalog_albums_artist JOIN catalog_entities e ON e.id=a.entity_id WHERE a.artist_id=? ORDER BY a.entity_id LIMIT ?`, from, containerMergeLimit+1)
	if err != nil {
		return err
	}
	var albums []album
	for rows.Next() {
		var a album
		if err = rows.Scan(&a.id, &a.title, &a.year, &a.edition); err != nil {
			rows.Close()
			return err
		}
		albums = append(albums, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(albums) > containerMergeLimit {
		return ErrMergeTooLarge
	}
	for _, a := range albums {
		var same int64
		candidates, err := tx.QueryContext(ctx, `SELECT a.entity_id,e.title,a.edition_key FROM catalog_albums a INDEXED BY catalog_albums_artist JOIN catalog_entities e ON e.id=a.entity_id WHERE a.artist_id=? AND e.year=? ORDER BY a.entity_id LIMIT 64`, into, a.year)
		if err != nil {
			return err
		}
		for candidates.Next() {
			var id int64
			var title, edition string
			if err = candidates.Scan(&id, &title, &edition); err != nil {
				candidates.Close()
				return err
			}
			if _, ok := mergeLocalEdition(edition, a.edition); ok && same == 0 && compactcatalog.IdentityText(title) == compactcatalog.IdentityText(a.title) {
				same = id
			}
		}
		err = candidates.Err()
		candidates.Close()
		if err != nil {
			return err
		}
		if same != 0 {
			// The album merge itself, without settling the artist: that is this merge's to finish.
			if err = mergeAlbumChildrenTx(ctx, tx, a.id, same); err != nil {
				return err
			}
			if err = absorbTx(ctx, tx, a.id, same, compactcatalog.Album); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, into, a.id, into); err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, a.id, map[string]any{"artist_id": into}); err != nil {
			return err
		}
	}
	// Song credits: each credited song names the stayer instead, once.
	credited, err := scanInt64s(tx.QueryContext(ctx, `SELECT song_id FROM catalog_song_artists INDEXED BY catalog_song_artists_artist WHERE artist_id=? ORDER BY song_id LIMIT ?`, from, containerMergeLimit+1))
	if err != nil {
		return err
	}
	if len(credited) > containerMergeLimit {
		return ErrMergeTooLarge
	}
	for _, song := range credited {
		artists, err := scanInt64s(tx.QueryContext(ctx, `SELECT artist_id FROM catalog_song_artists WHERE song_id=? ORDER BY artist_id`, song))
		if err != nil {
			return err
		}
		next := make([]int64, 0, len(artists))
		seen := map[int64]bool{}
		for _, a := range artists {
			if a == from {
				a = into
			}
			if !seen[a] {
				seen[a] = true
				next = append(next, a)
			}
		}
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, song, next); err != nil {
			return err
		}
	}
	return nil
}

// authorFilesTx lists the files of the books whose author text names this
// author, by every name the ledger keeps for it.
func authorFilesTx(ctx context.Context, tx *sql.Tx, author int64) ([]int64, error) {
	var library int64
	if err := tx.QueryRowContext(ctx, `SELECT library_id FROM catalog_entities WHERE id=?`, author).Scan(&library); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_key FROM catalog_identities WHERE public_id=(SELECT public_id FROM catalog_entities WHERE id=?) ORDER BY source_key LIMIT 64`, author)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		if name, ok := strings.CutPrefix(key, "author:"); ok && strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var files []int64
	for _, name := range names {
		found, err := scanInt64s(tx.QueryContext(ctx, `SELECT f.entity_id FROM catalog_book_context c INDEXED BY catalog_book_context_author CROSS JOIN catalog_book_files f ON f.book_id=c.book_id
 WHERE c.library_id=? AND c.author_key=? AND c.author_key!='' ORDER BY f.entity_id LIMIT ?`, library, name, containerMergeLimit+1))
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
		if len(files) > containerMergeLimit {
			return nil, ErrMergeTooLarge
		}
	}
	return files, nil
}

// settleAbandonedArtistTx removes an album artist that a merge left with no
// album and no song: retired when it has state of its own, deleted otherwise.
func settleAbandonedArtistTx(ctx context.Context, tx *sql.Tx, artist int64) error {
	if artist == 0 {
		return nil
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_albums a JOIN catalog_entities e ON e.id=a.entity_id AND e.retired=0 WHERE a.artist_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_song_artists WHERE artist_id=?1)`, artist).Scan(&used); err != nil || used {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=?)`, artist).Scan(&exists); err != nil || !exists {
		return err
	}
	return settleEmptied(ctx, tx, artist, artistRetireChecks)
}

// albumArtistTx is an album's artist (0 when it has none).
func albumArtistTx(ctx context.Context, tx *sql.Tx, album int64) (int64, error) {
	var artist int64
	err := tx.QueryRowContext(ctx, `SELECT artist_id FROM catalog_albums WHERE entity_id=?`, album).Scan(&artist)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return artist, err
}

// isContainerKind: kinds whose merge moves children rather than files.
func isContainerKind(kind compactcatalog.Kind) bool {
	return kind == compactcatalog.Album || kind == compactcatalog.Book || kind == compactcatalog.Artist
}

// mergeContainerTx is MergeTitlesTx for an album, book or artist.
func mergeContainerTx(ctx context.Context, tx *sql.Tx, from, into int64, kind compactcatalog.Kind) error {
	switch kind {
	case compactcatalog.Album:
		artist, err := albumArtistTx(ctx, tx, from)
		if err != nil {
			return err
		}
		if err = mergeAlbumChildrenTx(ctx, tx, from, into); err != nil {
			return err
		}
		if err = absorbTx(ctx, tx, from, into, kind); err != nil {
			return err
		}
		return settleAbandonedArtistTx(ctx, tx, artist)
	case compactcatalog.Book:
		if err := mergeBookChildrenTx(ctx, tx, from, into); err != nil {
			return err
		}
		return absorbTx(ctx, tx, from, into, kind)
	case compactcatalog.Artist:
		if err := mergeArtistChildrenTx(ctx, tx, from, into); err != nil {
			return err
		}
		return absorbTx(ctx, tx, from, into, kind)
	case compactcatalog.Author:
		// A book names its author in text, and that text reaches the author
		// through the identity ledger. Once the ledger's names for the absorbed
		// author lead to the one that stays, its books' files join that author
		// (and a later scan of them keeps them there).
		files, err := authorFilesTx(ctx, tx, from)
		if err != nil {
			return err
		}
		if err = absorbTx(ctx, tx, from, into, kind); err != nil {
			return err
		}
		for _, file := range files {
			if err = compactcatalog.TouchTx(ctx, tx, compactcatalog.DomainBrowseEdges, file); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w: kind %d", ErrNotMergeable, kind)
}
