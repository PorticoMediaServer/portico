package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// A saved COLLECTION directory is the viewer's exact source/pin/query-bound
// list of collections, not a bounded sample of the resource universe. Owner,
// server-visible and shared resources are enumerated as three independent raw
// streams, deduplicated by resource id BEFORE any metadata, count or sample work,
// reauthorized at their own current source/share/server tuple, and sorted by the
// same lower(name)/pin oracle the legacy directory uses. Every resource reports
// its complete visible member count; at most four selected member samples are
// hydrated. Hidden, retired, cross-library and unreadable members still advance
// the raw member order and are counted only when their own current subject tuple
// is visible. A shape filter is applied after the bounded raw resource window,
// so a sparse filtered page keeps the private raw continuation and never
// invents exhaustion.
//
// Directory and pin ordering are a separate family (RsSavedDirectory). UO owns
// saved writes; RB owns saved VIEWS.
const (
	rsSavedDirectoryCompute readPolicyID = 48
	rsSavedDirectoryDeliver readPolicyID = 49
)

const (
	// rsSavedDirectoryRawWindow is the number of raw union positions consumed by
	// one step. Every stream query binds window+1 and rejects an impossible
	// overflow instead of silently truncating the authorized union.
	rsSavedDirectoryRawWindow = 1
	// rsSavedDirectoryEntryWindow is the one raw member position consumed by a
	// step. The exact member count needs the complete stream, never a cap.
	rsSavedDirectoryEntryWindow = 1
	// rsSavedDirectorySamples is the fixed number of member samples per resource.
	rsSavedDirectorySamples = 4
	// rsSavedDirectoryPointCalls is the query budget of an armed payload window.
	// It covers the common fence, the exact payload pair and the final fence.
	rsSavedDirectoryPointCalls = 12
)

func init() {
	for _, id := range []readPolicyID{rsSavedDirectoryCompute, rsSavedDirectoryDeliver} {
		registerReadPolicy(readPolicyRecipe{id: id, domain: "rs-saved-collection-directory", kinds: []int64{1}, stateful: true, scope: readPrincipalScope, validate: rsSavedDirectoryEligibility})
	}
}

// SavedCollectionDirectoryInput selects one page of the viewer's collection
// directory. PinnedOnly uses the viewer's own persisted arrangement; Shape
// filters posters/squares AFTER the raw resource window. After is internal
// values-only continuation state, never a wire token.
type SavedCollectionDirectoryInput struct {
	View       BrowseInput
	PinnedOnly bool
	Shape      string
	After      *SavedCollectionDirectoryCursor
	Work       *SavedCollectionDirectoryWork
}

// SavedCollectionDirectoryCursor binds a continuation to the exact viewer,
// query, pinned mode, source generation, complete visible-set proof and raw
// (sort_key,id) position. Fields are unexported and never serialized.
type SavedCollectionDirectoryCursor struct {
	identity, proof [32]byte
	fence           browseFence
	generation      int64
	pinned          bool
	limit           int
	key, id         string
}

// SavedCollectionDirectoryItem is one authorized saved collection. EntryCount
// is the complete visible member count; Samples carries at most four hydrated
// member cards. Position/PinRevision/Pinned come from the viewer's pin points.
type SavedCollectionDirectoryItem struct {
	ID, Name, Summary, CreatedAt, Visibility, Role, Shape string
	Revision, PinRevision, Position, EntryCount           int64
	Pinned                                                bool
	Samples                                               []SavedCollectionEntry
}

// SavedCollectionDirectoryPage is one bounded directory page over the complete
// visible resource set. Total is the exact number of authorized resources and
// Members the exact number of visible members across all of them; Posters and
// Squares count the complete resource set, not the shape-filtered page. Pending
// never carries partial resources, counts or a cursor.
type SavedCollectionDirectoryPage struct {
	Resources []SavedCollectionDirectoryItem
	Total     int64
	Members   int64
	Posters   int64
	Squares   int64
	Exhausted bool
	Pending   bool
	Next      *SavedCollectionDirectoryCursor
	Work      *SavedCollectionDirectoryWork
}

