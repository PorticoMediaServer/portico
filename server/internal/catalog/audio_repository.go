package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"golang.org/x/text/unicode/norm"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"regexp"
	"strconv"
	"strings"
)

type Artist struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	Name      string `json:"name"`
}

// AlbumRole is the artist-relative release role published on an artist's albums.
// It is closed: the artist page's groups and the endpoint cannot disagree on a
// value outside this set.
type AlbumRole string

// JSONSchema publishes the closed role set to apigen without redeclaring Album.
func (AlbumRole) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"album", "ep", "single", "compilation", "appearance"}}
}

type Album struct {
	ProviderReleaseID      string `json:"providerReleaseId,omitempty"`
	ProviderReleaseGroupID string `json:"providerReleaseGroupId,omitempty"`
	ProviderReleaseDate    string `json:"providerReleaseDate,omitempty"`
	ProviderReleaseCountry string `json:"providerReleaseCountry,omitempty"`
	ProviderObservedAt     string `json:"providerObservedAt,omitempty"`
	ID                     string `json:"id"`
	LibraryID              string `json:"libraryId"`
	Title                  string `json:"title"`
	AlbumArtistID          string `json:"albumArtistId"`
	AlbumArtist            string `json:"albumArtist"`
	Year                   int    `json:"year,omitempty"`
	ProviderMatchStatus    string `json:"providerMatchStatus"`
	PosterURL              string `json:"posterUrl,omitempty"`
	// Role is the artist-relative release role for this artist page. Absent from
	// older servers.
	Role AlbumRole `json:"role,omitempty"`
	// HomeActions are the release's aggregate selection offers on an artist
	// page (get_artists_id_albums); absent elsewhere.
	HomeActions *HomeActions `json:"homeActions,omitempty"`
}
type Book struct {
	ID                  string      `json:"id"`
	LibraryID           string      `json:"libraryId"`
	Title               string      `json:"title"`
	Author              string      `json:"author,omitempty"`
	Narrator            string      `json:"narrator,omitempty"`
	ProviderMatchStatus string      `json:"providerMatchStatus"`
	PosterURL           string      `json:"posterUrl,omitempty"`
	Resume              *BookResume `json:"resume,omitempty"`
}
type BookResume struct {
	ItemID          string  `json:"itemId"`
	PositionSeconds float64 `json:"positionSeconds"`
}
type SongInfo struct {
	ProviderTitle          string `json:"providerTitle,omitempty"`
	ProviderArtist         string `json:"providerArtist,omitempty"`
	ProviderSourceURL      string `json:"providerSourceUrl,omitempty"`
	ProviderRecordingID    string `json:"providerRecordingId,omitempty"`
	ProviderReleaseID      string `json:"providerReleaseId,omitempty"`
	ProviderReleaseGroupID string `json:"providerReleaseGroupId,omitempty"`
	ProviderTrackID        string `json:"providerTrackId,omitempty"`
	ProviderReleaseStatus  string `json:"providerReleaseStatus"`
	ProviderObservedAt     string `json:"providerObservedAt,omitempty"`
	AlbumID                string `json:"albumId"`
	AlbumTitle             string `json:"albumTitle"`
	AlbumArtist            string `json:"albumArtist"`
	Artist                 string `json:"artist"`
	DiscNumber             *int   `json:"discNumber,omitempty"`
	TrackNumber            *int   `json:"trackNumber,omitempty"`
	ProviderMatchStatus    string `json:"providerMatchStatus"`
	LocalMetadataIssue     string `json:"localMetadataIssue,omitempty"`
}
type LocalAudioPolicy struct {
	Revision  int64  `json:"revision"`
	LocalMode string `json:"localMode"`
}
type BookFileInfo struct {
	LocalPolicy        *LocalAudioPolicy  `json:"localPolicy,omitempty"`
	LocalMetadata      *LocalBookMetadata `json:"localMetadata,omitempty"`
	BookID             string             `json:"bookId"`
	BookTitle          string             `json:"bookTitle"`
	DiscNumber         *int               `json:"discNumber,omitempty"`
	PartNumber         *int               `json:"partNumber,omitempty"`
	SourceBoundary     string             `json:"sourceBoundary"`
	LocalMetadataIssue string             `json:"localMetadataIssue,omitempty"`
}
type BookChapter struct {
	ID           string  `json:"id"`
	ItemID       string  `json:"itemId"`
	Index        int     `json:"index"`
	Title        string  `json:"title"`
	StartSeconds float64 `json:"startSeconds"`
	EndSeconds   float64 `json:"endSeconds"`
}

var leadingNumber = regexp.MustCompile(`^([0-9]{1,3})[ ._-]`)

