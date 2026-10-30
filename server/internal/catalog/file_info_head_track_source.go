package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// fileInfoHeadTrackSource adapts the offered playback head's audio or subtitle
// lane to fileInfoOrderedSource. It is the offered counterpart of
// fileInfoTrackSource: the raw lane reads what the file stores, this lane reads
// what playbackv1.Options would actually offer for the exact group head.
//
//   - Audio is the existing raw audio adapter. playbackv1.Options lists every
//     stored audio stream of a version's first file, so the offered audio lane
//     is exactly that adapter's matching rows: every raw stream row still
//     consumes one lane event, a video or subtitle stream is a reject, and only
//     accepted technical parent facts plus type='audio' can match. It owns no
//     second audio SQL.
//   - Subtitles are offered resources, not raw stream rows. A personal upload,
//     a sidecar or a disc default can be offered while having no
//     asset_subtitles row at all, so walking the raw subtitle table would miss
//     exactly the rows the client is about to play. Instead this lane visits
//     every subtitle_resources row of the exact parent asset by numeric ROWID
//     through subtitle_resources_source_id_fk and makes its eligibility
//     decision after the LIMIT.
//   - Offered subtitle order is the existing resource id BINARY order that
//     listTx and Options use. Compare resolves two ROWIDs inside SQLite, so a
//     long or non-ASCII resource id never becomes a Go string anchor or a cached
//     sort scalar.
//   - The occurrence stays the numeric ROWID plus the parent's numeric asset id.
//     No resource id, title, language, path, token, digest or counted array is
//     retained. AccountedBytes is fixed for the adapter, so a lane with many
//     resources is not charged as if it were resident.
//   - Eligibility is data only: item, deleted, shared/owner visibility, current
//     revision, accepted size and modified, and the exact disc-default
//     predicates. There is deliberately no library-source health filter and no
//     invented personal approval.
//
// The parent still owns two composition facts this row source cannot observe:
// a disc-scoped resource is offered only when the configured subtitle extractor
// can render it, and the exact source only while the storage guard admits it.
// The parent injects those capabilities when it hydrates/offers an occurrence;
// this source neither guesses them nor turns them into a lane flap.
//
// The subtitle collection revision is not read here. A personal resource edit
// must change the FileInfo head, so the parent's FileInfo scope token must bind
// subtitle_collections(item_id).revision (one primary-key point) rather than
// minting a new global epoch.
type fileInfoHeadAudioSource struct {
	raw fileInfoOrderedSource
}

// The fixed adapter charge mirrors the raw adapter. The ordered scan adds its
// own bounded share; captureBytes is the owner-audited size of retained
// captures.
const fileInfoHeadTrackSourceBytes int64 = 512

// The subtitle lane walks the existing (source_id, rowid) index. The parent
// asset is resolved inside SQLite from its numeric id, so its token never
// enters Go, and only r.rowid is selected. Eligibility is a separate point
// statement issued after the row exists, which keeps every raw resource row an
// event: a deleted row, another owner's personal row or a row bound to a
// different accepted stat consumes the lane's LIMIT instead of being filtered
// out of it.
const (
	fileInfoHeadSubtitleFirst = `SELECT r.rowid FROM subtitle_resources r INDEXED BY subtitle_resources_source_id_fk
 WHERE r.source_id=(SELECT token FROM catalog_assets WHERE id=?)
 ORDER BY r.rowid LIMIT 1`
	fileInfoHeadSubtitleSeek = `SELECT r.rowid FROM subtitle_resources r INDEXED BY subtitle_resources_source_id_fk
 WHERE r.source_id=(SELECT token FROM catalog_assets WHERE id=?)
 AND r.rowid>? ORDER BY r.rowid LIMIT 1`
	fileInfoHeadSubtitleAnchor = `SELECT 1 FROM subtitle_resources WHERE rowid=? AND source_id=(SELECT token FROM catalog_assets WHERE id=?)`
)

// Compare is an exact two-ROWID point read of the stored resource ids. BINARY
// ordering matches the ORDER BY r.id listTx uses, and the CASE never negates a
// text value or sorts a table. A point that no longer belongs to this asset is
// stale; equal ids can only be the same resource, so any other tie is invalid.
const fileInfoHeadSubtitleCompare = `SELECT CASE
 WHEN x.id COLLATE BINARY<y.id COLLATE BINARY THEN -1
 WHEN x.id COLLATE BINARY>y.id COLLATE BINARY THEN 1 ELSE 0 END
 FROM (SELECT id FROM subtitle_resources WHERE rowid=? AND source_id=(SELECT token FROM catalog_assets WHERE id=?)) x,
 (SELECT id FROM subtitle_resources WHERE rowid=? AND source_id=(SELECT token FROM catalog_assets WHERE id=?)) y`

