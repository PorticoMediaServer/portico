package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// CollectionPreparation is caller-owned typed ephemeral work under RB's F04
// lease. It has no worker, queue, SQL handle, durable projection or renewal.
// Trusted composition serializes Advance/Detail/Close and keeps the job context
// alive between polls; an HTTP poll context must not own the job lifetime.
type CollectionPreparation struct {
	owner         governor.OwnerID
	reader        *CollectionReader
	authority     *governor.Authority
	lease         *governor.PreparationLease
	in            CollectionInput
	fence         *browseFence
	source        []any
	cursor        rsAutoCursor
	nodes         []rsCollectionNode
	root          int32
	ready, closed bool
	nonempty      bool
	proof         [32]byte
}
type rsAutoCursor struct {
	stage                                            int
	provider, external, evidence, afterEvidence, key string
	origin, local, override, item, ordinal           int64
}

const (
	rsAutoIdentity = iota
	rsAutoEvidence
	rsAutoOrigins
	rsAutoLocal
	rsAutoOverrides
	rsAutoAuthored
	rsAutoDone
)

// CollectionAdvance distinguishes pending work from an exact result. No member
// values or partial total are exposed before the complete reduction is ready.
type CollectionAdvance struct {
	Ready        bool
	RawPositions int
}

// BeginCollectionPreparation captures an immutable query and conservatively
// leases all publication in its library, including provider/policy changes.
// Capacity is explicit, never a successful truncation. All tree storage is
// admitted before allocation; the remaining allowance funds cursors and fences.
func (r *CollectionReader) BeginCollectionPreparation(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, in CollectionInput) (*CollectionPreparation, *governor.Refusal, error) {
	if r == nil || a == nil || ctx == nil || in.Collection <= 0 || len(in.View.Database) == 0 || len(in.View.Database) > 256 || len(in.View.TokenHash) == 0 || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.Kind != 10 || in.View.After != nil || in.After != nil || in.View.Descending || in.View.KeepArticles || in.View.Limit < 1 || in.View.Limit > MaxPage || bytes < governor.PreparationMetadataBytes+16384 {
		return nil, nil, ErrInput
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%d/%d/%d/%d", in.View.Database, in.View.TokenHash, in.View.Library, in.Collection, in.View.policy.recipe, in.View.policy.subject)))
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("collection:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{Library: in.View.Library}}})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	capacity := int((d.Lease.PayloadBytes() - 16384) / int64(unsafe.Sizeof(rsCollectionNode{})))
	in.View.Database = strings.Clone(in.View.Database)
	in.View.TokenHash = strings.Clone(in.View.TokenHash)
	p := &CollectionPreparation{owner: owner, reader: r, authority: a, lease: d.Lease, in: in, nodes: make([]rsCollectionNode, 0, capacity)}
	p.in.View.policy = readPolicyBinding{recipe: rsCollectionComputePolicy, subject: in.Collection, state: p}
	return p, nil, nil
}
func (p *CollectionPreparation) Deadline() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.lease.Deadline()
}

// Close drops every retained value before attesting physical retirement. Output
// values already copied into BrowseOutput are independently admitted custody.
func (p *CollectionPreparation) Close() error {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	p.nodes = nil
	p.source = nil
	p.fence = nil
	p.cursor = rsAutoCursor{}
	p.in = CollectionInput{}
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
	rows, err := q("rs.auto.source", p.in.Collection, p.in.View.Library)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrDenied
	}
	if len(rows) != 1 {
		return nil, ErrUnqualified
	}
	if rows[0][8] != nil && rows[0][8].(int64) != 0 {
		return nil, ErrDenied
	}
	source := rows[0]
	if source[8] != nil {
		identity, e := q("rs.auto.identity", p.in.Collection, p.in.View.Library)
		if e != nil {
			return nil, e
		}
		if len(identity) != 1 {
			return nil, ErrStale
		}
		if len(identity[0][0].(string))+len(identity[0][1].(string)) > 4096 {
			return nil, governor.ErrSnapshotBudget
		}
		source = append(source, identity[0]...)
	}
	if p.source != nil && !equalSource(p.source, source) {
		return nil, ErrStale
	}
	return source, nil
}
func rsCollectionVisible(row []any, f browseFence) (bool, error) {
	if row[1].(int64) != 0 {
		return false, nil
	}
	if row[4].(int64) != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return false, ErrUnqualified
	}
	if row[2] == nil {
		return f.allowUnrated != 0, nil
	}
	return f.maximumAge < 0 || row[2].(int64) <= f.maximumAge, nil
}
func (p *CollectionPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput) (CollectionAdvance, BrowseReadResult, error) {
	if err := p.check(output, request); err != nil {
		return CollectionAdvance{}, BrowseReadResult{}, err
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, p.in.View.Now, func(ctx context.Context, r governor.Request, b time.Duration, at time.Time) (CollectionAdvance, BrowseReadResult, error) {
		return p.advance(ctx, r, b, output, at)
	})
}
func (p *CollectionPreparation) advance(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, at time.Time) (CollectionAdvance, BrowseReadResult, error) {
	if err := p.check(output, request); err != nil {
		_ = p.Close()
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
	var raw [][]any
	var accepted []int64
	nonempty := p.nonempty
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 40}
	root := func(q browseQuery) ([]any, error) { var e error; source, e = p.rootRead(q); return source, e }
	// Every quantum has one bounded source position, at most four candidate
	// points, then a fresh authorization/source publication check.
	steps := []readStep[CollectionAdvance]{func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
		if p.fence != nil && *p.fence != f {
			return ErrStale
		}
		fence = f
		return p.authority.CheckPreparation(p.lease)
	}}
	var name string
	var args []any
	switch c.stage {
	case rsAutoIdentity:
		if p.source != nil {
			return CollectionAdvance{}, BrowseReadResult{}, ErrInput
		}
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			if source[8] == nil {
				c.stage = rsAutoAuthored
				return nil
			}
			c.provider = source[len(source)-2].(string)
			c.external = source[len(source)-1].(string)
			c.stage = rsAutoEvidence
			if c.provider == "nfo" {
				c.stage = rsAutoLocal
			}
			return nil
		})
	case rsAutoEvidence:
		name = "evidence"
		args = []any{c.provider, c.external, c.afterEvidence}
	case rsAutoAuthored:
		name = "authored_tail"
		args = []any{p.in.Collection, c.key, c.item}
	default:
	}
	if name != "" {
		var found bool
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			sizes, e := q("rs.auto."+name+"_octets", args...)
			if e != nil {
				return e
			}
			if len(sizes) == 0 && c.stage == rsAutoAuthored {
				name = "authored_next"
				args = []any{p.in.Collection, c.key}
				sizes, e = q("rs.auto."+name+"_octets", args...)
				if e != nil {
					return e
				}
			}
			if len(sizes) == 0 {
				if c.stage == rsAutoEvidence {
					c.stage = rsAutoOverrides
				} else {
					c.stage = rsAutoDone
				}
				return nil
			}
			if len(sizes) != 1 {
				return ErrUnqualified
			}
			n, e := rsPointBytes(sizes[0])
			if e != nil {
				return e
			}
			if n > 4096 {
				return governor.ErrSnapshotBudget
			}
			found = true
			return w.arm(max(n, 1), 8)
		}, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			if !found {
				return nil
			}
			rows, e := q("rs.auto."+name, args...)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			if c.stage == rsAutoEvidence {
				c.evidence = rows[0][0].(string)
				c.origin = 0
				c.stage = rsAutoOrigins
				if strings.Trim(rows[0][1].(string), " ") == "" {
					c.afterEvidence = c.evidence
					c.evidence = ""
					c.stage = rsAutoEvidence
				}
				d.RawPositions++
			} else {
				c.item = rows[0][0].(int64)
				c.key = rows[0][1].(string)
				raw = [][]any{{c.item}}
				d.RawPositions++
			}
			return nil
		})
	}
	if c.stage == rsAutoOrigins || c.stage == rsAutoLocal || c.stage == rsAutoOverrides {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
			var rows [][]any
			var e error
			switch c.stage {
			case rsAutoOrigins:
				rows, e = q("rs.auto.origins", c.evidence, c.origin, int64(4))
			case rsAutoLocal:
				rows, e = q("rs.auto.local", in.Library, c.external, c.local, int64(4))
			case rsAutoOverrides:
				rows, e = q("rs.auto.overrides", p.in.Collection, c.override, int64(4))
			}
			if e != nil {
				return e
			}
			if len(rows) > 4 {
				return ErrUnqualified
			}
			raw = rows
			d.RawPositions += len(rows)
			if len(rows) == 0 {
				switch c.stage {
				case rsAutoOrigins:
					c.afterEvidence = c.evidence
					c.evidence = ""
					c.stage = rsAutoEvidence
				case rsAutoLocal:
					c.stage = rsAutoOverrides
				case rsAutoOverrides:
					c.stage = rsAutoDone
				}
			}
			return nil
		})
	}
	steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *CollectionAdvance) error {
		for _, row := range raw {
			id := row[0].(int64)
			switch c.stage {
			case rsAutoOrigins:
				c.origin = id
			case rsAutoLocal:
				c.local = id
			case rsAutoOverrides:
				c.override = id
			}
			if c.stage == rsAutoOverrides && row[1].(int64) != 1 {
				continue
			}
			if c.stage == rsAutoOrigins || c.stage == rsAutoLocal {
				acceptedName := "rs.auto.accepted"
				if c.stage == rsAutoLocal {
					acceptedName = "rs.auto.accepted_local"
				}
				bindings := []any{p.in.Collection, in.Library, id, c.provider, c.external}
				if c.stage == rsAutoOrigins {
					bindings = append(bindings, c.evidence)
				}
				rows, e := q(acceptedName, bindings...)
				if e != nil {
					return e
				}
				if len(rows) == 0 {
					continue
				}
				if len(rows) != 1 {
					return ErrUnqualified
				}
			}
			rows, e := q("rs.auto.member", id, in.Library)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				continue
			}
			if len(rows) != 1 {
				return ErrUnqualified
			}
			if c.stage != rsAutoAuthored && rows[0][3].(int64) != 1 {
				continue
			}
			if rows[0][1].(int64) == 0 {
				nonempty = true
			}
			visible, e := rsCollectionMemberEligible(q, rows[0], in, f)
			if e != nil {
				return e
			}
			if visible {
				accepted = append(accepted, id)
			}
		}
		return ctx.Err()
	})
	result, cost, err := publishRead(ctx, w, request, min(budget, time.Until(p.lease.Deadline())), output, in, steps, root)
	if err == nil {
		err = p.authority.CheckPreparation(p.lease)
	}
	if err != nil {
		_ = p.Close()
		return CollectionAdvance{}, cost, err
	}
	// Admission covers the worst case BEFORE mutating retained storage. Refuse
	// conservatively even if a full tree might have contained only duplicates.
	if rsCollectionMetadataBytes(p.in, c, fence, source) > 16384 || len(accepted) > cap(p.nodes)-len(p.nodes) {
		_ = p.Close()
		return CollectionAdvance{}, cost, governor.ErrSnapshotBudget
	}
	for _, id := range accepted {
		key := id
		if c.stage == rsAutoAuthored {
			c.ordinal++
			key = c.ordinal
		}
		p.root = rsNodeInsert(&p.nodes, p.root, key, id)
	}
	p.nonempty = nonempty
	p.cursor = c
	p.source = source
	p.fence = &fence
	if c.stage == rsAutoDone {
		if err = p.authority.ReadyPreparation(p.lease); err != nil {
			_ = p.Close()
			return CollectionAdvance{}, cost, err
		}
		p.proof = rsCollectionProof(p)
		p.ready = true
		result.Ready = true
	}
	return result, cost, nil
}

// Count all retained cursor/fence values (including repeated string references
// conservatively) before transferring them from the current output custody.
func rsCollectionMetadataBytes(in CollectionInput, c rsAutoCursor, f browseFence, source []any) int64 {
	n := int64(unsafe.Sizeof(CollectionPreparation{})+unsafe.Sizeof(rsAutoCursor{})+unsafe.Sizeof(browseFence{})) + int64(len(source))*32
	for _, v := range []string{in.View.Database, in.View.TokenHash, c.provider, c.external, c.evidence, c.afterEvidence, c.key, f.database, f.token, f.account, f.profile, f.membership, f.lifetime, f.library, f.role, f.memberLibraries, f.profileLibraries, f.profileLabels, f.expiresAt, f.horizon, f.root, f.bridgeRoot, f.policyWitness} {
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
