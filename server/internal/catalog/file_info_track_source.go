package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoTrackSource adapts one exact parent file occurrence's raw audio or
// subtitle rows to fileInfoOrderedSource. It is deliberately narrow:
//
//   - Next visits one raw indexed row per call. Every raw stream row is an
//     event, including a video or subtitle stream in the audio lane, so the
//     type decision follows LIMIT instead of filtering the audio subset before
//     it: an arbitrary number of other raw stream rows can precede the window
//     and each must still consume its own event. Subtitle rows are never
//     filtered by status, reason, personal offer or sidecar availability: raw
//     and offered are separate lanes.
//   - The occurrence is the lane row's numeric ROWID plus the parent file's
//     numeric asset id. The parent's exact parent/child/link/file occurrence
//     remains a separate runtime descriptor; no path, title, codec, token or
//     subtitle text id is retained here.
//   - The seek anchor is the last raw ROWID only. The audio stream index and
//     the subtitle text id used to continue the walk are derived from that
//     ROWID inside SQLite, so a legal long scalar never becomes an in-memory
//     anchor. A vanished anchor is stale rather than a silent restart, and no
//     rowid+1/index+1 arithmetic exists, so int64 extremes stay exact.
//   - Compare resolves two ROWID point keys inside SQLite and orders them with
//     the exact stored titleFiles order. Sort scalars never cross into Go, no
//     numeric field is negated (int64Min must not promote to REAL), no table is
//     sorted, and a missing point (child removed or moved) is readprep.ErrStale.
//   - The caller binds the parent file's ACCEPTED size and modified. They are
//     compared with the lane's stored technical parent row, asset_stream_facts
//     for audio and asset_subtitle_facts for subtitles, addressed by its
//     primary key, and never with the raw catalog_assets size/modified. A raw
//     NEW stat can therefore arrive while the accepted OLD frame is still
//     usable before pre-fence, and an accepted stat without a matching technical
//     parent row is a NULL point whose events are all non-matches; the surface
//     is truthfully empty instead of guessing OLD rows out of the change tables.
//     Live original-opening, item-info and source authority stays in check,
//     which the owner supplies and the ordered reducer calls before and after
//     every Step.
type fileInfoTrackSource struct {
	db             *sql.DB
	asset          int64
	size, modified int64
	lane           byte
	captureBytes   int64
	check          fileInfoBasisCheck
	sequence       int64
	anchor         int64
	done           bool
}

// The charge covers the fixed adapter, the reducer's own share is accounted by
// fileInfoOrderedPlan, and the owner-audited captures are added by the caller.
// It is independent of the row count: no per-row or per-scalar anchor is kept.
const fileInfoTrackSourceBytes int64 = 512

// Each statement below reads at most one raw indexed row or one existence point.
// Next therefore runs at most two point statements: the anchor-existence probe
// and the one first/seek row; the first event of a walk needs only one. The
// parent file is addressed by its numeric catalogue id, its token is resolved
// inside SQLite and never enters Go, and the technical parent fact is a
// primary-key point on that token. Size, modified, asset id and anchor rowid all
// stay bound parameters, so size and modified keep their exact int64 value.
//
// audio first / seek: the walk is the (asset_id,stream_index) index order over
// every raw stream row, with no type filter before LIMIT, so the audio type
// decision follows the window instead of skipping rows. seek derives the last
// stream_index from the last raw ROWID in one point subquery.
const (
	fileInfoTrackAudioFirst = `SELECT s.rowid,s.type,
 (SELECT f.size=? AND f.modified_ns=? FROM asset_stream_facts f WHERE f.asset_id=s.asset_id)
 FROM asset_streams s
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 ORDER BY s.stream_index LIMIT 1`
	fileInfoTrackAudioSeek = `SELECT s.rowid,s.type,
 (SELECT f.size=? AND f.modified_ns=? FROM asset_stream_facts f WHERE f.asset_id=s.asset_id)
 FROM asset_streams s
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 AND s.stream_index>(SELECT stream_index FROM asset_streams WHERE rowid=?)
 ORDER BY s.stream_index LIMIT 1`
	fileInfoTrackAudioAnchor = `SELECT 1 FROM asset_streams WHERE rowid=? AND asset_id=(SELECT token FROM catalog_assets WHERE id=?)`
)

