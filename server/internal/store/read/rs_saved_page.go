package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A saved COLLECTION is user-authored order over saved_resource_entries. The
// member page enumerates the complete current order before it reports an exact
// visible total, then hydrates only one bounded page of cards. Hidden,
// retired, cross-library and unreadable members still advance the raw order and
// are counted only when their own current subject tuple is visible.
const (
	rsSavedEntryWindow = 1
)

const (
	rsSavedMetadata = iota
	rsSavedEntry
	rsSavedEligible
	rsSavedAutomatic
	rsSavedCard
	rsSavedReading
	rsSavedArt
	rsSavedFinish
	rsSavedDone
)

func rsSavedEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	p, ok := in.policy.state.(*SavedCollectionWork)
	if !ok || p == nil || p.closed || p.lease == nil || p.view.Database != in.Database || p.view.TokenHash != in.TokenHash || in.Library != 0 || in.Kind != 1 || in.policy.subject != 0 {
		return "", ErrInput
	}
	if err := p.authority.CheckPreparation(p.lease); err != nil {
		return "", err
	}
	if p.fence != nil && !rsCollectionSameFence(*p.fence, f) {
		return "", ErrStale
	}
	rows, err := q("rs.saved.members.generation")
	if err != nil {
		return "", err
	}
	if len(rows) > 1 {
		return "", ErrUnqualified
	}
	var generation int64
	if len(rows) == 1 {
		generation = rows[0][0].(int64)
	}
	if p.captured && p.generation != generation || p.after != nil && (p.after.generation != generation || !rsCollectionSameFence(p.after.fence, f)) {
		return "", ErrStale
	}
	if in.policy.recipe == rsSavedDeliver {
		if !p.ready || p.stage != rsSavedDone || p.proof != p.digest() {
			return "", ErrUnqualified
		}
		return fmt.Sprintf("%d:%x", generation, p.proof), nil
	}
	if in.policy.recipe != rsSavedCompute || p.ready {
		return "", ErrInput
	}
	return strconv.FormatInt(generation, 10), nil
}

func (r *SavedCollectionReader) beginSaved(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, view BrowseInput, resource string, after *SavedCollectionCursor) (*SavedCollectionWork, *governor.Refusal, error) {
	if r == nil || a == nil || !r.pool.OwnedBy(a) || ctx == nil || owner == 0 || view.Now.IsZero() || view.Limit < 1 || view.Limit > MaxPage || len(view.Database) == 0 || len(view.Database) > 256 || len(view.TokenHash) == 0 || len(view.TokenHash) > 256 || view.After != nil || view.Descending || view.KeepArticles || bytes < 32768 {
		return nil, nil, ErrInput
	}
	// Members are selected by the stored resource, never by a caller library
	// hint. Every member is reauthorized against its own current library/kind.
	view.Library, view.Kind = 0, 1
	view.policy = readPolicyBinding{}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%d/%q/%d", view.Database, view.TokenHash, owner, resource, view.Limit)))
	if after != nil && after.identity != identity {
		return nil, nil, ErrStale
	}
	position := "first"
	if after != nil {
		position = fmt.Sprintf("%x/%q/%q", after.proof, after.key, after.id)
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%x/%s", identity, position)))
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("saved-members:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	view.Database, view.TokenHash = strings.Clone(view.Database), strings.Clone(view.TokenHash)
	p := &SavedCollectionWork{reader: r, authority: a, lease: d.Lease, job: ctx, owner: owner, started: time.Now(), view: view, resource: strings.Clone(resource), identity: identity, stage: rsSavedMetadata, membersHash: sha256.New(), used: 16384}
	p.current.resource.ID = p.resource
	p.current.resource.Shape = "posters"
	if after != nil {
		if e := p.charge(rsSavedDataBytes(*after)); e != nil {
			_ = p.close()
			return nil, nil, e
		}
		c := *after
		c.id, c.key = strings.Clone(c.id), strings.Clone(c.key)
		p.after = &c
	}
	p.view.policy = readPolicyBinding{recipe: rsSavedCompute, state: p}
	return p, nil, nil
}

// Close joins an in-flight poll and retires the original lease exactly once.
// Retained values are dropped before physical retirement is attested.
func (p *SavedCollectionWork) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}

func (p *SavedCollectionWork) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.current.inner != nil {
		_ = p.current.inner.Close()
	}
	p.records, p.entries, p.source, p.fence = nil, nil, nil, nil
	p.current = rsSavedCurrent{}
	p.view = BrowseInput{}
	p.after = nil
	p.resource = ""
	p.membersHash = nil
	p.job = nil
	_ = p.authority.CancelPreparation(p.lease)
	return p.authority.RetirePreparation(p.lease)
}

