package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const (
	rsCollectionQuantum   = 16
	rsCollectionNodeBytes = 16
	rsCollectionMetaBytes = 16384
	// Fixed per-row decode allowance for the narrow eligibility/publication rows
	// that share the payload window with the measured card/order-key point.
	rsCollectionFixedRowBytes = 512
)

const (
	rsCollectionMembers = iota
	rsCollectionDone
)

// rsCollectionNode is the exact viewer-authorized membership order retained
// until the first ready handoff. It holds no title, path, art or source value.
type rsCollectionNode struct {
	id, kind int64
}

// CollectionPreparation is caller-owned typed ephemeral work under RB's F-04
// lease. It has no worker, queue, SQL handle, durable projection or renewal.
// Advance/Detail/Artwork/Close serialize on this job's mutex, so a lease-expiry,
// cancel or handler goroutine cannot retire the lease twice or clear retained
// storage mid-enumeration; the caller still keeps the job context alive between
// polls and an HTTP poll context must not own the job lifetime. Detail and
// Artwork each close the preparation themselves (a detail page with art needs a
// second enumeration). A borrowed child shares an enclosing lease and never
// retires, readies or hands off its parent's reservation.
type CollectionPreparation struct {
	mu            sync.Mutex
	borrowed      bool
	started       time.Time
	owner         governor.OwnerID
	reader        *CollectionReader
	authority     *governor.Authority
	lease         *governor.PreparationLease
	in            CollectionInput
	fence         *browseFence
	source        []any
	cursor        rsCollectionCursor
	nodes         []rsCollectionNode
	ready, closed bool
	nonempty      bool
	complete      int
	proof         [32]byte
}

type rsCollectionCursor struct {
	stage int
	key   string
	item  int64
}

// CollectionAdvance distinguishes pending work from an exact result. No member
// values or partial total are exposed before the complete reduction is ready.
type CollectionAdvance struct {
	Ready        bool
	RawPositions int
}

// BeginCollectionPreparation captures an immutable query and conservatively
// leases all publication in its library, including source/policy changes.
// Capacity is explicit, never a successful truncation. All tree storage is
// admitted before allocation; the remaining allowance funds cursors and fences.
func (r *CollectionReader) BeginCollectionPreparation(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, in CollectionInput) (*CollectionPreparation, *governor.Refusal, error) {
	if r == nil || a == nil || ctx == nil || in.Collection <= 0 || len(in.View.Database) == 0 || len(in.View.Database) > 256 || len(in.View.TokenHash) == 0 || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.Now.IsZero() || in.View.Kind != 10 || in.View.After != nil || in.After != nil || in.View.Descending || in.View.KeepArticles || in.View.Limit < 1 || in.View.Limit > MaxPage || bytes < governor.PreparationMetadataBytes+rsCollectionMetaBytes {
		return nil, nil, ErrInput
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%d/%d/%d/%d", in.View.Database, in.View.TokenHash, in.View.Library, in.Collection, in.View.policy.recipe, in.View.policy.subject)))
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("collection:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{Library: in.View.Library}}})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	capacity := (d.Lease.PayloadBytes() - rsCollectionMetaBytes) / rsCollectionNodeBytes
	if capacity < 1 {
		_ = a.CancelPreparation(d.Lease)
		_ = a.RetirePreparation(d.Lease)
		return nil, nil, governor.ErrSnapshotBudget
	}
	in.View.Database = strings.Clone(in.View.Database)
	in.View.TokenHash = strings.Clone(in.View.TokenHash)
	p := &CollectionPreparation{started: time.Now(), owner: owner, reader: r, authority: a, lease: d.Lease, in: in, nodes: make([]rsCollectionNode, 0, int(capacity))}
	p.in.View.policy = readPolicyBinding{recipe: rsCollectionComputePolicy, subject: in.Collection, state: p}
	return p, nil, nil
}

func (p *CollectionPreparation) Deadline() time.Time {
	if p == nil || p.lease == nil {
		return time.Time{}
	}
	return p.lease.Deadline()
}

// Close drops every retained value before attesting physical retirement. Output
// values already copied into BrowseOutput are independently admitted custody.
// The mutex makes the exactly-once cancel/retire and the teardown of retained
// storage linear with Advance/Detail/Artwork and with another Close.
func (p *CollectionPreparation) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

// closeLocked is Close for callers that already hold p.mu.
func (p *CollectionPreparation) closeLocked() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.nodes = nil
	p.source = nil
	p.fence = nil
	p.cursor = rsCollectionCursor{}
	p.in = CollectionInput{}
	if p.borrowed {
		return nil
	}
	_ = p.authority.CancelPreparation(p.lease)
	return p.authority.RetirePreparation(p.lease)
}

func (p *CollectionPreparation) check(output BrowseOutput, request governor.Request) error {
	if p == nil || p.closed || output.Authority != p.authority || request.Owner != p.owner || output.Request.Owner != p.owner {
		return ErrInput
	}
	return p.authority.CheckPreparation(p.lease)
}

func (p *CollectionPreparation) rootRead(q browseQuery) ([]any, error) {
	rows, err := q("rs.collection_complete.source", p.in.View.Library, p.in.Collection)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrDenied
	}
	// Source8's authored-membership tuple does not prove inferred R21 members.
	// An automatic identity refuses this authored-only complete recipe.
	automatic, err := q("rs.subject.automatic", p.in.Collection)
	if err != nil {
		return nil, err
	}
	if len(automatic) != 0 {
		return nil, ErrUnqualified
	}
	if p.source != nil && !equalSource(p.source, rows[0]) {
		return nil, ErrStale
	}
	return rows[0], nil
}

