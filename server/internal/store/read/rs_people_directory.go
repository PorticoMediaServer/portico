package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type RSPeopleDirectoryInput struct {
	Views      []BrowseInput
	Query      string
	Order      string // "title"; native query/relevance requires RD's native bank reader
	Descending bool
	Limit      int
	After      *RSPeopleDirectoryCursor
}
type RSPeopleDirectoryCursor struct {
	identity   [32]byte
	name       string
	id         int64
	descending bool
	limit      int
}
type RSPersonSummary struct {
	ID                    int64
	Token, Name, SortName string
	Roles                 []string
	CreditCount           int
	Portrait              *RSPersonPortrait
}
type RSPeopleDirectory struct {
	People            []RSPersonSummary
	Total             int
	Next              *RSPeopleDirectoryCursor
	Pending, Complete bool
}
type rsPeopleDirectoryEntry struct {
	summary      RSPersonSummary
	source       []any
	workIdentity [32]byte
}
type rsPeopleDirectoryState struct {
	input        RSPeopleDirectoryInput
	after        int64
	exhausted    bool
	entries      []rsPeopleDirectoryEntry
	currentStart int64
	validated    int
}

func RSPeopleDirectoryDefinitions() []stmt.Definition {
	return []stmt.Definition{{ID: readStatementID("rs.people.directory.next"), SQL: `SELECT id FROM catalog_people WHERE id>? ORDER BY id LIMIT 1`, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_people_directory.go:RSPeoplePageReader"}, Params: []stmt.Param{{Name: "after", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(0)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}, Seeks: []string{"rowid>?"}}}}
}

func (r *RSPeoplePageReader) BeginDirectory(ctx context.Context, owner governor.OwnerID, deadline time.Time, reservedBytes int64, in RSPeopleDirectoryInput) (*RSPeoplePreparation, *governor.Refusal, error) {
	if r == nil || ctx == nil || owner == 0 || in.Limit < 1 || in.Limit > MaxPage {
		return nil, nil, ErrInput
	}
	// There is deliberately no fabricated relevance score, source-name walk
	// labelled native search, capped exact count or registry scan exemption.
	// The complete native-search branch remains an explicit prerequisite.
	if in.Query != "" || in.Order != "title" {
		return nil, nil, ErrUnqualified
	}
	d := &rsPeopleDirectoryState{input: in}
	if in.After != nil {
		c := *in.After
		d.input.After = &c
	}
	return r.begin(ctx, owner, deadline, reservedBytes, RSPersonPageInput{Views: in.Views, Limit: in.Limit}, d)
}

func (r *RSPeoplePageReader) Directory(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSPeoplePreparation) (RSPeopleDirectory, BrowseReadResult, error) {
	if r == nil || p == nil || p.reader != r {
		return RSPeopleDirectory{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory == nil || p.owner != request.Owner {
		return RSPeopleDirectory{}, BrowseReadResult{}, ErrInput
	}
	page, cost, err := liveRead(ctx, request, budget, output, r.pool, p.input.Views[0].Now.Add(time.Since(p.started)), func(ctx context.Context, q governor.Request, b time.Duration, at time.Time) (RSPeopleDirectory, BrowseReadResult, error) {
		return r.directory(ctx, q, b, output, p, at)
	})
	if err != nil {
		_ = p.close()
	}
	return page, cost, err
}
func (r *RSPeoplePageReader) directory(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSPeoplePreparation, at time.Time) (page RSPeopleDirectory, cost BrowseReadResult, err error) {
	started := time.Now()
	ctx, cancel := context.WithDeadline(ctx, minTimeRS(time.Now().Add(min(budget, time.Second)), p.lease.Deadline()))
	defer cancel()
	if err = r.authority.CheckPreparation(p.lease); err != nil {
		return
	}
	if !p.captured {
		if err = r.checkPeopleFences(ctx, request, output, p, at, &cost); err != nil {
			return
		}
		page.Pending = true
		return
	}
	if len(p.input.Views) > 1 {
		if err = r.checkPeopleFences(ctx, request, output, p, at, &cost); err != nil {
			return
		}
	}
	d := p.directory
	if !d.exhausted && p.current.person == 0 {
		view := p.input.Views[0]
		view.Now = at
		var id int64
		_, one, e := rsPeoplePublish(ctx, r.snapshots, request, time.Until(p.lease.Deadline()), output, view, []readStep[struct{}]{func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
			if f != p.fences[0] {
				return ErrStale
			}
			rows, e := q("rs.people.directory.next", d.after)
			if e != nil {
				return e
			}
			if len(rows) > 1 {
				return ErrUnqualified
			}
			if len(rows) == 1 {
				id = rows[0][0].(int64)
			}
			return nil
		}})
		addSearchReadCost(&cost, &one)
		if e != nil {
			return page, cost, e
		}
		if id == 0 {
			d.exhausted = true
			page.Pending = true
			return
		} else {
			d.after = id
			d.currentStart = p.used
			p.current = rsPersonReduction{person: id, works: map[int64]*rsPersonWork{}, departments: map[string]int{}}
		}
	}
	if !d.exhausted {
		if err = r.peopleQuantum(ctx, request, output, p, at, &cost); err != nil {
			return
		}
		if p.current.complete {
			c := &p.current
			var entry *rsPeopleDirectoryEntry
			if c.facts != nil {
				f := c.facts
				entry = &rsPeopleDirectoryEntry{summary: RSPersonSummary{ID: f.ID, Token: strings.Clone(f.Token), Name: strings.Clone(f.Name), SortName: strings.Clone(f.SortName), Roles: rsPeopleRoles(c.departments), CreditCount: c.creditCount, Portrait: rsClonePortrait(c.portrait)}, source: c.source, workIdentity: p.identity()}
			}
			// Actual references are dropped before refunding the job's local
			// sub-account. The governor reservation itself remains fully held.
			p.current = rsPersonReduction{}
			p.used = d.currentStart
			if entry != nil {
				n := int64(1024 + 4*(len(entry.summary.Token)+len(entry.summary.Name)+len(entry.summary.SortName)))
				for _, role := range entry.summary.Roles {
					n += int64(64 + 4*len(role))
				}
				n += rsPortraitBytes(entry.summary.Portrait)
				if err = p.charge(n); err != nil {
					return
				}
				d.entries = append(d.entries, *entry)
			}
		}
	}
	if len(p.input.Views) > 1 {
		if err = r.checkPeopleFences(ctx, request, output, p, at, &cost); err != nil {
			return
		}
	}
	if !d.exhausted {
		page.Pending = true
		return
	}
	// Sources for already scanned people are checked in bounded quanta too;
	// ready computation never turns a prior person source into authorization.
	view := p.input.Views[0]
	view.Now = at
	_, one, e := rsPeoplePublish(ctx, r.snapshots, request, time.Until(p.lease.Deadline()), output, view, []readStep[struct{}]{func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
		if f != p.fences[0] {
			return ErrStale
		}
		end := min(d.validated+2, len(d.entries)) // first shared fence uses five calls
		for d.validated < end {
			entry := d.entries[d.validated]
			rows, e := q("rs.people.source", entry.summary.ID)
			if e != nil {
				return e
			}
			if len(rows) != 1 || !equalSource(entry.source, rows[0]) {
				return ErrStale
			}
			d.validated++
		}
		return nil
	}})
	addSearchReadCost(&cost, &one)
	if e != nil {
		return page, cost, e
	}
	if d.validated < len(d.entries) {
		page.Pending = true
		return
	}
	if err = r.authority.ReadyPreparation(p.lease); err != nil {
		return
	}
	page, err = p.directoryPage(output.Request.Need[governor.MemoryBytes] / 2)
	if err != nil {
		return RSPeopleDirectory{}, cost, err
	}
	if err = p.handoff(at.Add(time.Since(started))); err != nil {
		return RSPeopleDirectory{}, cost, err
	}
	err = p.close()
	if err != nil {
		page = RSPeopleDirectory{}
	}
	return
}
func (p *RSPeoplePreparation) directoryPage(outputBytes int64) (RSPeopleDirectory, error) {
	d := p.directory
	in := d.input
	// Include all person source identities in continuation provenance, as well
	// as the canonical views' authority/data/order/artwork fences.
	h := sha256.New()
	base := p.identity()
	h.Write(base[:])
	for _, entry := range d.entries {
		fmt.Fprintf(h, "%d/%x\n", entry.summary.ID, entry.workIdentity)
	}
	var identity [32]byte
	copy(identity[:], h.Sum(nil))
	if a := in.After; a != nil && (a.identity != identity || a.descending != in.Descending || a.limit != in.Limit) {
		return RSPeopleDirectory{}, ErrStale
	}
	sort.Slice(d.entries, func(i, j int) bool {
		a, b := d.entries[i].summary, d.entries[j].summary
		less := a.SortName < b.SortName || a.SortName == b.SortName && a.ID < b.ID
		if in.Descending {
			return b.SortName < a.SortName || b.SortName == a.SortName && b.ID < a.ID
		}
		return less
	})
	start := 0
	if a := in.After; a != nil {
		for start < len(d.entries) {
			v := d.entries[start].summary
			before := v.SortName < a.name || v.SortName == a.name && v.ID <= a.id
			if in.Descending {
				before = v.SortName > a.name || v.SortName == a.name && v.ID >= a.id
			}
			if !before {
				break
			}
			start++
		}
	}
	end := min(start+in.Limit, len(d.entries))
	n := int64(4096)
	for _, entry := range d.entries[start:end] {
		v := entry.summary
		n += int64(512 + 4*(len(v.Token)+len(v.Name)+len(v.SortName)))
		for _, role := range v.Roles {
			n += int64(64 + 4*len(role))
		}
		n += rsPortraitBytes(v.Portrait)
	}
	if n > outputBytes {
		return RSPeopleDirectory{}, governor.ErrSnapshotBudget
	}
	out := RSPeopleDirectory{Total: len(d.entries), Complete: true}
	for _, entry := range d.entries[start:end] {
		v := entry.summary
		v.Token = strings.Clone(v.Token)
		v.Name = strings.Clone(v.Name)
		v.SortName = strings.Clone(v.SortName)
		v.Portrait = rsClonePortrait(v.Portrait)
		v.Roles = append([]string(nil), v.Roles...)
		for i := range v.Roles {
			v.Roles[i] = strings.Clone(v.Roles[i])
		}
		out.People = append(out.People, v)
	}
	if end < len(d.entries) {
		v := out.People[len(out.People)-1]
		out.Next = &RSPeopleDirectoryCursor{identity: identity, name: v.SortName, id: v.ID, descending: in.Descending, limit: in.Limit}
	}
	return out, nil
}

func rsPortraitBytes(p *RSPersonPortrait) int64 {
	if p == nil {
		return 0
	}
	return int64(512 + 4*(len(p.Small)+len(p.Large)+len(p.Ambient)+len(p.MIME)))
}
