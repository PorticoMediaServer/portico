package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type fileInfoBasisCheck func(context.Context) error
type fileInfoChildCheck func(context.Context, int64) (bool, error)

type fileInfoFlatSource struct {
	db                                      *sql.DB
	root                                    int64
	children                                bool
	check                                   fileInfoBasisCheck
	visible                                 fileInfoChildCheck
	captureBytes                            int64
	child, childAfter, assetAfter, sequence int64
	hasChild, hasAsset                      bool
	state                                   int // 0 choose one raw child, 1 walk its raw links, 2 end
}

func newFileInfoFlatSource(db *sql.DB, root int64, children bool, captureBytes int64, check fileInfoBasisCheck, visible fileInfoChildCheck) (*fileInfoFlatSource, error) {
	if db == nil || root <= 0 || captureBytes < 0 || check == nil || children && visible == nil {
		return nil, readprep.ErrInvalid
	}
	source := &fileInfoFlatSource{db: db, root: root, children: children, check: check, visible: visible, captureBytes: captureBytes}
	if !children {
		source.child, source.state = root, 1
	}
	return source, nil
}
func (s *fileInfoFlatSource) AccountedBytes() int64 { return 512 + s.captureBytes }
func (s *fileInfoFlatSource) Check(ctx context.Context) error {
	return s.check(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
}
func (s *fileInfoFlatSource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	if s.state == 2 {
		return fileInfoOrderedRow{}, false, nil
	}
	if s.sequence == math.MaxInt64 {
		return fileInfoOrderedRow{}, false, readprep.ErrInvalid
	}
	s.sequence++
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	q := dbwork.ReadHandle(ctx, s.db)
	out := fileInfoOrderedRow{Sequence: s.sequence}
	if s.state == 0 {
		query := `SELECT id,retired FROM catalog_entities INDEXED BY catalog_entities_parent WHERE parent_id=?`
		args := []any{s.root}
		if s.hasChild {
			query += ` AND id>?`
			args = append(args, s.childAfter)
		}
		query += ` ORDER BY id LIMIT 1`
		var child int64
		var retired bool
		err := q.QueryRowContext(ctx, query, args...).Scan(&child, &retired)
		if errors.Is(err, sql.ErrNoRows) {
			s.state = 2
			return out, false, nil
		}
		if err != nil {
			return out, false, err
		}
		s.childAfter, s.hasChild, s.hasAsset = child, true, false
		if !retired {
			allowed, err := s.visible(ctx, child)
			if err != nil {
				return out, false, err
			}
			if allowed {
				s.child, s.state = child, 1
			}
		}
		// A rejected/empty child is still one raw unit; no filter precedes LIMIT.
		return out, true, nil
	}
	query := `SELECT l.asset_id,l.available,a.info_order_available FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`
	args := []any{s.child}
	if s.hasAsset {
		query += ` AND l.asset_id>?`
		args = append(args, s.assetAfter)
	}
	query += ` ORDER BY l.asset_id LIMIT 1`
	var asset int64
	var available, offered bool
	err := q.QueryRowContext(ctx, query, args...).Scan(&asset, &available, &offered)
	if errors.Is(err, sql.ErrNoRows) {
		if s.children {
			s.state = 0
			return out, true, nil
		}
		s.state = 2
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	s.assetAfter, s.hasAsset = asset, true
	out.Occurrence, out.Match, out.Eligible = fileInfoOccurrence{s.child, asset}, true, available && offered
	return out, true, nil
}

// Exact accepted BestVersionOrder, including integer extremes and full BINARY
// token comparison. No descending numeric field is negated (MinInt64 must not
// promote to an imprecise REAL). Each CTE is two point lookups and <=2 OLD-delta
// probes; the complete sort strings stay inside SQLite and no TEMP sort exists.
const fileInfoAcceptedOrderFrame = `SELECT l.part_index,a.info_order_available,
 COALESCE((SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=8 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),a.height) AS height,
 COALESCE((SELECT o.old_integer FROM catalog_info_changes c JOIN catalog_info_scalar_old o ON o.change_id=c.id AND o.field=2 WHERE c.asset_id=a.id AND c.asset_token=a.token AND c.phase<3 ORDER BY c.slot LIMIT 1),a.size) AS size,a.token
 FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id=?`
const fileInfoAcceptedOrderCompare = `WITH x AS (` + fileInfoAcceptedOrderFrame + `),y AS (` + fileInfoAcceptedOrderFrame + `)
 SELECT CASE
 WHEN x.part_index<y.part_index THEN -1 WHEN x.part_index>y.part_index THEN 1
 WHEN x.info_order_available>y.info_order_available THEN -1 WHEN x.info_order_available<y.info_order_available THEN 1
 WHEN x.height>y.height THEN -1 WHEN x.height<y.height THEN 1
 WHEN x.size>y.size THEN -1 WHEN x.size<y.size THEN 1
 WHEN x.token COLLATE BINARY<y.token COLLATE BINARY THEN -1 WHEN x.token COLLATE BINARY>y.token COLLATE BINARY THEN 1 ELSE 0 END FROM x CROSS JOIN y`

func (s *fileInfoFlatSource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	// Existing container ordering is child-id first, then complete file order.
	if a.Item < b.Item {
		return -1, nil
	}
	if a.Item > b.Item {
		return 1, nil
	}
	if a == b {
		return 0, nil
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	var cmp int
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, fileInfoAcceptedOrderCompare, a.Item, a.Asset, b.Item, b.Asset).Scan(&cmp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, readprep.ErrStale
	}
	return cmp, err
}