// partWordNumber is a number that follows "part", "chapter" or "track" (or
// their short forms) as a word of its own; trailingNumber is one that ends a
// file's name.
var partWordNumber = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:part|pt|chapter|chap|ch|track)[ ._#-]*([0-9]+)`)
var trailingNumber = regexp.MustCompile(`[ ._-]([0-9]{1,3})$`)

// partNumberFromName reads an audiobook file's place in its book from its own
// name, for a file that carries no track tag and does not start with a number:
// the number after the last "part", "chapter" or "track" word ("Part 2 of 12",
// "Chapter 01"), else a number that ends the name ("Book Title - 03"). Without
// it such files play in the order they happened to be catalogued.
func partNumberFromName(path string) any {
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if matches := partWordNumber.FindAllStringSubmatch(name, -1); len(matches) > 0 {
		return tagNumber(matches[len(matches)-1][1])
	}
	if match := trailingNumber.FindStringSubmatch(name); match != nil {
		return tagNumber(match[1])
	}
	return nil
}

func tagNumber(s string) any {
	first, _, _ := strings.Cut(s, "/")
	n, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || n < 1 || n > 9999 {
		return nil
	}
	return n
}
func firstTag(tags map[string]string, names ...string) string {
	for _, name := range names {
		// One line: tabs and line breaks inside a tagged name become spaces (CD-27).
		if value := strings.Join(strings.Fields(tags[name]), " "); value != "" {
			return value
		}
	}
	return ""
}
func localKey(s string) string {
	return strings.ToLower(norm.NFC.String(strings.Join(strings.Fields(s), " ")))
}

// albumRetireChecks are the personal, owner and provider state that keep an
// emptied album: with any of it the album is retired (it keeps its identity and
// state and leaves browse); with none it is deleted (the identity ledger still
// gives the same public id if it comes back).
var albumRetireChecks = []string{
	`SELECT EXISTS(SELECT 1 FROM catalog_songs WHERE album_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM mb_jobs WHERE kind='album' AND entity_id=? AND manual=1)`,
	`SELECT EXISTS(SELECT 1 FROM mb_album_links WHERE album_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM mb_song_links WHERE item_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM container_personal_state WHERE container_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_fields WHERE kind='album' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_history WHERE kind='album' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions WHERE kind='album' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE kind='album' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_uploads WHERE kind='album' AND entity_id=?)`,
}

// bookRetireChecks is the same rule for an audiobook a file has left.
var bookRetireChecks = []string{
	`SELECT EXISTS(SELECT 1 FROM book_resume WHERE book_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM container_personal_state WHERE container_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM catalog_book_group_members WHERE book_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_fields WHERE kind='book' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_history WHERE kind='book' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions WHERE kind='book' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE kind='book' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_uploads WHERE kind='book' AND entity_id=?)`,
}

// settleEmptiedBook is retireEmptiedAlbumTx for a book: into is the book its
// last file went to, or 0 when the file went nowhere.
func settleEmptiedBook(ctx context.Context, tx *sql.Tx, book, into int64) error {
	var files bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_files WHERE book_id=?)`, book).Scan(&files); err != nil || files {
		return err
	}
	return settleEmptiedInto(ctx, tx, book, into, bookRetireChecks)
}

// artistRetireChecks is the same rule for the album's artist.
var artistRetireChecks = []string{
	`SELECT EXISTS(SELECT 1 FROM catalog_albums WHERE artist_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM catalog_song_artists WHERE artist_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM container_personal_state WHERE container_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_fields WHERE kind='artist' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_owner_history WHERE kind='artist' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions WHERE kind='artist' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_selections WHERE kind='artist' AND entity_id=?)`,
	`SELECT EXISTS(SELECT 1 FROM artwork_uploads WHERE kind='artist' AND entity_id=?)`,
}

func anyPresent(ctx context.Context, tx *sql.Tx, id int64, checks []string) (bool, error) {
	for _, check := range checks {
		var present bool
		if err := tx.QueryRowContext(ctx, check, id).Scan(&present); err != nil {
			return false, err
		}
		if present {
			return true, nil
		}
	}
	return false, nil
}

// retireEmptiedAlbumTx settles the album a song just left: while songs remain
// nothing happens; an empty album with state is retired, without state it is
// deleted; its artist then gets the same treatment. Two albums are never
// merged by content: only the id passed in can go. into is the album the last
// song went to (0 when it went nowhere): a deleted album's id leads there.
func retireEmptiedAlbumTx(ctx context.Context, tx *sql.Tx, album, into int64) error {
	var artist int64
	if err := tx.QueryRowContext(ctx, `SELECT artist_id FROM catalog_albums WHERE entity_id=?`, album).Scan(&artist); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var songs bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_songs WHERE album_id=?)`, album).Scan(&songs); err != nil || songs {
		return err
	}
	if err := settleEmptiedInto(ctx, tx, album, into, albumRetireChecks); err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_albums a JOIN catalog_entities e ON e.id=a.entity_id AND e.retired=0 WHERE a.artist_id=?1)
 OR EXISTS(SELECT 1 FROM catalog_song_artists WHERE artist_id=?1)`, artist).Scan(&used); err != nil || used {
		return err
	}
	return settleEmptied(ctx, tx, artist, artistRetireChecks)
}

func settleEmptied(ctx context.Context, tx *sql.Tx, id int64, checks []string) error {
	return settleEmptiedInto(ctx, tx, id, 0, checks)
}

// settleEmptiedInto is settleEmptied for a container whose last member moved
// to another container of its kind (into, when not 0). Deleted, its public id
// leads to that container from then on, as a merged-away title's does: an
// album or book first filed from its folder while a scan is still reading tags
// moves to its real artist or author with another id a moment later, and a
// page, link or request that already holds the first id reaches it there
// instead of a 404. A retired container keeps its own id (and its state).
func settleEmptiedInto(ctx context.Context, tx *sql.Tx, id, into int64, checks []string) error {
	if held, err := entityMutationHeldTx(ctx, tx, id); err != nil || held {
		return err
	}
	blocked, err := anyPresent(ctx, tx, id, checks)
	if err != nil {
		return err
	}
	if blocked {
		return compactcatalog.RetireEntityTx(ctx, tx, id)
	}
	if into != 0 && into != id {
		for _, q := range []string{
			// Ids it had absorbed lead on, and then its own.
			`UPDATE catalog_merged_ids SET entity_id=?2 WHERE entity_id=?1`,
			`INSERT OR REPLACE INTO catalog_merged_ids(public_id,entity_id) SELECT public_id,?2 FROM catalog_entities WHERE id=?1`,
		} {
			if _, err = tx.ExecContext(ctx, q, id, into); err != nil {
				return err
			}
		}
	}
	return compactcatalog.DeleteEntityTx(ctx, tx, id)
}

// audioTagKey is a track's identity from its own tags, or "" when the tags
// were not read or do not say which track of which release this is.
func audioTagKey(f assets.Facts, kind string, disc, track any) string {
	if f.InventoryOnly || firstTag(f.Tags, "title") == "" || firstTag(f.Tags, "album") == "" {
		return ""
	}
	d, _ := disc.(int)
	t, _ := track.(int)
	if d <= 0 {
		d = 1
	}
	artist := firstTag(f.Tags, "album_artist", "albumartist", "artist", "author")
	album, title := firstTag(f.Tags, "album"), firstTag(f.Tags, "title")
	if compactcatalog.TrackKey(artist, album, d, t, title) == "" {
		return ""
	}
	year := 0
	if value := firstTag(f.Tags, "date", "year"); len(value) >= 4 {
		year, _ = strconv.Atoi(value[:4])
	}
	release := albumIdentityKey(f.Tags, artist, album, year)
	if kind == "audiobook" {
		book, _ := json.Marshal([]string{localKey(artist), localKey(album), localKey(firstTag(f.Tags, "narrator", "composer")), localKey(f.Tags["edition"]), f.Tags["isbn"], f.Tags["asin"]})
		release = string(book)
	}
	key, _ := json.Marshal([]any{release, d, t, compactcatalog.IdentityText(title)})
	return kind + ":" + string(key)
}

// artist finds or creates a library's artist by name.
func (s *Service) artist(ctx context.Context, tx *sql.Tx, library int64, name string) (int64, error) {
	name = norm.NFC.String(name)
	key := localKey(name)
	id, created, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey(key), Title: name})
	if err != nil || !created {
		return id, err
	}
	return id, compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"local_key": key})
}

func (s *Service) commitAudio(ctx context.Context, tx *sql.Tx, library, kind, root, asset, path string, f assets.Facts) (err error) {
	_, outer := ctx.Value(audioMergeKey{}).(*audioMergeAdmissions)
	ctx, admissions := withAudioMergeAdmissions(ctx)
	defer func() {
		if err == nil && !outer {
			err = finishAudioMergeAdmissionsTx(ctx, tx, admissions)
		}
	}()

	if kind == "music" && len(f.MusicCueTracks) > 0 {
		c := f.MusicCueTracks[0]
		if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) SELECT ?,1,e.id,?,? FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE a.token=? AND cl.library_id=? AND e.kind=7 AND NOT EXISTS(SELECT 1 FROM music_virtual_tracks v WHERE v.asset_id=a.token) ORDER BY e.id LIMIT 1`, asset, c.Start, c.End, asset, library); e != nil {
			return e
		}
		for n := range f.MusicCueTracks {
			if e := s.commitAudioTrack(ctx, tx, library, kind, root, asset, path, f, n+1); e != nil {
				return e
			}
		}
		// Removed cue entries lose only this source link; retained personal/owner
		// state and entity identity remain available if the cue returns later.
		if _, e := tx.ExecContext(ctx, `DELETE FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?) AND entity_id IN(SELECT entity_id FROM music_virtual_tracks WHERE asset_id=? AND ordinal>?)`, asset, asset, len(f.MusicCueTracks)); e != nil {
			return e
		}
		if e := retireRemovedMusicCueTx(ctx, tx, asset, len(f.MusicCueTracks)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM music_virtual_tracks WHERE asset_id=? AND ordinal>?`, asset, len(f.MusicCueTracks))
		return e
	}
	if kind == "music" {
		if _, e := tx.ExecContext(ctx, `DELETE FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?) AND entity_id IN(SELECT entity_id FROM music_virtual_tracks WHERE asset_id=? AND ordinal>1)`, asset, asset); e != nil {
			return e
		}
		if e := retireRemovedMusicCueTx(ctx, tx, asset, 1); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET start_seconds=0,end_seconds=NULL WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?) AND entity_id IN(SELECT entity_id FROM music_virtual_tracks WHERE asset_id=? AND ordinal=1)`, asset, asset); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM music_virtual_tracks WHERE asset_id=?`, asset); e != nil {
			return e
		}
	}
	return s.commitAudioTrack(ctx, tx, library, kind, root, asset, path, f, 0)
}
func (s *Service) commitAudioTrack(ctx context.Context, tx *sql.Tx, library, kind, root, asset, path string, f assets.Facts, cueOrdinal int) error {
	if f.AudioCodec == "" && !f.InventoryOnly {
		return errors.New("audio library source has no audio stream")
	}
	if err := persistAudioEvidence(tx, library, asset, f); err != nil {
		return err
	}
	handle, err := compactcatalog.LibraryTx(ctx, tx, library)
	if err != nil {
		return err
	}
	assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, asset)
	if err != nil {
		return err
	}
	var existingItem int64
	if err := tx.QueryRow(`SELECT l.entity_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities i ON i.id=l.entity_id WHERE l.asset_id=? AND i.library_id=? LIMIT 1`, assetID, handle).Scan(&existingItem); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = compactcatalog.RequireEntityAcquirableTx(ctx, tx, existingItem); err != nil {
		return err
	}
	projected, errProjection := audioProjection(tx, library, existingItem, f)
	if errProjection != nil {
		return errProjection
	}
	f = projected
	if cueOrdinal > 0 {
		c := f.MusicCueTracks[cueOrdinal-1]
		tags := map[string]string{}
		for k, v := range f.Tags {
			tags[k] = v
		}
		tags["title"] = c.Title
		if tags["title"] == "" {
			tags["title"] = "Track " + strconv.Itoa(cueOrdinal)
		}
		tags["track"] = strconv.Itoa(cueOrdinal)
		f.Tags = tags
	}
	title := firstTag(f.Tags, "title")
	if title == "" {
		title, _ = FilenameTitle(path)
	}
	if len(title) > 500 {
		title = title[:500]
	}
	folder := filepath.Dir(path)
	disc := tagNumber(firstTag(f.Tags, "disc"))
	discFolder := naming.DiscFolder
	if kind == "audiobook" {
		// "Series/Vol. 2 - Title" is a book, not the series' second disc.
		discFolder = naming.AudiobookDiscFolder
	}
	if number, ok := discFolder(filepath.Base(folder)); ok {
		if disc == nil {
			disc = number
		}
		folder = filepath.Dir(folder)
	}
	albumTitle := firstTag(f.Tags, "album")
	if albumTitle == "" {
		albumTitle = filepath.Base(folder)
		if kind == "audiobook" && filepath.Clean(folder) == filepath.Clean(root) {
			albumTitle = title
		} else if kind == "audiobook" {
			// The title folder without its place in a series, year, narrator or ASIN.
			if rel, e := filepath.Rel(root, path); e == nil && !strings.HasPrefix(rel, "..") {
				if read := naming.ReadAudiobookFolder(rel); read.Title != "" {
					albumTitle = read.Title
				}
			}
		}
		if f.LocalMetadataIssue == "" {
			f.LocalMetadataIssue = "album_or_book_identity_from_local_path"
		}
	}
	albumTitle = norm.NFC.String(albumTitle)
	track := tagNumber(firstTag(f.Tags, "track"))
	if track == nil {
		if match := leadingNumber.FindStringSubmatch(filepath.Base(path)); match != nil {
			track = tagNumber(match[1])
		}
	}
	if track == nil && kind == "audiobook" {
		track = partNumberFromName(path)
	}
	itemKind := compactcatalog.Track
	if kind == "audiobook" {
		itemKind = compactcatalog.Part
	}
	var item int64
	err = tx.QueryRow(`SELECT l.entity_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_entities i ON i.id=l.entity_id WHERE l.asset_id=? AND i.library_id=? AND i.kind=? AND (?=0 OR EXISTS(SELECT 1 FROM music_virtual_tracks v WHERE v.entity_id=i.id AND v.asset_id=? AND v.ordinal=?)) LIMIT 1`, assetID, handle, itemKind, cueOrdinal, asset, cueOrdinal).Scan(&item)
	// The attach point (attach.go). Before tags are read the file is known by
	// its place; once they are, by what it is, so a second file with the same
	// tags is another version of the same track rather than a second track.
	tagKey := audioTagKey(f, kind, disc, track)
	if cueOrdinal > 0 {
		tagKey += ":cue:" + strconv.Itoa(cueOrdinal)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if item, err = AttachTrackTx(ctx, tx, TrackFile{CueOrdinal: cueOrdinal, Library: handle, Kind: itemKind, Asset: assetID, Path: path, Title: title, TagKey: tagKey}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if item, err = identifyAudioFileTx(ctx, tx, item, tagKey, kind, TrackFile{CueOrdinal: cueOrdinal, Library: handle, Kind: itemKind, Asset: assetID, Path: path, Title: title, TagKey: tagKey}, f.Tags); err != nil {
		return err
	}
	if cueOrdinal > 0 {
		c := f.MusicCueTracks[cueOrdinal-1]
		if c.End <= c.Start {
			return errors.New("invalid virtual music range")
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) VALUES(?,?,?,?,?) ON CONFLICT(asset_id,ordinal) DO UPDATE SET entity_id=excluded.entity_id,start_seconds=excluded.start_seconds,end_seconds=excluded.end_seconds`, asset, cueOrdinal, item, c.Start, c.End); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET start_seconds=?,end_seconds=? WHERE entity_id=? AND asset_id=?`, c.Start, c.End, item, assetID); e != nil {
			return e
		}
	}
	// An observed physical asset keeps its existing logical parent when local
	// tags still describe that parent. A folder rename is not a regrouping hint.
	if firstTag(f.Tags, "album") == "" {
		var prior string
		query := `SELECT a.title FROM catalog_songs x JOIN catalog_entities a ON a.id=x.album_id WHERE x.entity_id=?`
		if kind == "audiobook" {
			query = `SELECT b.title FROM catalog_book_files x JOIN catalog_entities b ON b.id=x.book_id WHERE x.entity_id=?`
		}
		if e := tx.QueryRow(query, item).Scan(&prior); e == nil {
			albumTitle = prior
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	fields := map[string]any{"title": title}
	if value := firstTag(f.Tags, "title_sort"); value != "" {
		fields["sort_title"] = value
	}
	if kind == "audiobook" && firstTag(f.Tags, "title_sort") == "" {
		fields["sort_title"] = naturalAudioName(path)
	}
	if f.ArtworkKey != "" {
		fields["poster_url"] = "local:" + f.ArtworkKey
	}
	if err = compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, fields); err != nil {
		return err
	}
	// Track the primary cover alongside sidecar carriers so replacement or
	// removal releases only automatic artwork, preserving owner selections.
	if f.ArtworkKey != "" {
		if err = commitSidecars(ctx, tx, library, []sidecarCarrier{{"item", item, "poster", f.ArtworkKey}}); err != nil {
			return err
		}
	}
	if err = compactcatalog.ProjectAudioLocalGenresItem(ctx, tx, library, item); err != nil {
		return err
	}

	if kind == "music" {
		performer := firstTag(f.Tags, "artist")
		if performer == "" {
			performer = "Unknown artist"
		}
		albumArtist := firstTag(f.Tags, "album_artist", "albumartist")
		if albumArtist == "" {
			albumArtist = performer
			if f.Tags["compilation"] == "1" || f.Tags["compilation"] == "true" {
				albumArtist = "Various Artists"
			}
		}
		if f.Tags["compilation"] == "1" || strings.EqualFold(f.Tags["compilation"], "true") {
			albumArtist = "Various Artists"
		}
		albumArtistID, err := s.artist(ctx, tx, handle, albumArtist)
		if err != nil {
			return err
		}
		if sortName := firstTag(f.Tags, "album_artist_sort"); sortName != "" {
			if err = compactcatalog.SetFieldsTx(ctx, tx, albumArtistID, compactcatalog.Automatic, map[string]any{"sort_title": sortName}); err != nil {
				return err
			}
		}
		year := 0
		value := firstTag(f.Tags, "date", "year")
		if len(value) >= 4 {
			year, _ = strconv.Atoi(value[:4])
		}
		var album int64
		groupKey := albumIdentityKey(f.Tags, albumArtist, albumTitle, year)
		// A track stays on an album the owner pinned to a release or separated
		// from another.
		if e := tx.QueryRow(`SELECT x.album_id FROM catalog_songs x WHERE x.entity_id=? AND (EXISTS(SELECT 1 FROM mb_jobs j WHERE j.kind='album' AND j.entity_id=x.album_id AND j.manual=1) OR EXISTS(SELECT 1 FROM catalog_kept_apart k WHERE k.entity_id=x.album_id))`, item).Scan(&album); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		// An album the owner merged into another is still known by its name:
		// the ledger sends its songs to the album that stayed, and the merge's
		// choice holds over the tags (no edition split, no key rewrite).
		merged := false
		if album == 0 {
			var key string
			if e := tx.QueryRow(`SELECT a.id,d.local_key FROM catalog_identities i JOIN catalog_entities a ON a.public_id=i.public_id JOIN catalog_albums d ON d.entity_id=a.id
 WHERE i.root=(SELECT root FROM catalog_libraries WHERE id=?1) AND i.source_key=?2 AND a.library_id=?1`, handle, compactcatalog.AlbumKey(groupKey)).Scan(&album, &key); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			merged = album != 0 && key != groupKey
		}
		if album == 0 {
			if e := tx.QueryRow(`SELECT a.id FROM catalog_entities a JOIN catalog_albums d ON d.entity_id=a.id WHERE a.library_id=? AND d.local_key=?`, handle, groupKey).Scan(&album); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		if album == 0 && (firstTag(f.Tags, "album") == "" || firstTag(f.Tags, "album_artist", "artist") == "" || strings.HasPrefix(f.LocalMetadataIssue, "invalid_")) {
			if e := tx.QueryRow(`SELECT album_id FROM catalog_songs WHERE entity_id=?`, item).Scan(&album); e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		if album == 0 {
			e := tx.QueryRow(`SELECT x.album_id FROM catalog_songs x JOIN catalog_entities a ON a.id=x.album_id JOIN catalog_albums d ON d.entity_id=a.id
 WHERE x.entity_id=? AND a.title=? AND d.artist_id=? AND a.year=? AND (COALESCE((SELECT release_id FROM mb_album_links WHERE album_id=a.id),'')='' OR ?='' OR (SELECT release_id FROM mb_album_links WHERE album_id=a.id)=?)`, item, albumTitle, albumArtistID, year, f.Tags["musicbrainz_albumid"], f.Tags["musicbrainz_albumid"]).Scan(&album)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		if album != 0 && !merged {
			var pinned bool
			if err = tx.QueryRow(`SELECT manual FROM mb_jobs WHERE kind='album' AND entity_id=?`, album).Scan(&pinned); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if !pinned {
				compatible, e := localEditionCompatible(tx, album, audioEdition(f.Tags))
				if e != nil {
					return e
				}
				if !compatible && !strings.HasPrefix(groupKey, "musicbrainz:") {
					album = 0
				}
			}
		}
		if album == 0 {
			// Missing per-track edition tags do not split one physical album.
			// Require exactly one compatible release in this library. A release ID
			// upgrades the grouping key when only some files carry that ID.
			rows, e := tx.Query(`SELECT a.id,d.edition_key FROM catalog_albums d INDEXED BY catalog_albums_artist JOIN catalog_entities a ON a.id=d.entity_id
 WHERE d.artist_id=? AND a.library_id=? AND a.title=? AND a.year=?`, albumArtistID, handle, albumTitle, year)
			if e != nil {
				return e
			}
			var candidate int64
			count := 0
			for rows.Next() {
				var id int64
				var old string
				if e = rows.Scan(&id, &old); e != nil {
					rows.Close()
					return e
				}
				if _, ok := mergeLocalEdition(old, audioEdition(f.Tags)); ok {
					candidate = id
					count++
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if count == 1 {
				album = candidate
			}
		}
		if album == 0 {
			key := groupKey
			var created bool
			if album, created, err = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Album, Parent: albumArtistID, Key: compactcatalog.AlbumKey(key), Title: albumTitle, Year: year}); err != nil {
				return err
			}
			if created {
				if err = compactcatalog.SetFactsTx(ctx, tx, album, map[string]any{"artist_id": albumArtistID, "local_key": key, "edition_key": audioEdition(f.Tags)}); err != nil {
					return err
				}
			}
		}
		var pinned bool
		if err = tx.QueryRow(`SELECT COALESCE((SELECT manual FROM mb_jobs WHERE kind='album' AND entity_id=?),0)`, album).Scan(&pinned); err != nil {
			return err
		}
		if !pinned && !merged {
			var oldKey, oldEdition string
			if err = tx.QueryRow(`SELECT local_key,edition_key FROM catalog_albums WHERE entity_id=?`, album).Scan(&oldKey, &oldEdition); err != nil {
				return err
			}
			if merged, compatible := mergeLocalEdition(oldEdition, audioEdition(f.Tags)); compatible {
				chosen := oldKey
				if strings.HasPrefix(groupKey, "musicbrainz:") || !strings.HasPrefix(oldKey, "musicbrainz:") && audioEdition(f.Tags) != emptyAudioEdition {
					chosen = groupKey
				}
				if err = recordAlbumGroupTx(ctx, tx, album, chosen); err != nil {
					return err
				}
				if err = compactcatalog.SetFactsTx(ctx, tx, album, map[string]any{"local_key": chosen, "edition_key": merged}); err != nil {
					return err
				}
			}
		}
		if sortName := firstTag(f.Tags, "album_sort"); sortName != "" {
			if err = compactcatalog.SetFieldsTx(ctx, tx, album, compactcatalog.Automatic, map[string]any{"sort_title": sortName}); err != nil {
				return err
			}
		}
		var previous int64
		if e := tx.QueryRow(`SELECT album_id FROM catalog_songs WHERE entity_id=?`, item).Scan(&previous); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, album, item, album); err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"album_id": album, "disc_number": disc, "track_number": track}); err != nil {
			return err
		}
		names := audioNames(f.Tags, "artists", "artist")
		if len(names) == 0 {
			names = []string{performer}
		}
		// An album the owner moved to an artist of its own ("Not this artist")
		// keeps its songs there: a song that names the artist the album left
		// credits the one it went to.
		var filedUnder int64
		if err = tx.QueryRow(`SELECT artist_id FROM catalog_albums WHERE entity_id=?`, album).Scan(&filedUnder); err != nil {
			return err
		}
		credited := make([]int64, 0, len(names))
		for _, name := range names {
			artistID, e := s.artist(ctx, tx, handle, name)
			if e != nil {
				return e
			}
			if artistID == albumArtistID && filedUnder != 0 && filedUnder != albumArtistID {
				artistID = filedUnder
			}
			if len(names) == 1 && (artistID != albumArtistID || firstTag(f.Tags, "album_artist_sort") == "") {
				if name := firstTag(f.Tags, "artist_sort"); name != "" {
					if e = compactcatalog.SetFieldsTx(ctx, tx, artistID, compactcatalog.Automatic, map[string]any{"sort_title": name}); e != nil {
						return e
					}
				}
			}
			credited = append(credited, artistID)
		}
		if err = compactcatalog.SetSongArtistsTx(ctx, tx, item, credited); err != nil {
			return err
		}
		if err = musicAlbumBackdropTx(ctx, tx, album, f.ArtworkExtra); err != nil {
			return err
		}
		if key := f.ArtworkExtra["album/backdrop"]; key != "" {
			if err = commitSidecars(ctx, tx, library, []sidecarCarrier{{"album", album, "backdrop", key}}); err != nil {
				return err
			}
		}
		// Artist sidecars resolve to the album artist; a compilation's folder
		// art belongs to the release, never to "Various Artists".
		if carriers := artistSidecarCarriers(albumArtistID, albumArtist, f.ArtworkExtra); len(carriers) > 0 {
			if everr := commitSidecars(ctx, tx, library, carriers); everr != nil {
				return everr
			}
		}
		// The song (and its artist credits above) has left `previous` by now,
		// so an emptied album's artist is retireable in the same transaction.
		if previous != 0 && previous != album {
			if err = retireEmptiedAlbumTx(ctx, tx, previous, album); err != nil {
				return err
			}
		}
		return nil
	}
	author := firstTag(f.Tags, "author", "artist")
	narrator := firstTag(f.Tags, "narrator", "composer")
	var previousBook int64
	if e := tx.QueryRow(`SELECT book_id FROM catalog_book_files WHERE entity_id=?`, item).Scan(&previousBook); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var book int64
	if firstTag(f.Tags, "album") == "" || author == "" || strings.HasPrefix(f.LocalMetadataIssue, "invalid_") {
		if e := tx.QueryRow(`SELECT book_id FROM catalog_book_files WHERE entity_id=?`, item).Scan(&book); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	if book == 0 {
		e := tx.QueryRow(`SELECT x.book_id FROM catalog_book_files x JOIN catalog_entities b ON b.id=x.book_id JOIN catalog_books d ON d.entity_id=b.id WHERE x.entity_id=? AND b.title=? AND d.author=? AND d.narrator=?`, item, albumTitle, author, narrator).Scan(&book)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	if book != 0 && firstTag(f.Tags, "isbn", "asin", "edition") != "" {
		var raw string
		if e := tx.QueryRow(`SELECT local_metadata_payload FROM catalog_books WHERE entity_id=?`, book).Scan(&raw); e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if raw != "" {
			var prior LocalBookMetadata
			if json.Unmarshal([]byte(raw), &prior) != nil {
				return errors.New("invalid local book metadata")
			}
			if f.Tags["edition"] != "" && prior.Edition != "" && f.Tags["edition"] != prior.Edition {
				book = 0
			}
			for _, i := range prior.Identifiers {
				if v := f.Tags[i.Scheme]; v != "" && v != i.Value {
					book = 0
				}
			}
		}
	}
	if book == 0 {
		key := localKey(folder + "|" + albumTitle + "|" + author + "|" + narrator + "|" + f.Tags["isbn"] + "|" + f.Tags["asin"] + "|" + f.Tags["edition"])
		var created bool
		if book, created, err = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Book, Key: compactcatalog.BookKey(key), Title: albumTitle}); err != nil {
			return err
		}
		if created {
			if err = compactcatalog.SetFactsTx(ctx, tx, book, map[string]any{"library_id": handle, "local_key": key, "author": author, "narrator": narrator}); err != nil {
				return err
			}
		}
	}
	if author != "" {
		// Authors are browse entities of their own, alive while a live book names them.
		if _, _, err = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Author, Key: compactcatalog.AuthorKey(author), Title: author}); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=? AND parent_id IS NOT ?`, book, item, book); err != nil {
		return err
	}
	if err = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"book_id": book, "disc_number": disc, "part_number": track}); err != nil {
		return err
	}
	if err = saveLocalBookMetadata(tx, book, f.Tags, f.TagSources); err != nil {
		return err
	}
	if err = joinBookByIdentifierTx(ctx, tx, book, f.Tags); err != nil {
		return err
	}
	// A file whose book identity changed (its tags arrived after a first scan
	// without them, or were edited) has left `previousBook`; an emptied book goes.
	if previousBook != 0 && previousBook != book {
		if err = settleEmptiedBook(ctx, tx, previousBook, book); err != nil {
			return err
		}
	}
	chapters := make([]compactcatalog.Chapter, 0, len(f.Chapters))
	for index, c := range f.Chapters {
		chapterTitle := c.Title
		if chapterTitle == "" {
			chapterTitle = "Chapter " + strconv.Itoa(index+1)
		}
		chapters = append(chapters, compactcatalog.Chapter{Title: chapterTitle, Start: c.Start, End: c.End})
	}
	return compactcatalog.SetBookChaptersTx(ctx, tx, item, chapters)
}
func intPointer(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	value := int(n.Int64)
	return &value
}
func (s *Service) songInfo(item string) (*SongInfo, error) {
	var info SongInfo
	var disc, track sql.NullInt64
	err := s.read().QueryRow(`SELECT pid(album.public_id),album.title,artist.title,
 COALESCE((SELECT group_concat(title,'; ') FROM (SELECT performer.title FROM catalog_song_artists sa JOIN catalog_entities performer ON performer.id=sa.artist_id WHERE sa.song_id=s.entity_id ORDER BY performer.title)),''),
 s.disc_number,s.track_number,COALESCE((SELECT status FROM mb_jobs WHERE kind='song' AND entity_id=item.id),'unmatched'),
 COALESCE((SELECT issue.issue FROM catalog_asset_links link JOIN catalog_asset_issues issue ON issue.asset_id=link.asset_id AND issue.library_id=item.library_id WHERE link.entity_id=item.id LIMIT 1),''),
 s.recording_id,s.release_id,s.release_group_id,s.track_id,s.release_status,s.provider_observed_at,s.provider_title,s.provider_artist
 FROM catalog_entities item JOIN catalog_songs s ON s.entity_id=item.id
 JOIN catalog_entities album ON album.id=s.album_id JOIN catalog_albums a ON a.entity_id=album.id
 JOIN catalog_entities artist ON artist.id=a.artist_id
 WHERE item.public_id=pid_blob(?) AND item.kind=7 AND album.retired=0
 AND EXISTS(SELECT 1 FROM catalog_song_artists sa WHERE sa.song_id=item.id)`, item).Scan(
		&info.AlbumID, &info.AlbumTitle, &info.AlbumArtist, &info.Artist, &disc, &track,
		&info.ProviderMatchStatus, &info.LocalMetadataIssue, &info.ProviderRecordingID,
		&info.ProviderReleaseID, &info.ProviderReleaseGroupID, &info.ProviderTrackID,
		&info.ProviderReleaseStatus, &info.ProviderObservedAt, &info.ProviderTitle,
		&info.ProviderArtist)
	if info.ProviderRecordingID != "" {
		info.ProviderSourceURL = "https://musicbrainz.org/recording/" + info.ProviderRecordingID
	}
	info.DiscNumber = intPointer(disc)
	info.TrackNumber = intPointer(track)
	return &info, err
}
func (s *Service) bookFileInfo(item string) (*BookFileInfo, error) {
	var info BookFileInfo
	var disc, part sql.NullInt64
	var payload, library string
	err := s.read().QueryRow(`SELECT pid(b.public_id),b.title,f.disc_number,f.part_number,
 COALESCE((SELECT issue.issue FROM catalog_asset_links link JOIN catalog_asset_issues issue ON issue.asset_id=link.asset_id AND issue.library_id=item.library_id WHERE link.entity_id=item.id LIMIT 1),''),
 l.library_id,d.local_metadata_payload
 FROM catalog_entities item JOIN catalog_book_files f ON f.entity_id=item.id
 JOIN catalog_entities b ON b.id=f.book_id JOIN catalog_books d ON d.entity_id=b.id
 JOIN catalog_libraries l ON l.id=b.library_id
 WHERE item.public_id=pid_blob(?) AND item.kind=9 AND b.retired=0`, item).Scan(&info.BookID, &info.BookTitle, &disc, &part, &info.LocalMetadataIssue, &library, &payload)
	info.DiscNumber = intPointer(disc)
	info.PartNumber = intPointer(part)
	info.SourceBoundary = "whole_source"
	if err == nil {
		var policy LocalAudioPolicy
		e := s.read().QueryRow(`SELECT revision,local_mode FROM audio_metadata_policies WHERE library_id=?`, library).Scan(&policy.Revision, &policy.LocalMode)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return &info, e
		}
		if e == nil {
			info.LocalPolicy = &policy
		}
		if e == nil && policy.LocalMode != "off" && payload != "" {
			var local LocalBookMetadata
			if e = json.Unmarshal([]byte(payload), &local); e != nil {
				return &info, e
			}
			info.LocalMetadata = &local
		}
	}
	return &info, err
}

