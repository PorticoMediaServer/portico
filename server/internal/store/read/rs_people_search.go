package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// RSPeopleSearchInput keeps the directory's complete trusted credit scope
// explicit, just like RSPersonPageInput. Native search supplies the global
// selected People order; it does not stand in for role/count/portrait reads.
// A hit without a visible credit in this scope refuses instead of silently
// dropping it and presenting an underfilled page as the selected top forty.
type RSPeopleSearchInput struct {
	Views            []BrowseInput
	Query            search.Query
	Order, Direction string
	After            *RSPeopleSearchCursor
}
type RSPeopleSearchCursor struct {
	scope  [32]byte
	native *RSSearchCursor
}
type RSPeopleSearchPage struct {
	People            []RSPersonSummary
	Next              *RSPeopleSearchCursor
	CountStatus       SearchCountStatus
	Pending, Complete bool
}
type RSPeopleSearchReader struct {
	search *RSSearchReader
	people *RSPeoplePageReader
}

func NewRSPeopleSearchReader(pool *governor.Snapshots, a *governor.Authority) (*RSPeopleSearchReader, error) {
	s, e := NewRSSearchReader(a, pool)
	if e != nil {
		return nil, e
	}
	p, e := NewRSPeoplePageReader(pool, a)
	if e != nil {
		return nil, e
	}
	return &RSPeopleSearchReader{search: s, people: p}, nil
}

// RSPeopleSearchPreparation owns exactly one search F04 lease. The private
// people reducer borrows that lease; it never begins, hands off or retires it.
// Its storage, summary copies and native retained proofs share the same cap.
type RSPeopleSearchPreparation struct {
	reader   *RSPeopleSearchReader
	search   *RSSearchPreparation
	work     *RSPeoplePreparation
	scope    [32]byte
	selected bool
	ids      []int64
	next     *RSSearchCursor
	entries  []rsPeopleDirectoryEntry
	retained int64
}

func (p *RSPeopleSearchPreparation) Close() error {
	if p == nil || p.search == nil {
		return nil
	}
	p.search.mu.Lock()
	defer p.search.mu.Unlock()
	return p.close()
}
func (p *RSPeopleSearchPreparation) close() error {
	p.work = nil
	p.ids = nil
	p.next = nil
	p.entries = nil
	return p.search.close()
}
func (r *RSPeopleSearchReader) Begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, bytes int64, in RSPeopleSearchInput) (*RSPeopleSearchPreparation, *governor.Refusal, error) {
	if r == nil || len(in.Views) == 0 || len(in.Views) > 64 || (in.Order != "title" && in.Order != "relevance") {
		return nil, nil, ErrInput
	}
	views := slices.Clone(in.Views)
	sort.Slice(views, func(i, j int) bool {
		if views[i].Library != views[j].Library {
			return views[i].Library < views[j].Library
		}
		return views[i].Kind < views[j].Kind
	})
	h := sha256.New()
	libraries := []BrowseInput{}
	for i, v := range views {
		if v.Database == "" || len(v.Database) > 256 || v.TokenHash == "" || len(v.TokenHash) > 256 || v.Library <= 0 || v.Kind <= 0 || v.Now.IsZero() || v.After != nil || v.Descending || v.KeepArticles || v.Database != views[0].Database || v.TokenHash != views[0].TokenHash || !v.Now.Equal(views[0].Now) {
			return nil, nil, ErrInput
		}
		recipe, e := resolveReadPolicy(v)
		if e != nil {
			return nil, nil, e
		}
		if !validReadPolicyScope(v, recipe) {
			return nil, nil, ErrInput
		}
		if i > 0 && views[i-1].Library == v.Library && views[i-1].Kind == v.Kind {
			return nil, nil, ErrInput
		}
		v.Limit = MaxPage
		views[i] = v
		key := v
		key.Now = time.Time{}
		fmt.Fprintf(h, "%#v\n", key)
		if i == 0 || views[i-1].Library != v.Library {
			v.Kind = 0
			v.policy = readPolicyBinding{}
			libraries = append(libraries, v)
		}
	}
	var scope [32]byte
	copy(scope[:], h.Sum(nil))
	var after *RSSearchCursor
	if in.After != nil {
		if in.After.scope != scope {
			return nil, nil, ErrStale
		}
		after = in.After.native
	}
	sp, ref, e := r.search.Begin(ctx, owner, deadline, bytes, RSSearchInput{Views: libraries, Query: in.Query, Group: RSSearchPeople, Full: true, Order: in.Order, Direction: in.Direction, Capacity: 64, After: after})
	if e != nil || ref != nil {
		return nil, ref, e
	}
	p := &RSPeopleSearchPreparation{reader: r, search: sp, scope: scope}
	p.work = &RSPeoplePreparation{reader: r.people, lease: sp.lease, owner: owner, input: RSPersonPageInput{Views: views, Limit: MaxPage}, started: sp.started}
	return p, nil, nil
}