func (p *SavedCollectionWork) check(req governor.Request, out BrowseOutput) error {
	if p == nil || p.closed || p.reader == nil || out.Authority != p.authority || req.Owner != p.owner || out.Request.Owner != p.owner {
		return ErrInput
	}
	return p.authority.CheckPreparation(p.lease)
}

func (p *SavedCollectionWork) capacity(extra int64) error {
	cursors := int64(4 * (len(p.current.entryID) + len(p.current.key) + len(p.current.resource.ID)))
	if extra < 0 || p.used+extra+cursors > p.lease.PayloadBytes() {
		return governor.ErrSnapshotBudget
	}
	return nil
}

func (p *SavedCollectionWork) charge(n int64) error {
	if err := p.capacity(n); err != nil {
		return err
	}
	p.used += n
	return nil
}

// rsSavedPermission verifies the current owner token, server visibility or a
// current share row. A missing resource, a VIEW resource, a deleted row or a
// foreign private collection is not permitted. Each query returns at most one
// row and refuses an ambiguous source.
func rsSavedPermission(q browseQuery, token, resource string) ([]any, bool, bool, error) {
	var source []any
	permitted, owned := false, false
	for _, name := range []string{"source", "server", "share"} {
		args := []any{token, resource}
		if name == "server" {
			args = []any{resource}
		}
		rows, err := q("rs.saved.members."+name, args...)
		if err != nil {
			return nil, false, false, err
		}
		if len(rows) > 1 {
			return nil, false, false, ErrUnqualified
		}
		source = append(source, int64(len(rows)))
		if len(rows) == 1 {
			source = append(source, rows[0]...)
			permitted = true
			if name == "source" {
				owned = true
			}
		}
	}
	return source, permitted, owned, nil
}

func (p *SavedCollectionWork) run(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time, w *rsByteWindow, steps []readStep[struct{}]) (BrowseReadResult, error) {
	in := p.view
	in.Now = at
	var fence browseFence
	var source []any
	guard := func(_ context.Context, _ browseQuery, f browseFence, _ *struct{}) error {
		if p.fence != nil && *p.fence != f {
			return ErrStale
		}
		fence = f
		return p.authority.CheckPreparation(p.lease)
	}
	steps = append([]readStep[struct{}]{guard}, steps...)
	roots := []rootRead{func(q browseQuery) ([]any, error) {
		s, allowed, _, err := rsSavedPermission(q, p.view.TokenHash, p.resource)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrDenied
		}
		if p.source != nil && !equalSource(p.source, s) {
			return nil, ErrStale
		}
		source = s
		return s, nil
	}}
	_, cost, err := publishRead(ctx, w, req, min(budget, time.Until(p.lease.Deadline())), out, in, steps, roots...)
	if err != nil {
		return cost, err
	}
	if !p.captured {
		if err = p.charge(rsSavedDataBytes(fence)); err != nil {
			return cost, err
		}
		p.fence = &fence
		p.generation, err = strconv.ParseInt(fence.policyWitness, 10, 64)
		if err != nil {
			return cost, ErrUnqualified
		}
		p.captured = true
	}
	if p.source == nil {
		p.source = source
	}
	return cost, p.capacity(0)
}