// Advance enumerates every current authored (order_key,item_id) position in
// exact order. Hidden, retired and cross-library rows still consume a raw
// position and advance the cursor; they never terminate the complete count.
func (p *CollectionPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput) (CollectionAdvance, BrowseReadResult, error) {
	if p == nil {
		return CollectionAdvance{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(output, request); err != nil {
		return CollectionAdvance{}, BrowseReadResult{}, err
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, p.in.View.Now.Add(time.Since(p.started)), func(ctx context.Context, r governor.Request, b time.Duration, at time.Time) (CollectionAdvance, BrowseReadResult, error) {
		return p.advance(ctx, r, b, output, at)
	})
}

// advance is the retained-state quantum for Advance, which holds p.mu across it.
func (p *CollectionPreparation) advance(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, at time.Time) (CollectionAdvance, BrowseReadResult, error) {
	if err := p.check(output, request); err != nil {
		_ = p.closeLocked()
		return CollectionAdvance{}, BrowseReadResult{}, err
	}
	if p.ready {
		return CollectionAdvance{Ready: true}, BrowseReadResult{}, nil
	}
	if p.lease.Deadline().Before(request.Deadline) {
		request.Deadline = p.lease.Deadline()
	}
	in := p.in.View
	in.Now = at
	c := p.cursor
	var source []any
	var fence browseFence
	var accepted []rsCollectionNode
	nonempty := p.nonempty
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 40, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	root := func(q browseQuery) ([]any, error) { var e error; source, e = p.rootRead(q); return source, e }
	steps := []readStep[CollectionAdvance]{func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
		if p.fence != nil && !rsCollectionSameFence(*p.fence, f) {
			return ErrStale
		}
		fence = f
		if len(source) > 0 {
			if _, err := rsCollectionRootProof(q, source, f); err != nil {
				return err
			}
		}
		return p.authority.CheckPreparation(p.lease)
	}}
	var name string
	var args []any
	var batch [][]any
	if c.stage == rsCollectionMembers {
		name = "member"
		args = []any{p.in.Collection, c.key, c.item, int64(rsCollectionQuantum)}
	}
	if name != "" {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			sizes, e := q("rs.collection_complete."+name+"_octets", args...)
			if e != nil {
				return e
			}
			if len(sizes) == 0 && name == "member" {
				name = "member_next"
				args = []any{p.in.Collection, c.key, int64(rsCollectionQuantum)}
				sizes, e = q("rs.collection_complete."+name+"_octets", args...)
				if e != nil {
					return e
				}
			}
			if len(sizes) == 0 {
				c.stage = rsCollectionDone
				return nil
			}
			if len(sizes) > rsCollectionQuantum {
				return ErrUnqualified
			}
			var largest int64
			for _, row := range sizes {
				n, e := rsPointBytes(row)
				if e != nil {
					return e
				}
				if n < 0 || n > 4096 {
					return governor.ErrSnapshotBudget
				}
				largest = max(largest, n)
			}
			return w.arm(max(largest, 1), 2)
		}, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			if c.stage == rsCollectionDone {
				return nil
			}
			rows, e := q("rs.collection_complete."+name, args...)
			if e != nil {
				return e
			}
			if len(rows) == 0 || len(rows) > rsCollectionQuantum {
				return ErrStale
			}
			batch = rows
			return w.arm(rsCollectionFixedRowBytes, uint16(3*len(rows)+16))
		}, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			for _, raw := range batch {
				c.item = raw[0].(int64)
				c.key = raw[1].(string)
				d.RawPositions++
				facts, e := q("rs.collection_complete.member_facts", in.Library, c.item)
				if e != nil {
					return e
				}
				if len(facts) == 0 {
					// A cross-library or missing current entity is not a visible
					// member of this library's collection and never proves emptiness.
					continue
				}
				if len(facts) != 1 {
					return ErrUnqualified
				}
				row := facts[0]
				if row[1].(int64) == 0 {
					nonempty = true
				}
				visible, e := rsCollectionMemberEligible(q, row, in, f)
				if e != nil {
					return e
				}
				if visible {
					accepted = append(accepted, rsCollectionNode{id: row[0].(int64), kind: row[3].(int64)})
				}
			}
			return ctx.Err()
		})
	}
	result, cost, err := publishRead(ctx, w, request, min(budget, time.Until(p.lease.Deadline())), output, in, steps, root)
	if err == nil {
		err = p.authority.CheckPreparation(p.lease)
	}
	if err != nil {
		_ = p.closeLocked()
		return CollectionAdvance{}, cost, err
	}
	// Admission covers the worst case BEFORE mutating retained storage. Refuse
	// conservatively rather than truncating the exact answer.
	if rsCollectionMetadataBytes(p.in, c, fence, source) > rsCollectionMetaBytes || len(accepted) > cap(p.nodes)-len(p.nodes) {
		_ = p.closeLocked()
		return CollectionAdvance{}, cost, governor.ErrSnapshotBudget
	}
	p.nodes = append(p.nodes, accepted...)
	p.nonempty = nonempty
	p.cursor = c
	p.source = source
	p.fence = &fence
	if c.stage == rsCollectionDone {
		if !p.borrowed {
			if err = p.authority.ReadyPreparation(p.lease); err != nil {
				_ = p.closeLocked()
				return CollectionAdvance{}, cost, err
			}
		}
		p.complete = rsCollectionDone
		p.proof = rsCollectionProof(p)
		p.ready = true
		result.Ready = true
	}
	return result, cost, nil
}