// revalidate makes the ONE native call for a hydration poll. It joins the kept
// hits and all own-scope anchors into the new output grant before any summary
// is reused. Additional returned matches do not alter the selected page.
func (r *RSPeopleSearchReader) revalidate(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation) (*BrowseReadResult, error) {
	s := p.search
	v := s.input.Views[s.library]
	v.Kind = 0
	v.Limit = 20
	v.Now = v.Now.Add(time.Since(s.started))
	in := FullSearchInput{View: v, After: cloneFullCursor(s.after)}
	in.Retained = append(in.Retained, s.stamp)
	for _, a := range s.anchors {
		if a != nil {
			in.Retained = append(in.Retained, a)
		}
	}
	page, cost, e := r.search.read(ctx, req, budget, out, in, s.input.Query)
	if e != nil {
		return cost, e
	}
	if page.Stamp == nil || page.Partial || page.Disposition == SearchUnavailable {
		return cost, ErrUnqualified
	}
	for _, prior := range in.Retained {
		if prior == nil {
			return cost, ErrUnqualified
		}
		if e = page.Stamp.ValidateRetained(ctx, out, prior); e != nil {
			return cost, e
		}
	}
	if rsSearchRetainedBytes(s)+2*page.Stamp.MetadataBytes()+p.retained+p.work.used > s.lease.PayloadBytes() {
		return cost, governor.ErrSnapshotBudget
	}
	stamp, e := page.Stamp.RetainHits(s.hits)
	if e != nil {
		return cost, e
	}
	s.stamp = stamp
	if len(s.anchors) > 0 {
		s.anchors[s.library], e = page.Stamp.RetainHits(nil)
	}
	return cost, e
}
func rsPeopleSummaryBytes(v RSPersonSummary) int64 {
	n := int64(1024+4*(len(v.Token)+len(v.Name)+len(v.SortName))) + rsPortraitBytes(v.Portrait)
	for _, role := range v.Roles {
		n += int64(64 + 4*len(role))
	}
	return n
}
func rsPeopleCloneSummary(v RSPersonSummary) RSPersonSummary {
	v.Token = strings.Clone(v.Token)
	v.Name = strings.Clone(v.Name)
	v.SortName = strings.Clone(v.SortName)
	v.Roles = slices.Clone(v.Roles)
	for i := range v.Roles {
		v.Roles[i] = strings.Clone(v.Roles[i])
	}
	v.Portrait = rsClonePortrait(v.Portrait)
	return v
}
func (r *RSPeopleSearchReader) advance(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation) (bool, *BrowseReadResult, error) {
	s, w := p.search, p.work
	var cost *BrowseReadResult
	if !p.selected {
		page, c, e := r.search.pollLocked(ctx, req, budget, out, s, true)
		cost = c
		if e != nil || page.Pending {
			return false, c, e
		}
		if len(page.Groups) != 1 || page.Groups[0].Group != RSSearchPeople || page.Partial {
			return false, c, ErrUnqualified
		}
		g := page.Groups[0]
		p.next = g.Next
		for _, hit := range g.Hits {
			p.ids = append(p.ids, hit.ID)
		}
		p.selected = true
		// The cursor retains an own-scope stamp, in addition to the search state.
		p.retained = 8192 + int64(cap(p.ids))*16
		if p.next != nil {
			p.retained += 2 * p.next.anchor.MetadataBytes()
			for _, a := range p.next.anchors {
				p.retained += 2 * a.MetadataBytes()
			}
		}
		return false, c, nil
	}
	var e error
	cost, e = r.revalidate(ctx, req, budget, out, p)
	if e != nil {
		return false, cost, e
	}
	if cost == nil {
		cost = &BrowseReadResult{}
	}
	base := rsSearchRetainedBytes(s) + p.retained
	if base+w.used > s.lease.PayloadBytes() {
		return false, cost, governor.ErrSnapshotBudget
	}
	// charge() in the borrowed reducer sees the total shared reservation, not a
	// second independent allowance. Restore its local sub-account on return.
	w.used += base
	defer func() { w.used -= base }()
	at := w.input.Views[0].Now.Add(time.Since(s.started))
	if !w.captured {
		e = r.people.checkPeopleFences(ctx, req, out, w, at, cost)
		return false, cost, e
	}
	if len(p.entries) == len(p.ids) {
		return true, cost, nil
	}
	if w.current.person == 0 {
		w.current = rsPersonReduction{person: p.ids[len(p.entries)], works: map[int64]*rsPersonWork{}, departments: map[string]int{}}
	}
	if e = r.people.checkPeopleFences(ctx, req, out, w, at, cost); e != nil {
		return false, cost, e
	}
	if e = r.people.peopleQuantum(ctx, req, out, w, at, cost); e != nil {
		return false, cost, e
	}
	if !w.current.complete {
		return false, cost, nil
	}
	c := &w.current
	if c.facts == nil {
		return false, cost, ErrUnqualified
	}
	f := c.facts
	entry := rsPeopleDirectoryEntry{summary: RSPersonSummary{ID: f.ID, Token: f.Token, Name: f.Name, SortName: f.SortName, Roles: rsPeopleRoles(c.departments), CreditCount: c.creditCount, Portrait: c.portrait}, source: c.source, workIdentity: w.identity()}
	n := rsPeopleSummaryBytes(entry.summary)
	if n > w.lease.PayloadBytes()-w.used {
		return false, cost, governor.ErrSnapshotBudget
	}
	entry.summary = rsPeopleCloneSummary(entry.summary)
	p.entries = append(p.entries, entry)
	p.retained += n
	// Drop all credit payloads before refunding their local account. Previously
	// completed summaries and native proofs remain charged in retained/base.
	w.current = rsPersonReduction{}
	// Keep only the canonical scope fences, which survive between people.
	w.used = base
	for _, f := range w.fences {
		w.used += rsPeopleFenceBytes(f)
	}
	return false, cost, nil
}
func (r *RSPeopleSearchReader) publicationFence(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation) (BrowseReadResult, error) {
	w := p.work
	root := func(q browseQuery) ([]any, error) {
		at := w.input.Views[0].Now.Add(time.Since(w.started))
		for i, v := range w.input.Views {
			v.Now = at
			f, e := readBrowseFence(q, v)
			if e != nil {
				return nil, e
			}
			if f != w.fences[i] {
				return nil, ErrStale
			}
		}
		for _, entry := range p.entries {
			rows, e := q("rs.people.source", entry.summary.ID)
			if e != nil {
				return nil, e
			}
			if len(rows) != 1 || !equalSource(entry.source, rows[0]) {
				return nil, ErrStale
			}
		}
		return nil, nil
	}
	v := w.input.Views[0]
	v.Now = v.Now.Add(time.Since(w.started))
	_, cost, e := publishRead(ctx, rsSearchPublicationSnapshots{r.search.pool}, req, budget, out, v, []readStep[struct{}]{func(context.Context, browseQuery, browseFence, *struct{}) error { return nil }}, root)
	return cost, e
}

