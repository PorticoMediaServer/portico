package read

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// CollectionDetail contains the complete viewer-scoped count and a bounded
// page. Automatic order is stable item-ID order; authored order is the exact
// (order_key,item_id) order. It is an internal component, not a new HTTP DTO.
// CollectionDetailCursor binds a continuation to the exact viewer, source and
// query. A subsequent request prepares current membership, then proves this
// cursor in the fresh authorized handoff; an offset alone is not a wire token.
type CollectionDetailCursor struct {
	fence      browseFence
	source     []any
	collection int64
	offset     int
}

type CollectionDetail struct {
	Collection         BrowseItem
	Next               *CollectionDetailCursor
	Origin             string
	ExactCount         int
	Items              []TitleItem
	Offset, NextOffset int
	Exhausted          bool
}

// Detail performs the first ready handoff under the ORIGINAL preparation
// lease. Preparing work cannot return a successful exact response. Caller
// retains separately admitted output through encoding and client IO.
func (p *CollectionPreparation) Detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, after *CollectionDetailCursor) (CollectionDetail, BrowseReadResult, error) {
	offset := 0
	if after != nil {
		offset = after.offset
	}
	if err := p.check(output, request); err != nil {
		_ = p.Close()
		return CollectionDetail{}, BrowseReadResult{}, err
	}
	if !p.ready || offset < 0 || offset > len(p.nodes) {
		return CollectionDetail{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, p.in.View.Now, func(ctx context.Context, r governor.Request, b time.Duration, at time.Time) (CollectionDetail, BrowseReadResult, error) {
		if after != nil && (p.fence == nil || after.fence != *p.fence || !equalSource(after.source, p.source) || after.collection != p.in.Collection) {
			return CollectionDetail{}, BrowseReadResult{}, ErrStale
		}
		return p.detail(ctx, r, b, output, offset, at)
	})
}
func (p *CollectionPreparation) detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, offset int, at time.Time) (CollectionDetail, BrowseReadResult, error) {
	if err := p.check(output, request); err != nil {
		_ = p.Close()
		return CollectionDetail{}, BrowseReadResult{}, err
	}
	if !p.ready || offset < 0 || offset > len(p.nodes) {
		return CollectionDetail{}, BrowseReadResult{}, ErrInput
	}
	if p.lease.Deadline().Before(request.Deadline) {
		request.Deadline = p.lease.Deadline()
	}
	in := p.in.View
	in.Now = at
	ids := []int64{p.in.Collection}
	end := min(len(p.nodes), offset+in.Limit)
	for rank := offset; rank < end; rank++ {
		ids = append(ids, rsNodeRank(p.nodes, p.root, rank))
	}
	w := &rsByteWindow{source: p.reader.snapshots}
	steps := []readStep[CollectionDetail]{func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
		if p.fence == nil || *p.fence != f {
			return ErrStale
		}
		d.Origin = "owner"
		if p.cursor.provider != "" {
			d.Origin = "automatic"
		}
		d.ExactCount = len(p.nodes)
		d.Offset = offset
		d.NextOffset = end
		d.Exhausted = end == len(p.nodes)
		if !d.Exhausted {
			d.Next = &CollectionDetailCursor{fence: f, source: append([]any(nil), p.source...), collection: p.in.Collection, offset: end}
		}
		return p.authority.CheckPreparation(p.lease)
	}}
	// Four points per business scope, at most eight exact-byte calls. Actual
	// lengths forecast the next payload scope and are rechecked before decoding.
	for first := 0; first < len(ids); first += 4 {
		group := ids[first:min(first+4, len(ids))]
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
			var largest, total int64
			for _, id := range group {
				sizes, e := q("rs.auto.card_octets", id, in.Library)
				if e != nil {
					return e
				}
				if len(sizes) != 1 {
					return ErrStale
				}
				n, e := rsPointBytes(sizes[0])
				if e != nil {
					return e
				}
				largest = max(largest, n)
				total += n
			}
			if total > output.Request.Need[governor.MemoryBytes]/8 {
				return governor.ErrSnapshotBudget
			}
			return w.arm(max(largest, 1), uint16(2*len(group)))
		}, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
			for _, id := range group {
				rows, e := q("rs.auto.card", id, in.Library)
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrStale
				}
				v := rows[0]
				item, e := itemValue(v)
				if e != nil {
					return e
				}
				if id == p.in.Collection {
					if v[7].(int64) != 10 {
						return ErrStale
					}
					d.Collection = item
					continue
				}
				visible, e := rsCollectionVisible([]any{v[0], v[4], v[6], v[7], v[8]}, f)
				if e != nil {
					return e
				}
				if !visible {
					return ErrStale
				}
				d.Items = append(d.Items, TitleItem{BrowseItem: item, Kind: v[7].(int64)})
			}
			return ctx.Err()
		})
	}
	d, cost, err := publishRead(ctx, w, request, min(budget, time.Until(p.lease.Deadline())), output, in, steps, p.rootRead)
	if err == nil {
		err = p.authority.HandoffPreparation(p.lease)
	}
	if err != nil {
		_ = p.Close()
		return CollectionDetail{}, cost, err
	}
	if err = p.Close(); err != nil {
		return CollectionDetail{}, cost, err
	}
	return d, cost, nil
}
