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
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RSPeopleSearchInput is the complete trusted credit scope for a native People
// query. The native bank supplies the global selected order; it never stands in
// for role/count/portrait/known-for reads. A ranked hit with no visible credit
// in this explicit scope refuses instead of being dropped from an underfilled
// page presented as the selected top forty.
//
// This adapter is keyset-only. The separately published People offset/count
// mode keeps its existing 1,000-match cap; native keysets neither expose that
// offset universe nor silently replace its capped count with an exact one.
type RSPeopleSearchInput struct {
	Views     []BrowseInput
	Query     search.Query
	Order     string // relevance (default) or title
	Direction string // empty uses the public sort default for Order
	After     *RSPeopleSearchCursor
}

// Cursors are private continuation values, never wire tokens or grants. They
// carry the native keyset and the exact scope identity that issued it.
type RSPeopleSearchCursor struct {
	scope  [32]byte
	native *RSSearchCursor
}

// RSPeopleSearchSummary is the exact selected-person directory card. Role,
// credit count, portrait and known-for are computed over every trusted
// library/kind view in the request, not only the view that ranked the hit.
type RSPeopleSearchSummary struct {
	RSPersonSummary
	KnownFor []TitleItem
}
type RSPeopleSearchPage struct {
	People            []RSPeopleSearchSummary
	Next              *RSPeopleSearchCursor
	CountStatus       SearchCountStatus
	Pending, Complete bool
}

// RSPeopleSearchReader composes native People selection with the merged
// complete PersonPage hydrator. It owns no statement, publisher, cache or
// second admission domain: the native search lease is borrowed by hydration.
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

// RSPeopleSearchDefinitions is inert composition metadata, not a new SQL bank.
// It names the already merged native People bank, role/count/credit-image and
// portrait points. There is deliberately no second name-FTS statement here.
func RSPeopleSearchDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition(nil), NativeFullSearchDefinitions()...)
	defs = append(defs, PersonCreditsDefinitions()...)
	return append(defs, RSPeoplePageDefinitions()...)
}

// RSPeopleSearchPreparation owns exactly ONE search F-04 lease. The private
// people reducer borrows that same lease; it never begins, hands off or
// retires a second preparation. Retained native proofs, selected ids, summary
// copies and reducer workspace all share the search reservation.
type RSPeopleSearchPreparation struct {
	reader   *RSPeopleSearchReader
	search   *RSSearchPreparation
	work     *RSPeoplePreparation
	scope    [32]byte
	selected bool
	ids      []int64
	next     *RSSearchCursor
	entries  []rsPeopleSearchEntry
	retained int64
}