func rsSavedDataBytes(v any) int64 {
	var size func(reflect.Value) int64
	size = func(v reflect.Value) int64 {
		if !v.IsValid() {
			return 0
		}
		n := int64(v.Type().Size())
		switch v.Kind() {
		case reflect.String:
			n += 4 * int64(v.Len())
		case reflect.Pointer:
			if !v.IsNil() {
				n += size(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				n += size(v.Field(i))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				n += size(v.Index(i))
			}
		}
		return n
	}
	return size(reflect.ValueOf(v))
}

func rsSavedClone[T any](v T) T {
	var copyValue func(reflect.Value) reflect.Value
	copyValue = func(v reflect.Value) reflect.Value {
		switch v.Kind() {
		case reflect.String:
			return reflect.ValueOf(strings.Clone(v.String())).Convert(v.Type())
		case reflect.Pointer:
			if v.IsNil() {
				return reflect.Zero(v.Type())
			}
			x := reflect.New(v.Type().Elem())
			x.Elem().Set(copyValue(v.Elem()))
			return x
		case reflect.Slice:
			if v.IsNil() {
				return reflect.Zero(v.Type())
			}
			x := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				x.Index(i).Set(copyValue(v.Index(i)))
			}
			return x
		case reflect.Struct:
			x := reflect.New(v.Type()).Elem()
			for i := 0; i < v.NumField(); i++ {
				x.Field(i).Set(copyValue(v.Field(i)))
			}
			return x
		default:
			return v
		}
	}
	return copyValue(reflect.ValueOf(v)).Interface().(T)
}

// rsSavedPrincipal keeps only the authenticated principal birth/lifetime facts.
// A member's own library/kind/policy fence must never be compared with the
// saved resource's principal fence.
func rsSavedPrincipal(f browseFence) browseFence {
	f.library = ""
	f.libraryID = 0
	f.kind = 1
	f.container = 0
	f.dataRevision = 0
	f.orderRevision = 0
	f.metadataRevision = 0
	f.viewerRevision = 0
	f.ageRevision = 0
	f.root = ""
	f.bridgeRoot = ""
	f.originIncarnation = [16]byte{}
	f.originRevision = 0
	f.policy = 0
	f.policySubject = 0
	f.policyWitness = ""
	return f
}

func (p *SavedCollectionWork) digest() [32]byte {
	h := sha256.New()
	for _, r := range p.records {
		fmt.Fprintf(h, "%q/%q/%q/%q/%d/%q/%d/%q/", r.ID, r.Name, r.Summary, r.CreatedAt, r.Revision, r.Visibility, r.EntryCount, r.Shape)
	}
	_, _ = h.Write(p.membersHash.Sum(nil))
	fmt.Fprintf(h, "%d", p.total)
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}

func (p *SavedCollectionWork) markEligible(out BrowseOutput) error {
	c := &p.current
	fmt.Fprintf(p.membersHash, "%q/%q/%q/%d/%d/%d/", c.resource.ID, c.entryID, c.key, c.item, c.library, c.kind)
	c.resource.EntryCount++
	p.total++
	if c.resource.EntryCount == 1 {
		c.resource.Shape = "posters"
		switch c.kind {
		case 5, 6, 7, 8, 9, 13:
			c.resource.Shape = "squares"
		}
	}
	beyond := p.after == nil || c.key > p.after.key || c.key == p.after.key && c.entryID > p.after.id
	shown := beyond && len(p.entries) < p.view.Limit
	if beyond && !shown {
		p.more = true
	}
	if shown {
		p.stage = rsSavedCard
		return nil
	}
	if c.inner != nil {
		p.closeInner()
	}
	p.stage = rsSavedEntry
	return nil
}