// newFileInfoHeadTrackSource binds one exact group head's offered lane. item is
// the numeric catalog_entities id of the opening's root, asset is the head
// file's numeric catalog_assets id, size and modified are the accepted parent
// values (never the raw NEW stat), lane is fileInfoLaneAudio or
// fileInfoLaneSubtitles, actor is the caller's already-keyed FileInfo actor,
// captureBytes is the owner-audited size of the captures it retains, and check
// is the owner's live authority probe. Every invalid parameter is refused
// before a row is read. The accepted scalars are int64 by schema and by
// accepted InfoAsset, so no artificial lower bound is invented.
func newFileInfoHeadTrackSource(db *sql.DB, item, asset, size, modified int64, lane byte, actor string, captureBytes int64, check fileInfoBasisCheck) (fileInfoOrderedSource, error) {
	if db == nil || item <= 0 || asset <= 0 || actor == "" || captureBytes < 0 || check == nil ||
		(lane != fileInfoLaneAudio && lane != fileInfoLaneSubtitles) {
		return nil, readprep.ErrInvalid
	}
	if lane == fileInfoLaneAudio {
		// playbackv1.Options offers every stored audio stream of the version's
		// first file, including tracks without a language or title. The raw
		// audio adapter is exactly that set, with the accepted technical parent
		// binding and no second SQL copy.
		raw, err := newFileInfoTrackSource(db, asset, size, modified, fileInfoLaneAudio, captureBytes, check)
		if err != nil {
			return nil, err
		}
		return &fileInfoHeadAudioSource{raw: raw}, nil
	}
	return &fileInfoHeadSubtitleSource{db: db, item: item, asset: asset, size: size, modified: modified, actor: actor, captureBytes: captureBytes, check: check}, nil
}

func (s *fileInfoHeadAudioSource) AccountedBytes() int64 { return s.raw.AccountedBytes() }
func (s *fileInfoHeadAudioSource) Check(ctx context.Context) error {
	return s.raw.Check(ctx)
}
func (s *fileInfoHeadAudioSource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	return s.raw.Compare(ctx, a, b)
}

// Next keeps the raw adapter's events and promotes only its accepted audio
// matches to eligible. Rejected streams stay rejected, so the ordered scan sees
// the same raw order while the offered total counts exactly the audio tracks
// Options lists.
func (s *fileInfoHeadAudioSource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	row, exists, err := s.raw.Next(ctx)
	if err != nil || !exists {
		return row, exists, err
	}
	row.Eligible = row.Match
	return row, true, nil
}

type fileInfoHeadSubtitleSource struct {
	db             *sql.DB
	item, asset    int64
	size, modified int64
	actor          string
	captureBytes   int64
	check          fileInfoBasisCheck
	sequence       int64
	anchor         int64
	done           bool
}

func (s *fileInfoHeadSubtitleSource) AccountedBytes() int64 {
	return fileInfoHeadTrackSourceBytes + s.captureBytes
}

func (s *fileInfoHeadSubtitleSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}

// Next advances exactly one raw resource row. An absent row ends the walk; an
// ineligible row is one consumed lane event with match=false, never a skipped
// row. Sequence increases by one per raw event so the reducer can prove
// progress, and int64 saturation is an invalid state rather than a wraparound.
// Only numeric ROWIDs and the parent asset cross into Go.
func (s *fileInfoHeadSubtitleSource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	if s.done {
		return fileInfoOrderedRow{}, false, nil
	}
	if s.sequence == math.MaxInt64 {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	rowid, exists, err := s.nextRaw(ctx)
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
	// The decision is after the row exists, so no eligibility filter can change
	// which raw row the LIMIT/SKIP cursor selected.
	match, err := subtitles.FileInfoHeadResourceMatch(ctx, dbwork.ReadHandle(ctx, s.db), rowid, s.item, s.asset, s.size, s.modified, s.actor)
	if errors.Is(err, sql.ErrNoRows) {
		return fileInfoOrderedRow{}, false, readprep.ErrStale
	}
	if err != nil {
		return fileInfoOrderedRow{}, false, err
	}
	s.anchor, s.sequence = rowid, s.sequence+1
	return fileInfoOrderedRow{Occurrence: fileInfoOccurrence{Item: rowid, Asset: s.asset}, Sequence: s.sequence, Match: match, Eligible: match}, true, nil
}

func (s *fileInfoHeadSubtitleSource) nextRaw(ctx context.Context) (int64, bool, error) {
	query, args := fileInfoHeadSubtitleFirst, []any{s.asset}
	if s.anchor > 0 {
		if err := s.anchorPresent(ctx); err != nil {
			return 0, false, err
		}
		query, args = fileInfoHeadSubtitleSeek, []any{s.asset, s.anchor}
	}
	var rowid int64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, args...).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return rowid, true, nil
}