type rsSavedDirectoryCurrent struct {
	innerBytes          int64
	resource            SavedCollectionDirectoryItem
	entryID, key        string
	item, library, kind int64
	libraryName         string
	memberStarted       bool
	view                BrowseInput
	card                SavedCollectionEntry
	inner               *CollectionPreparation
}

// SavedCollectionDirectoryWork is caller-owned typed ephemeral work under RB's
// F-04 lease. It has no worker, queue, SQL handle, durable projection or
// renewal. Advance/Delivery/Close serialize on this job's mutex so a lease
// expiry, cancel or concurrent Close cannot retire the lease twice or clear
// retained storage mid-enumeration. The caller keeps the job context alive
// between polls; an HTTP poll context must not own the job lifetime.
type SavedCollectionDirectoryWork struct {
	membersHash             hash.Hash
	mu                      sync.Mutex
	reader                  *SavedCollectionReader
	authority               *governor.Authority
	lease                   *governor.PreparationLease
	job                     context.Context
	owner                   governor.OwnerID
	started                 time.Time
	view                    BrowseInput
	pinned                  bool
	shape                   string
	after                   *SavedCollectionDirectoryCursor
	identity, proof         [32]byte
	stage, stream           int
	rawName, rawID, shareID string
	streamStarted           bool
	seen                    map[string]bool
	sortKeys                map[string]string
	current                 rsSavedDirectoryCurrent
	records                 []SavedCollectionDirectoryItem
	total                   int64
	pinOrder                int64
	used                    int64
	fence                   *browseFence
	generation              int64
	captured, ready, closed bool
}

func (*SavedCollectionDirectoryWork) readPolicyState() {}

const (
	rsSavedDirectoryStart = iota
	rsSavedDirectoryNext
	rsSavedDirectoryMetadata
	rsSavedDirectoryEntry
	rsSavedDirectoryEligible
	rsSavedDirectoryAutomatic
	rsSavedDirectoryCard
	rsSavedDirectoryReading
	rsSavedDirectoryArt
	rsSavedDirectoryFinish
	rsSavedDirectoryDone
)

// BeginDirectory explicitly admits resumable directory work for the viewer.
// The returned work owns its own lease; a nested automatic collection reducer
// borrows this same grant and can never Ready, Handoff or Retire it.
func (r *SavedCollectionReader) BeginDirectory(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, in SavedCollectionDirectoryInput) (*SavedCollectionDirectoryWork, *governor.Refusal, error) {
	if in.View.After != nil {
		return nil, nil, ErrInput
	}
	return r.beginSavedDirectory(ctx, a, owner, deadline, bytes, in.View, in.PinnedOnly, in.Shape, in.After)
}