// Count all retained cursor/fence values (including repeated string references
// conservatively) before transferring them from the current output custody.
func rsCollectionMetadataBytes(in CollectionInput, c rsCollectionCursor, f browseFence, source []any) int64 {
	n := int64(4096 + 1024)
	for _, v := range []string{in.View.Database, in.View.TokenHash, c.key, f.database, f.token, f.account, f.profile, f.membership, f.lifetime, f.library, f.role, f.memberLibraries, f.profileLibraries, f.profileLabels, f.expiresAt, f.horizon, f.root, f.bridgeRoot, f.policyWitness} {
		n += int64(len(v))
	}
	for _, v := range source {
		switch v := v.(type) {
		case string:
			n += int64(len(v))
		case []byte:
			n += int64(len(v))
		}
	}
	return n
}

// CollectionDetailCursor binds a continuation to the exact viewer, source,
// query and complete visible set. A subsequent request prepares current
// membership, then proves this cursor in the fresh authorized handoff; an
// offset alone is not a wire token.
type CollectionDetailCursor struct {
	fence      browseFence
	source     []any
	collection int64
	offset     int
	proof      [32]byte
}

// CollectionDetail contains the complete viewer-scoped count and a bounded
// page. Authored order is the exact (order_key,item_id) order. Revision is the
// membership/order revision, while Collection.Revision remains the entity
// revision. This is an internal component.
type CollectionDetail struct {
	Collection         BrowseItem
	Next               *CollectionDetailCursor
	LibraryID          string
	Revision           int64
	Origin             string
	ExactCount         int
	Items              []TitleItem
	Offset, NextOffset int
	Exhausted          bool
}