// anchorPresent distinguishes "the last raw row is gone" from "the lane is
// exhausted". A removed or re-bound resource therefore fails the live check
// instead of silently restarting the walk from the current first row.
func (s *fileInfoHeadSubtitleSource) anchorPresent(ctx context.Context) error {
	var present int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, fileInfoHeadSubtitleAnchor, s.anchor, s.asset).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	return err
}

// Compare orders two exact resource occurrences by the stored id without
// reading either id into Go. Both keys must belong to this source's asset; a
// missing point means the resource moved or was removed under the walk and is
// stale. Equal stored ids can only be the same occurrence, because
// subtitle_resources.id is the primary key, so any other tie is an invalid ring
// rather than an arbitrary order.
func (s *fileInfoHeadSubtitleSource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	if a.Asset != s.asset || b.Asset != s.asset || a.Item <= 0 || b.Item <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var cmp int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, fileInfoHeadSubtitleCompare, a.Item, s.asset, b.Item, s.asset).Scan(&cmp)
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

// fileInfoHeadQueryer is the one-row read surface the hydration helpers need.
// A dbwork read handle (*sql.DB) or the caller's admitted *sql.Tx both satisfy
// it.
type fileInfoHeadQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// fileInfoHeadSubtitleTextBytes reports the exact text cost of hydrating one
// offered subtitle occurrence. The parent reserves this before calling
// fileInfoHeadSubtitleRow, so a legal long resource title or evidence is charged
// rather than entering the payload for free. A row that is no longer offered is
// readprep.ErrStale.
func fileInfoHeadSubtitleTextBytes(ctx context.Context, q fileInfoHeadQueryer, rowid, item, asset, size, modified int64, actor string) (int64, error) {
	bytes, err := subtitles.FileInfoHeadResourceTextBytes(ctx, q, rowid, item, asset, size, modified, actor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	if err != nil {
		return 0, err
	}
	if bytes < 0 {
		return 0, readprep.ErrInvalid
	}
	return bytes, nil
}

// fileInfoHeadSubtitleRow hydrates one offered subtitle occurrence into the
// informational track shape. ID is the exact accepted subtitle resource id and
// the remaining fields are the revision's language, format, title, default,
// forced, hearing-impaired and external (non-embedded) facts. Ordinal is left
// for the parent, which knows the occurrence's place in the delivered window;
// it is not a stored scalar. The helper reads only the pointed resource and
// returns readprep.ErrStale when the row is no longer offered. The parent must
// reserve fileInfoHeadSubtitleTextBytes before calling, and still applies the
// extractor/storage capability for a disc-scoped resource.
func fileInfoHeadSubtitleRow(ctx context.Context, q fileInfoHeadQueryer, rowid, item, asset, size, modified int64, actor string) (FileInfoHeadTrack, error) {
	r, err := subtitles.FileInfoHeadResourceRow(ctx, q, rowid, item, asset, size, modified, actor)
	if errors.Is(err, sql.ErrNoRows) {
		return FileInfoHeadTrack{}, readprep.ErrStale
	}
	if err != nil {
		return FileInfoHeadTrack{}, err
	}
	if r.ID == "" || r.Revision <= 0 {
		return FileInfoHeadTrack{}, readprep.ErrInvalid
	}
	return FileInfoHeadTrack{
		FileInfoRawTrack: FileInfoRawTrack{
			TitleFileTrack: TitleFileTrack{
				Language: r.Language,
				Codec:    r.Format,
				Title:    r.Title,
				Default:  r.Default,
				Forced:   r.Forced,
				External: r.Origin != "embedded",
			},
			HearingImpaired: r.HearingImpaired,
		},
		ID: r.ID,
	}, nil
}