func (p *SavedCollectionWork) advance(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time) (BrowseReadResult, error) {
	if err := p.check(req, out); err != nil {
		return BrowseReadResult{}, err
	}
	if p.ready {
		return BrowseReadResult{}, nil
	}
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 768, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	var steps []readStep[struct{}]
	point := func(name string, args []any, consume func([]any) error) {
		var exists bool
		steps = append(steps, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			sizes, e := q("rs.saved.members."+name+"_octets", args...)
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
			exists = true
			return w.arm(max(n, 1), 8)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !exists {
				return ErrStale
			}
			rows, e := q("rs.saved.members."+name, args...)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			return consume(rows[0])
		})
	}
	switch p.stage {
	case rsSavedMetadata:
		var allowed, owned bool
		var role string
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			_, a, o, e := rsSavedPermission(q, p.view.TokenHash, p.current.resource.ID)
			if e != nil {
				return e
			}
			allowed, owned = a, o
			if !allowed {
				return ErrDenied
			}
			if !owned {
				r, e := q("rs.saved.members.share_role", p.current.resource.ID, f.account, f.profile)
				if e != nil {
					return e
				}
				if len(r) == 1 {
					role = r[0][0].(string)
				}
			}
			return nil
		})
		var sizes [][]any
		steps = append(steps, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !allowed {
				return nil
			}
			var e error
			sizes, e = q("rs.saved.members.metadata_octets", p.current.resource.ID)
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
			return w.arm(max(n, 1), 8)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !allowed {
				return nil
			}
			rows, e := q("rs.saved.members.metadata", p.current.resource.ID)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			v := rows[0]
			r := p.current.resource
			r.Name, r.Summary, r.CreatedAt, r.Revision, r.Visibility = v[0].(string), v[1].(string), v[2].(string), v[3].(int64), v[4].(string)
			r.Role = "viewer"
			if owned {
				r.Role = "owner"
			} else if role == "editor" {
				r.Role = "editor"
			}
			if e = p.charge(512 + rsSavedDataBytes(r)); e != nil {
				return e
			}
			p.current.resource = r
			p.stage = rsSavedEntry
			return nil
		})
	case rsSavedEntry:
		var row int64
		steps = append(steps, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			c := &p.current
			var rows [][]any
			var e error
			limit := int64(rsSavedEntryWindow + 1)
			if !c.memberStarted {
				rows, e = q("rs.saved.members.entry.first", c.resource.ID, limit)
			} else {
				rows, e = q("rs.saved.members.entry.tail", c.resource.ID, c.key, c.entryID, limit)
				if e == nil && len(rows) == 0 {
					rows, e = q("rs.saved.members.entry.next", c.resource.ID, c.key, limit)
				}
			}
			if e != nil {
				return e
			}
			// The extra row only proves the continuation exists; the tail seek
			// re-reads it from the exact (order_key,id) cursor in the next step.
			if len(rows) > rsSavedEntryWindow+1 {
				return ErrUnqualified
			}
			if len(rows) == 0 {
				p.stage = rsSavedFinish
				return nil
			}
			row = rows[0][0].(int64)
			return nil
		})
		var exists bool
		steps = append(steps, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if row == 0 {
				return nil
			}
			sizes, e := q("rs.saved.members.entry_octets", row, p.current.resource.ID)
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
			exists = true
			return w.arm(max(n, 1), 2)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !exists {
				return nil
			}
			rows, e := q("rs.saved.members.entry", row, p.current.resource.ID)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			v := rows[0]
			p.current.entryID, p.current.key = v[0].(string), v[1].(string)
			p.current.memberStarted = true
			p.current.item = 0
			if v[2] != nil {
				p.current.item = v[2].(int64)
			}
			p.stage = rsSavedEligible
			return nil
		})
	case rsSavedEligible:
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			c := &p.current
			if c.item == 0 {
				p.stage = rsSavedEntry
				return nil
			}
			rows, e := q("rs.saved.members.item", c.item)
			if e != nil {
				return e
			}
			if len(rows) == 0 || rows[0][3].(int64) != 0 {
				p.stage = rsSavedEntry
				return nil
			}
			if len(rows) != 1 {
				return ErrUnqualified
			}
			c.library, c.kind = rows[0][1].(int64), rows[0][2].(int64)
			c.view = p.view
			c.view.Library, c.view.Kind, c.view.Now = c.library, c.kind, at
			c.view.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: c.item}
			if c.kind == 20 || c.kind == 21 {
				c.view.policy.recipe = rsReadingNativePolicy
				if p.reader.mobi {
					c.view.policy.recipe = rsReadingMobiPolicy
				}
			}
			if c.kind == 10 {
				automatic, e := q("rs.subject.automatic", c.item)
				if e != nil {
					return e
				}
				if len(automatic) != 0 {
					p.stage = rsSavedAutomatic
					return nil
				}
			}
			child, e := readBrowseFence(q, c.view)
			if errors.Is(e, ErrDenied) {
				p.stage = rsSavedEntry
				return nil
			}
			if e != nil {
				return e
			}
			if rsSavedPrincipal(child) != rsSavedPrincipal(f) {
				return ErrStale
			}
			names, e := q("rs.saved.members.library_name", child.library)
			if e != nil {
				return e
			}
			if len(names) != 1 {
				return ErrStale
			}
			c.libraryName = names[0][0].(string)
			return p.markEligible(out)
		})
	case rsSavedAutomatic:
		// The complete automatic collection reducer owns inferred R21
		// membership. It is a separate task (rs_auto_collection*); until that
		// reducer is composed beside this file, the merged complete authored
		// reducer refuses an automatic root with ErrUnqualified rather than
		// inventing an authored empty set. The nested work borrows this job's
		// existing lease and payload charge; it never takes a second lease,
		// ledger or renewal.
		if p.current.inner == nil {
			const reserve = int64(1 << 20)
			if e := p.charge(reserve); e != nil {
				return BrowseReadResult{}, e
			}
			r := &CollectionReader{snapshots: p.reader.snapshots, pool: p.reader.pool}
			child := &CollectionPreparation{borrowed: true, started: p.started, owner: p.owner, reader: r, authority: p.authority, lease: p.lease, in: CollectionInput{View: p.current.view, Collection: p.current.item}, nodes: make([]rsCollectionNode, 0, int((reserve-16384)/int64(unsafe.Sizeof(rsCollectionNode{}))))}
			child.in.View.policy = readPolicyBinding{recipe: rsCollectionComputePolicy, subject: p.current.item, state: child}
			p.current.inner = child
			p.current.innerBytes = reserve
		}
		step, cost, e := p.current.inner.advance(ctx, req, min(budget, time.Until(p.lease.Deadline())), out, at)
		if errors.Is(e, ErrDenied) {
			p.closeInner()
			p.stage = rsSavedEntry
			return cost, nil
		}
		if e != nil {
			return cost, e
		}
		if !step.Ready {
			return cost, nil
		}
		c := &p.current
		if c.inner.nonempty && len(c.inner.nodes) == 0 {
			p.closeInner()
			p.stage = rsSavedEntry
			return cost, nil
		}
		if p.fence == nil || rsSavedPrincipal(*c.inner.fence) != rsSavedPrincipal(*p.fence) {
			return cost, ErrStale
		}
		c.libraryName = c.inner.fence.library
		c.view.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: c.item, state: c.inner}
		return cost, p.markEligible(out)
	case rsSavedCard:
		point("card", []any{p.fence.profile, p.current.item, p.current.library, p.current.kind}, func(v []any) error {
			item, e := itemValue(v)
			if e != nil {
				return e
			}
			c := &p.current
			c.card = SavedCollectionEntry{EntryID: c.entryID, OrderKey: c.key, Library: c.libraryName, Item: TitleItem{BrowseItem: item, Kind: c.kind, Overview: v[8].(string)}, State: stateValue(v[9:])}
			if e = p.charge(512 + rsSavedDataBytes(c.card)); e != nil {
				return e
			}
			p.stage = rsSavedArt
			if c.kind == 20 || c.kind == 21 {
				p.stage = rsSavedReading
			}
			return nil
		})
	case rsSavedReading:
		r, _ := NewRSReadingReader(p.reader.pool)
		r.snapshots = p.reader.snapshots
		r.mobi = p.reader.mobi
		in := ReadingFactsInput{View: p.current.view, Item: p.current.item}
		in.View.Now = at
		facts, cost, e := r.facts(ctx, req, min(budget, time.Until(p.lease.Deadline())), out, in)
		if e != nil {
			return cost, e
		}
		if e = p.charge(512 + rsSavedDataBytes(facts)); e != nil {
			return cost, e
		}
		p.current.card.Reading = &facts
		p.stage = rsSavedArt
		return cost, p.authority.CheckPreparation(p.lease)
	case rsSavedArt:
		r := &ForegroundReader{snapshots: p.reader.snapshots, pool: p.reader.pool}
		in := ArtworkInput{View: p.current.view, Item: p.current.item, Role: "poster", PageSlot: true}
		in.View.Now = at
		art, cost, e := r.artwork(ctx, req, min(budget, time.Until(p.lease.Deadline())), out, in)
		if errors.Is(e, ErrNotReady) {
			p.current.card.ArtworkPending = true
			e = nil
		} else if e == nil {
			p.current.card.Artwork = art
		}
		if e != nil {
			return cost, e
		}
		if e = p.charge(256 + rsSavedDataBytes(art)); e != nil {
			return cost, e
		}
		p.entries = append(p.entries, p.current.card)
		p.current.card = SavedCollectionEntry{}
		if p.current.inner != nil {
			p.closeInner()
		}
		p.stage = rsSavedEntry
		return cost, p.authority.CheckPreparation(p.lease)
	case rsSavedFinish:
		p.records = append(p.records, p.current.resource)
		p.current = rsSavedCurrent{}
		p.stage = rsSavedDone
	case rsSavedDone:
	default:
		return BrowseReadResult{}, ErrInput
	}
	cost, err := p.run(ctx, req, budget, out, at, w, steps)
	if err != nil {
		return cost, err
	}
	if p.stage == rsSavedDone {
		p.proof = p.digest()
		if p.after != nil && p.after.proof != p.proof {
			return cost, ErrStale
		}
		if err = p.authority.ReadyPreparation(p.lease); err != nil {
			return cost, err
		}
		p.ready = true
	}
	return cost, p.authority.CheckPreparation(p.lease)
}

