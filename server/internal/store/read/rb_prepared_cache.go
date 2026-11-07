package read

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// This private values-only cache is not a second scheduler. The governor alone
// admits its four bounded physical holdings and excludes conflicting publication.
// Closed source recipes capture/recheck authoritative fences and supply ordered
// IDs or exact numeric facets. No result is an authorization grant.
type rbPreparedCache struct {
	mu      sync.Mutex
	a       *governor.Authority
	now     func() time.Time
	entries [governor.PreparationMaxActive]*rbPreparedEntry
}
type rbPreparedPurpose uint8

const (
	rbPreparedOrder rbPreparedPurpose = iota + 1
	rbPreparedNumericFacets
)

type rbPreparedKey struct {
	owner governor.OwnerID
	query [32]byte // closed canonical query, including viewer/database/time anchor
}
type rbNumericFacet struct{ Value, Count int64 }
type rbPreparedEntry struct {
	ctx                                   context.Context
	dependencies                          governor.PublicationFootprint
	cache                                 *rbPreparedCache
	key                                   rbPreparedKey
	purpose                               rbPreparedPurpose
	lease                                 *governor.PreparationLease
	fence                                 browseFence
	captured, complete, delivered, closed bool
	ids                                   []int64
	facets                                []rbNumericFacet
	total                                 int64
}
type rbPreparedPage struct {
	ids       []int64
	facets    []rbNumericFacet
	total     int64
	next      int
	exhausted bool
}

var errRBPreparationCapacity = errors.New("prepared result exceeds admitted retained bytes")

func newRBPreparedCache(a *governor.Authority, now func() time.Time) (*rbPreparedCache, error) {
	if a == nil || now == nil {
		return nil, ErrInput
	}
	return &rbPreparedCache{a: a, now: now}, nil
}

// begin's context is the server-owned preparation lifetime, not a poll context.
// Repeated identical work attaches to the existing entry WITHOUT renewing its
// context, original deadline, retained-byte ceiling or governor lease.
func (c *rbPreparedCache) begin(ctx context.Context, key rbPreparedKey, purpose rbPreparedPurpose, deadline time.Time, bytes int64, dependencies governor.PublicationFootprint) (*rbPreparedEntry, error) {
	if c == nil || ctx == nil || key.owner == 0 || key.query == ([32]byte{}) || (purpose != rbPreparedOrder && purpose != rbPreparedNumericFacets) || bytes < 8192 || bytes%4096 != 0 || !governor.PublicationCovered(dependencies, dependencies) {
		return nil, ErrInput
	}
	if !c.mu.TryLock() {
		return nil, governor.ErrBusy
	}
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	free := -1
	for i, p := range c.entries {
		if p == nil {
			if free < 0 {
				free = i
			}
			continue
		}
		if !c.now().Before(p.lease.Deadline()) || p.ctx.Err() != nil {
			same := p.key == key
			terminal := p.ctx.Err()
			if terminal == nil {
				terminal = governor.ErrPreparationExpired
			}
			if err := p.closeLocked(); err != nil {
				return nil, err
			}
			if same {
				return nil, terminal
			}
			if free < 0 {
				free = i
			}
			continue
		}
		if p.key == key && !p.closed {
			if p.purpose != purpose || !slices.Equal(p.dependencies, dependencies) {
				return nil, ErrInput
			}
			if !p.delivered {
				if err := c.a.CheckPreparation(p.lease); err != nil {
					_ = p.closeLocked()
					return nil, err
				}
			}
			return p, nil
		}
	}
	if free < 0 {
		return nil, governor.ErrBusy
	}
	d := c.a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 399, ID: hex.EncodeToString(key.query[:])}, Owner: key.owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: dependencies})
	if d.Lease == nil {
		return nil, governor.ErrBusy
	}
	p := &rbPreparedEntry{ctx: ctx, cache: c, key: key, purpose: purpose, lease: d.Lease, dependencies: slices.Clone(dependencies)}
	// Payload allocation waits for the actual captured fence byte charge.
	c.entries[free] = p
	return p, nil
}
func (p *rbPreparedEntry) checkLocked() error {
	if p == nil || p.cache == nil || p.closed {
		return ErrInput
	}
	if err := p.ctx.Err(); err != nil {
		_ = p.closeLocked()
		return err
	}
	if !p.cache.now().Before(p.lease.Deadline()) {
		_ = p.closeLocked()
		return governor.ErrPreparationExpired
	}
	if !p.delivered {
		if err := p.cache.a.CheckPreparation(p.lease); err != nil {
			_ = p.closeLocked()
			return err
		}
	}
	return nil
}