func (r *SavedCollectionReader) beginSavedDirectory(ctx context.Context, a *governor.Authority, owner governor.OwnerID, deadline time.Time, bytes int64, view BrowseInput, pinned bool, shape string, after *SavedCollectionDirectoryCursor) (*SavedCollectionDirectoryWork, *governor.Refusal, error) {
	if r == nil || a == nil || !r.pool.OwnedBy(a) || ctx == nil || owner == 0 || view.Now.IsZero() || view.Limit < 1 || view.Limit > MaxPage || len(view.Database) == 0 || len(view.Database) > 256 || len(view.TokenHash) == 0 || len(view.TokenHash) > 256 || view.After != nil || view.Descending || view.KeepArticles || bytes < 32768 || shape != "" && shape != "posters" && shape != "squares" {
		return nil, nil, ErrInput
	}
	// The directory is selected by the authenticated principal, never by a
	// caller library hint. Each resource and each member is reauthorized against
	// its own current library and kind.
	view.Library, view.Kind = 0, 1
	view.policy = readPolicyBinding{}
	identityHash := sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%d/%t/%q/%d", view.Database, view.TokenHash, owner, pinned, shape, view.Limit)))
	if after != nil && after.identity != identityHash {
		return nil, nil, ErrStale
	}
	position := "first"
	if after != nil {
		position = fmt.Sprintf("%x/%q/%q", after.proof, after.key, after.id)
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%x/%s", identityHash, position)))
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("saved-directory:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	view.Database, view.TokenHash = strings.Clone(view.Database), strings.Clone(view.TokenHash)
	p := &SavedCollectionDirectoryWork{reader: r, authority: a, lease: d.Lease, job: ctx, owner: owner, started: time.Now(), view: view, pinned: pinned, shape: strings.Clone(shape), identity: identityHash, seen: map[string]bool{}, membersHash: sha256.New(), used: 16384, stage: rsSavedDirectoryStart}
	if after != nil {
		if e := p.charge(rsSavedDataBytes(*after)); e != nil {
			_ = p.close()
			return nil, nil, e
		}
		c := *after
		c.id, c.key = strings.Clone(c.id), strings.Clone(c.key)
		p.after = &c
	}
	p.view.policy = readPolicyBinding{recipe: rsSavedDirectoryCompute, state: p}
	return p, nil, nil
}

// Close joins an in-flight poll and retires the original lease exactly once.
// Retained values are dropped before physical retirement is attested.
func (p *SavedCollectionDirectoryWork) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}

func (p *SavedCollectionDirectoryWork) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.current.inner != nil {
		_ = p.current.inner.Close()
	}
	p.records, p.seen, p.sortKeys, p.fence = nil, nil, nil, nil
	p.current = rsSavedDirectoryCurrent{}
	p.view = BrowseInput{}
	p.after = nil
	p.shape, p.rawName, p.rawID, p.shareID = "", "", "", ""
	p.membersHash = nil
	p.job = nil
	_ = p.authority.CancelPreparation(p.lease)
	return p.authority.RetirePreparation(p.lease)
}

func (p *SavedCollectionDirectoryWork) check(req governor.Request, out BrowseOutput) error {
	if p == nil || p.closed || p.reader == nil || out.Authority != p.authority || req.Owner != p.owner || out.Request.Owner != p.owner {
		return ErrInput
	}
	return p.authority.CheckPreparation(p.lease)
}

func (p *SavedCollectionDirectoryWork) capacity(extra int64) error {
	cursors := int64(4 * (len(p.rawName) + len(p.rawID) + len(p.shareID) + len(p.current.entryID) + len(p.current.key) + len(p.current.resource.ID)))
	if extra < 0 || p.used+extra+cursors > p.lease.PayloadBytes() {
		return governor.ErrSnapshotBudget
	}
	return nil
}

func (p *SavedCollectionDirectoryWork) charge(n int64) error {
	if err := p.capacity(n); err != nil {
		return err
	}
	p.used += n
	return nil
}

func rsSavedDirectoryOwner(f browseFence) string {
	return identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: f.account, ProfileID: f.profile})
}