func (p *SavedCollectionWork) deliver(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time) (SavedCollectionPage, BrowseReadResult, error) {
	if !p.ready {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	view := p.view
	view.Now = at
	view.policy = readPolicyBinding{recipe: rsSavedDeliver, state: p}
	roots := []rootRead{func(q browseQuery) ([]any, error) {
		s, allowed, _, e := rsSavedPermission(q, view.TokenHash, p.resource)
		if e != nil {
			return nil, e
		}
		if !allowed {
			return nil, ErrDenied
		}
		if !equalSource(s, p.source) {
			return nil, ErrStale
		}
		return s, nil
	}}
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 16, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	page, cost, err := publishRead(ctx, w, req, min(budget, time.Until(p.lease.Deadline())), out, view, []readStep[SavedCollectionPage]{func(_ context.Context, _ browseQuery, f browseFence, d *SavedCollectionPage) error {
		if p.fence == nil || !rsCollectionSameFence(*p.fence, f) {
			return ErrStale
		}
		if len(p.entries) > p.view.Limit {
			return ErrUnqualified
		}
		var lastKey, lastID string
		d.Total = p.total
		if len(p.entries) > 0 {
			last := p.entries[len(p.entries)-1]
			lastKey, lastID = last.OrderKey, last.EntryID
		}
		bytes := rsSavedDataBytes(p.records) + rsSavedDataBytes(p.entries) + rsSavedDataBytes(f) + 2048
		if bytes > out.Request.Need[governor.MemoryBytes]/2 {
			return governor.ErrSnapshotBudget
		}
		d.Resources = rsSavedClone(p.records)
		d.Entries = rsSavedClone(p.entries)
		for _, e := range d.Entries {
			d.Items = append(d.Items, e.Item)
		}
		d.Exhausted = !p.more
		if p.more {
			d.Next = &SavedCollectionCursor{identity: p.identity, proof: p.proof, fence: f, generation: p.generation, key: strings.Clone(lastKey), id: strings.Clone(lastID)}
		}
		return p.authority.CheckPreparation(p.lease)
	}}, roots...)
	if err == nil {
		err = p.authority.HandoffPreparation(p.lease)
	}
	if err != nil {
		return SavedCollectionPage{}, cost, err
	}
	if err = p.close(); err != nil {
		return SavedCollectionPage{}, cost, err
	}
	return page, cost, nil
}

func (r *SavedCollectionReader) savedPage(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, resource string, after *SavedCollectionCursor, p *SavedCollectionWork) (SavedCollectionPage, BrowseReadResult, error) {
	if r == nil || ctx == nil || budget <= 0 {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	oneShot := p == nil
	locked := false
	if p != nil {
		p.mu.Lock()
		locked = true
		defer p.mu.Unlock()
	}
	if p != nil {
		if p.reader != r || p.resource != resource || p.view.Database != view.Database || p.view.TokenHash != view.TokenHash || p.view.Limit != view.Limit || (p.after == nil) != (after == nil) || after != nil && *p.after != *after {
			return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
		}
	}
	clock := view.Now
	if p != nil {
		clock = p.view.Now.Add(time.Since(p.started))
		if out.live != nil {
			clock = p.view.Now.Add(out.live.started.Sub(p.started))
		}
	}
	return liveRead(ctx, req, budget, out, r.pool, clock, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (page SavedCollectionPage, cost BrowseReadResult, err error) {
		started := time.Now()
		deadline := started.Add(min(budget, time.Second))
		if req.Deadline.Before(deadline) {
			deadline = req.Deadline
		}
		if oneShot {
			var refusal *governor.Refusal
			p, refusal, err = r.beginSaved(ctx, out.Authority, req.Owner, deadline, 8<<20, view, resource, after)
			if err != nil {
				return
			}
			if p == nil {
				cost.Refusal = refusal
				return SavedCollectionPage{}, cost, governor.ErrBusy
			}
		}
		if !locked {
			p.mu.Lock()
			defer p.mu.Unlock()
		}
		defer func() {
			if err != nil || oneShot {
				_ = p.close()
			}
			if err != nil {
				page = SavedCollectionPage{}
			}
		}()
		if err = p.check(req, out); err != nil {
			return
		}
		for {
			if err = ctx.Err(); err != nil {
				return
			}
			if !time.Now().Before(deadline) {
				err = context.DeadlineExceeded
				return
			}
			var one BrowseReadResult
			if !p.ready {
				one, err = p.advance(ctx, req, time.Until(deadline), out, at.Add(time.Since(started)))
				addSearchReadCost(&cost, &one)
				if err != nil {
					return
				}
			}
			if p.ready {
				page, one, err = p.deliver(ctx, req, time.Until(deadline), out, at.Add(time.Since(started)))
				addSearchReadCost(&cost, &one)
				return
			}
			if !oneShot {
				page = SavedCollectionPage{Pending: true, Work: p}
				return
			}
		}
	})
}

func (p *SavedCollectionWork) closeInner() {
	if p.current.inner != nil {
		_ = p.current.inner.Close()
		p.current.inner = nil
		p.used -= p.current.innerBytes
		p.current.innerBytes = 0
	}
}
