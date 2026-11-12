package db

import (
	"database/sql"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// batchTx is a transaction that writes many rows. It keeps each statement prepared for the
// life of the transaction, and it holds the one insert function for each large table: the
// place where that table's integer codes are checked.
type batchTx struct {
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
	// watchedDirty are the holders whose watched marks this transaction must settle for
	// every profile before it ends. See watched_marks.go.
	watchedDirty []int64
	// watchedArrived are holders that gained new, unwatched items, with those items: a
	// lighter case.
	watchedArrived map[int64][]int64
	// watchedLeft are holders that only lost items, and watchedRated holders whose items'
	// ratings changed: lighter cases too.
	watchedLeft  []int64
	watchedRated []int64
}

func newBatchTx(tx *sql.Tx) *batchTx { return &batchTx{tx: tx, stmts: map[string]*sql.Stmt{}} }

func (b *batchTx) close() {
	for _, s := range b.stmts {
		s.Close()
	}
}

func (b *batchTx) stmt(query string) (*sql.Stmt, error) {
	if s, ok := b.stmts[query]; ok {
		return s, nil
	}
	s, err := b.tx.Prepare(query)
	if err == nil {
		b.stmts[query] = s
	}
	return s, err
}

func (b *batchTx) exec(query string, args ...any) (sql.Result, error) {
	s, err := b.stmt(query)
	if err != nil {
		return nil, err
	}
	return s.Exec(args...)
}

// queryRow returns a row to Scan. A statement that fails to prepare reports it from Scan.
func (b *batchTx) queryRow(query string, args ...any) *sql.Row {
	s, err := b.stmt(query)
	if err != nil {
		return b.tx.QueryRow(query, args...)
	}
	return s.QueryRow(args...)
}

// nz stores a zero value as NULL: an unknown year or an empty codec is absent, not 0 or "".
func nz[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func (b *batchTx) insertFile(directoryID int64, f *ImportFile, scan, now int64) (int64, error) {
	if err := checkCode("files.role", fileRoleNames, f.Role); err != nil {
		return 0, err
	}
	if f.Problem != ProblemNone {
		if err := checkCode("files.problem", fileProblemNames, f.Problem); err != nil {
			return 0, err
		}
	}
	var probedAt any = now
	if f.Unprobed {
		probedAt = nil
	}
	res, err := b.exec(`INSERT INTO files (directory_id, name, role, size, modified_ns, seen_scan, probed_at, container, duration_ms, bitrate,
		problem) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, directoryID, f.Name, f.Role, f.Size, f.ModifiedNS, scan, probedAt,
		nz(f.Container), nz(f.DurationMS), nz(f.Bitrate), nz(f.Problem))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, b.insertStreams(id, f.Streams)
}

func (b *batchTx) insertStreams(fileID int64, streams []Stream) error {
	for index, s := range streams {
		if err := checkCode("media_streams.kind", streamKindNames, s.Kind); err != nil {
			return err
		}
		if _, err := b.exec(`INSERT INTO media_streams (file_id, stream_index, kind, codec, profile, language, title, is_default, is_forced,
			is_hearing_impaired, bitrate, width, height, frame_rate, bit_depth, hdr, channels, channel_layout, sample_rate)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, fileID, index, s.Kind, nz(s.Codec), nz(s.Profile),
			nz(s.Language), nz(s.Title), s.Default, s.Forced, s.HearingImpaired, nz(s.Bitrate), nz(s.Width), nz(s.Height),
			nz(s.FrameRate), nz(s.BitDepth), nz(s.HDR), nz(s.Channels), nz(s.ChannelLayout), nz(s.SampleRate)); err != nil {
			return err
		}
	}
	return nil
}

// itemRow is an item ready to store: what the scan said plus what the import worked out.
type itemRow struct {
	*ImportItem
	libraryID  int64
	publicID   string
	parentID   int64
	rootID     int64
	addedAt    int64
	ratingAge  *int
	durationMS int64
}

func (b *batchTx) insertItem(row itemRow) (int64, error) {
	if err := checkCode("items.kind", itemKindNames, row.Kind); err != nil {
		return 0, err
	}
	if row.Title == "" {
		return 0, invalid("an item needs a title")
	}
	res, err := b.exec(`INSERT INTO items (public_id, library_id, kind, parent_id, root_id, index_number, parent_index, title, sort_title,
		original_title, year, release_date, added_at, updated_at, content_rating, rating_age, community_rating, duration_ms, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, row.publicID, row.libraryID, row.Kind, nz(row.parentID),
		nz(row.rootID), row.IndexNumber, row.ParentIndex, row.Title, sorttext.Key(row.Title, row.SortTitle, row.Language),
		nz(row.OriginalTitle), nz(row.Year), nz(row.ReleaseDate), row.addedAt, row.addedAt, nz(row.ContentRating), row.ratingAge,
		nz(row.CommunityRating), nz(row.durationMS), nz(row.Summary))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (b *batchTx) insertItemKey(libraryID int64, key ItemKey, itemID int64) error {
	if err := checkCode("item_keys.kind", keyKindNames, key.Kind); err != nil {
		return err
	}
	_, err := b.exec(`INSERT INTO item_keys (library_id, kind, key, item_id) VALUES (?, ?, ?, ?)`, libraryID, key.Kind, key.Key, itemID)
	return err
}

func (b *batchTx) insertMedia(itemID int64, position int, v *ImportVersion, facts versionFacts, addedAt int64) (int64, error) {
	res, err := b.exec(`INSERT INTO media (public_id, item_id, position, name, edition, container, duration_ms, bitrate, size, width, height,
		video_codec, audio_codec, audio_channels, hdr, added_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID("med"), itemID, position, nz(v.Name), nz(v.Edition), nz(facts.container), nz(facts.durationMS), nz(facts.bitrate),
		nz(facts.size), nz(facts.width), nz(facts.height), nz(facts.videoCodec), nz(facts.audioCodec), nz(facts.audioChannel),
		nz(facts.hdr), addedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, b.versionsChanged(itemID)
}

// widestPresent is the width of the widest version of the items row in scope that can still
// be played: a version one of whose files is missing does not count, so a title whose 4K copy
// was deleted stops sorting as 4K.
const widestPresent = `(SELECT max(m.width) FROM media m WHERE m.item_id = items.id AND NOT EXISTS (SELECT 1 FROM media_parts p
	JOIN files f ON f.id = p.file_id WHERE p.media_id = m.id AND f.missing_since_at IS NOT NULL))`

// versionsChanged must be called, in the same transaction, by whatever has just given an item
// a version, taken one from it, changed a version's picture size, or found one of a version's
// files gone or back. It writes the width of the item's widest playable version on the item,
// which the sort by resolution is read from.
func (b *batchTx) versionsChanged(items ...int64) error {
	for _, id := range items {
		if _, err := b.exec(`UPDATE items SET best_width = `+widestPresent+` WHERE id = ? AND best_width IS NOT `+widestPresent, id); err != nil {
			return err
		}
	}
	return nil
}

func (b *batchTx) insertMarker(mediaID int64, m Marker) error {
	if err := checkCode("media_markers.kind", markerKindNames, m.Kind); err != nil {
		return err
	}
	if err := checkCode("media_markers.source", markerSourceNames, m.Source); err != nil {
		return err
	}
	_, err := b.exec(`INSERT INTO media_markers (media_id, kind, source, start_ms, end_ms, title) VALUES (?, ?, ?, ?, ?, ?)`,
		mediaID, m.Kind, m.Source, m.StartMS, m.EndMS, nz(m.Title))
	return err
}

func (b *batchTx) insertCredit(itemID int64, position int, personID int64, role CreditRole, character string) error {
	if err := checkCode("credits.role", creditRoleNames, role); err != nil {
		return err
	}
	_, err := b.exec(`INSERT INTO credits (item_id, position, person_id, role, character) VALUES (?, ?, ?, ?, ?)`,
		itemID, position, personID, role, nz(character))
	return err
}

func (b *batchTx) insertImage(itemID int64, position int, image ImportImage) error {
	if err := checkCode("images.role", imageRoleNames, image.Role); err != nil {
		return err
	}
	if err := checkCode("images.source", imageSourceNames, image.Source); err != nil {
		return err
	}
	// The first picture found for a role is the one shown until a person chooses another.
	_, err := b.exec(`INSERT INTO images (item_id, role, source, file_hash, width, height, position, selected)
		VALUES (?, ?, ?, ?, ?, ?, ?, NOT EXISTS (SELECT 1 FROM images WHERE item_id = ? AND role = ? AND selected = 1))`,
		itemID, image.Role, image.Source, nz(image.FileHash), nz(image.Width), nz(image.Height), position, itemID, image.Role)
	return err
}

// tagItem gives an item a tag. The tag row takes its copies of the item's library, kind and
// rating from the item itself, so they cannot be given wrongly.
func (b *batchTx) tagItem(itemID, tagID int64) error {
	_, err := b.exec(`INSERT INTO item_tags (item_id, tag_id, library_id, kind, rating_age)
		SELECT id, ?, library_id, kind, rating_age FROM items WHERE id = ? ON CONFLICT DO NOTHING`, tagID, itemID)
	return err
}

// movedUnder must be called, in the same transaction, by whatever has just put an item under
// another holder: a scan that finds its file elsewhere, or a merge. What a title inherits
// from its holder moves with it: the rating that decides which profiles may see it, and the
// top of its tree, for it and for everything under it. rating is the item's own where it has
// one, else its new holder's; what is under it takes that rating too, except a title with a
// rating of its own, which keeps it. An episode moved from a show rated 8 into one rated 18 is then
// hidden from a profile limited to 8 at once.
func (b *batchTx) movedUnder(id, root int64, rating *int) error {
	if _, err := b.exec(`UPDATE items SET rating_age = ? WHERE id = ?`, rating, id); err != nil {
		return err
	}
	if _, err := b.exec(`WITH RECURSIVE t(id) AS (SELECT id FROM items WHERE parent_id = ? UNION ALL
		SELECT i.id FROM items i JOIN t ON i.parent_id = t.id)
		UPDATE items SET root_id = ?, rating_age = CASE WHEN content_rating IS NULL THEN ? ELSE rating_age END
		WHERE id IN (SELECT id FROM t)`, id, root, rating); err != nil {
		return err
	}
	// Each one's tag rows carry its rating too, for a limited profile's genre totals.
	moved, err := queryInts(b.tx, `WITH RECURSIVE t(id) AS (SELECT ? UNION ALL SELECT i.id FROM items i JOIN t ON i.parent_id = t.id)
		SELECT id FROM t`, id)
	if err != nil {
		return err
	}
	for _, each := range moved {
		if err := b.ratingChanged(each); err != nil {
			return err
		}
	}
	return nil
}

// ratingChanged must be called, in the same transaction, by whatever has just changed the
// rating_age of an item or of the items under it. It writes each one's present rating into
// its tag rows, which a rating-limited profile's genre choices and totals are counted from,
// and notes the holders around the item: a rating decides what a limited profile may see
// under a show, so it decides the show's watched mark. The caller's transaction must end
// with settleWatchedForAll.
func (b *batchTx) ratingChanged(itemID int64) error {
	if _, err := b.exec(`UPDATE item_tags SET rating_age = (SELECT rating_age FROM items WHERE id = item_tags.item_id)
		WHERE item_id = ?1 OR item_id IN (SELECT id FROM items WHERE root_id = ?1)`, itemID); err != nil {
		return err
	}
	holders, err := holdersAround(b, itemID)
	if err != nil {
		return err
	}
	b.watchedRated = append(b.watchedRated, holders...)
	// A title rated differently from what holds it can leave that holder empty.
	if err := b.flagHolders(itemID); err != nil {
		return err
	}
	// A collection holding the title takes the lowest rating of its films: see collection_rating.go.
	// So does a series holding the book.
	collections, err := queryInts(b.tx, `SELECT item_id FROM item_relations WHERE kind IN `+heldByRelation+` AND related_item_id = ?`, itemID)
	if err != nil {
		return err
	}
	return b.collectionsRated(collections...)
}
