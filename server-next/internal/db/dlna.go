package db

import (
	"context"
	"database/sql"
	"errors"
)

// DLNA reads the catalogue with no viewer at all, by the owner's decision: while the owner has
// DLNA on, any device on the home network may browse the video and music libraries and play
// every original file. It writes nothing. Nothing else may call these.

// DLNALibraryKinds are the libraries DLNA shows: video and music, never books.
var DLNALibraryKinds = []string{LibraryMovies, LibraryShows, LibraryMusic, LibraryAnime}

// DLNAEntry is a library, a folder-like title (a show, a season, an artist, an album) or a
// title that plays, with what a DLNA listing says of it.
type DLNAEntry struct {
	ID       string // a library's or an item's public id
	Library  bool
	Kind     ItemKind
	Title    string
	Year     int
	Index    int
	Children int
	// For a title that plays: its first version's facts. FileID is the file to serve; a
	// version in several parts serves its first.
	FileID     int64
	Size       int64
	Container  string
	DurationMS int64
	VideoCodec string
	Width      int
	Height     int
	Channels   int
	Artist     string
	Album      string
}

// DLNALibraries lists the video and music libraries.
func (d *DB) DLNALibraries(ctx context.Context) ([]DLNAEntry, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT public_id, name, (SELECT count(*) FROM items i WHERE i.library_id = l.id AND i.parent_id IS NULL
		AND i.kind IN (?, ?, ?)) FROM libraries l WHERE kind IN (?, ?, ?, ?) ORDER BY position, id`,
		KindMovie, KindShow, KindArtist, LibraryMovies, LibraryShows, LibraryMusic, LibraryAnime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DLNAEntry
	for rows.Next() {
		e := DLNAEntry{Library: true}
		if err := rows.Scan(&e.ID, &e.Title, &e.Children); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const dlnaEntryColumns = `i.public_id, i.kind, i.title, ifnull(i.year, 0), ifnull(i.index_number, 0), i.child_count,
	ifnull(f.id, 0), ifnull(f.size, 0), ifnull(m.container, ifnull(f.container, '')), ifnull(m.duration_ms, ifnull(i.duration_ms, 0)),
	ifnull(m.video_codec, ''), ifnull(m.width, 0), ifnull(m.height, 0), ifnull(m.audio_channels, 0),
	ifnull(root.title, ''), ifnull(parent.title, '')`

// The first version of a title and its first part's local file, when it has one.
const dlnaEntryJoins = `LEFT JOIN media m ON m.id = (SELECT id FROM media WHERE item_id = i.id ORDER BY position, id LIMIT 1)
	LEFT JOIN media_parts mp ON mp.media_id = m.id AND mp.position = (SELECT min(position) FROM media_parts WHERE media_id = m.id)
	LEFT JOIN files f ON f.id = mp.file_id AND f.missing_since_at IS NULL
	LEFT JOIN items parent ON parent.id = i.parent_id LEFT JOIN items root ON root.id = i.root_id`

func scanDLNAEntry(row interface{ Scan(...any) error }) (DLNAEntry, error) {
	var e DLNAEntry
	err := row.Scan(&e.ID, &e.Kind, &e.Title, &e.Year, &e.Index, &e.Children, &e.FileID, &e.Size, &e.Container, &e.DurationMS,
		&e.VideoCodec, &e.Width, &e.Height, &e.Channels, &e.Artist, &e.Album)
	if e.Kind != KindTrack {
		e.Artist, e.Album = "", ""
	}
	return e, err
}

// DLNAChildren lists what is inside a library (its films, shows and artists) or a title (a
// show's seasons, a season's episodes, an artist's albums, an album's tracks), a page at a
// time, with how many there are. Extras and books are left out.
func (d *DB) DLNAChildren(ctx context.Context, parentID string, offset, limit int) ([]DLNAEntry, int, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var where string
	var args []any
	var library, item int64
	switch {
	case tx.QueryRow(`SELECT id FROM libraries WHERE public_id = ? AND kind IN (?, ?, ?, ?)`, parentID,
		LibraryMovies, LibraryShows, LibraryMusic, LibraryAnime).Scan(&library) == nil:
		where, args = `i.library_id = ? AND i.parent_id IS NULL AND i.kind IN (?, ?, ?) ORDER BY i.sort_title, i.id`,
			[]any{library, KindMovie, KindShow, KindArtist}
	case tx.QueryRow(`SELECT i.id FROM items i JOIN libraries l ON l.id = i.library_id WHERE i.public_id = ? AND l.kind IN (?, ?, ?, ?)`,
		parentID, LibraryMovies, LibraryShows, LibraryMusic, LibraryAnime).Scan(&item) == nil:
		where, args = `i.parent_id = ? AND i.kind IN (?, ?, ?, ?) ORDER BY i.parent_index, i.index_number, i.sort_title, i.id`,
			[]any{item, KindSeason, KindEpisode, KindAlbum, KindTrack}
	default:
		return nil, 0, ErrNotFound
	}
	var total int
	if err := tx.QueryRow(`SELECT count(*) FROM items i WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(`SELECT `+dlnaEntryColumns+` FROM items i `+dlnaEntryJoins+` WHERE `+where+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []DLNAEntry
	for rows.Next() {
		e, err := scanDLNAEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// DLNAEntryByID is one library or title of the DLNA tree, for a player that asks about it.
func (d *DB) DLNAEntryByID(ctx context.Context, id string) (DLNAEntry, error) {
	libs, err := d.DLNALibraries(ctx)
	if err != nil {
		return DLNAEntry{}, err
	}
	for _, l := range libs {
		if l.ID == id {
			return l, nil
		}
	}
	e, err := scanDLNAEntry(d.read.QueryRowContext(ctx, `SELECT `+dlnaEntryColumns+` FROM items i `+dlnaEntryJoins+`
		JOIN libraries l ON l.id = i.library_id WHERE i.public_id = ? AND l.kind IN (?, ?, ?, ?) AND i.kind != ?`,
		id, LibraryMovies, LibraryShows, LibraryMusic, LibraryAnime, KindExtra))
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	return e, err
}

// DLNAParent is the id of what a title sits in: its parent title, or its library.
func (d *DB) DLNAParent(ctx context.Context, id string) (string, error) {
	var parent string
	err := d.read.QueryRowContext(ctx, `SELECT ifnull(p.public_id, l.public_id) FROM items i JOIN libraries l ON l.id = i.library_id
		LEFT JOIN items p ON p.id = i.parent_id WHERE i.public_id = ?`, id).Scan(&parent)
	return parent, notFound(err)
}
