package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// RSSearchGroup is disjoint by native subject as well as kind. Live TV has no
// native owner and is deliberately unsupported, never an empty successful group.
type RSSearchGroup int64

const (
	RSSearchPeople  RSSearchGroup = -1
	RSSearchLiveTV  RSSearchGroup = -2
	RSSearchReading RSSearchGroup = 20
)

type RSSearchInput struct {
	Views     []BrowseInput // explicit scope; each library is independently checked
	Query     search.Query
	Group     RSSearchGroup // zero previews all native groups; positive entity kind
	Full      bool          // 40/group instead of 8/group
	Order     string        // relevance, title, releaseYear or dateAdded
	Direction string        // asc/desc; empty uses the public sort default
	Capacity  int           // retained-match bound, <=512; overflow refuses without output
	After     *RSSearchCursor
}
type RSSearchCursor struct {
	identity [32]byte
	offset   int
	rank     *FullSearchCursor
	order    *rsSearchOrderKey
	anchor   *FullSearchReadStamp
	anchors  []*FullSearchReadStamp
}
type RSSearchGroupPage struct {
	Group        RSSearchGroup
	Hits         []FullSearchHit
	HasMore      bool
	HasMoreKnown bool
	CountStatus  SearchCountStatus
	Count        uint64
	Next         *RSSearchCursor
}
type RSSearchPage struct {
	Groups                 []RSSearchGroupPage
	Pending, Complete      bool
	Partial, BudgetStopped bool
	NativeCandidates       int
}

// rsSearchCustody is an integration prerequisite, not a certificate supplied by
// clients. After the ONE native read, it must certify that ALL retained hits
// have current visibility under the same accepted source, authority and bank.
// It must use evidence joined by that read, never another LIVE read or cached
// permission decisions. A changed source/authority returns ErrStale/ErrDenied.
// The default binding uses RD's sealed read stamp, including pending/final
// pages. Test injection is package-private and supplies no native certification.
type rsSearchCustody func(context.Context, BrowseOutput, FullSearchInput, FullSearchPage, []FullSearchHit, *FullSearchReadStamp) error

type RSSearchReader struct {
	authority *governor.Authority
	read      FullSearchRead
	custody   rsSearchCustody
	pool      *governor.Snapshots
}

func NewRSSearchReader(a *governor.Authority, pool *governor.Snapshots) (*RSSearchReader, error) {
	if a == nil || pool == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	n, e := NewNativeFullSearchReader(pool)
	if e != nil {
		return nil, e
	}
	r, e := newRSSearchReaderWithRead(a, n.Read, rsNativeSearchCustody)
	if e == nil {
		r.pool = pool
	}
	return r, e
}
func newRSSearchReaderWithRead(a *governor.Authority, read FullSearchRead, custody rsSearchCustody) (*RSSearchReader, error) {
	if a == nil || read == nil {
		return nil, ErrInput
	}
	return &RSSearchReader{authority: a, read: read, custody: custody}, nil
}

type RSSearchPreparation struct {
	mu         sync.Mutex
	reader     *RSSearchReader
	lease      *governor.PreparationLease
	owner      governor.OwnerID
	input      RSSearchInput
	identity   [32]byte
	started    time.Time
	after      *FullSearchCursor
	stamp      *FullSearchReadStamp
	cursorBase *FullSearchCursor
	lastRank   *canonicalCandidateProof
	hits       []FullSearchHit
	closed     bool
	candidates int
	exhausted  bool
	seen       map[fullResultKey]struct{}
	library    int
	streams    []rsSearchStream
	anchors    []*FullSearchReadStamp
}

type rsSearchStream struct {
	after     *FullSearchCursor
	lastRank  *canonicalCandidateProof
	matched   int
	done      bool
	exhausted bool
}