// Detail performs the first ready handoff under the ORIGINAL preparation
// lease. Preparing work cannot return a successful exact response. Caller
// retains separately admitted output through encoding and client IO. Detail
// closes the preparation even when it fails; a page with art starts a second
// preparation, because Artwork also consumes and closes the first one.
func (p *CollectionPreparation) Detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, after *CollectionDetailCursor) (CollectionDetail, BrowseReadResult, error) {
	if p == nil {
		return CollectionDetail{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	offset := 0
	if after != nil {
		offset = after.offset
	}
	if err := p.check(output, request); err != nil {
		_ = p.closeLocked()
		return CollectionDetail{}, BrowseReadResult{}, err
	}
	if p.borrowed || !p.ready || offset < 0 || offset > len(p.nodes) {
		return CollectionDetail{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, p.in.View.Now.Add(time.Since(p.started)), func(ctx context.Context, r governor.Request, b time.Duration, at time.Time) (CollectionDetail, BrowseReadResult, error) {
		if after != nil && (p.fence == nil || !rsCollectionSameFence(after.fence, *p.fence) || !equalSource(after.source, p.source) || after.collection != p.in.Collection || after.proof != p.proof) {
			_ = p.closeLocked()
			return CollectionDetail{}, BrowseReadResult{}, ErrStale
		}
		return p.detail(ctx, r, b, output, offset, at)
	})
}

// detail is the retained-state delivery for Detail, which holds p.mu across it.
func (p *CollectionPreparation) detail(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, offset int, at time.Time) (CollectionDetail, BrowseReadResult, error) {
	if err := p.check(output, request); err != nil {
		_ = p.closeLocked()
		return CollectionDetail{}, BrowseReadResult{}, err
	}
	if p.borrowed || !p.ready || offset < 0 || offset > len(p.nodes) {
		return CollectionDetail{}, BrowseReadResult{}, ErrInput
	}
	if p.lease.Deadline().Before(request.Deadline) {
		request.Deadline = p.lease.Deadline()
	}
	in := p.in.View
	in.Now = at
	in.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: p.in.Collection, state: p}
	type point struct {
		id   int64
		kind int64
	}
	points := make([]point, 0, 1+in.Limit)
	points = append(points, point{id: p.in.Collection})
	end := min(len(p.nodes), offset+in.Limit)
	for rank := offset; rank < end; rank++ {
		points = append(points, point{id: p.nodes[rank].id, kind: p.nodes[rank].kind})
	}
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 40, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	steps := []readStep[CollectionDetail]{func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
		if p.fence == nil || !rsCollectionSameFence(*p.fence, f) {
			return ErrStale
		}
		if _, e := rsCollectionRootProof(q, p.source, f); e != nil {
			return e
		}
		d.LibraryID = f.library
		d.Revision = p.source[3].(int64)
		d.Origin = "owner"
		d.ExactCount = len(p.nodes)
		d.Offset = offset
		d.NextOffset = end
		d.Exhausted = end == len(p.nodes)
		if !d.Exhausted {
			d.Next = &CollectionDetailCursor{fence: f, source: append([]any(nil), p.source...), collection: p.in.Collection, offset: end, proof: p.proof}
		}
		return p.authority.CheckPreparation(p.lease)
	}}
	// Four points per business scope, at most eleven exact-byte calls. Actual
	// lengths forecast the next payload scope and are rechecked before decoding.
	for first := 0; first < len(points); first += 4 {
		group := points[first:min(first+4, len(points))]
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
			var largest, total int64
			for _, pt := range group {
				sizes, e := q("rs.collection_complete.point_octets", in.Library, pt.id)
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
			return w.arm(max(largest, rsCollectionFixedRowBytes), uint16(5*len(group)))
		}, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionDetail) error {
			for _, pt := range group {
				rows, e := q("rs.collection_complete.point", in.Library, pt.id)
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
				if pt.id == p.in.Collection {
					if v[7].(int64) != 10 || v[4].(int64) != 0 {
						return ErrStale
					}
					d.Collection = item
					continue
				}
				visible, e := rsCollectionMemberEligible(q, []any{v[0], v[4], v[6], v[7], v[8]}, in, f)
				if e != nil {
					return e
				}
				if !visible || v[7].(int64) != pt.kind {
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
		_ = p.closeLocked()
		return CollectionDetail{}, cost, err
	}
	if err = p.closeLocked(); err != nil {
		return CollectionDetail{}, cost, err
	}
	return d, cost, nil
}

// Artwork delivers one collection art fact only after complete member reduction
// proves the root eligible. The same source footprint remains charged through
// its fresh art/source publication; output then owns the copied fact. Like
// Detail, Artwork closes the preparation, so a page that needs both runs a
// second complete enumeration for its art.
func (p *CollectionPreparation) Artwork(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, role string, pageSlot bool) (ArtworkFact, BrowseReadResult, error) {
	if p == nil {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(out, request); err != nil {
		return ArtworkFact{}, BrowseReadResult{}, err
	}
	if p.borrowed || !p.ready {
		return ArtworkFact{}, BrowseReadResult{}, ErrInput
	}
	in := ArtworkInput{View: p.in.View, Item: p.in.Collection, Role: role, PageSlot: pageSlot}
	in.View.Now = in.View.Now.Add(time.Since(p.started))
	in.View.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: p.in.Collection, state: p}
	if p.lease.Deadline().Before(request.Deadline) {
		request.Deadline = p.lease.Deadline()
	}
	art := &ForegroundReader{snapshots: p.reader.snapshots, pool: p.reader.pool}
	fact, cost, err := art.Artwork(ctx, request, min(budget, time.Until(p.lease.Deadline())), out, in)
	if err == nil {
		err = p.authority.HandoffPreparation(p.lease)
	}
	closeErr := p.closeLocked()
	if err != nil {
		return ArtworkFact{}, cost, err
	}
	if closeErr != nil {
		return ArtworkFact{}, cost, closeErr
	}
	return fact, cost, nil
}
