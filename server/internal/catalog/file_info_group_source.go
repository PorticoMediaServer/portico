package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoGroupKey is a numeric keyset cursor. Shape is the accepted
// catalog_info_shapes identity on the link, Part the link's part index and
// Asset the numeric catalog_assets id. No string, token or label is ever a
// cursor, so a legal long scalar cannot become retained state.
type fileInfoGroupKey struct {
	Shape, Part, Asset int64
}

// fileInfoGroupRow is one raw catalogue link in one of the two bounded walks.
// Offered is the accepted Options availability (asset.info_order_available);
// Available is the displayed link availability. They are deliberately distinct.
// Size and Duration are the accepted scalar values, before the informational
// fence rolled them forward.
type fileInfoGroupRow struct {
	Shape, Part, Asset int64
	Offered, Available bool
	Size               int64
	Duration           float64
}

// fileInfoGroupSource is the narrow raw seam for the group reducer:
//
//   - NextPart walks exactly the existing (entity,part_index,asset_id) link
//     index, one raw row per call. It is the first pass: all files, the offered
//     filter, the global duplicate-part test and the global head candidate.
//   - NextShape walks (entity,info_shape_id,part_index,asset_id) contiguously,
//     one raw row per call. Each shape is a bounded run, so the reducer keeps a
//     constant-size per-shape accumulator.
//   - NextShapeFile replays only one exact shape from its beginning or after a
//     numeric key. It never scans the whole catalogue again.
//   - CompareFiles orders two assets by the complete accepted BestVersionOrder
//     with two primary-key point reads inside SQLite.
//   - CompareGroups orders two directory candidates by their shape head first,
//     then their group head. Both are point comparisons; no sort crosses Go.
//
// Every method performs at most one indexed raw-row read or one exact point
// comparison. The reducer counts each call as a budgeted operation.
type fileInfoGroupSource interface {
	AccountedBytes() int64
	Check(context.Context) error
	NextPart(context.Context, fileInfoGroupKey, bool) (fileInfoGroupRow, bool, error)
	NextShape(context.Context, fileInfoGroupKey, bool) (fileInfoGroupRow, bool, error)
	NextShapeFile(context.Context, int64, fileInfoGroupKey, bool) (fileInfoGroupRow, bool, error)
	CompareFiles(context.Context, int64, int64) (int, error)
	CompareGroups(context.Context, fileInfoOccurrence, fileInfoOccurrence) (int, error)
}

// Accepted scalar overlays, installed in the same transaction as raw physical
// NEW. While a change is before activation the OLD delta is the accepted value;
// a ready item normally has no outstanding delta and the canonical row is exact.
// The accepted rule is compactcatalog.ReadInfoAsset: it overlays the first
// pre-fence OLD delta per field in (slot,field) order, so an active slot-0 delta
// owns the field while it is still before activation (phase<3) and a queued
// slot-1 delta is accepted only once the active slot-0 change has activated
// (phase>=3) and no longer overlays. Each overlay below is therefore exactly one
// correlated subquery with that WHERE and ORDER BY: probing slot 1 first (or
// COALESCE(slot1,slot0)) would publish a queued OLD while the active OLD still
// owns the field. The unique(asset_id,asset_token,slot) index supplies both the
// equality prefix and the slot order, so this stays an index seek with no
// temporary sort, and it is also the comparator file_info_sql_source uses.
const (
	fileInfoGroupSizeFrame = `COALESCE(
 (SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=2 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),
 a.size)`
	fileInfoGroupDurationFrame = `COALESCE(
 (SELECT o.old_real FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=9 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),
 a.duration)`
)

