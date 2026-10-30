package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoMemberSource adapts one captured ready group descriptor to the existing
// fileInfoOrderedSource lane, so a group's own files page through exactly the
// same reducer, ordinal accounting and delivery contract as every other lane. It
// is deliberately narrow:
//
//   - The group is the numeric descriptor of an exact ready group summary, not a
//     label, path, edition or shape text. The runtime resolves the signed group
//     into that descriptor before this source exists; no user scalar is retained
//     here and no text id, token or sort scalar ever enters Go.
//   - A singleton visits exactly its captured head: one point read of the
//     (entity,asset) primary key. It never walks the item, so a one-file version
//     costs one row regardless of how many files the item holds.
//   - A shape composite walks the existing catalog_asset_links_shape index in
//     (part,asset) keyset order restricted to the captured shape identity.
//   - A global composite walks every link of the item through
//     catalog_asset_links_order in (part,asset) keyset order, exactly as the
//     group reducer's part-major pass does.
//   - Every raw link row in the walked range is one event. Match is only
//     (accepted Options availability AND displayed link availability), so a
//     non-offered or non-displayed file still consumes its own raw row and the
//     lane's total/ordinals describe the raw range truthfully instead of a
//     silently pre-filtered subset. The published occurrence is the item's numeric
//     catalog_entities id plus the file's numeric catalog_assets id; the part,
//     shape, path and every label hydrate after ready.
//   - The head membership point is proved before any raw row is published: the
//     captured group head must still be linked to this item under the captured
//     shape identity. For a composite that is the shape's first offered head; for
//     a clustered singleton it is the singleton's own later offered head, which
//     need not equal ShapeHead. A vanished head, a re-shaped link or a NULL
//     accepted shape fails stale rather than approximating a different group from
//     the current row, and the reducer's owner-supplied Check keeps the original
//     opening, item informational head and source authority live before and after
//     every Step. No source health filter is consulted.
//   - Compare orders two members by the complete accepted BestVersionOrder (which
//     already begins with the link's part index) inside SQLite, bound to this
//     exact entity. Part and item are therefore exact in the same comparison, as
//     fileInfoFlatSource keeps its container order exact before the file order;
//     a residual tie between two distinct assets is an invariant failure because
//     the accepted order is total over distinct asset tokens.
type fileInfoMemberSource struct {
	db           *sql.DB
	item         int64
	group        fileInfoGroupNumeric
	captureBytes int64
	check        fileInfoBasisCheck
	after        fileInfoGroupKey
	hasAfter     bool
	done         bool
	validated    bool
	sequence     int64
}

// The charge covers the fixed adapter; the caller adds its own owner-audited
// captures and the ordered reducer accounts its own arrays separately. It never
// grows with the number of files or groups.
const fileInfoMemberSourceBytes int64 = 512