func (p *RSSearchPreparation) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}
func (p *RSSearchPreparation) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.hits = nil
	p.after = nil
	p.stamp = nil
	p.seen = nil
	p.input = RSSearchInput{}
	p.cursorBase = nil
	p.lastRank = nil
	p.streams = nil
	p.anchors = nil
	_ = p.reader.authority.CancelPreparation(p.lease)
	return p.reader.authority.RetirePreparation(p.lease)
}
func (r *RSSearchReader) Begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, bytes int64, in RSSearchInput) (*RSSearchPreparation, *governor.Refusal, error) {
	if r == nil || ctx == nil || owner == 0 || len(in.Views) == 0 || in.Query.Mode() != search.ReadFull || in.Capacity < 1 || in.Capacity > 512 || !rsSearchValidOrder(in.Order) {
		return nil, nil, ErrInput
	}
	if in.Direction == "" {
		in.Direction = rsSearchDefaultDirection(in.Order)
	}
	if in.Direction != "asc" && in.Direction != "desc" {
		return nil, nil, ErrInput
	}
	if in.Group == RSSearchLiveTV {
		return nil, nil, ErrUnqualified
	}
	// One source anchor per library plus the kept-hit stamp must fit the
	// native call's approved 128-stamp bound. Wider scopes refuse explicitly.
	if len(in.Views) > MaxNativeRetained-1 {
		return nil, nil, ErrInput
	}
	if in.Group < RSSearchPeople || in.Group > 99 || in.Group != 0 && !rsSearchPublicGroup(in.Group) || (in.Full && in.Group == 0) {
		return nil, nil, ErrInput
	}
	if in.Group == 21 {
		in.Group = RSSearchReading
	}
	in.Views = append([]BrowseInput(nil), in.Views...)
	sort.Slice(in.Views, func(i, j int) bool { return in.Views[i].Library < in.Views[j].Library })
	keyViews := make([]BrowseInput, len(in.Views))
	for i, v := range in.Views {
		base := in.Views[0]
		if v.Library <= 0 || v.Database == "" || v.TokenHash == "" || v.Now.IsZero() || v.After != nil || v.Kind != 0 || v.Descending || v.KeepArticles || v.policy.recipe != 0 || v.Database != base.Database || v.TokenHash != base.TokenHash || !v.Now.Equal(base.Now) || i > 0 && v.Library == in.Views[i-1].Library {
			return nil, nil, ErrInput
		}
		keyViews[i] = v
		keyViews[i].Now = time.Time{}
		keyViews[i].Limit = 0
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%d/%#v/%s/%d/%t/%s/%d", owner, keyViews, in.Query.Fold(), in.Group, in.Full, in.Order+"/"+in.Direction, in.Capacity)))
	if in.After != nil && (in.After.identity != identity || in.After.offset != 0 || in.After.rank != nil && (in.After.offset != 0 || in.After.rank.native != nil || in.After.rank.query != in.Query.Fold() || in.After.rank.bank <= 0 || in.After.rank.revision <= 0 || in.After.rank.input < 0)) {
		return nil, nil, ErrStale
	}
	if len(in.Views) > 1 && in.After != nil && (in.After.order == nil || len(in.After.anchors) != len(in.Views)) {
		return nil, nil, ErrStale
	}
	if len(in.Views) > 1 && in.After != nil {
		if in.After.anchor != nil || in.After.rank != nil || slices.Contains(in.After.anchors, nil) {
			return nil, nil, ErrStale
		}
	}
	if in.After != nil {
		c := *in.After
		c.rank = cloneFullCursor(c.rank)
		if c.order != nil {
			key := *c.order
			c.order = &key
		}
		in.After = &c
	}
	cursorKey := "first"
	if in.After != nil {
		cursorKey = fmt.Sprintf("offset:%d", in.After.offset)
		if in.After.order != nil {
			cursorKey = fmt.Sprintf("order:%#v", *in.After.order)
		}
		if c := in.After.rank; c != nil {
			cursorKey = fmt.Sprintf("rank:%d/%d/%d/%s/%d/%d/%d/%d/%d", c.bank, c.revision, c.input, c.query, c.after.key.family, c.after.key.id, c.after.key.library, c.after.tier, c.after.subtier)
		}
	}
	position := sha256.Sum256([]byte(cursorKey))
	d := r.authority.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("rs-search:%x:%x", identity, position)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{}}})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	p := &RSSearchPreparation{reader: r, lease: d.Lease, owner: owner, input: in, identity: identity, started: time.Now()}
	if len(in.Views) > 1 {
		p.streams = make([]rsSearchStream, len(in.Views))
		if in.Group != 0 {
			p.anchors = make([]*FullSearchReadStamp, len(in.Views))
			if in.After != nil {
				copy(p.anchors, in.After.anchors)
				p.input.After.anchors = nil
			}
		}
	} else if in.After != nil {
		p.after = cloneFullCursor(in.After.rank)
	}
	// Conservative bounded retained DTO capacity; actual native SQL/transient
	// byte accounting remains entirely RD-owned. Wide retained pages refuse.
	if rsSearchRetainedBytes(p) > p.lease.PayloadBytes() {
		_ = p.close()
		return nil, nil, governor.ErrSnapshotBudget
	}
	p.hits = make([]FullSearchHit, 0, in.Capacity)
	if in.Group == 0 {
		p.seen = make(map[fullResultKey]struct{}, NativePreviewCandidates)
	}
	return p, nil, nil
}