// Both walks are explicit covering forwards seeks. A composite row-value bound
// is deliberately not used: SQLite can scan from the start of the equal prefix
// instead of seeking the remaining columns. Each level below is a separate
// single-column seek after an equality prefix, so a huge duplicate-part run is
// still read once per raw row. The accepted scalar probes are unique-key point
// reads; neither walk sorts raw rows or scans an unrelated table.
const (
	fileInfoGroupPartFirst = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT 1`
	fileInfoGroupPartCurrent = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.part_index=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
	fileInfoGroupPartNext = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.part_index>? ORDER BY l.part_index,l.asset_id LIMIT 1`

	fileInfoGroupShapeFirst = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? ORDER BY l.info_shape_id,l.part_index,l.asset_id LIMIT 1`
	fileInfoGroupShapeCurrent = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? AND l.part_index=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
	fileInfoGroupShapePartNext = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? AND l.part_index>? ORDER BY l.part_index,l.asset_id LIMIT 1`
	fileInfoGroupShapeNext = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id>? ORDER BY l.info_shape_id,l.part_index,l.asset_id LIMIT 1`

	fileInfoGroupShapeFileFirst = `SELECT l.info_shape_id,l.part_index,l.asset_id,l.available,a.info_order_available,` + fileInfoGroupSizeFrame + `,` + fileInfoGroupDurationFrame + `
 FROM catalog_asset_links l INDEXED BY catalog_asset_links_shape JOIN catalog_assets a ON a.id=l.asset_id
 WHERE l.entity_id=? AND l.info_shape_id=? ORDER BY l.part_index,l.asset_id LIMIT 1`
)

// The charge covers the fixed adapter; the owner-audited captures are added by
// the caller and the reducer charges its own arrays separately. It does not
// grow with the number of links or shapes.
const fileInfoGroupSourceBytes int64 = 512

type fileInfoGroupSQLSource struct {
	db           *sql.DB
	item         int64
	captureBytes int64
	check        fileInfoBasisCheck
}

// newFileInfoGroupSource binds one exact playable item to the two bounded raw
// walks. check proves the original opening, item informational head and source
// authority remain the captured basis; the reducer calls it before and after
// every Step. No live read transaction is retained between statements.
func newFileInfoGroupSource(db *sql.DB, item int64, captureBytes int64, check fileInfoBasisCheck) (fileInfoGroupSource, error) {
	if db == nil || item <= 0 || captureBytes < 0 || check == nil {
		return nil, readprep.ErrInvalid
	}
	return &fileInfoGroupSQLSource{db: db, item: item, captureBytes: captureBytes, check: check}, nil
}

func (s *fileInfoGroupSQLSource) AccountedBytes() int64 {
	return fileInfoGroupSourceBytes + s.captureBytes
}

func (s *fileInfoGroupSQLSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}

func (s *fileInfoGroupSQLSource) NextPart(ctx context.Context, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	if !hasAfter {
		return s.scan(ctx, fileInfoGroupPartFirst, []any{s.item})
	}
	row, exists, err := s.scan(ctx, fileInfoGroupPartCurrent, []any{s.item, after.Part, after.Asset})
	if err != nil || exists {
		return row, exists, err
	}
	return s.scan(ctx, fileInfoGroupPartNext, []any{s.item, after.Part})
}

func (s *fileInfoGroupSQLSource) NextShape(ctx context.Context, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	if !hasAfter {
		return s.scan(ctx, fileInfoGroupShapeFirst, []any{s.item})
	}
	row, exists, err := s.scan(ctx, fileInfoGroupShapeCurrent, []any{s.item, after.Shape, after.Part, after.Asset})
	if err != nil || exists {
		return row, exists, err
	}
	row, exists, err = s.scan(ctx, fileInfoGroupShapePartNext, []any{s.item, after.Shape, after.Part})
	if err != nil || exists {
		return row, exists, err
	}
	return s.scan(ctx, fileInfoGroupShapeNext, []any{s.item, after.Shape})
}

func (s *fileInfoGroupSQLSource) NextShapeFile(ctx context.Context, shape int64, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	if shape <= 0 {
		return fileInfoGroupRow{}, false, readprep.ErrInvalid
	}
	if !hasAfter {
		return s.scan(ctx, fileInfoGroupShapeFileFirst, []any{s.item, shape})
	}
	if after.Shape != shape {
		return fileInfoGroupRow{}, false, readprep.ErrInvalid
	}
	row, exists, err := s.scan(ctx, fileInfoGroupShapeCurrent, []any{s.item, shape, after.Part, after.Asset})
	if err != nil || exists {
		return row, exists, err
	}
	return s.scan(ctx, fileInfoGroupShapePartNext, []any{s.item, shape, after.Part})
}

// scan reads exactly one raw indexed row. A NULL accepted shape on a ready
// basis is corrupt/stale, never approximated from the current asset frame.
func (s *fileInfoGroupSQLSource) scan(ctx context.Context, query string, args []any) (fileInfoGroupRow, bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var row fileInfoGroupRow
	var shape sql.NullInt64
	var duration float64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, query, args...).
		Scan(&shape, &row.Part, &row.Asset, &row.Available, &row.Offered, &row.Size, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return fileInfoGroupRow{}, false, nil
	}
	if err != nil {
		return fileInfoGroupRow{}, false, err
	}
	if !shape.Valid || shape.Int64 <= 0 || row.Asset <= 0 {
		return fileInfoGroupRow{}, false, readprep.ErrStale
	}
	row.Shape, row.Duration = shape.Int64, duration
	return row, true, nil
}

// CompareFiles is exactly one complete BestVersionOrder point comparison. The
// shared accepted frame keeps every numeric sort value and BINARY token inside
// SQLite; no descending integer is negated in Go.
func (s *fileInfoGroupSQLSource) CompareFiles(ctx context.Context, a, b int64) (int, error) {
	if a <= 0 || b <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var cmp int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, fileInfoAcceptedOrderCompare, s.item, a, s.item, b).Scan(&cmp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	if err != nil {
		return 0, err
	}
	if cmp < -1 || cmp > 1 {
		return 0, readprep.ErrInvalid
	}
	return cmp, nil
}

// CompareGroups orders directory candidates by shape-first appearance, then by
// the group's own head. A shape head that compares equal must be the same asset;
// the token is unique, so an equal-with-different-asset result is invalid.
func (s *fileInfoGroupSQLSource) CompareGroups(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	if a.Item <= 0 || a.Asset <= 0 || b.Item <= 0 || b.Asset <= 0 {
		return 0, readprep.ErrInvalid
	}
	if a == b {
		return 0, nil
	}
	cmp, err := s.CompareFiles(ctx, a.Item, b.Item)
	if err != nil || cmp != 0 {
		return cmp, err
	}
	return s.CompareFiles(ctx, a.Asset, b.Asset)
}