// rsPeopleSearchEntry keeps the private exact summary and its source witness
// while the shared native proofs stay charged in the search preparation.
type rsPeopleSearchEntry struct {
	summary  RSPersonSummary
	knownFor []TitleItem
	source   []any
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
	if in.Direction == "" {
		in.Direction = rsSearchDefaultDirection(in.Order)
	}
	if in.Direction != "asc" && in.Direction != "desc" {
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
	fmt.Fprintf(h, "%q/%q/%q\n", in.Query.Fold(), in.Order, in.Direction)
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
			// Native People work is scoped per library but selects people, not
			// catalogue kinds. Hydration keeps the complete kind views above.
			v.Kind = 0
			v.policy = readPolicyBinding{}
			libraries = append(libraries, v)
		}
	}
	var scope [32]byte
	copy(scope[:], h.Sum(nil))
	var after *RSSearchCursor
	if in.After != nil {
		if in.After.scope != scope || in.After.native == nil {
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

// revalidate advances the ONE native call for a hydration poll. It carries a
// fence/revision witness for the retained scope instead of re-reading every
// retained row: each poll proves the current bank/source/permission fence and
// advances at most one bounded native page. A pending page is progress only
// when it kept a continuation; a pending census or a proof that cannot advance
// refuses explicitly and never becomes a ready empty page. The retained
// summaries themselves are re-proved by the borrower's source fences and by the
// complete delivery fence before any payload is published.
func (r *RSPeopleSearchReader) revalidate(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation) (bool, *BrowseReadResult, error) {
	s := p.search
	v := s.input.Views[s.library]
	v.Kind = 0
	v.Limit = 20
	v.Now = v.Now.Add(time.Since(s.started))
	in := FullSearchInput{View: v, After: cloneFullCursor(s.after)}
	if s.stamp == nil {
		return false, nil, ErrUnqualified
	}
	// s.stamp is the kept scope's fence witness: the selection reduced it to
	// metadata only, so no retained row is re-read on a hydration poll.
	in.Retained = append(in.Retained, s.stamp)
	for _, a := range s.anchors {
		if a != nil {
			in.Retained = append(in.Retained, a)
		}
	}
	page, cost, e := r.search.read(ctx, req, budget, out, in, s.input.Query)
	if e != nil {
		return false, cost, e
	}
	switch page.Disposition {
	case SearchResults:
	case SearchPending:
		if page.Next == nil || page.Stamp == nil {
			return false, cost, ErrUnqualified
		}
		// Keep the continuation so the next poll resumes this walk instead of
		// restarting it; the retained set is not re-read either way.
		s.after = cloneFullCursor(page.Next)
		return false, cost, nil
	default:
		return false, cost, ErrUnqualified
	}
	if page.Stamp == nil || page.Partial {
		return false, cost, ErrUnqualified
	}
	s.after = cloneFullCursor(page.Next)
	for _, hit := range page.Hits {
		// The native walk proves other groups' candidates for the same query;
		// only person hits are this adapter's selected scope.
		if hit.Subject != search.ManifestPerson {
			continue
		}
		if hit.Kind != 0 || hit.Library != 0 && hit.Library != v.Library {
			return false, cost, ErrInput
		}
	}
	for _, prior := range in.Retained {
		if prior == nil {
			return false, cost, ErrUnqualified
		}
		if e = page.Stamp.ValidateRetained(ctx, out, prior); e != nil {
			return false, cost, e
		}
	}
	if rsSearchRetainedBytes(s)+2*page.Stamp.MetadataBytes()+p.retained+p.work.used > s.lease.PayloadBytes() {
		return false, cost, governor.ErrSnapshotBudget
	}
	fence, e := page.Stamp.RetainHits(nil)
	if e != nil {
		return false, cost, e
	}
	// A fresh fence issued by THIS poll's read is required by the delivery
	// fence; it carries no per-hit proof, which the people delivery fence
	// re-proves from each selected person's current source instead.
	s.stamp = fence
	return true, cost, nil
}

func rsPeopleSearchSummaryBytes(v RSPeopleSearchSummary) int64 {
	n := int64(1024+4*(len(v.Token)+len(v.Name)+len(v.SortName))) + rsPortraitBytes(v.Portrait)
	for _, role := range v.Roles {
		n += int64(64 + 4*len(role))
	}
	for _, known := range v.KnownFor {
		n += int64(256 + 4*len(known.Title))
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
func rsPeopleSearchCloneSummary(v RSPeopleSearchSummary) RSPeopleSearchSummary {
	v.RSPersonSummary = rsPeopleCloneSummary(v.RSPersonSummary)
	v.KnownFor = slices.Clone(v.KnownFor)
	for i := range v.KnownFor {
		v.KnownFor[i] = rsCloneTitle(v.KnownFor[i])
	}
	return v
}

// rsPeopleSearchKnownFor mirrors the complete person page's known-for order
// (credit position, then newest added, then canonical id) over the same
// complete trusted scope rather than the ranking view alone.
func (w *RSPeoplePreparation) rsPeopleKnownFor() ([]TitleItem, error) {
	c := &w.current
	if len(c.order) == 0 {
		return nil, nil
	}
	if err := w.charge(int64(len(c.order)) * 256); err != nil {
		return nil, err
	}
	known := slices.Clone(c.order)
	sort.Slice(known, func(i, j int) bool {
		a, b := known[i], known[j]
		if a.ordinal != b.ordinal {
			return a.ordinal < b.ordinal
		}
		if a.added != b.added {
			return a.added > b.added
		}
		return a.item.ID < b.item.ID
	})
	n := min(8, len(known))
	out := make([]TitleItem, 0, n)
	for _, work := range known[:n] {
		out = append(out, rsCloneTitle(work.item))
	}
	return out, nil
}

func rsPeopleSearchCursorBytes(c *RSSearchCursor) int64 {
	if c == nil {
		return 0
	}
	n := int64(4096)
	if c.anchor != nil {
		n += 2 * c.anchor.MetadataBytes()
	}
	for _, a := range c.anchors {
		if a != nil {
			n += 2 * a.MetadataBytes()
		}
	}
	return n
}

// rsPeopleSelectionAdvanced reports whether a pending selection poll kept a
// continuation or completed its library stream. A pending page with neither
// cannot reach a selected page on any later poll, so it is an explicit census
// refusal rather than an unbounded retry loop.
func rsPeopleSelectionAdvanced(s *RSSearchPreparation, library int) bool {
	if len(s.streams) == 0 {
		return s.after != nil || s.exhausted
	}
	if library < 0 || library >= len(s.streams) {
		return false
	}
	return s.streams[library].after != nil || s.streams[library].done
}

// advance performs one native revalidation plus at most one borrowed hydration
// quantum. Only that one original lease is held; the reducer's charge() sees
// the shared reservation, and its local sub-account is restored on return.
func (r *RSPeopleSearchReader) advance(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSPeopleSearchPreparation) (bool, *BrowseReadResult, error) {
	s, w := p.search, p.work
	if !p.selected {
		read := s.library
		page, cost, e := r.search.pollLocked(ctx, req, budget, out, s, true)
		if e != nil {
			return false, cost, e
		}
		if page.Pending {
			if !rsPeopleSelectionAdvanced(s, read) {
				return false, cost, ErrUnqualified
			}
			return false, cost, nil
		}
		if len(page.Groups) != 1 || page.Groups[0].Group != RSSearchPeople || page.Partial {
			return false, cost, ErrUnqualified
		}
		if s.stamp == nil {
			return false, cost, ErrUnqualified
		}
		g := page.Groups[0]
		p.next = g.Next
		seen := make(map[int64]struct{}, len(g.Hits))
		for _, hit := range g.Hits {
			if hit.Subject != search.ManifestPerson || hit.Kind != 0 || hit.ID <= 0 || len(p.ids) == 40 {
				return false, cost, ErrUnqualified
			}
			if _, duplicate := seen[hit.ID]; duplicate {
				continue
			}
			seen[hit.ID] = struct{}{}
			p.ids = append(p.ids, hit.ID)
		}
		p.selected = true
		p.retained = 8192 + int64(cap(p.ids))*16 + int64(len(g.Hits))*32 + rsPeopleSearchCursorBytes(p.next)
		// Hydration polls carry only this fence witness. The per-hit native
		// proofs are not re-read row by row: each selected summary is re-proved
		// from its current person source by the borrower's fences and by the
		// complete delivery fence.
		if s.stamp, e = s.stamp.RetainHits(nil); e != nil {
			return false, cost, e
		}
		return false, cost, nil
	}
	ready, cost, e := r.revalidate(ctx, req, budget, out, p)
	if e != nil || !ready {
		return false, cost, e
	}
	if cost == nil {
		cost = &BrowseReadResult{}
	}
	base := rsSearchRetainedBytes(s) + p.retained
	if base+w.used > s.lease.PayloadBytes() {
		return false, cost, governor.ErrSnapshotBudget
	}
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
	if c.facts == nil || c.creditCount == 0 {
		// A native ranked hit without one visible credit in the complete
		// trusted scope is refused, never silently dropped.
		return false, cost, ErrUnqualified
	}
	known, e := w.rsPeopleKnownFor()
	if e != nil {
		return false, cost, e
	}
	summary := RSPeopleSearchSummary{
		RSPersonSummary: RSPersonSummary{ID: c.facts.ID, Token: c.facts.Token, Name: c.facts.Name, SortName: c.facts.SortName, Roles: rsPeopleRoles(c.departments), CreditCount: c.creditCount, Portrait: c.portrait},
		KnownFor:        known,
	}
	n := rsPeopleSearchSummaryBytes(summary)
	if n > w.lease.PayloadBytes()-w.used {
		return false, cost, governor.ErrSnapshotBudget
	}
	p.entries = append(p.entries, rsPeopleSearchEntry{summary: RSPersonSummary{ID: summary.ID, Token: summary.Token, Name: summary.Name, SortName: summary.SortName, Roles: summary.Roles, CreditCount: summary.CreditCount, Portrait: summary.Portrait}, knownFor: summary.KnownFor, source: c.source})
	p.retained += n
	// Drop credit payloads before refunding the job's local sub-account.
	// Completed summaries and native proofs remain charged in base.
	w.current = rsPersonReduction{}
	w.used = base
	for _, f := range w.fences {
		w.used += rsPeopleFenceBytes(f)
	}
	return false, cost, nil
}

// publicationFence re-proves every canonical view and person source in a
// fresh shared publication snapshot after encoding. Portrait payload access
// stays behind RSPeoplePageReader's peoplePortrait point and its own final
// re-proof; this adapter never duplicates artwork SQL.
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
			if i >= len(w.fences) || f != w.fences[i] {
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
// people or counts. The one original lease and LIVE/output custody remain held
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
		copies += rsPeopleSearchSummaryBytes(RSPeopleSearchSummary{RSPersonSummary: entry.summary, KnownFor: entry.knownFor})
	}
	if copies > out.Request.Need[governor.MemoryBytes]/2 || copies > s.lease.PayloadBytes()-rsSearchRetainedBytes(s)-p.retained-p.work.used {
		err = governor.ErrSnapshotBudget
		return
	}
	for _, entry := range p.entries {
		page.People = append(page.People, rsPeopleSearchCloneSummary(RSPeopleSearchSummary{RSPersonSummary: entry.summary, KnownFor: entry.knownFor}))
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
	// Cancellation cannot suppress a security denial observed by the final
	// shared driver. Its detached validation still uses the original deadline.
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