// Poll issues ONE native read under the caller's ConsumeForeground. Its result
// is a trusted draft; public delivery uses Publish. The caller supplies the
// native recipe's 4096-row request budget without implicit enlargement here.
func (r *RSSearchReader) Poll(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSSearchPreparation) (result RSSearchPage, cost *BrowseReadResult, err error) {
	if r == nil || p == nil || p.reader != r || ctx == nil {
		return result, nil, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return r.pollLocked(ctx, request, budget, output, p, false)
}

// Poll is a trusted preparation interface. Publish keeps the ready lease held
// through encoding and the fresh shared publication fence before delivery.
func (r *RSSearchReader) pollLocked(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSSearchPreparation, keepReady bool) (result RSSearchPage, cost *BrowseReadResult, err error) {
	if output.live == nil || output.Authority != r.authority || p.closed || p.owner != request.Owner {
		return result, nil, ErrInput
	}
	defer func() {
		if err != nil {
			_ = p.close()
			result = RSSearchPage{}
		}
	}()
	if err = output.live.checkContext(ctx); err != nil {
		return
	}
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	preview := p.input.Group == 0
	v := p.input.Views[p.library]
	v.Kind = int64(p.input.Group)
	if v.Kind < 0 || p.input.Group == RSSearchReading {
		v.Kind = 0
	}
	v.Limit = 20
	v.Now = v.Now.Add(time.Since(p.started))
	input := FullSearchInput{View: v, After: cloneFullCursor(p.after), Preview: preview}
	if preview {
		input.CandidateBudget = NativePreviewCandidates - p.candidates
		if len(p.streams) > 1 {
			// Give every library a work quantum before revisiting a broad one.
			// All MATCH and reverse-owner work still shares the same 2,000 total.
			input.CandidateBudget = min(input.CandidateBudget, NativePreviewCandidates/len(p.streams))
		}
		if input.CandidateBudget <= 0 {
			err = ErrInput
			return
		}
	}
	if p.stamp != nil {
		input.Retained = append(input.Retained, p.stamp)
	}
	for _, anchor := range p.anchors {
		if anchor != nil {
			input.Retained = append(input.Retained, anchor)
		}
	}
	if p.input.After != nil && p.input.After.anchor != nil {
		input.Retained = append(input.Retained, p.input.After.anchor)
	}
	var page FullSearchPage
	page, cost, err = r.read(ctx, request, min(budget, time.Until(p.lease.Deadline())), output, input, p.input.Query)
	if err != nil {
		return
	}
	page, err = ownFullSearchPage(page, 20)
	if err != nil {
		return
	}
	if page.NativeCandidates < 0 || preview && page.NativeCandidates > input.CandidateBudget {
		err = ErrInput
		return
	}
	p.candidates += page.NativeCandidates
	if len(p.hits) > 0 && r.custody == nil {
		err = ErrUnqualified
		return
	}
	if r.custody != nil {
		priors := input.Retained
		if len(priors) == 0 {
			priors = []*FullSearchReadStamp{nil}
		}
		for _, prior := range priors {
			if err = r.custody(ctx, output, input, page, p.hits, prior); err != nil {
				return
			}
		}
	}
	if !preview && page.Partial {
		err = ErrUnqualified
		return
	}
	if p.after != nil && page.Next != nil && (p.after.bank != page.Next.bank || p.after.revision != page.Next.revision || p.after.input != page.Next.input || p.after.query != page.Next.query) {
		err = ErrStale
		return
	}
	if page.Disposition == SearchUnavailable {
		err = ErrUnqualified
		return
	}
	if page.Disposition == SearchResults {
		if !preview && !page.Exhausted && (page.Next == nil || len(page.Hits) == 0) {
			err = ErrInput
			return
		}
		if !preview && page.Next != nil && compareFullRank(page.Next.after, hitRank(page.Hits[len(page.Hits)-1])) != 0 {
			err = ErrStale
			return
		}
		for _, h := range page.Hits {
			if h.Library != v.Library || h.Subject == search.ManifestPerson && h.Kind != 0 || h.Subject == search.ManifestEntity && (h.Kind <= 0 || h.Kind > 99) || v.Kind > 0 && (h.Subject != search.ManifestEntity || h.Kind != v.Kind) {
				err = ErrInput
				return
			}
			if !preview {
				rank := hitRank(h)
				if p.lastRank != nil && compareFullRank(*p.lastRank, rank) >= 0 {
					err = ErrStale
					return
				}
				p.lastRank = &rank
			}
			if err = p.keep(h); err != nil {
				return
			}
			if len(p.streams) > 1 && rsSearchHitGroup(h) == p.input.Group && (p.input.After == nil || p.input.After.order == nil || rsSearchCompareKey(rsSearchOrderKeyFor(h), *p.input.After.order, rsSearchOrderFor(p.input.Group, p.input.Order), p.input.Direction) > 0) {
				p.streams[p.library].matched++
			}
		}
	}
	p.exhausted = page.Exhausted
	if page.Next != nil {
		if page.Next.query != p.input.Query.Fold() {
			err = ErrStale
			return
		}
		p.cursorBase = cloneFullCursor(page.Next)
		p.cursorBase.native = nil
	}
	p.after = cloneFullCursor(page.Next)
	// Current native evidence covers both revalidated prior hits and new hits.
	// Only RD may issue its bounded subset; RS never constructs an authority stamp.
	if page.Stamp != nil {
		if rsSearchRetainedBytes(p)+2*page.Stamp.MetadataBytes() > p.lease.PayloadBytes() {
			err = governor.ErrSnapshotBudget
			return
		}
		p.stamp, err = page.Stamp.RetainHits(p.hits)
		if err != nil {
			return
		}
		if len(p.anchors) > 0 {
			p.anchors[p.library], err = page.Stamp.RetainHits(nil)
			if err != nil {
				return
			}
		}
	}
	if len(p.streams) > 1 {
		stream := &p.streams[p.library]
		stream.after, stream.lastRank = p.after, p.lastRank
		stream.exhausted = page.Exhausted
		stream.done = page.Exhausted || preview && page.Disposition == SearchResults && page.Next == nil || !preview && p.input.Order == "relevance" && p.input.Direction == "desc" && stream.matched >= p.pageLimit()+1
		p.exhausted = true
		for _, s := range p.streams {
			p.exhausted = p.exhausted && s.exhausted
		}
	}
	used := rsSearchRetainedBytes(p)
	if used > p.lease.PayloadBytes() {
		err = governor.ErrSnapshotBudget
		return
	}
	stop := page.Exhausted
	if len(p.streams) > 1 {
		stop = true
		for _, s := range p.streams {
			stop = stop && s.done
		}
	}
	if preview {
		stop = stop || p.previewFilled() || p.candidates == NativePreviewCandidates || len(p.streams) == 0 && page.Disposition == SearchResults && page.Next == nil
	} else {
		stop = stop || len(p.streams) == 0 && p.hasLookahead()
	}
	if !stop {
		if len(p.streams) > 1 {
			for n := 1; n <= len(p.streams); n++ {
				i := (p.library + n) % len(p.streams)
				if !p.streams[i].done {
					p.library = i
					p.after, p.lastRank = p.streams[i].after, p.streams[i].lastRank
					break
				}
			}
		}
		result.Pending = true
		result.NativeCandidates = p.candidates
		return
	}
	if err = output.live.checkContext(ctx); err != nil {
		return
	}
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	if err = r.authority.ReadyPreparation(p.lease); err != nil {
		return
	}
	if used > output.Request.Need[governor.MemoryBytes]/2 {
		err = governor.ErrSnapshotBudget
		return
	}
	result, err = p.compose()
	if err != nil {
		return
	}
	if keepReady {
		return
	}
	if err = r.authority.HandoffPreparation(p.lease); err != nil {
		return
	}
	_ = p.close()
	return
}

func (p *RSSearchPreparation) keep(h FullSearchHit) error {
	g := rsSearchHitGroup(h)
	if !rsSearchPublicGroup(g) || p.input.Group != 0 && p.input.Group != g {
		return nil
	}
	order := rsSearchOrderFor(g, p.input.Order)
	if p.input.After != nil && p.input.After.order != nil && rsSearchCompareKey(rsSearchOrderKeyFor(h), *p.input.After.order, order, p.input.Direction) <= 0 {
		return nil
	}
	if p.seen != nil {
		key := hitRank(h).key
		if _, found := p.seen[key]; !found {
			if len(p.seen) == NativePreviewCandidates {
				return ErrUnqualified
			}
			p.seen[key] = struct{}{}
		}
	}
	width := p.pageLimit() + 1
	count, worst, same := 0, -1, -1
	for i, old := range p.hits {
		if rsSearchHitGroup(old) != g {
			continue
		}
		count++
		if old.Subject == h.Subject && old.ID == h.ID {
			same = i
		}
		if worst < 0 || rsSearchCompare(old, p.hits[worst], order, p.input.Direction) > 0 {
			worst = i
		}
	}
	replace := -1
	if same >= 0 {
		if rsSearchCompare(h, p.hits[same], order, p.input.Direction) >= 0 {
			return nil
		}
		replace = same
	} else if count == width {
		if rsSearchCompare(h, p.hits[worst], order, p.input.Direction) >= 0 {
			return nil
		}
		replace = worst
	} else if len(p.hits) == p.input.Capacity {
		return ErrUnqualified
	}
	if rsSearchRetainedBytes(p)+rsSearchHitBytes(h) > p.lease.PayloadBytes() {
		return governor.ErrSnapshotBudget
	}
	if replace >= 0 {
		p.hits[replace] = h
	} else {
		p.hits = append(p.hits, h)
	}
	return nil
}
func (p *RSSearchPreparation) pageLimit() int {
	if p.input.Full {
		return 40
	}
	return 8
}
func (p *RSSearchPreparation) previewFilled() bool {
	for _, g := range rsSearchGroups {
		n := 0
		for _, h := range p.hits {
			if rsSearchHitGroup(h) == g {
				n++
			}
		}
		if n < 8 {
			return false
		}
	}
	return true
}
func (p *RSSearchPreparation) hasLookahead() bool {
	return p.input.Group != 0 && p.input.Order == "relevance" && p.input.Direction == "desc" && len(p.hits) > p.pageLimit()
}
func (p *RSSearchPreparation) compose() (RSSearchPage, error) {
	keys := []int{int(p.input.Group)}
	if p.input.Group == 0 {
		keys = keys[:0]
		for _, g := range rsSearchGroups {
			keys = append(keys, int(g))
		}
		sort.Ints(keys)
	}
	// Sort the private fixed-capacity buffer in place. The output DTO copy is
	// the only second full-hit buffer; no per-group growing slice is retained.
	sort.Slice(p.hits, func(i, j int) bool {
		a, b := rsSearchHitGroup(p.hits[i]), rsSearchHitGroup(p.hits[j])
		if a != b {
			return a < b
		}
		return rsSearchCompare(p.hits[i], p.hits[j], rsSearchOrderFor(a, p.input.Order), p.input.Direction) < 0
	})
	preview := p.input.Group == 0
	out := RSSearchPage{Complete: true, Partial: preview, BudgetStopped: preview && p.candidates >= NativePreviewCandidates, NativeCandidates: p.candidates}
	for _, k := range keys {
		g := RSSearchGroup(k)
		start, end := 0, 0
		for start < len(p.hits) && rsSearchHitGroup(p.hits[start]) < g {
			start++
		}
		end = start
		for end < len(p.hits) && rsSearchHitGroup(p.hits[end]) == g {
			end++
		}
		hits := p.hits[start:end]
		n := min(p.pageLimit(), len(hits))
		more := len(hits) > n
		gp := RSSearchGroupPage{Group: g, Hits: append([]FullSearchHit(nil), hits[:n]...), HasMore: more, HasMoreKnown: more || !preview && p.exhausted, Count: uint64(len(hits)), CountStatus: SearchCountUnknown}
		if len(hits) > 0 {
			gp.CountStatus = SearchCountLowerBound
		}
		if more && !preview {
			gp.Next = &RSSearchCursor{identity: p.identity}
			gp.Next.anchors = slices.Clone(p.anchors)
			if len(p.streams) == 0 && p.stamp != nil {
				var e error
				gp.Next.anchor, e = p.stamp.RetainHits(nil)
				if e != nil {
					return RSSearchPage{}, e
				}
			}
			if len(p.streams) == 0 && p.input.Order == "relevance" && p.input.Direction == "desc" && p.cursorBase != nil {
				gp.Next.rank = cloneFullCursor(p.cursorBase)
				gp.Next.rank.after = hitRank(hits[n-1])
			} else {
				key := rsSearchOrderKeyFor(hits[n-1])
				gp.Next.order = &key
			}
		}
		out.Groups = append(out.Groups, gp)
	}
	return out, nil
}

var rsSearchGroups = []RSSearchGroup{RSSearchPeople, 1, 2, 4, 5, 6, 7, 8, 10, 13, RSSearchReading}

func rsSearchValidOrder(order string) bool {
	return order == "relevance" || order == "title" || order == "releaseYear" || order == "dateAdded"
}
func rsSearchHitGroup(h FullSearchHit) RSSearchGroup {
	if h.Subject == search.ManifestPerson {
		return RSSearchPeople
	}
	if h.Kind == 21 {
		return RSSearchReading
	}
	return RSSearchGroup(h.Kind)
}
func rsSearchPublicGroup(g RSSearchGroup) bool { return slices.Contains(rsSearchGroups, g) || g == 21 }
