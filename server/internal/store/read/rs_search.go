package read

import (
	"context"
	"crypto/sha256"
	"fmt"
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
	Views    []BrowseInput // explicit authorized scope; currently exactly one library
	Query    search.Query
	Group    RSSearchGroup // zero previews all native groups; positive entity kind
	Full     bool          // 40/group instead of 8/group
	Order    string        // relevance or title; alternate orders require complete collection
	Capacity int           // retained-match bound, <=512; overflow refuses without output
	After    *RSSearchCursor
}
type RSSearchCursor struct {
	identity [32]byte
	offset   int
	rank     *FullSearchCursor
}
type RSSearchGroupPage struct {
	Group   RSSearchGroup
	Hits    []FullSearchHit
	HasMore bool
	Next    *RSSearchCursor
}
type RSSearchPage struct {
	Groups            []RSSearchGroupPage
	Pending, Complete bool
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
}

func NewRSSearchReader(a *governor.Authority, pool *governor.Snapshots) (*RSSearchReader, error) {
	n, e := NewNativeFullSearchReader(pool)
	if e != nil {
		return nil, e
	}
	return newRSSearchReaderWithRead(a, n.Read, rsNativeSearchCustody)
}
func newRSSearchReaderWithRead(a *governor.Authority, read FullSearchRead, custody rsSearchCustody) (*RSSearchReader, error) {
	if a == nil || read == nil {
		return nil, ErrInput
	}
	return &RSSearchReader{a, read, custody}, nil
}

type RSSearchPreparation struct {
	mu         sync.Mutex
	reader     *RSSearchReader
	lease      *governor.PreparationLease
	owner      governor.OwnerID
	input      RSSearchInput
	identity   [32]byte
	after      *FullSearchCursor
	stamp      *FullSearchReadStamp
	cursorBase *FullSearchCursor
	lastRank   *canonicalCandidateProof
	hits       []FullSearchHit
	closed     bool
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
	p.cursorBase = nil
	p.lastRank = nil
	_ = p.reader.authority.CancelPreparation(p.lease)
	return p.reader.authority.RetirePreparation(p.lease)
}
func (r *RSSearchReader) Begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, bytes int64, in RSSearchInput) (*RSSearchPreparation, *governor.Refusal, error) {
	if r == nil || ctx == nil || owner == 0 || len(in.Views) == 0 || in.Query.Mode() != search.ReadFull || in.Capacity < 1 || in.Capacity > 512 || (in.Order != "relevance" && in.Order != "title") {
		return nil, nil, ErrInput
	}
	if in.Order != "relevance" {
		return nil, nil, ErrUnqualified
	}
	if len(in.Views) != 1 || in.Group == RSSearchLiveTV {
		return nil, nil, ErrUnqualified
	}
	if in.Group < RSSearchPeople || in.Group > 99 || in.Group != 0 && !rsSearchPublicGroup(in.Group) || (in.Full && in.Group == 0) {
		return nil, nil, ErrInput
	}
	if in.Group == 21 {
		in.Group = RSSearchReading
	}
	v := in.Views[0]
	if v.Library <= 0 || v.Database == "" || v.TokenHash == "" || v.Now.IsZero() || v.After != nil || v.Kind != 0 || v.Descending || v.KeepArticles {
		return nil, nil, ErrInput
	}
	keyView := v
	keyView.Now = time.Time{}
	keyView.Limit = 0
	identity := sha256.Sum256([]byte(fmt.Sprintf("%d/%#v/%s/%d/%t/%s/%d", owner, keyView, in.Query.Fold(), in.Group, in.Full, in.Order, in.Capacity)))
	if in.After != nil && (in.After.identity != identity || in.After.offset < 0 || in.After.rank != nil && (in.After.offset != 0 || in.After.rank.native != nil || in.After.rank.query != in.Query.Fold() || in.After.rank.bank <= 0 || in.After.rank.revision <= 0 || in.After.rank.input < 0)) {
		return nil, nil, ErrStale
	}
	in.Views = append([]BrowseInput(nil), in.Views...)
	if in.After != nil {
		c := *in.After
		c.rank = cloneFullCursor(c.rank)
		in.After = &c
	}
	cursorKey := "first"
	if in.After != nil {
		cursorKey = fmt.Sprintf("offset:%d", in.After.offset)
		if c := in.After.rank; c != nil {
			cursorKey = fmt.Sprintf("rank:%d/%d/%d/%s/%d/%d/%d/%d/%d", c.bank, c.revision, c.input, c.query, c.after.key.family, c.after.key.id, c.after.key.library, c.after.tier, c.after.subtier)
		}
	}
	position := sha256.Sum256([]byte(cursorKey))
	d := r.authority.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("rs-search:%x:%x", identity, position)}, Owner: owner, Deadline: deadline, ReservedBytes: bytes})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	p := &RSSearchPreparation{reader: r, lease: d.Lease, owner: owner, input: in, identity: identity}
	if in.After != nil {
		p.after = cloneFullCursor(in.After.rank)
	}
	// Conservative bounded retained DTO capacity; actual native SQL/transient
	// byte accounting remains entirely RD-owned. Wide retained pages refuse.
	if p.lease.PayloadBytes() < 4096+int64(in.Capacity)*256 {
		_ = p.close()
		return nil, nil, governor.ErrSnapshotBudget
	}
	return p, nil, nil
}

