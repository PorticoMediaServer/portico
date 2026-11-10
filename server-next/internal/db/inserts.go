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
	return res.LastInsertId()
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