// normalizeAudioGenres splits the bounded delimiters a tagger may use for one
// genre field, trims, and keeps the first sixteen names under sixty-four
// characters, deduplicated by normalized text. The normalized text is the
// facet identity, so "Science Fiction" and "science  fiction" are one facet.
func normalizeAudioGenres(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	var parts []string
	if json.Unmarshal([]byte(raw), &parts) != nil {
		parts = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '/' })
	}
	if len(parts) > 128 {
		parts = parts[:128]
	}
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || len(name) > 64 {
			continue
		}
		id := localKey(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, name)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// joinBookByIdentifierTx records the ISBN and ASIN a book's tags carry as its
// provider identity, and joins any other book of the library already known by
// one of them into it: two copies of one audiobook (two folders, two formats)
// are one book, as films and shows with one provider identity are one title.
// Books that cannot be one (a different identifier already accepted, or an
// owner separated them) stay apart.
func joinBookByIdentifierTx(ctx context.Context, tx *sql.Tx, book int64, tags map[string]string) error {
	for _, scheme := range []string{"isbn", "asin"} {
		value := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(tags[scheme])))
		if value == "" || len(value) > 64 {
			continue
		}
		p := compactcatalog.ProviderIdentity{Provider: strings.ToLower(scheme), Kind: "book", ID: value}
		// Every file of a book carries its identifiers; only the first to
		// bring one does the join.
		var known bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_external_ids WHERE entity_id=? AND provider=? AND provider_kind='book' AND provider_id=?)`, book, p.Provider, p.ID).Scan(&known); err != nil {
			return err
		}
		if known {
			continue
		}
		if _, err := tx.ExecContext(ctx, `SAVEPOINT book_join`); err != nil {
			return err
		}
		err := compactcatalog.RecordProviderIdentityTx(ctx, tx, book, p)
		if err == nil {
			_, err = AdoptProviderIdentityTx(ctx, tx, book, p)
		}
		if err != nil {
			if _, e := tx.ExecContext(ctx, `ROLLBACK TO book_join`); e != nil {
				return e
			}
			if !errors.Is(err, compactcatalog.ErrIdentityConflict) && !errors.Is(err, ErrNotMergeable) {
				_, _ = tx.ExecContext(ctx, `RELEASE book_join`)
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `RELEASE book_join`); err != nil {
			return err
		}
	}
	return nil
}

// Retain the removed cue identity and personal state, but hide an obsolete
// virtual track from browse/search when no other source still supplies it.
func retireRemovedMusicCueTx(ctx context.Context, tx *sql.Tx, asset string, keep int) error {
	rows, e := tx.QueryContext(ctx, `SELECT v.entity_id FROM music_virtual_tracks v WHERE v.asset_id=? AND v.ordinal>? AND NOT EXISTS(SELECT 1 FROM catalog_asset_links l WHERE l.entity_id=v.entity_id) ORDER BY v.ordinal`, asset, keep)
	if e != nil {
		return e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = compactcatalog.RetireEntityTx(ctx, tx, id); e != nil {
			return e
		}
	}
	return nil
}
