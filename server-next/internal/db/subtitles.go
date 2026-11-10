package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SubtitleSource is where a stored subtitle came from.
type SubtitleSource int

// SubtitleFromProvider is a subtitle someone searched for and downloaded. (Uploading one is
// not offered.)
const SubtitleFromProvider SubtitleSource = 1

// Subtitle is a downloaded subtitle of one version, offered to everyone who may play it. Its
// text is a file in the data folder named by FileHash.
type Subtitle struct {
	PublicID        string
	MediaID         int64
	Provider        string
	ProviderRef     string // the provider's own id for it
	FileHash        string
	Format          string // srt
	Language        string
	Title           string
	Forced          bool
	HearingImpaired bool
}

// SubtitleTarget is what a subtitle search is about: a film or an episode the viewer may play.
type SubtitleTarget struct {
	ItemID  int64
	Kind    ItemKind
	Title   string
	Year    int
	IDs     []ExternalID // the film's or episode's own ids at providers
	ShowIDs []ExternalID // an episode's show's
	Show    string       // an episode's show
	Season  int
	Episode int
	// MediaID is the version a downloaded subtitle is kept with: the one asked for, or the
	// title's first.
	MediaID int64
}

// SubtitleTarget returns what a subtitle search for a title the viewer may see is about, with
// the version a download is kept with. A title the viewer may not see, a version of another
// title, or a title that is not a film or an episode is ErrNotFound.
func (d *DB) SubtitleTarget(ctx context.Context, v Viewer, itemPublicID, versionPublicID string) (SubtitleTarget, error) {
	var t SubtitleTarget
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return t, err
	}
	var root sql.NullInt64
	var show sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind, i.title, ifnull(i.year, 0), ifnull(i.parent_index, 0), ifnull(i.index_number, 0),
		i.root_id, (SELECT title FROM items r WHERE r.id = i.root_id) FROM items i
		WHERE i.public_id = @id AND i.kind IN (1, 4) AND `+acc.visible("i"), append(v.args(), sql.Named("id", itemPublicID))...).
		Scan(&t.ItemID, &t.Kind, &t.Title, &t.Year, &t.Season, &t.Episode, &root, &show)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.Show = show.String
	ids := func(itemID int64) ([]ExternalID, error) {
		return queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`, []any{itemID},
			func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} })
	}
	if t.IDs, err = ids(t.ItemID); err != nil {
		return t, err
	}
	if root.Valid {
		if t.ShowIDs, err = ids(root.Int64); err != nil {
			return t, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM media WHERE item_id = ? AND (? = '' OR public_id = ?) ORDER BY position, id LIMIT 1`,
		t.ItemID, versionPublicID, versionPublicID).Scan(&t.MediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// AddSubtitle keeps a downloaded subtitle with its version. One provider's subtitle is kept
// once per version: downloading it again answers the one already there.
func (d *DB) AddSubtitle(ctx context.Context, s Subtitle) (Subtitle, error) {
	if s.Provider == "" || s.ProviderRef == "" || s.FileHash == "" || s.Format == "" {
		return s, invalid("a downloaded subtitle needs its provider, its id there, its file and its format")
	}
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		var library, item int64
		err := tx.QueryRow(`SELECT i.library_id, i.id FROM media m JOIN items i ON i.id = m.item_id WHERE m.id = ?`, s.MediaID).Scan(&library, &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(`SELECT public_id, file_hash, format, ifnull(language, ''), ifnull(title, ''), is_forced, is_hearing_impaired
			FROM subtitles WHERE media_id = ? AND provider = ? AND provider_ref = ?`, s.MediaID, s.Provider, s.ProviderRef).
			Scan(&s.PublicID, &s.FileHash, &s.Format, &s.Language, &s.Title, &s.Forced, &s.HearingImpaired)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().UnixMilli()
		s.PublicID = NewID("sub")
		if _, err := tx.Exec(`INSERT INTO subtitles (public_id, media_id, source, provider, provider_ref, file_hash, format, language, title,
			is_forced, is_hearing_impaired, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, nullif(?, ''), nullif(?, ''), ?, ?, ?)`,
			s.PublicID, s.MediaID, SubtitleFromProvider, s.Provider, s.ProviderRef, s.FileHash, s.Format, s.Language, s.Title,
			s.Forced, s.HearingImpaired, now); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: item, LibraryID: library})
	})
	return s, err
}

// RemoveSubtitle removes a downloaded subtitle; the owner and administrators may. It returns
// the subtitle's file, and whether another subtitle row still uses the same file.
func (d *DB) RemoveSubtitle(ctx context.Context, publicID string) (hash string, stillUsed bool, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		var library, item int64
		err := tx.QueryRow(`SELECT s.file_hash, i.library_id, i.id FROM subtitles s JOIN media m ON m.id = s.media_id
			JOIN items i ON i.id = m.item_id WHERE s.public_id = ?`, publicID).Scan(&hash, &library, &item)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM subtitles WHERE public_id = ?`, publicID); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM subtitles WHERE file_hash = ?)`, hash).Scan(&stillUsed); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: item, LibraryID: library})
	})
	return hash, stillUsed, err
}

// downloadedTracks are a version's downloaded subtitles as tracks the player can choose.
func downloadedTracks(ctx context.Context, tx *sql.Tx, mediaID int64) ([]Track, error) {
	return queryAll(ctx, tx, `SELECT public_id, file_hash, format, ifnull(language, ''), ifnull(title, ''), is_forced, is_hearing_impaired
		FROM subtitles WHERE media_id = ? ORDER BY language, id`, []any{mediaID},
		func(t *Track) []any {
			t.Kind = StreamSubtitle
			return []any{&t.Downloaded, &t.DownloadedHash, &t.Codec, &t.Language, &t.Title, &t.Forced, &t.HearingImpaired}
		})
}