// Poll must run inside the caller's ConsumeForeground custody. Exactly one
// FullSearchRead is issued; no fanout or serial LIVE reuse. Complete means the
// requested page plus its verified lookahead is ready, or the native stream
// is exhausted. A bounded raw prefix alone never proves completion.
func (r *RSSearchReader) Poll(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSSearchPreparation) (result RSSearchPage, cost *BrowseReadResult, err error) {
	if r == nil || p == nil || p.reader != r || ctx == nil {
		return result, nil, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if output.live == nil || output.Authority != r.authority {
		return result, nil, ErrInput
	}
	if p.closed || p.owner != request.Owner {
		return result, nil, ErrInput
	}
	defer func() {
		if err != nil {
			_ = p.close()
			result = RSSearchPage{}
		}
	}()
	if err = output.live.check(); err != nil {
		return
	}
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	v := p.input.Views[0]
	v.Kind = int64(p.input.Group)
	if v.Kind < 0 || p.input.Group == RSSearchReading {
		v.Kind = 0
	}
	v.Limit = 20
	v.Now = time.Now()
	input := FullSearchInput{View: v, After: cloneFullCursor(p.after)}
	var page FullSearchPage
	page, cost, err = r.read(ctx, request, min(budget, time.Until(p.lease.Deadline())), output, input, p.input.Query)
	if err != nil {
		return
	}
	page, err = ownFullSearchPage(page, 20)
	if err != nil {
		return
	}
	if len(p.hits) > 0 && r.custody == nil {
		err = ErrUnqualified
		return
	}
	if r.custody != nil {
		if err = r.custody(ctx, output, input, page, p.hits, p.stamp); err != nil {
			return
		}
	}
	if p.after != nil && page.Next != nil && (p.after.bank != page.Next.bank || p.after.revision != page.Next.revision || p.after.input != page.Next.input || p.after.query != page.Next.query) {
		err = ErrStale
		return
	}
	if page.Disposition != SearchResults {
		if page.Disposition == SearchUnavailable {
			err = ErrUnqualified
			return
		}
		if page.Next != nil {
			if page.Next.query != p.input.Query.Fold() {
				err = ErrStale
				return
			}
			p.after = cloneFullCursor(page.Next)
		}
		p.stamp = page.Stamp
		if rsSearchRetainedBytes(p) > p.lease.PayloadBytes() {
			err = governor.ErrSnapshotBudget
			return
		}
		result.Pending = true
		return
	}
	if page.Next != nil && (page.Next.query != p.input.Query.Fold() || len(page.Hits) == 0 || compareFullRank(page.Next.after, hitRank(page.Hits[len(page.Hits)-1])) != 0) {
		err = ErrStale
		return
	}
	if !page.Exhausted && (page.Next == nil || len(page.Hits) == 0) {
		err = ErrInput
		return
	}
	if p.after != nil && page.Next != nil && (p.after.bank != page.Next.bank || p.after.revision != page.Next.revision || p.after.input != page.Next.input || p.after.query != page.Next.query) {
		err = ErrStale
		return
	}
	for _, h := range page.Hits {
		if h.Library != v.Library || (h.Subject == search.ManifestPerson && h.Kind != 0) || (h.Subject == search.ManifestEntity && (h.Kind <= 0 || h.Kind > 99)) || (v.Kind > 0 && (h.Subject != search.ManifestEntity || h.Kind != v.Kind)) {
			err = ErrInput
			return
		}
		rank := hitRank(h)
		if p.lastRank != nil && compareFullRank(*p.lastRank, rank) >= 0 {
			err = ErrStale
			return
		}
		p.lastRank = &rank
		if !rsSearchPublicGroup(rsSearchHitGroup(h)) {
			continue
		}
		if p.input.Group != 0 && rsSearchHitGroup(h) != p.input.Group {
			continue
		}
		if len(p.hits) == p.input.Capacity {
			err = ErrUnqualified
			return
		}
		p.hits = append(p.hits, h)
	}
	p.stamp = page.Stamp
	if page.Next != nil {
		p.cursorBase = cloneFullCursor(page.Next)
		p.cursorBase.native = nil
	}
	p.after = cloneFullCursor(page.Next)
	used := rsSearchRetainedBytes(p)
	if used > p.lease.PayloadBytes() {
		err = governor.ErrSnapshotBudget
		return
	}
	if err = output.live.check(); err != nil {
		return
	}
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	if !page.Exhausted && !p.hasLookahead() {
		result.Pending = true
		return
	}
	if err = r.authority.ReadyPreparation(p.lease); err != nil {
		return
	}
	// The current output grant takes custody before the preparation retires.
	if used > output.Request.Need[governor.MemoryBytes]/2 {
		err = governor.ErrSnapshotBudget
		return
	}
	result, err = p.compose()
	if err != nil {
		return
	}
	if err = r.authority.HandoffPreparation(p.lease); err != nil {
		return
	}
	_ = p.close()
	return
}
func (p *RSSearchPreparation) compose() (RSSearchPage, error) {
	groups := map[RSSearchGroup][]FullSearchHit{}
	for _, h := range p.hits {
		g := RSSearchGroup(h.Kind)
		if h.Kind == 21 {
			g = RSSearchReading
		}
		if h.Subject == search.ManifestPerson {
			g = RSSearchPeople
		}
		if p.input.Group == 0 || p.input.Group == g {
			groups[g] = append(groups[g], h)
		}
	}
	if p.input.Group != 0 && groups[p.input.Group] == nil {
		groups[p.input.Group] = []FullSearchHit{}
	}
	keys := make([]int, 0, len(groups))
	for g := range groups {
		keys = append(keys, int(g))
	}
	sort.Ints(keys)
	out := RSSearchPage{Complete: true}
	limit := 8
	if p.input.Full {
		limit = 40
	}
	for _, k := range keys {
		g := RSSearchGroup(k)
		hits := groups[g]
		start := 0
		if p.input.After != nil && p.input.After.rank == nil {
			start = p.input.After.offset
		}
		if start > len(hits) {
			return RSSearchPage{}, ErrStale
		}
		end := min(start+limit, len(hits))
		more := len(hits) > end // lookahead9/41, never a guessed total
		gp := RSSearchGroupPage{Group: g, Hits: append([]FullSearchHit(nil), hits[start:end]...), HasMore: more}
		if more && p.input.Group != 0 {
			gp.Next = &RSSearchCursor{identity: p.identity, offset: end}
			if p.cursorBase != nil {
				gp.Next.rank = cloneFullCursor(p.cursorBase)
				gp.Next.rank.after = hitRank(hits[end-1])
				gp.Next.offset = 0
			}
		}
		out.Groups = append(out.Groups, gp)
	}
	return out, nil
}

// Relevance is a globally ordered stream. A requested group needs only its
// bounded visible page and one real lookahead; alternate orders require a
// complete match set and remain explicitly unsupported until their source facts
// are available. All-group previews still require complete native exhaustion.
func (p *RSSearchPreparation) hasLookahead() bool {
	if p.input.Group == 0 {
		return false
	}
	limit := 8
	if p.input.Full {
		limit = 40
	}
	n := 0
	for _, h := range p.hits {
		g := RSSearchGroup(h.Kind)
		if h.Kind == 21 {
			g = RSSearchReading
		}
		if h.Subject == search.ManifestPerson {
			g = RSSearchPeople
		}
		if g == p.input.Group {
			n++
		}
	}
	return n > limit
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

func rsSearchPublicGroup(g RSSearchGroup) bool {
	switch g {
	case RSSearchPeople, 1, 2, 4, 5, 6, 7, 8, 10, 13, RSSearchReading, 21:
		return true
	}
	return false
}
