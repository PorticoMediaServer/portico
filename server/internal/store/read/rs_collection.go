package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// CollectionReader provides harness-only authored membership windows. Automatic
// membership and exact totals require their own complete source recipes.
type CollectionReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewCollectionReader(s *governor.Snapshots) (*CollectionReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &CollectionReader{snapshots: s, pool: s}, nil
}

type CollectionInput struct {
	View       BrowseInput
	Collection int64
	After      *CollectionCursor
}

// CollectionCursor binds the raw authored order to its exact source and viewer.
// It is internal values-only state; cutover needs the authenticated wire codec.
type CollectionCursor struct {
	fence      browseFence
	collection int64
	key        string
	item       int64
	source     []any
}

type CollectionPage struct {
	Items                 []TitleItem
	Next                  *CollectionCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// Members consumes at most64 authored positions and emits at most40 currently
// permitted same-library items. A hidden raw tail advances Next even when no
// item is emitted; Exhausted alone proves that no positions remain.
func (r *CollectionReader) Members(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in CollectionInput) (CollectionPage, BrowseReadResult, error) {
	if r == nil {
		return CollectionPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (CollectionPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.members(ctx, request, budget, output, in)
	})
}

func (r *CollectionReader) members(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in CollectionInput) (CollectionPage, BrowseReadResult, error) {
	if r == nil || in.Collection <= 0 || in.View.Kind != 10 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return CollectionPage{}, BrowseReadResult{}, ErrInput
	}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.collection.source", in.Collection, in.View.Library)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		if in.After != nil && !equalSource(in.After.source, rows[0]) {
			return nil, ErrStale
		}
		source = rows[0]
		return rows[0], nil
	}
	return publishRead(ctx, r.snapshots, request, budget, output, in.View, []readStep[CollectionPage]{
		func(ctx context.Context, q browseQuery, f browseFence, p *CollectionPage) error {
			if in.After != nil && (in.After.collection != in.Collection || in.After.fence != f) {
				return ErrStale
			}
			if !admittedItem(source, f) {
				return ErrDenied
			}
			return ctx.Err()
		},
		func(ctx context.Context, q browseQuery, f browseFence, p *CollectionPage) error {
			type seek struct {
				name string
				args []any
			}
			seeks := []seek{{"first", []any{in.Collection}}}
			if in.After != nil {
				seeks = []seek{{"tail", []any{in.Collection, in.After.key, in.After.item}}, {"next", []any{in.Collection, in.After.key}}}
			}
			for _, s := range seeks {
				limit := MaxRaw - p.RawRows
				rows, err := q("rs.collection."+s.name, append(s.args, int64(limit))...)
				if err != nil {
					return err
				}
				if len(rows) > limit {
					return ErrUnqualified
				}
				p.RawRows += len(rows)
				for _, row := range rows {
					p.RawPositions++
					p.Next = &CollectionCursor{fence: f, collection: in.Collection, key: row[7].(string), item: row[0].(int64), source: source}
					// Count/advance every raw relation before current visibility rejection.
					if row[8].(int64) != in.View.Library || !admittedItem(row, f) {
						continue
					}
					if row[9].(int64) < 1 || row[9].(int64) > 10 {
						return ErrUnqualified
					}
					if row[10].(int64) != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
						return ErrUnqualified
					}
					item, err := itemValue(row)
					if err != nil {
						return err
					}
					p.Items = append(p.Items, TitleItem{BrowseItem: item, Kind: row[9].(int64)})
					if len(p.Items) == in.View.Limit {
						return ctx.Err()
					}
				}
				if p.RawRows == MaxRaw {
					return ctx.Err()
				}
			}
			p.Exhausted = true
			p.Next = nil
			return ctx.Err()
		},
	}, root)
}