// capture compares the WHOLE fence, including every registered policy recipe,
// subject and witness when the shared seam supplies those comparable fields.
// It is called from a fresh authorized snapshot, before retaining source values.
func (p *rbPreparedEntry) capture(f browseFence) error {
	if p == nil || p.cache == nil {
		return ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return err
	}
	if p.captured && p.fence != f {
		_ = p.closeLocked()
		return ErrStale
	}
	if !p.captured {
		// Charge ACTUAL retained strings (including future policy witnesses),
		// never columns times maximum field lengths. The separate read-window
		// reservation owns source copies while these cache copies are made.
		var stringsBytes int64
		value := reflect.ValueOf(f)
		for i := 0; i < value.NumField(); i++ {
			if field := value.Field(i); field.Kind() == reflect.String {
				stringsBytes += int64(field.Len())
			}
		}
		payload := p.lease.PayloadBytes() - 2*stringsBytes
		payload = payload / 4096 * 4096
		if payload <= 0 {
			_ = p.closeLocked()
			return errRBPreparationCapacity
		}
		// Current common-fence strings get independent exact-length backing.
		for _, field := range []*string{&f.database, &f.token, &f.account, &f.profile, &f.membership, &f.lifetime, &f.library, &f.role, &f.memberLibraries, &f.profileLibraries, &f.profileLabels, &f.expiresAt, &f.horizon, &f.root, &f.bridgeRoot} {
			*field = strings.Clone(*field)
		}
		// Fixed vectors avoid old+new growth copies and uncharged hash maps.
		if p.purpose == rbPreparedOrder {
			p.ids = make([]int64, 0, int(payload/8))
		} else {
			p.facets = make([]rbNumericFacet, 0, int(payload/16))
		}
	}
	p.fence = f
	p.captured = true
	return nil
}
func (p *rbPreparedEntry) retainIDs(ids []int64) error {
	if p == nil || p.cache == nil {
		return ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return err
	}
	if p.purpose != rbPreparedOrder || !p.captured || p.complete {
		return ErrInput
	}
	if len(ids) > cap(p.ids)-len(p.ids) {
		_ = p.closeLocked()
		return errRBPreparationCapacity
	}
	for _, id := range ids {
		if id <= 0 {
			_ = p.closeLocked()
			return ErrInput
		}
	}
	p.ids = append(p.ids, ids...)
	p.total += int64(len(ids))
	return nil
}

// Ordered numeric source streams make each value's count a contiguous run.
// Intersections/deduplication remain each closed producer's obligation; the
// cache never invents distinct counts from overlapping source memberships.
func (p *rbPreparedEntry) retainNumeric(value, count int64) error {
	if p == nil || p.cache == nil {
		return ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return err
	}
	if p.purpose != rbPreparedNumericFacets || !p.captured || p.complete || count < 1 {
		return ErrInput
	}
	if p.total > int64(^uint64(0)>>1)-count {
		_ = p.closeLocked()
		return ErrInput
	}
	n := len(p.facets)
	if n > 0 && value < p.facets[n-1].Value {
		_ = p.closeLocked()
		return ErrInput
	}
	if n > 0 && value == p.facets[n-1].Value {
		p.facets[n-1].Count += count
	} else {
		if n == cap(p.facets) {
			_ = p.closeLocked()
			return errRBPreparationCapacity
		}
		p.facets = append(p.facets, rbNumericFacet{value, count})
	}
	p.total += count
	return nil
}

// finish is private to exhausted closed source recipes. A full raw window is
// never exhaustion. No ready/exact result is published for a partial reduction.
func (p *rbPreparedEntry) finish(exhausted bool) error {
	if p == nil || p.cache == nil {
		return ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return err
	}
	if !p.captured || !exhausted {
		return ErrInput
	}
	if err := p.cache.a.ReadyPreparation(p.lease); err != nil {
		_ = p.closeLocked()
		return err
	}
	p.complete = true
	return nil
}

// handoff is invoked ONLY inside the fresh authorized response snapshot, AFTER
// its caller has acquired independent output custody. The full fence matches
// before ANY copy is returned, then the governor linearizes the first delivery.
// Later pages reauthorize/match the fence just as strictly; optional retention
// can never extend the original absolute deadline. No raw SQL capability escapes.
func (p *rbPreparedEntry) handoff(f browseFence, offset, limit int) (rbPreparedPage, error) {
	if p == nil || p.cache == nil {
		return rbPreparedPage{}, ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	if err := p.checkLocked(); err != nil {
		return rbPreparedPage{}, err
	}
	if !p.complete || offset < 0 || limit < 1 || limit > 200 {
		return rbPreparedPage{}, ErrInput
	}
	if p.fence != f {
		_ = p.closeLocked()
		return rbPreparedPage{}, ErrStale
	}
	n := len(p.ids)
	if p.purpose == rbPreparedNumericFacets {
		n = len(p.facets)
	}
	if offset > n {
		return rbPreparedPage{}, ErrInput
	}
	end := offset + min(limit, n-offset)
	out := rbPreparedPage{total: p.total, next: end, exhausted: end == n}
	if p.purpose == rbPreparedOrder {
		out.ids = append([]int64(nil), p.ids[offset:end]...)
	} else {
		out.facets = append([]rbNumericFacet(nil), p.facets[offset:end]...)
	}
	if !p.delivered {
		if err := p.cache.a.HandoffPreparation(p.lease); err != nil {
			_ = p.closeLocked()
			return rbPreparedPage{}, err
		}
		p.delivered = true
	}
	return out, nil
}
func (p *rbPreparedEntry) close() error {
	if p == nil || p.cache == nil {
		return ErrInput
	}
	p.cache.mu.Lock()
	defer p.cache.mu.Unlock()
	return p.closeLocked()
}
func (p *rbPreparedEntry) closeLocked() error {
	if p.closed {
		return nil
	}
	// Drop every owned buffer before attesting physical retirement. The source
	// and response drivers must already have joined their active operations.
	p.ids = nil
	p.facets = nil
	p.fence = browseFence{}
	p.closed = true
	if err := p.cache.a.CancelPreparation(p.lease); err != nil {
		return err
	}
	if err := p.cache.a.RetirePreparation(p.lease); err != nil {
		return err
	}
	for i, held := range p.cache.entries {
		if held == p {
			p.cache.entries[i] = nil
			break
		}
	}
	return nil
}