// rsSavedDirectoryPermission verifies the current owner token, current server
// visibility or a current share row. A missing resource, a VIEW resource, a
// deleted row or a foreign private collection is not permitted. Each query
// returns at most one row and refuses an ambiguous source.
func rsSavedDirectoryPermission(q browseQuery, token, resource string) ([]any, bool, bool, error) {
	var source []any
	permitted, owned := false, false
	for _, name := range []string{"source", "server", "share"} {
		args := []any{token, resource}
		if name == "server" {
			args = []any{resource}
		}
		rows, err := q("rs.saved.directory.permission."+name, args...)
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

// rsSavedDirectoryEligibility proves the original lease plus the one current
// source generation. A deliver snapshot additionally requires the ready complete
// visible-set proof, so no partial directory is ever published.
func rsSavedDirectoryEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	p, ok := in.policy.state.(*SavedCollectionDirectoryWork)
	if !ok || p == nil || p.closed || p.lease == nil || p.view.Database != in.Database || p.view.TokenHash != in.TokenHash || in.Library != 0 || in.Kind != 1 || in.policy.subject != 0 {
		return "", ErrInput
	}
	if err := p.authority.CheckPreparation(p.lease); err != nil {
		return "", err
	}
	if p.fence != nil && !rsCollectionSameFence(*p.fence, f) {
		return "", ErrStale
	}
	rows, err := q("rs.saved.directory.generation")
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
	if in.policy.recipe == rsSavedDirectoryDeliver {
		if !p.ready || p.stage != rsSavedDirectoryDone || p.proof != p.digest() {
			return "", ErrUnqualified
		}
		return fmt.Sprintf("%d:%x", generation, p.proof), nil
	}
	if in.policy.recipe != rsSavedDirectoryCompute || p.ready {
		return "", ErrInput
	}
	return strconv.FormatInt(generation, 10), nil
}

func (p *SavedCollectionDirectoryWork) run(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time, w *rsByteWindow, steps []readStep[struct{}]) (BrowseReadResult, error) {
	in := p.view
	in.Now = at
	var fence browseFence
	guard := func(_ context.Context, _ browseQuery, f browseFence, _ *struct{}) error {
		if p.fence != nil && *p.fence != f {
			return ErrStale
		}
		fence = f
		return p.authority.CheckPreparation(p.lease)
	}
	steps = append([]readStep[struct{}]{guard}, steps...)
	// The three union streams are the source. The common fence plus the exact
	// generation witness and the complete visible-set proof bind them; there is
	// no single resource root to compare.
	_, cost, err := publishRead(ctx, w, req, min(budget, time.Until(p.lease.Deadline())), out, in, steps)
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
	return cost, p.capacity(0)
}

func (p *SavedCollectionDirectoryWork) digest() [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%d/%d/%d/", p.generation, p.pinOrder, p.total)
	for _, r := range p.records {
		fmt.Fprintf(h, "%q/%q/%q/%q/%q/%q/%d/%d/%d/%t/%s/%d/", r.ID, r.Name, r.Summary, r.CreatedAt, r.Visibility, r.Role, r.Revision, r.PinRevision, r.Position, r.Pinned, r.Shape, r.EntryCount)
		for _, e := range r.Samples {
			fmt.Fprintf(h, "%q/%q/%q/%d/%d/%d/%s/", e.EntryID, e.OrderKey, e.Library, e.Item.ID, e.Item.Kind, e.Item.Revision, e.Artwork.Digest)
		}
	}
	if p.membersHash != nil {
		_, _ = h.Write(p.membersHash.Sum(nil))
	}
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}

// rsSavedDirectoryLower is the SQLite lower() oracle for ASCII: bytewise ASCII
// case folding, no Unicode normalization, embedded NUL bytes preserved.
func rsSavedDirectoryLower(s string) string {
	b := []byte(s)
	for n, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[n] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// sortKey is the canonical persisted arrangement: lower(name), or the public
// printf('%08d',position) key + char(31) + lower(name) when the viewer's pinned
// arrangement is requested. A missing position sorts after arranged positions.
func (p *SavedCollectionDirectoryWork) sortKey(r SavedCollectionDirectoryItem) string {
	if p.sortKeys != nil {
		return p.sortKeys[r.ID]
	}
	if p.pinned {
		return fmt.Sprintf("%08d\x1f%s", r.Position, rsSavedDirectoryLower(r.Name))
	}
	return rsSavedDirectoryLower(r.Name)
}

func (p *SavedCollectionDirectoryWork) markEligible() error {
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
	if c.resource.EntryCount <= rsSavedDirectorySamples {
		p.stage = rsSavedDirectoryCard
		return nil
	}
	if c.inner != nil {
		p.closeInner()
	}
	p.stage = rsSavedDirectoryEntry
	return nil
}

func (p *SavedCollectionDirectoryWork) advance(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time) (BrowseReadResult, error) {
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
			sizes, e := q("rs.saved.directory."+name+"_octets", args...)
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
			return w.arm(max(n, 1), rsSavedDirectoryPointCalls)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !exists {
				return ErrStale
			}
			rows, e := q("rs.saved.directory."+name, args...)
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
	case rsSavedDirectoryStart:
		// Bind the viewer's own arrangement revision before the union so a pin
		// order change cannot tear or silently repage an accepted directory.
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			rows, e := q("rs.saved.directory.pin_order", rsSavedDirectoryOwner(f))
			if e != nil {
				return e
			}
			if len(rows) > 1 {
				return ErrUnqualified
			}
			if len(rows) == 1 {
				p.pinOrder = rows[0][0].(int64)
			}
			p.stage = rsSavedDirectoryNext
			return nil
		})
	case rsSavedDirectoryNext:
		if p.stream == 3 {
			p.stage = rsSavedDirectoryDone
			break
		}
		var row int64
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			var rows [][]any
			var e error
			limit := int64(rsSavedDirectoryRawWindow + 1)
			if p.stream == 2 {
				rows, e = q("rs.saved.directory.share", f.account, f.profile, p.shareID, limit)
			} else {
				name := "owner"
				var args []any
				if p.stream == 0 {
					args = []any{rsSavedDirectoryOwner(f)}
				} else {
					name = "server"
				}
				if !p.streamStarted {
					rows, e = q("rs.saved.directory."+name+".first", append(args, limit)...)
				} else {
					rows, e = q("rs.saved.directory."+name+".tail", append(args, p.rawName, p.rawID, limit)...)
					if e == nil && len(rows) == 0 {
						rows, e = q("rs.saved.directory."+name+".next", append(args, p.rawName, limit)...)
					}
				}
			}
			if e != nil {
				return e
			}
			// The cap+1 window proves the stream is still exact; the extra row is
			// simply not consumed in this step.
			if len(rows) > rsSavedDirectoryRawWindow+1 {
				return ErrUnqualified
			}
			if len(rows) == 0 {
				p.stream++
				p.streamStarted = false
				p.rawID, p.rawName, p.shareID = "", "", ""
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
			name := "resource"
			if p.stream == 2 {
				name = "share_resource"
			}
			sizes, e := q("rs.saved.directory."+name+"_octets", row)
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
			return w.arm(max(n, 1), rsSavedDirectoryPointCalls)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !exists {
				return nil
			}
			name := "resource"
			if p.stream == 2 {
				name = "share_resource"
			}
			rows, e := q("rs.saved.directory."+name, row)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			id := rows[0][0].(string)
			if id == "" {
				return ErrUnqualified
			}
			if p.stream == 2 {
				p.shareID = id
			} else {
				p.rawID = id
				p.rawName = rows[0][1].(string)
				p.streamStarted = true
			}
			if p.seen[id] {
				return nil
			}
			p.current = rsSavedDirectoryCurrent{resource: SavedCollectionDirectoryItem{ID: id, Shape: "posters", Position: 99999999}}
			p.stage = rsSavedDirectoryMetadata
			return nil
		})
	case rsSavedDirectoryMetadata:
		var allowed, owned bool
		var role string
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			_, a, o, e := rsSavedDirectoryPermission(q, p.view.TokenHash, p.current.resource.ID)
			if e != nil {
				return e
			}
			allowed, owned = a, o
			if !allowed {
				p.current = rsSavedDirectoryCurrent{}
				p.stage = rsSavedDirectoryNext
				return nil
			}
			if !owned {
				r, e := q("rs.saved.directory.permission.share_role", p.current.resource.ID, f.account, f.profile)
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
			sizes, e = q("rs.saved.directory.metadata_octets", p.current.resource.ID)
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
			return w.arm(max(n, 1), rsSavedDirectoryPointCalls)
		}, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			if !allowed {
				return nil
			}
			rows, e := q("rs.saved.directory.metadata", p.current.resource.ID)
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
			owner := rsSavedDirectoryOwner(f)
			pins, e := q("rs.saved.directory.pin", owner, r.ID)
			if e != nil {
				return e
			}
			if len(pins) > 1 {
				return ErrUnqualified
			}
			if len(pins) == 1 {
				r.Pinned = pins[0][0].(int64) != 0
				r.PinRevision = pins[0][1].(int64)
			}
			positions, e := q("rs.saved.directory.position", owner, r.ID)
			if e != nil {
				return e
			}
			if len(positions) > 1 {
				return ErrUnqualified
			}
			if len(positions) == 1 {
				r.Position = positions[0][0].(int64)
			}
			if p.pinned && !r.Pinned {
				p.seen[r.ID] = true
				p.current = rsSavedDirectoryCurrent{}
				p.stage = rsSavedDirectoryNext
				return nil
			}
			if e = p.charge(512 + rsSavedDataBytes(r)); e != nil {
				return e
			}
			p.seen[r.ID] = true
			p.current.resource = r
			p.stage = rsSavedDirectoryEntry
			return nil
		})
	case rsSavedDirectoryEntry:
		var row int64
		steps = append(steps, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			c := &p.current
			var rows [][]any
			var e error
			limit := int64(rsSavedDirectoryEntryWindow + 1)
			if !c.memberStarted {
				rows, e = q("rs.saved.directory.entry.first", c.resource.ID, limit)
			} else {
				rows, e = q("rs.saved.directory.entry.tail", c.resource.ID, c.key, c.entryID, limit)
				if e == nil && len(rows) == 0 {
					rows, e = q("rs.saved.directory.entry.next", c.resource.ID, c.key, limit)
				}
			}
			if e != nil {
				return e
			}
			// The cap+1 window only proves the continuation exists; the tail seek
			// re-reads it from the exact (order_key,id) cursor in the next step.
			if len(rows) > rsSavedDirectoryEntryWindow+1 {
				return ErrUnqualified
			}
			if len(rows) == 0 {
				p.stage = rsSavedDirectoryFinish
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
			sizes, e := q("rs.saved.directory.entry_octets", row, p.current.resource.ID)
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
			return w.arm(max(n, 1), rsSavedDirectoryPointCalls)
		}, func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if !exists {
				return nil
			}
			rows, e := q("rs.saved.directory.entry", row, p.current.resource.ID)
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
			p.stage = rsSavedDirectoryEligible
			return nil
		})
	case rsSavedDirectoryEligible:
		steps = append(steps, func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			c := &p.current
			if c.item == 0 {
				p.stage = rsSavedDirectoryEntry
				return nil
			}
			rows, e := q("rs.saved.directory.item", c.item)
			if e != nil {
				return e
			}
			if len(rows) == 0 || rows[0][3].(int64) != 0 {
				p.stage = rsSavedDirectoryEntry
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
					p.stage = rsSavedDirectoryAutomatic
					return nil
				}
			}
			child, e := readBrowseFence(q, c.view)
			if errors.Is(e, ErrDenied) {
				p.stage = rsSavedDirectoryEntry
				return nil
			}
			if e != nil {
				return e
			}
			if rsSavedPrincipal(child) != rsSavedPrincipal(f) {
				return ErrStale
			}
			names, e := q("rs.saved.directory.library_name", child.library)
			if e != nil {
				return e
			}
			if len(names) != 1 {
				return ErrStale
			}
			c.libraryName = names[0][0].(string)
			return p.markEligible()
		})
	case rsSavedDirectoryAutomatic:
		// The complete automatic collection reducer owns inferred R21
		// membership. It is a separate task (rs_auto_collection*); until that
		// reducer is composed beside this family the authored reducer refuses an
		// automatic root with ErrUnqualified rather than inventing an authored
		// empty set. The nested work borrows this job's existing lease and payload
		// charge; it never takes a second lease, ledger or renewal.
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
			p.stage = rsSavedDirectoryEntry
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
			p.stage = rsSavedDirectoryEntry
			return cost, nil
		}
		if p.fence == nil || rsSavedPrincipal(*c.inner.fence) != rsSavedPrincipal(*p.fence) {
			return cost, ErrStale
		}
		c.libraryName = c.inner.fence.library
		c.view.policy = readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: c.item, state: c.inner}
		return cost, p.markEligible()
	case rsSavedDirectoryCard:
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
			p.stage = rsSavedDirectoryArt
			if c.kind == 20 || c.kind == 21 {
				p.stage = rsSavedDirectoryReading
			}
			return nil
		})
	case rsSavedDirectoryReading:
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
		p.stage = rsSavedDirectoryArt
		return cost, p.authority.CheckPreparation(p.lease)
	case rsSavedDirectoryArt:
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
		p.current.resource.Samples = append(p.current.resource.Samples, p.current.card)
		p.current.card = SavedCollectionEntry{}
		if p.current.inner != nil {
			p.closeInner()
		}
		p.stage = rsSavedDirectoryEntry
		return cost, p.authority.CheckPreparation(p.lease)
	case rsSavedDirectoryFinish:
		p.records = append(p.records, p.current.resource)
		p.current = rsSavedDirectoryCurrent{}
		p.stage = rsSavedDirectoryNext
	case rsSavedDirectoryDone:
	default:
		return BrowseReadResult{}, ErrInput
	}
	cost, err := p.run(ctx, req, budget, out, at, w, steps)
	if err != nil {
		return cost, err
	}
	if p.stage == rsSavedDirectoryDone {
		keys := make(map[string]string, len(p.records))
		for _, r := range p.records {
			key := p.sortKey(r)
			if err = p.charge(64 + int64(len(key))); err != nil {
				return cost, err
			}
			keys[r.ID] = key
		}
		p.sortKeys = keys
		sort.Slice(p.records, func(i, j int) bool {
			a, b := p.sortKey(p.records[i]), p.sortKey(p.records[j])
			if a == b {
				return p.records[i].ID < p.records[j].ID
			}
			return a < b
		})
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

func (p *SavedCollectionDirectoryWork) deliver(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, at time.Time) (SavedCollectionDirectoryPage, BrowseReadResult, error) {
	if !p.ready {
		return SavedCollectionDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	view := p.view
	view.Now = at
	view.policy = readPolicyBinding{recipe: rsSavedDirectoryDeliver, state: p}
	w := &rsByteWindow{source: p.reader.snapshots, preflightCalls: 16, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	page, cost, err := publishRead(ctx, w, req, min(budget, time.Until(p.lease.Deadline())), out, view, []readStep[SavedCollectionDirectoryPage]{func(_ context.Context, q browseQuery, f browseFence, d *SavedCollectionDirectoryPage) error {
		if p.fence == nil || !rsCollectionSameFence(*p.fence, f) {
			return ErrStale
		}
		// Rebind the viewer's pin arrangement at the actual publication point.
		rows, e := q("rs.saved.directory.pin_order", rsSavedDirectoryOwner(f))
		if e != nil {
			return e
		}
		if len(rows) > 1 {
			return ErrUnqualified
		}
		var pinOrder int64
		if len(rows) == 1 {
			pinOrder = rows[0][0].(int64)
		}
		if pinOrder != p.pinOrder {
			return ErrStale
		}
		// Walk the private raw continuation. Shape filtering happens AFTER the
		// bounded raw window, so a filtered page never loses its raw position and
		// never claims exhaustion it has not proved.
		start := 0
		for start < len(p.records) && p.after != nil {
			r := p.records[start]
			key := p.sortKey(r)
			if key > p.after.key || key == p.after.key && r.ID > p.after.id {
				break
			}
			start++
		}
		end := min(start+p.view.Limit+1, len(p.records))
		if end-start > p.view.Limit+1 {
			return ErrUnqualified
		}
		window := p.records[start:end]
		more := len(window) > p.view.Limit
		if more {
			window = window[:p.view.Limit]
		}
		selected := make([]SavedCollectionDirectoryItem, 0, len(window))
		for _, r := range p.records {
			if r.Shape == "squares" {
				d.Squares++
			} else {
				d.Posters++
			}
		}
		for _, r := range window {
			if p.shape == "" || p.shape == r.Shape {
				selected = append(selected, r)
			}
		}
		var lastKey, lastID string
		if len(window) > 0 {
			last := window[len(window)-1]
			lastKey, lastID = p.sortKey(last), last.ID
		}
		bytes := rsSavedDataBytes(selected) + rsSavedDataBytes(f) + 2048
		if bytes > out.Request.Need[governor.MemoryBytes]/2 {
			return governor.ErrSnapshotBudget
		}
		d.Resources = rsSavedClone(selected)
		d.Total = int64(len(p.records))
		d.Members = p.total
		d.Exhausted = !more
		if more {
			d.Next = &SavedCollectionDirectoryCursor{identity: p.identity, proof: p.proof, fence: f, generation: p.generation, pinned: p.pinned, limit: p.view.Limit, key: strings.Clone(lastKey), id: strings.Clone(lastID)}
		}
		return p.authority.CheckPreparation(p.lease)
	}})
	if err == nil {
		err = p.authority.HandoffPreparation(p.lease)
	}
	if err != nil {
		return SavedCollectionDirectoryPage{}, cost, err
	}
	if err = p.close(); err != nil {
		return SavedCollectionDirectoryPage{}, cost, err
	}
	return page, cost, nil
}

// DirectoryPage returns one bounded page of the viewer's exact saved collection
// directory. A one-shot read admits and retires its own work; an explicitly
// begun work survives polls and is retired by its caller.
func (r *SavedCollectionReader) DirectoryPage(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in SavedCollectionDirectoryInput) (SavedCollectionDirectoryPage, BrowseReadResult, error) {
	if r == nil || in.View.After != nil {
		return SavedCollectionDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	return r.savedDirectoryPage(ctx, req, budget, out, in.View, in.PinnedOnly, in.Shape, in.After, in.Work)
}

func (r *SavedCollectionReader) savedDirectoryPage(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, view BrowseInput, pinned bool, shape string, after *SavedCollectionDirectoryCursor, p *SavedCollectionDirectoryWork) (SavedCollectionDirectoryPage, BrowseReadResult, error) {
	if r == nil || ctx == nil || budget <= 0 {
		return SavedCollectionDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	oneShot := p == nil
	locked := false
	if p != nil {
		p.mu.Lock()
		locked = true
		defer p.mu.Unlock()
	}
	if p != nil {
		if p.reader != r || p.pinned != pinned || p.shape != shape || p.view.Database != view.Database || p.view.TokenHash != view.TokenHash || p.view.Limit != view.Limit || (p.after == nil) != (after == nil) || after != nil && *p.after != *after {
			return SavedCollectionDirectoryPage{}, BrowseReadResult{}, ErrInput
		}
	}
	clock := view.Now
	if p != nil {
		clock = p.view.Now.Add(time.Since(p.started))
		if out.live != nil {
			clock = p.view.Now.Add(out.live.started.Sub(p.started))
		}
	}
	return liveRead(ctx, req, budget, out, r.pool, clock, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (page SavedCollectionDirectoryPage, cost BrowseReadResult, err error) {
		started := time.Now()
		deadline := started.Add(min(budget, time.Second))
		if req.Deadline.Before(deadline) {
			deadline = req.Deadline
		}
		if oneShot {
			var refusal *governor.Refusal
			p, refusal, err = r.beginSavedDirectory(ctx, out.Authority, req.Owner, deadline, 8<<20, view, pinned, shape, after)
			if err != nil {
				return
			}
			if p == nil {
				cost.Refusal = refusal
				return SavedCollectionDirectoryPage{}, cost, governor.ErrBusy
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
				page = SavedCollectionDirectoryPage{}
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
				page = SavedCollectionDirectoryPage{Pending: true, Work: p}
				return
			}
		}
	})
}

func (p *SavedCollectionDirectoryWork) closeInner() {
	if p.current.inner != nil {
		_ = p.current.inner.Close()
		p.current.inner = nil
		p.used -= p.current.innerBytes
		p.current.innerBytes = 0
	}
}
