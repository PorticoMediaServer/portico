package read

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// FullSearchHit keeps entity/person identities disjoint. It is an internal
// harness DTO, not a wire codec, person visibility proof or native attestation.
type FullSearchHit struct {
	Subject             search.ManifestSubject
	ID, Library, Kind   int64
	Tier, PeopleSubtier uint8
	Item                BrowseItem
	PersonName          string
}

// FullSearchCursor is opaque and query/bank-bound. A future wire codec must
// authenticate it and bind the actual viewer/source/profile cohort as well;
// the pure rank cursor cannot itself authorize a native read.
type FullSearchCursor struct {
	query                 string
	bank, revision, input int64
	after                 canonicalCandidateProof
	native                *nativeContinuation
}
type FullSearchInput struct {
	View  BrowseInput
	After *FullSearchCursor
}
type FullSearchPage struct {
	Disposition SearchDisposition
	Hits        []FullSearchHit
	Next        *FullSearchCursor
	Exhausted   bool
	// Stamp is present only for a real native read joined to its fresh final
	// fence, including pending and exhausted pages. Synthetic adapters may not
	// manufacture one; nil cannot validate retained results from another poll.
	Stamp *FullSearchReadStamp
}

// FullSearchRead is RD's separate full-mode seam. Trusted composition must use
// a qualified SAME-pool public LIVE reader, actual accepted bank/profile/source
// evidence and fixed-slot READ attestation. No native implementation is
// installed by this component. A WRITE receipt cannot supply this evidence.
type FullSearchRead func(context.Context, governor.Request, time.Duration, BrowseOutput, FullSearchInput, search.Query) (FullSearchPage, *BrowseReadResult, error)

func NewFullSearchDispatcher(requests *search.SearchRequests, g governor.Governor, a *governor.Authority, pool *governor.Snapshots, titles TitleSearchRead, full FullSearchRead) (*SearchDispatcher, error) {
	d, err := NewSearchDispatcher(requests, g, a, pool, titles)
	if err != nil {
		return nil, err
	}
	d.full = full
	return d, nil
}

func cloneFullCursor(c *FullSearchCursor) *FullSearchCursor {
	if c == nil {
		return nil
	}
	out := *c
	out.query = strings.Clone(c.query)
	out.after.lead.Revision = strings.Clone(c.after.lead.Revision)
	out.native = cloneNativeContinuation(c.native)
	return &out
}
func hitRank(h FullSearchHit) canonicalCandidateProof {
	return canonicalCandidateProof{key: fullResultKey{h.Subject, h.ID, h.Library}, tier: h.Tier, subtier: h.PeopleSubtier}
}

func ownFullSearchPage(p FullSearchPage, limit int) (FullSearchPage, error) {
	if p.Disposition < SearchResults || p.Disposition > SearchUnavailable || len(p.Hits) > limit || p.Exhausted && p.Next != nil || p.Disposition != SearchResults && (len(p.Hits) != 0 || p.Exhausted || p.Next != nil && (p.Disposition != SearchPending || p.Next.native == nil)) {
		return FullSearchPage{}, ErrInput
	}
	if p.Next != nil && (len(p.Next.query) > 1536 || len(p.Next.after.lead.Revision) > 4096 || p.Next.bank <= 0 || p.Next.revision <= 0 || p.Next.input < 0) {
		return FullSearchPage{}, ErrInput
	}
	for i, h := range p.Hits {
		if h.ID <= 0 || h.Library < 0 || h.Tier > 5 || h.PeopleSubtier > 2 || h.Subject != search.ManifestEntity && h.Subject != search.ManifestPerson || h.Subject == search.ManifestPerson && h.Tier != 3 || h.Subject == search.ManifestEntity && (h.Tier == 3 || h.PeopleSubtier != 0) || len(h.Item.Title) > 65536 || len(h.PersonName) > 65536 || !utf8.ValidString(h.Item.Title) || !utf8.ValidString(h.PersonName) {
			return FullSearchPage{}, ErrInput
		}
		if h.Subject == search.ManifestEntity && (h.Item.ID != h.ID || h.PersonName != "") || h.Subject == search.ManifestPerson && h.Item != (BrowseItem{}) {
			return FullSearchPage{}, ErrInput
		}
		if i > 0 && compareFullRank(hitRank(p.Hits[i-1]), hitRank(h)) >= 0 {
			return FullSearchPage{}, ErrInput
		}
	}
	p.Hits = slices.Clone(p.Hits)
	for i := range p.Hits {
		p.Hits[i].Item.Title = strings.Clone(p.Hits[i].Item.Title)
		p.Hits[i].PersonName = strings.Clone(p.Hits[i].PersonName)
	}
	p.Next = cloneFullCursor(p.Next)
	if p.Stamp != nil {
		stamp := *p.Stamp
		p.Stamp = &stamp
	}
	return p, nil
}

// A finite top-K, not a query-wide seen set. Only the canonical lead-source
// AND inverse relation route may emit a resolved result. The candidate proof
// already contains its BEST complete rank, irrespective of arrival route.
type fullSearchTop struct {
	scope *fullQueryScope
	limit int
	after *FullSearchCursor
	hits  []FullSearchHit
	more  bool
}

func newFullSearchTop(scope *fullQueryScope, limit int, after *FullSearchCursor) (*fullSearchTop, error) {
	if scope == nil || limit < 1 || limit > MaxSearchTitles {
		return nil, ErrInput
	}
	if after != nil && (after.query != scope.query.Fold() || after.bank != scope.bank || after.revision != scope.revision || after.input != scope.input) {
		return nil, ErrStale
	}
	return &fullSearchTop{scope: scope, limit: limit, after: cloneFullCursor(after)}, nil
}
func (p *fullSearchTop) observe(proof canonicalCandidateProof, source search.FieldKey, route fullRouteKey, hit FullSearchHit) error {
	if hitRank(hit).key != proof.key || hit.Tier != proof.tier || hit.PeopleSubtier != proof.subtier {
		return ErrInput
	}
	if source != proof.lead || route != proof.route {
		return nil
	}
	if p.after != nil && compareFullRank(proof, p.after.after) <= 0 {
		return nil
	}
	owned, err := ownFullSearchPage(FullSearchPage{Disposition: SearchResults, Hits: []FullSearchHit{hit}}, 1)
	if err != nil {
		return err
	}
	hit = owned.Hits[0]
	for _, old := range p.hits {
		if hitRank(old).key == proof.key {
			return nil
		}
	}
	at := 0
	for at < len(p.hits) && compareFullRank(hitRank(p.hits[at]), proof) < 0 {
		at++
	}
	if at >= p.limit {
		p.more = true
		return nil
	}
	p.hits = slices.Insert(p.hits, at, hit)
	if len(p.hits) > p.limit {
		p.hits = p.hits[:p.limit]
		p.more = true
	}
	return nil
}
func (p *fullSearchTop) finish(complete bool) (FullSearchPage, error) {
	if !complete {
		return FullSearchPage{Disposition: SearchPending}, nil
	}
	out := FullSearchPage{Disposition: SearchResults, Hits: p.hits, Exhausted: !p.more}
	if p.more && len(p.hits) > 0 {
		last := hitRank(p.hits[len(p.hits)-1])
		out.Next = &FullSearchCursor{query: p.scope.query.Fold(), bank: p.scope.bank, revision: p.scope.revision, input: p.scope.input, after: last}
	}
	return ownFullSearchPage(out, p.limit)
}