// The head membership point is the link primary key. Both composite walks are
// single-row indexed seeks: an equality prefix first, then a strict keyset bound
// on the next column, so a duplicate-part run is still read once per raw row and
// no statement sorts, materialises or scans an unrelated table.
const (
	fileInfoMemberHead = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.asset_id=?`

	fileInfoMemberShapeFirst = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? ORDER BY l.part_index,l.asset_id LIMIT 1`
	fileInfoMemberShapeCurrent = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? AND l.part_index=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
	fileInfoMemberShapePartNext = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? AND l.part_index>? ORDER BY l.part_index,l.asset_id LIMIT 1`

	fileInfoMemberPartFirst = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT 1`
	fileInfoMemberPartCurrent = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.part_index=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
	fileInfoMemberPartNext = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.part_index>? ORDER BY l.part_index,l.asset_id LIMIT 1`
)

// fileInfoMemberRow is one raw link of the walked range with the two independent
// availability facts kept apart: Offered is the accepted Options availability,
// Available is the displayed link availability.
type fileInfoMemberRow struct {
	Shape, Part, Asset int64
	Offered, Available bool
}

// newFileInfoMemberSource binds one exact playable item and one captured ready
// group descriptor to a single member lane. item is the numeric catalog_entities
// id, group is the validated numeric descriptor the runtime resolved from the
// exact ready group summary, captureBytes is the owner-audited size of the
// captures it retains and check is the owner's live authority probe. Every
// invalid parameter or descriptor is refused before any row is read.
func newFileInfoMemberSource(db *sql.DB, item int64, group fileInfoGroupNumeric, captureBytes int64, check fileInfoBasisCheck) (fileInfoOrderedSource, error) {
	if db == nil || item <= 0 || captureBytes < 0 || check == nil || !fileInfoGroupDescriptorOK(group) {
		return nil, readprep.ErrInvalid
	}
	return &fileInfoMemberSource{db: db, item: item, group: group, captureBytes: captureBytes, check: check}, nil
}

func (s *fileInfoMemberSource) AccountedBytes() int64 {
	return fileInfoMemberSourceBytes + s.captureBytes
}

func (s *fileInfoMemberSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}

// Next advances exactly one raw link. A vanished row ends the walk; a rejected
// availability is a match=false event, never a skipped row. Sequence increases by
// one per raw event so the reducer can prove progress, and int64 saturation is an
// invalid state rather than a wraparound. A singleton spends only its captured
// head point; a composite spends the head membership probe once and then one
// indexed row per event.
func (s *fileInfoMemberSource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	if s.done {
		return fileInfoOrderedRow{}, false, nil
	}
	if s.sequence == math.MaxInt64 {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	if !s.validated {
		head, exists, err := s.head(ctx)
		if err != nil {
			return fileInfoOrderedRow{}, false, err
		}
		if !exists {
			return fileInfoOrderedRow{}, false, readprep.ErrStale
		}
		s.validated = true
		if s.group.Type == fileInfoGroupSingleton {
			// The head point is the whole singleton: no second row exists.
			s.done = true
			return s.event(head), true, nil
		}
	}
	row, exists, err := s.nextRaw(ctx)
	if err != nil {
		return fileInfoOrderedRow{}, false, err
	}
	if !exists {
		s.done = true
		return fileInfoOrderedRow{}, false, nil
	}
	if row.Asset <= 0 || row.Shape <= 0 {
		return fileInfoOrderedRow{}, false, readprep.ErrStale
	}
	if s.hasAfter && !(row.Part > s.after.Part || row.Part == s.after.Part && row.Asset > s.after.Asset) {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	s.after = fileInfoGroupKey{Shape: row.Shape, Part: row.Part, Asset: row.Asset}
	s.hasAfter = true
	return s.event(row), true, nil
}

// event is the one published raw unit. A member is matched only when it is both
// an accepted Options file and displayed; the lane's eligible count is the same
// intersection, so a page can never claim an unoffered or hidden member.
func (s *fileInfoMemberSource) event(row fileInfoMemberRow) fileInfoOrderedRow {
	s.sequence++
	match := row.Offered && row.Available
	return fileInfoOrderedRow{
		Occurrence: fileInfoOccurrence{Item: s.item, Asset: row.Asset},
		Sequence:   s.sequence,
		Match:      match,
		Eligible:   match,
	}
}

// head proves the captured group membership point. The captured Head must still
// be linked to this item and must still carry the captured accepted shape
// identity (the group's shape, which the ready summary recorded for every group
// type). A composite's Head is its ShapeHead; a clustered singleton's Head may be
// a later offered file, so probing ShapeHead there would publish the wrong file.
// A NULL shape, a missing link or a different shape identity is stale.
func (s *fileInfoMemberSource) head(ctx context.Context) (fileInfoMemberRow, bool, error) {
	row, exists, err := s.scan(ctx, fileInfoMemberHead, s.item, s.group.Head)
	if err != nil || !exists {
		return fileInfoMemberRow{}, false, err
	}
	if row.Asset != s.group.Head || row.Shape != s.group.Shape {
		return fileInfoMemberRow{}, false, readprep.ErrStale
	}
	return row, true, nil
}

func (s *fileInfoMemberSource) nextRaw(ctx context.Context) (fileInfoMemberRow, bool, error) {
	if s.group.Type == fileInfoGroupShapeComposite {
		return s.nextShape(ctx)
	}
	return s.nextPart(ctx)
}

func (s *fileInfoMemberSource) nextShape(ctx context.Context) (fileInfoMemberRow, bool, error) {
	if !s.hasAfter {
		row, exists, err := s.scan(ctx, fileInfoMemberShapeFirst, s.item, s.group.Shape)
		if err != nil || !exists {
			return fileInfoMemberRow{}, exists, err
		}
		return row, true, s.shapeHeld(row)
	}
	row, exists, err := s.scan(ctx, fileInfoMemberShapeCurrent, s.item, s.group.Shape, s.after.Part, s.after.Asset)
	if err != nil || exists {
		return row, exists, err
	}
	row, exists, err = s.scan(ctx, fileInfoMemberShapePartNext, s.item, s.group.Shape, s.after.Part)
	if err != nil || !exists {
		return row, exists, err
	}
	return row, true, s.shapeHeld(row)
}

// shapeHeld refuses a row the shape index walk should not have returned under the
// captured shape. The walk is index-bounded, so this is an invariant failure
// rather than a routing decision.
func (s *fileInfoMemberSource) shapeHeld(row fileInfoMemberRow) error {
	if row.Shape != s.group.Shape {
		return readprep.ErrStale
	}
	return nil
}

func (s *fileInfoMemberSource) nextPart(ctx context.Context) (fileInfoMemberRow, bool, error) {
	if !s.hasAfter {
		return s.scan(ctx, fileInfoMemberPartFirst, s.item)
	}
	row, exists, err := s.scan(ctx, fileInfoMemberPartCurrent, s.item, s.after.Part, s.after.Asset)
	if err != nil || exists {
		return row, exists, err
	}
	return s.scan(ctx, fileInfoMemberPartNext, s.item, s.after.Part)
}

// scan reads exactly one raw indexed row. A NULL accepted shape or a missing
// asset link is corrupt/stale, never approximated from the current row.
func (s *fileInfoMemberSource) scan(ctx context.Context, query string, args ...any) (fileInfoMemberRow, bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var row fileInfoMemberRow
	var shape sql.NullInt64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, args...).
		Scan(&shape, &row.Part, &row.Asset, &row.Available, &row.Offered)
	if errors.Is(err, sql.ErrNoRows) {
		return fileInfoMemberRow{}, false, nil
	}
	if err != nil {
		return fileInfoMemberRow{}, false, err
	}
	if !shape.Valid || shape.Int64 <= 0 || row.Asset <= 0 {
		return fileInfoMemberRow{}, false, readprep.ErrStale
	}
	row.Shape = shape.Int64
	return row, true, nil
}

// Compare is exactly one complete accepted BestVersionOrder point comparison
// bound to this item. The shared accepted frame keeps the link's part index, the
// accepted availability, height and size, and the BINARY asset token inside
// SQLite; no descending integer is negated and no sort scalar crosses into Go, so
// part and item remain exact without a second in-Go ordering layer.
func (s *fileInfoMemberSource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	if a.Item != s.item || b.Item != s.item || a.Asset <= 0 || b.Asset <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var cmp int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, fileInfoAcceptedOrderCompare, s.item, a.Asset, s.item, b.Asset).Scan(&cmp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	if err != nil {
		return 0, err
	}
	// The accepted order ends in the unique asset token, so two distinct member
	// assets cannot tie. Anything else (including a missing point) is an invalid
	// ring rather than an arbitrary order.
	if cmp == 0 || cmp < -1 || cmp > 1 {
		return 0, readprep.ErrInvalid
	}
	return cmp, nil
}
