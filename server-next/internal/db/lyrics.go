package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"
)

// LyricsSource is where a song's stored lyrics came from. The numbers are the order in which
// one replaces another: lyrics are replaced only by lyrics from the same source or a later one.
type LyricsSource int

const (
	LyricsFromProvider LyricsSource = 1 // the library's lyrics provider, LRCLIB
	LyricsFromFile     LyricsSource = 2 // the song file's own tags
	LyricsPasted       LyricsSource = 3 // pasted by the owner: what everyone sees, and no fetch replaces it
)

var lyricsSourceNames = map[LyricsSource]string{LyricsFromProvider: "provider", LyricsFromFile: "file", LyricsPasted: "pasted"}

func (s LyricsSource) String() string { return lyricsSourceNames[s] }

// MaxLyrics is the longest lyrics kept, in bytes. No song's are longer.
const MaxLyrics = 256 << 10

// Lyrics are one song's stored lyrics. An empty Body from a provider records that it has
// none, so the song is not asked about again at once.
type Lyrics struct {
	Source    LyricsSource
	Body      string
	Timed     bool // LRC lines with times, so the words follow the song
	Language  string
	UpdatedAt time.Time
}

// SetLyrics is the one write of a song's lyrics. Pasted lyrics are replaced only by pasting
// again; lyrics from the file's tags only by the tags again or by pasting; a provider's only
// by anything. It reports whether the lyrics were stored. Background work passes Background.
func (d *DB) SetLyrics(ctx context.Context, p Priority, itemID int64, l Lyrics) (bool, error) {
	stored := false
	err := d.write(ctx, p, func(tx *sql.Tx) error {
		var library int64
		err := tx.QueryRow(`SELECT library_id FROM items WHERE id = ? AND kind = ?`, itemID, KindTrack).Scan(&library)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if stored, err = putLyrics(tx, itemID, l, time.Now().UnixMilli()); err != nil || !stored {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: itemID, LibraryID: library})
	})
	return stored, err
}

// putLyrics stores lyrics unless the song has lyrics from a source that outranks them.
func putLyrics(tx *sql.Tx, itemID int64, l Lyrics, now int64) (bool, error) {
	if lyricsSourceNames[l.Source] == "" {
		return false, invalid("lyrics source %d", l.Source)
	}
	if len(l.Body) > MaxLyrics || !utf8.ValidString(l.Body) {
		return false, invalid("lyrics are longer than %d bytes or are not text", MaxLyrics)
	}
	if l.Body == "" && l.Source != LyricsFromProvider {
		return false, invalid("no lyrics")
	}
	res, err := tx.Exec(`INSERT INTO lyrics (item_id, source, body, timed, language, updated_at) VALUES (?, ?, nullif(?, ''), ?, nullif(?, ''), ?)
		ON CONFLICT (item_id) DO UPDATE SET source = excluded.source, body = excluded.body, timed = excluded.timed,
		language = excluded.language, updated_at = excluded.updated_at WHERE excluded.source >= lyrics.source`,
		itemID, l.Source, l.Body, l.Timed && l.Body != "", l.Language, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SongLyrics returns a song's stored lyrics, or ErrNotFound when it has none stored. A
// provider's "none" is returned with an empty Body.
func (d *DB) SongLyrics(ctx context.Context, itemID int64) (Lyrics, error) {
	var l Lyrics
	var body, language sql.NullString
	var at int64
	err := d.read.QueryRowContext(ctx, `SELECT source, body, timed, language, updated_at FROM lyrics WHERE item_id = ?`, itemID).
		Scan(&l.Source, &body, &l.Timed, &language, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	l.Body, l.Language, l.UpdatedAt = body.String, language.String, time.UnixMilli(at)
	return l, err
}

// LyricsSong is what a lyrics provider is asked about one song.
type LyricsSong struct {
	ID         int64
	Title      string
	Artist     string
	Album      string
	DurationMS int64

	sortTitle string
}

// Song returns what a lyrics provider is asked about one song.
func (d *DB) Song(ctx context.Context, itemID int64) (LyricsSong, error) {
	songs, err := d.songs(ctx, songColumns+` WHERE t.id = ? AND t.kind = ?`, []any{itemID, KindTrack})
	if err != nil {
		return LyricsSong{}, err
	}
	if len(songs) == 0 {
		return LyricsSong{}, ErrNotFound
	}
	return songs[0], nil
}

// The song's artist is the first artist credited on the song, else the artist it is filed under.
const songColumns = `SELECT t.id, t.sort_title, t.title, ifnull((SELECT p.name FROM credits c JOIN people p ON p.id = c.person_id
	WHERE c.item_id = t.id AND c.role = 8 ORDER BY c.position LIMIT 1), ifnull(r.title, '')), ifnull(a.title, ''), ifnull(t.duration_ms, 0)
	FROM items t LEFT JOIN items a ON a.id = t.parent_id LEFT JOIN items r ON r.id = t.root_id`

func scanSong(s *LyricsSong) []any {
	return []any{&s.ID, &s.sortTitle, &s.Title, &s.Artist, &s.Album, &s.DurationMS}
}

// SongsWithoutLyrics lists a library's songs that have no lyrics: none stored, no lyrics file
// beside them, and no provider answer of "none" since notSince. A page at a time by title;
// after is the last song of the page before, or nil for the first page.
func (d *DB) SongsWithoutLyrics(ctx context.Context, libraryID int64, notSince time.Time, after *LyricsSong, limit int) ([]LyricsSong, error) {
	where, args := ` WHERE t.library_id = ? AND t.kind = ?`, []any{libraryID, KindTrack}
	if after != nil {
		where += ` AND (t.sort_title, t.id) > (?, ?)`
		args = append(args, after.sortTitle, after.ID)
	}
	where += ` AND NOT EXISTS (SELECT 1 FROM lyrics l WHERE l.item_id = t.id AND (l.body IS NOT NULL OR l.updated_at >= ?))
		AND NOT EXISTS (SELECT 1 FROM media m JOIN files f ON f.media_id = m.id WHERE m.item_id = t.id AND f.role = ? AND f.missing_since_at IS NULL)
		ORDER BY t.sort_title, t.id LIMIT ?`
	return d.songs(ctx, songColumns+where, append(args, notSince.UnixMilli(), FileLyrics, limit))
}

func (d *DB) songs(ctx context.Context, query string, args []any) ([]LyricsSong, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, query, args, scanSong)
}