// subtitle first / seek: the existing asset_subtitles_asset(asset_id,id) index
// walks one raw row per call. Ordering against the stored origin,stream_index,id
// order is Compare's job, so the walk never sorts or materialises the lane. seek
// derives the last text id from the last raw ROWID; the id itself is never
// retained. The parent binding is asset_subtitle_facts, not the raw asset row.
const (
	fileInfoTrackSubtitleFirst = `SELECT s.rowid,
 (SELECT f.size=? AND f.modified_ns=? FROM asset_subtitle_facts f WHERE f.asset_id=s.asset_id)
 FROM asset_subtitles s INDEXED BY asset_subtitles_asset
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 ORDER BY s.id LIMIT 1`
	fileInfoTrackSubtitleSeek = `SELECT s.rowid,
 (SELECT f.size=? AND f.modified_ns=? FROM asset_subtitle_facts f WHERE f.asset_id=s.asset_id)
 FROM asset_subtitles s INDEXED BY asset_subtitles_asset
 WHERE s.asset_id=(SELECT token FROM catalog_assets WHERE id=?)
 AND s.id>(SELECT id FROM asset_subtitles WHERE rowid=?)
 ORDER BY s.id LIMIT 1`
	fileInfoTrackSubtitleAnchor = `SELECT 1 FROM asset_subtitles WHERE rowid=? AND asset_id=(SELECT token FROM catalog_assets WHERE id=?)`
)

// Both comparators are exact two-ROWID point keys. Audio uses the stored numeric
// stream_index. Subtitles use origin,stream_index,id with BINARY collation for
// the two text fields, exactly as title_files_batch orders raw subtitles. The
// CASE never negates a value, so int64Min compares exactly.
const (
	fileInfoTrackAudioCompare = `SELECT CASE
 WHEN x.stream_index<y.stream_index THEN -1 WHEN x.stream_index>y.stream_index THEN 1 ELSE 0 END
 FROM (SELECT stream_index FROM asset_streams WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?) x,
 (SELECT stream_index FROM asset_streams WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?) y`
	fileInfoTrackSubtitleCompare = `SELECT CASE
 WHEN x.origin COLLATE BINARY<y.origin COLLATE BINARY THEN -1
 WHEN x.origin COLLATE BINARY>y.origin COLLATE BINARY THEN 1
 WHEN x.stream_index<y.stream_index THEN -1
 WHEN x.stream_index>y.stream_index THEN 1
 WHEN x.id COLLATE BINARY<y.id COLLATE BINARY THEN -1
 WHEN x.id COLLATE BINARY>y.id COLLATE BINARY THEN 1 ELSE 0 END
 FROM (SELECT origin,stream_index,id FROM asset_subtitles WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?) x,
 (SELECT origin,stream_index,id FROM asset_subtitles WHERE asset_id=(SELECT token FROM catalog_assets WHERE id=?) AND rowid=?) y`
)

// newFileInfoTrackSource binds one exact parent file's accepted facts to one raw
// lane. asset is the numeric catalog_assets id, size and modified are the parent
// values the original opening accepted (not the raw NEW row) and are compared
// with the lane's stored technical fact row, lane is fileInfoLaneAudio or
// fileInfoLaneSubtitles, captureBytes is the owner-audited size of the captures
// it retains, and check is the owner's live authority probe. Every invalid
// parameter is refused before any row is read. Both scalars are int64 by schema
// and by accepted InfoAsset, so no artificial lower bound is invented here.
func newFileInfoTrackSource(db *sql.DB, asset int64, size, modified int64, lane byte, captureBytes int64, check fileInfoBasisCheck) (fileInfoOrderedSource, error) {
	if db == nil || asset <= 0 || captureBytes < 0 || check == nil ||
		(lane != fileInfoLaneAudio && lane != fileInfoLaneSubtitles) {
		return nil, readprep.ErrInvalid
	}
	return &fileInfoTrackSource{db: db, asset: asset, size: size, modified: modified, lane: lane, captureBytes: captureBytes, check: check}, nil
}

func (s *fileInfoTrackSource) AccountedBytes() int64 {
	return fileInfoTrackSourceBytes + s.captureBytes
}