// Publish delivers complete native-ordered summaries only. Pending exposes no
// people or counts. The one original F04 lease and LIVE/output remain charged
// through encoding, both fresh fences and the joined consumer.
func (r *RSPeopleSearchReader) Publish(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation, encode func(context.Context, RSPeopleSearchPage) ([]byte, error), consume func(context.Context, []byte) error) (done bool, cost *BrowseReadResult, err error) {
	started := time.Now()
	defer func() {
		if cost != nil {
			cost.Elapsed = time.Since(started)
		}
	}()
	if r == nil || p == nil || p.reader != r || ctx == nil || budget <= 0 || encode == nil || consume == nil {
		return false, nil, ErrInput
	}
	s := p.search
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || out.live == nil || out.Authority != r.search.authority || s.owner != req.Owner {
		return false, nil, ErrInput
	}
	deadline := earliest(req.Deadline, out.Request.Deadline, s.lease.Deadline(), time.Now().Add(min(budget, time.Second)))
	operation, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	defer func() {
		if err != nil || done {
			err = errors.Join(err, p.close())
		}
	}()
	if err = out.live.checkContext(operation); err != nil {
		return
	}
	if err = r.search.authority.CheckPreparation(s.lease); err != nil {
		return
	}
	ready, c, e := r.advance(operation, req, time.Until(deadline), out, p)
	cost = c
	err = e
	if e != nil || !ready {
		return
	}
	page := RSPeopleSearchPage{Complete: true, CountStatus: SearchCountUnknown}
	if p.next != nil {
		page.Next = &RSPeopleSearchCursor{scope: p.scope, native: p.next}
	}
	copies := int64(4096)
	for _, entry := range p.entries {
		copies += rsPeopleSummaryBytes(entry.summary)
	}
	if copies > out.Request.Need[governor.MemoryBytes]/2 || copies > s.lease.PayloadBytes()-rsSearchRetainedBytes(s)-p.retained-p.work.used {
		err = governor.ErrSnapshotBudget
		return
	}
	for _, entry := range p.entries {
		page.People = append(page.People, rsPeopleCloneSummary(entry.summary))
	}
	payload, e := encode(operation, page)
	if e != nil {
		err = e
		return
	}
	if int64(len(payload)) > out.Request.Need[governor.MemoryBytes]/2-copies || int64(len(payload)) > s.lease.PayloadBytes()-rsSearchRetainedBytes(s)-p.retained-p.work.used-copies {
		err = governor.ErrSnapshotBudget
		return
	}
	validation, stop := context.WithDeadline(context.WithoutCancel(operation), deadline)
	defer stop()
	if cost == nil {
		cost = &BrowseReadResult{}
	}
	one, e := r.search.publicationFence(validation, req, time.Until(deadline), out, s)
	addSearchReadCost(cost, &one)
	if e != nil {
		err = e
		return
	}
	one, e = r.publicationFence(validation, req, time.Until(deadline), out, p)
	addSearchReadCost(cost, &one)
	if e != nil {
		err = e
		return
	}
	if err = operation.Err(); err != nil {
		return
	}
	if err = out.live.checkContext(operation); err != nil {
		return
	}
	if err = r.search.authority.ReadyPreparation(s.lease); err != nil {
		return
	}
	if err = r.search.authority.HandoffPreparation(s.lease); err != nil {
		return
	}
	done = true
	err = consume(operation, payload)
	if err == nil {
		err = errors.Join(operation.Err(), out.live.checkContext(operation))
	}
	return
}