func (s *fileInfoTrackSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}

// Next advances exactly one raw lane row. An absent row ends the walk; a
// rejected type or a mismatched/missing technical parent is a match=false
// event, never a skipped row. Sequence increases by one per raw event so the
// reducer can prove progress, and int64 saturation is an invalid state rather
// than a wraparound. At most the anchor probe and one raw row statement run.
func (s *fileInfoTrackSource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	if s.done {
		return fileInfoOrderedRow{}, false, nil
	}
	if s.sequence == math.MaxInt64 {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	rowid, match, exists, err := s.nextRaw(ctx)
	if err != nil {
		return fileInfoOrderedRow{}, false, err
	}
	if !exists {
		s.done = true
		return fileInfoOrderedRow{}, false, nil
	}
	if rowid <= 0 {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	s.anchor, s.sequence = rowid, s.sequence+1
	// Raw lanes make no playback-offer claim: eligible stays false and offered
	// head tracks remain a separate lane with their own exact occurrence.
	return fileInfoOrderedRow{Occurrence: fileInfoOccurrence{Item: rowid, Asset: s.asset}, Sequence: s.sequence, Match: match}, true, nil
}

func (s *fileInfoTrackSource) nextRaw(ctx context.Context) (int64, bool, bool, error) {
	if s.lane == fileInfoLaneAudio {
		return s.nextAudio(ctx)
	}
	return s.nextSubtitles(ctx)
}

func (s *fileInfoTrackSource) nextAudio(ctx context.Context) (int64, bool, bool, error) {
	query, args := fileInfoTrackAudioFirst, []any{s.size, s.modified, s.asset}
	if s.anchor > 0 {
		if err := s.anchorPresent(ctx, fileInfoTrackAudioAnchor); err != nil {
			return 0, false, false, err
		}
		query, args = fileInfoTrackAudioSeek, append(args, s.anchor)
	}
	var rowid int64
	var kind string
	// NULL is a missing technical parent fact, never a match.
	var valid sql.NullInt64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, args...).Scan(&rowid, &kind, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return rowid, valid.Valid && valid.Int64 == 1 && kind == "audio", true, nil
}

func (s *fileInfoTrackSource) nextSubtitles(ctx context.Context) (int64, bool, bool, error) {
	query, args := fileInfoTrackSubtitleFirst, []any{s.size, s.modified, s.asset}
	if s.anchor > 0 {
		if err := s.anchorPresent(ctx, fileInfoTrackSubtitleAnchor); err != nil {
			return 0, false, false, err
		}
		query, args = fileInfoTrackSubtitleSeek, append(args, s.anchor)
	}
	var rowid int64
	// NULL is a missing technical parent fact, never a match.
	var valid sql.NullInt64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, args...).Scan(&rowid, &valid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return rowid, valid.Valid && valid.Int64 == 1, true, nil
}

// anchorPresent is the one point probe that distinguishes "the last raw row is
// gone" from "the lane is exhausted". A removed child (or a row that no longer
// belongs to this parent asset) therefore fails the live check instead of
// silently restarting the walk from the current first row.
func (s *fileInfoTrackSource) anchorPresent(ctx context.Context, query string) error {
	var present int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, s.anchor, s.asset).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	return err
}

// Compare orders two exact lane occurrences without reading their sort scalars
// into Go. Both keys must belong to this source's asset; a missing point means
// the child moved or was removed under the walk and is stale. Equal stored keys
// are only truthful for the same occurrence (the audio primary key and the
// subtitle primary key make a duplicate impossible otherwise), so any other tie
// is an invalid ring rather than an arbitrary order.
func (s *fileInfoTrackSource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	if a.Asset != s.asset || b.Asset != s.asset || a.Item <= 0 || b.Item <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	query := fileInfoTrackAudioCompare
	if s.lane == fileInfoLaneSubtitles {
		query = fileInfoTrackSubtitleCompare
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var cmp int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, s.asset, a.Item, s.asset, b.Item).Scan(&cmp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	if err != nil {
		return 0, err
	}
	if cmp == 0 || cmp < -1 || cmp > 1 {
		return 0, readprep.ErrInvalid
	}
	return cmp, nil
}
