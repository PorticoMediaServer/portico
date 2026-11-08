package read

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const rsPeopleQuantum = 4

// RSPeoplePageReader composes complete, scoped people results. It never calls
// Credits: that raw primitive has its own output and continuation semantics.
// There is no runtime registration, provider fetch, cache, worker or scheduler.
type RSPeoplePageReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
	authority *governor.Authority
}

func NewRSPeoplePageReader(s *governor.Snapshots, a *governor.Authority) (*RSPeoplePageReader, error) {
	if s == nil || a == nil || !s.OwnedBy(a) {
		return nil, ErrInput
	}
	return &RSPeoplePageReader{snapshots: s, pool: s, authority: a}, nil
}

type RSPersonPageInput struct {
	// Views is the complete trusted library/kind scope for this response. No
	// cross-library/global completion is implied beyond these explicit views.
	// All views must name the same database/viewer. Policy belongs to RS's
	// shared closed recipes; this family cannot opt into Reading/containers.
	Views      []BrowseInput
	Person     int64 // canonical catalog_people identity, including local/NFO people
	Class      PersonCreditClass
	Department string
	Kind       int64 // zero means all requested kinds
	Limit      int
	After      *RSPersonPageCursor
}

// Cursors are private values, never wire tokens or authorization grants. They
// contain no preparation pointer, retained SQL cursor or exported proof bit.
type RSPersonPageCursor struct {
	identity   [32]byte
	person     int64
	class      PersonCreditClass
	department string
	kind       int64
	limit      int
	title      string
	id         int64
}
type RSPersonRole struct{ Role, Department string }
type RSPersonCredit struct {
	Item                                    TitleItem
	Roles                                   []RSPersonRole
	Role, Character, Department, CreditKind string
}
type RSPersonGroup struct {
	Department string
	Kind       int64
	Total      int
}
type RSPersonPortrait struct {
	Small, Large, Ambient, MIME string
	Width, Height, Revision     int64
}
type RSPersonPage struct {
	Person            *PersonFacts
	Portrait          *RSPersonPortrait
	Credits           []RSPersonCredit
	KnownFor          []TitleItem
	Roles             []string
	Groups            []RSPersonGroup
	Total             int
	Next              *RSPersonPageCursor
	Pending, Complete bool
}

type rsPersonWork struct {
	item    TitleItem
	roles   []RSPersonRole
	ordinal int64
	added   string
	source  []any
	view    int
}
type rsPersonReduction struct {
	person       int64
	source       []any
	facts        *PersonFacts
	portrait     *RSPersonPortrait
	portraitRead bool
	works        map[int64]*rsPersonWork
	departments  map[string]int // credit positions, matching public role ranking
	creditCount  int
	after        *PersonCreditsCursor
	exhausted    bool
	order        []*rsPersonWork
	validated    int
	complete     bool
}

// RSPeoplePreparation is one owner/query job. Polls attach to this same object;
// they cannot renew its absolute deadline. Close drops all private values and
// physically retires the governor reservation. Methods serialize on this job.
// It does not keep a native snapshot or a reference to consumer output.
type RSPeoplePreparation struct {
	mu        sync.Mutex
	reader    *RSPeoplePageReader
	lease     *governor.PreparationLease
	owner     governor.OwnerID
	input     RSPersonPageInput
	fences    []browseFence
	captured  bool
	current   rsPersonReduction
	used      int64
	started   time.Time
	closed    bool
	directory *rsPeopleDirectoryState
}

func (p *RSPeoplePreparation) Close() error {
	if p == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}
func (p *RSPeoplePreparation) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.input = RSPersonPageInput{}
	p.fences = nil
	p.current = rsPersonReduction{}
	p.directory = nil
	p.used = 0
	return p.reader.authority.RetirePreparation(p.lease)
}
func (p *RSPeoplePreparation) charge(n int64) error {
	if n < 0 || n > p.lease.PayloadBytes()-p.used {
		return governor.ErrSnapshotBudget
	}
	p.used += n
	return nil
}

// BeginPage admits only the F-04 memory/publication lease. ctx is the original
// server-owned job lifetime, not a poll lifetime. Capacity is explicit; no
// result or total is truncated to fit it. All dependencies are conservative
// global scopes until writer domain footprints are fully composed at cutover.
func (r *RSPeoplePageReader) BeginPage(ctx context.Context, owner governor.OwnerID, deadline time.Time, reservedBytes int64, in RSPersonPageInput) (*RSPeoplePreparation, *governor.Refusal, error) {
	if r == nil || ctx == nil || owner == 0 || in.Person <= 0 || in.Class > PersonCrewCredits || in.Kind < 0 || in.Limit < 1 || in.Limit > MaxPage || len(in.Department) > 128 || strings.ContainsAny(in.Department, "\r\n\x00") || in.Class != PersonAllCredits && in.Department != "" {
		return nil, nil, ErrInput
	}
	return r.begin(ctx, owner, deadline, reservedBytes, in, nil)
}

func (r *RSPeoplePageReader) begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, reservedBytes int64, in RSPersonPageInput, directory *rsPeopleDirectoryState) (*RSPeoplePreparation, *governor.Refusal, error) {
	if len(in.Views) == 0 {
		return nil, nil, ErrInput
	}
	if len(in.Views) > 64 {
		return nil, nil, governor.ErrSnapshotBudget
	}
	for n, v := range in.Views {
		if v.Database == "" || len(v.Database) > 256 || v.TokenHash == "" || len(v.TokenHash) > 256 || v.Library <= 0 || v.Kind <= 0 || v.Now.IsZero() || v.After != nil || v.Descending || v.KeepArticles || v.Database != in.Views[0].Database || v.TokenHash != in.Views[0].TokenHash {
			return nil, nil, ErrInput
		}
		if _, err := resolveReadPolicy(v); err != nil {
			return nil, nil, err
		}
		for _, prior := range in.Views[:n] {
			if prior.Library == v.Library && prior.Kind == v.Kind {
				return nil, nil, ErrInput
			}
		}
	}
	// The key includes the exact scope, filter, page and private continuation.
	// It is never a caller-selected short cache key or another owner's job.
	h := sha256.New()
	fmt.Fprintf(h, "%d/%d/%d/%q/%d/%d/%#v\n", owner, in.Person, in.Class, in.Department, in.Kind, in.Limit, in.After)
	for _, v := range in.Views {
		fmt.Fprintf(h, "%#v\n", v)
	}
	if directory != nil {
		d := directory.input
		fmt.Fprintf(h, "directory/%q/%q/%t/%d/%#v\n", d.Query, d.Order, d.Descending, d.Limit, d.After)
	}
	key := h.Sum(nil)
	decision := r.authority.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 73, ID: fmt.Sprintf("rs-people:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: reservedBytes})
	if decision.Refusal != nil {
		return nil, decision.Refusal, nil
	}
	p := &RSPeoplePreparation{reader: r, lease: decision.Lease, owner: owner, input: in, directory: directory, started: time.Now()}
	if err := p.charge(4096 + int64(len(in.Views))*4096); err != nil {
		_ = p.close()
		return nil, nil, err
	}
	p.input.Views = slices.Clone(in.Views)
	for i := range p.input.Views {
		p.input.Views[i].Limit = MaxPage
	}
	if in.After != nil {
		c := *in.After
		p.input.After = &c
	}
	p.current = rsPersonReduction{person: in.Person, works: map[int64]*rsPersonWork{}, departments: map[string]int{}}
	return p, nil, nil
}

// Page advances at most FOUR raw credit positions per poll. Each payload has
// its own three-call native scope; no 64-point business loop or longer snapshot
// is used. Pending carries no profile, groups, approximate count or credits.
// ConsumeForeground keeps LIVE and copied output charged through serialization.
func (r *RSPeoplePageReader) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSPeoplePreparation) (RSPersonPage, BrowseReadResult, error) {
	if r == nil || p == nil || p.reader != r {
		return RSPersonPage{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.directory != nil || p.owner != request.Owner {
		return RSPersonPage{}, BrowseReadResult{}, ErrInput
	}
	page, cost, err := liveRead(ctx, request, budget, output, r.pool, p.input.Views[0].Now.Add(time.Since(p.started)), func(ctx context.Context, q governor.Request, b time.Duration, at time.Time) (RSPersonPage, BrowseReadResult, error) {
		return r.page(ctx, q, b, output, p, at)
	})
	if err != nil {
		_ = p.close()
	}
	return page, cost, err
}

func (r *RSPeoplePageReader) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, p *RSPeoplePreparation, at time.Time) (page RSPersonPage, cost BrowseReadResult, err error) {
	started := time.Now()
	ctx, cancel := context.WithDeadline(ctx, minTimeRS(time.Now().Add(min(budget, time.Second)), p.lease.Deadline()))
	defer cancel()
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
	err = r.peopleQuantum(ctx, request, output, p, at, &cost)
	if err != nil {
		return
	}
	if len(p.input.Views) > 1 {
		if err = r.checkPeopleFences(ctx, request, output, p, at, &cost); err != nil {
			return
		}
	}
	if !p.current.complete {
		page.Pending = true
		return
	}
	if p.current.facts == nil {
		return page, cost, ErrDenied
	}
	if err = r.authority.ReadyPreparation(p.lease); err != nil {
		return
	}
	page, err = p.personPage(output.Request.Need[governor.MemoryBytes] / 2)
	if err != nil {
		return RSPersonPage{}, cost, err
	}
	if err = p.handoff(at.Add(time.Since(started))); err != nil {
		return RSPersonPage{}, cost, err
	}
	err = p.close()
	if err != nil {
		page = RSPersonPage{}
	}
	return
}

func (p *RSPeoplePreparation) handoff(at time.Time) error {
	for _, f := range p.fences {
		expires, e1 := time.Parse(time.RFC3339Nano, f.expiresAt)
		horizon, e2 := time.Parse(time.RFC3339Nano, f.horizon)
		if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
			return ErrDenied
		}
	}
	return p.reader.authority.HandoffPreparation(p.lease)
}

func minTimeRS(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Preparations retain their private reducer across polls, so a failed attempt
// is terminal. Retrying a partially mutated reducer would double-count source
// positions. A later explicit job can start over within its own admission.
func rsPeoplePublish[T any](ctx context.Context, s browseSnapshots, q governor.Request, b time.Duration, out BrowseOutput, in BrowseInput, steps []readStep[T], roots ...rootRead) (T, BrowseReadResult, error) {
	var copied int64
	page, cost, err := publishAttempt(ctx, s, q, b, out, in, steps, &copied, roots...)
	cost.Attempts = 1
	if !cost.Publication.Started.IsZero() {
		cost.PublicationWindows = append(cost.PublicationWindows, cost.Publication)
	}
	return page, cost, err
}

func (r *RSPeoplePageReader) checkPeopleFences(ctx context.Context, request governor.Request, output BrowseOutput, p *RSPeoplePreparation, at time.Time, cost *BrowseReadResult) error {
	if err := r.authority.CheckPreparation(p.lease); err != nil {
		return err
	}
	for i, view := range p.input.Views {
		view.Now = at
		var roots []rootRead
		if p.current.person > 0 {
			roots = []rootRead{func(q browseQuery) ([]any, error) {
				rows, err := q("rs.people.source", p.current.person)
				if err != nil {
					return nil, err
				}
				if len(rows) != 1 || p.current.source != nil && !equalSource(p.current.source, rows[0]) {
					return nil, ErrStale
				}
				if p.current.source == nil {
					p.current.source = rows[0]
				}
				return rows[0], nil
			}}
		}
		f, one, err := rsPeoplePublish(ctx, r.snapshots, request, time.Until(minTimeRS(request.Deadline, p.lease.Deadline())), output, view, []readStep[browseFence]{func(_ context.Context, _ browseQuery, f browseFence, out *browseFence) error { *out = f; return nil }}, roots...)
		addSearchReadCost(cost, &one)
		if err != nil {
			return err
		}
		if p.captured {
			if f != p.fences[i] {
				return ErrStale
			}
		} else {
			if err := p.charge(rsPeopleFenceBytes(f)); err != nil {
				return err
			}
			p.fences = append(p.fences, f)
		}
	}
	p.captured = true
	return r.authority.CheckPreparation(p.lease)
}

func rsPeopleFenceBytes(f browseFence) int64 {
	n := int64(1024)
	for _, s := range []string{f.database, f.token, f.account, f.profile, f.membership, f.lifetime, f.library, f.role, f.memberLibraries, f.profileLibraries, f.profileLabels, f.expiresAt, f.horizon, f.root, f.bridgeRoot, f.policyWitness} {
		n += 4 * int64(len(s))
	}
	return n
}

func (p *RSPeoplePreparation) rowView(row []any) int {
	for i, v := range p.input.Views {
		f := p.fences[i]
		if row[3] != nil && row[7] == v.Library && row[8] == v.Kind && row[4] == int64(0) && row[9] != nil && row[9] == row[2] && row[10] == int64(1) &&
			(row[6] != nil && (f.maximumAge < 0 || row[6].(int64) <= f.maximumAge) || row[6] == nil && f.allowUnrated != 0) {
			return i
		}
	}
	return -1
}

func (r *RSPeoplePageReader) validatePeopleWorks(ctx context.Context, request governor.Request, output BrowseOutput, p *RSPeoplePreparation, at time.Time, cost *BrowseReadResult) error {
	c := &p.current
	view := p.input.Views[0]
	view.Now = at
	_, one, err := rsPeoplePublish(ctx, r.snapshots, request, time.Until(p.lease.Deadline()), output, view, []readStep[struct{}]{func(_ context.Context, q browseQuery, f browseFence, _ *struct{}) error {
		if f != p.fences[0] {
			return ErrStale
		}
		end := min(c.validated+2, len(c.order))
		for c.validated < end {
			work := c.order[c.validated]
			v := p.input.Views[work.view]
			rows, err := q("rs.people.page.work_source", work.item.ID, v.Library, v.Kind)
			if err != nil {
				return err
			}
			if len(rows) != 1 || !equalSource(work.source, rows[0]) {
				return ErrStale
			}
			c.validated++
		}
		return nil
	}}, func(q browseQuery) ([]any, error) {
		rows, e := q("rs.people.source", c.person)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 || !equalSource(c.source, rows[0]) {
			return nil, ErrStale
		}
		return rows[0], nil
	})
	addSearchReadCost(cost, &one)
	if err == nil {
		c.complete = c.validated == len(c.order)
	}
	return err
}

// peopleQuantum uses the existing byte gateway and source stream. Prospective
// octets are scalar; QueryExactBytes checks them again in EACH payload window.
func (r *RSPeoplePageReader) peopleQuantum(ctx context.Context, request governor.Request, output BrowseOutput, p *RSPeoplePreparation, at time.Time, cost *BrowseReadResult) error {
	if err := r.authority.CheckPreparation(p.lease); err != nil {
		return err
	}
	c := &p.current
	if len(c.works) > 0 && c.facts == nil {
		return r.peopleFacts(ctx, request, output, p, at, cost)
	}
	if c.facts != nil && !c.portraitRead {
		err := r.peoplePortrait(ctx, request, output, p, at, cost)
		if err == nil {
			c.portraitRead = true
		}
		return err
	}
	if c.exhausted {
		return r.validatePeopleWorks(ctx, request, output, p, at, cost)
	}
	view := p.input.Views[0]
	view.Now = at
	var raw [][]any
	var selected []int
	var done bool
	var maxBytes int64
	w := &rsByteWindow{source: r.snapshots, preflightCalls: 14, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	root := func(q browseQuery) ([]any, error) {
		rows, err := q("rs.people.source", c.person)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		if c.source != nil && !equalSource(c.source, rows[0]) {
			return nil, ErrStale
		}
		if c.source == nil {
			c.source = rows[0]
		}
		return rows[0], nil
	}
	steps := []readStep[struct{}]{func(ctx context.Context, q browseQuery, f browseFence, _ *struct{}) error {
		if f != p.fences[0] {
			return ErrStale
		}
		type seek struct {
			name string
			args []any
		}
		seeks := []seek{{"first", []any{c.person}}}
		if a := c.after; a != nil {
			seeks = []seek{{"image_tail", []any{c.person, a.work, a.ordinal, a.image}}, {"ordinal_tail", []any{c.person, a.work, a.ordinal}}, {"next", []any{c.person, a.work}}}
		}
		done = true
		for _, s := range seeks {
			limit := rsPeopleQuantum - len(raw)
			rows, err := q("rs.people."+s.name, append(s.args, int64(limit))...)
			if err != nil {
				return err
			}
			if len(rows) > limit {
				return ErrUnqualified
			}
			raw = append(raw, rows...)
			if len(raw) == rsPeopleQuantum {
				done = false
				break
			}
		}
		for _, row := range raw {
			i := p.rowView(row)
			selected = append(selected, i)
			n := int64(0)
			if i >= 0 {
				v := p.input.Views[i]
				points, err := q("rs.people.page.credit_octets", row[2], row[1], c.person, v.Library, v.Kind)
				if err != nil {
					return err
				}
				if len(points) != 1 {
					return ErrStale
				}
				n, err = rsPointBytes(points[0])
				if err != nil {
					return err
				}
			}
			maxBytes = max(maxBytes, n)
		}
		if maxBytes > 0 {
			return w.arm(maxBytes, 3)
		}
		return ctx.Err()
	}}
	// Every step has one exact-byte pair plus one scalar work-source point.
	for n := 0; n < rsPeopleQuantum; n++ {
		steps = append(steps, func(ctx context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if n >= len(raw) {
				return nil
			}
			row := raw[n]
			if i := selected[n]; i >= 0 {
				v := p.input.Views[i]
				rows, err := q("rs.people.page.credit", row[2], row[1], c.person, v.Library, v.Kind)
				if err != nil {
					return err
				}
				if len(rows) != 1 || !admittedItem(rows[0], p.fences[i]) {
					return ErrStale
				}
				if err = p.reduce(rows[0], row[1].(int64)); err != nil {
					return err
				}
				points, err := q("rs.people.page.work_source", row[0], v.Library, v.Kind)
				if err != nil {
					return err
				}
				if len(points) != 1 {
					return ErrStale
				}
				work := c.works[row[0].(int64)]
				if work.source != nil && !equalSource(work.source, points[0]) {
					return ErrStale
				}
				work.source = points[0]
				work.view = i
			}
			c.after = &PersonCreditsCursor{work: row[0].(int64), ordinal: row[1].(int64), image: row[2].(int64)}
			if n+1 < len(raw) && maxBytes > 0 {
				return w.arm(maxBytes, 3)
			}
			return ctx.Err()
		})
	}
	_, one, err := rsPeoplePublish(ctx, w, request, time.Until(minTimeRS(request.Deadline, p.lease.Deadline())), output, view, steps, root)
	addSearchReadCost(cost, &one)
	if err != nil {
		return err
	}
	c.exhausted = done
	if done && len(c.works) == 0 {
		c.complete = true
	}
	return r.authority.CheckPreparation(p.lease)
}

func (r *RSPeoplePageReader) peopleFacts(ctx context.Context, request governor.Request, output BrowseOutput, p *RSPeoplePreparation, at time.Time, cost *BrowseReadResult) error {
	c := &p.current
	view := p.input.Views[0]
	view.Now = at
	w := &rsByteWindow{source: r.snapshots, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	_, one, err := rsPeoplePublish(ctx, w, request, time.Until(minTimeRS(request.Deadline, p.lease.Deadline())), output, view, []readStep[struct{}]{
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			rows, e := q("rs.people.person_octets", c.person)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrDenied
			}
			n, e := rsPointBytes(rows[0])
			if e != nil {
				return e
			}
			if e = p.charge(512 + 4*n); e != nil {
				return e
			}
			return w.arm(n, 2)
		},
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			rows, e := q("rs.people.person", c.person)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrDenied
			}
			v := rows[0]
			c.facts = &PersonFacts{ID: v[0].(int64), Token: v[1].(string), Name: v[2].(string), SortName: v[3].(string), Biography: v[4].(string), BirthPlace: v[5].(string), BiographySource: v[6].(string), BirthDate: v[7].(string), DeathDate: v[8].(string), Revision: v[9].(int64)}
			return nil
		},
	}, func(q browseQuery) ([]any, error) {
		rows, e := q("rs.people.source", c.person)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 || !equalSource(c.source, rows[0]) {
			return nil, ErrStale
		}
		return rows[0], nil
	})
	addSearchReadCost(cost, &one)
	return err
}

func (r *RSPeoplePageReader) peoplePortrait(ctx context.Context, request governor.Request, output BrowseOutput, p *RSPeoplePreparation, at time.Time, cost *BrowseReadResult) error {
	c := &p.current
	view := p.input.Views[0]
	view.Now = at
	w := &rsByteWindow{source: r.snapshots, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	choice := ""
	var digest string
	var revision int64
	_, one, err := rsPeoplePublish(ctx, w, request, time.Until(p.lease.Deadline()), output, view, []readStep[struct{}]{
		func(context.Context, browseQuery, browseFence, *struct{}) error { return nil },
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			for _, name := range []string{"locked", "matched"} {
				rows, e := q("rs.people.page.portrait_octets", c.person, name)
				if e != nil {
					return e
				}
				if len(rows) > 1 {
					return ErrUnqualified
				}
				if len(rows) == 1 {
					n, e := rsPointBytes(rows[0])
					if e != nil {
						return e
					}
					if e = p.charge(512 + 4*n); e != nil {
						return e
					}
					choice = name
					if n == 0 {
						// A locked-empty selection (NULL digest) is a present
						// blocking choice with no payload. The render step sees
						// the choice and produces no portrait; the matched row
						// behind it is never selected.
						return ctx.Err()
					}
					return w.arm(n, 2)
				}
			}
			return nil
		},
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if choice == "" {
				return nil
			}
			rows, e := q("rs.people.page.portrait", c.person, choice)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			v := rows[0]
			if v[0] == nil {
				// The locked choice is present but has no digest: this person
				// has no portrait, and matched art behind the choice stays
				// blocked. The source witness below still verifies the person.
				c.portrait = nil
				return nil
			}
			if v[1] == nil {
				// A nonempty selection whose source is unavailable or retiring
				// is distinct from locked-empty and is not served.
				return ErrStale
			}
			digest = v[0].(string)
			revision = v[7].(int64)
			c.portrait = &RSPersonPortrait{Small: v[1].(string), Large: v[2].(string), Ambient: v[3].(string), MIME: v[4].(string), Width: v[5].(int64), Height: v[6].(int64), Revision: revision}
			return nil
		},
	}, func(q browseQuery) ([]any, error) {
		if digest != "" {
			// The final snapshot re-proves the selected digest/revision is still
			// served by a source that is not retiring. A source that begins
			// retiring after the payload window never publishes.
			rows, e := q("rs.people.page.portrait_verify", c.person, choice, digest, revision)
			if e != nil {
				return nil, e
			}
			if len(rows) != 1 {
				c.portrait = nil
				return nil, ErrStale
			}
		}
		rows, e := q("rs.people.source", c.person)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 || !equalSource(c.source, rows[0]) {
			return nil, ErrStale
		}
		return rows[0], nil
	})
	addSearchReadCost(cost, &one)
	return err
}

func rsClonePortrait(p *RSPersonPortrait) *RSPersonPortrait {
	if p == nil {
		return nil
	}
	v := *p
	v.Small = strings.Clone(v.Small)
	v.Large = strings.Clone(v.Large)
	v.Ambient = strings.Clone(v.Ambient)
	v.MIME = strings.Clone(v.MIME)
	return &v
}

func (p *RSPeoplePreparation) reduce(v []any, ordinal int64) error {
	c := &p.current
	item, err := itemValue(v)
	if err != nil {
		return err
	}
	role := RSPersonRole{Role: v[8].(string), Department: v[9].(string)}
	n := int64(768 + 4*(len(v[3].(string))+len(v[7].(string))+len(role.Role)+len(role.Department)))
	if err = p.charge(n); err != nil {
		return err
	}
	w := c.works[item.ID]
	if w == nil {
		w = &rsPersonWork{item: TitleItem{BrowseItem: item, Kind: v[11].(int64)}, ordinal: ordinal}
		if v[10] != nil {
			w.added = v[10].(string)
		}
		c.works[item.ID] = w
		c.order = append(c.order, w)
	}
	w.ordinal = min(w.ordinal, ordinal)
	if !slices.Contains(w.roles, role) {
		w.roles = append(w.roles, role)
	}
	if role.Department != "" {
		c.departments[role.Department]++
	}
	c.creditCount++
	return nil
}

func rsPeopleCast(department string) bool {
	return strings.EqualFold(department, "Acting") || strings.EqualFold(department, "Cast")
}
func (p *RSPeoplePreparation) matches(role RSPersonRole, kind int64) bool {
	in := p.input
	return (in.Class == PersonAllCredits || in.Class == PersonCastCredits && rsPeopleCast(role.Department) || in.Class == PersonCrewCredits && !rsPeopleCast(role.Department)) && (in.Department == "" || role.Department == in.Department) && (in.Kind == 0 || kind == in.Kind)
}
func rsSQLiteNoCase(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func (p *RSPeoplePreparation) identity() [32]byte {
	h := sha256.New()
	for _, f := range p.fences {
		fmt.Fprintf(h, "%#v\n", f)
	}
	fmt.Fprintf(h, "%#v\n", p.current.source)
	for _, work := range p.current.order {
		fmt.Fprintf(h, "%d/%#v\n", work.item.ID, work.source)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func rsClonePerson(in *PersonFacts) *PersonFacts {
	v := *in
	v.Token = strings.Clone(v.Token)
	v.Name = strings.Clone(v.Name)
	v.SortName = strings.Clone(v.SortName)
	v.Biography = strings.Clone(v.Biography)
	v.BirthPlace = strings.Clone(v.BirthPlace)
	v.BiographySource = strings.Clone(v.BiographySource)
	v.BirthDate = strings.Clone(v.BirthDate)
	v.DeathDate = strings.Clone(v.DeathDate)
	return &v
}
func rsCloneTitle(v TitleItem) TitleItem { v.Title = strings.Clone(v.Title); return v }

func (p *RSPeoplePreparation) personPage(outputBytes int64) (RSPersonPage, error) {
	var out RSPersonPage
	identity := p.identity()
	in := p.input
	c := &p.current
	if a := in.After; a != nil && (a.identity != identity || a.person != in.Person || a.class != in.Class || a.department != in.Department || a.kind != in.Kind || a.limit != in.Limit) {
		return out, ErrStale
	}
	// Sorting/group scratch uses the preparation reservation; ONLY the emitted
	// page, known-for and exact groups use separately admitted output bytes.
	if err := p.charge(int64(len(c.works)) * 256); err != nil {
		return out, err
	}
	works := make([]*rsPersonWork, 0, len(c.works))
	known := make([]*rsPersonWork, 0, len(c.works))
	departments := map[string]map[int64]bool{}
	kinds := map[int64]int{}
	for _, w := range c.works {
		known = append(known, w)
		selected := false
		for _, role := range w.roles {
			if departments[role.Department] == nil {
				departments[role.Department] = map[int64]bool{}
			}
			departments[role.Department][w.item.ID] = true
			selected = selected || p.matches(role, w.item.Kind)
		}
		kinds[w.item.Kind]++
		if selected {
			works = append(works, w)
		}
	}
	sort.Slice(works, func(i, j int) bool {
		a, b := rsSQLiteNoCase(works[i].item.Title), rsSQLiteNoCase(works[j].item.Title)
		if a != b {
			return a < b
		}
		return works[i].item.ID < works[j].item.ID
	})
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
	needed := int64(4096 + 4*(len(c.facts.Token)+len(c.facts.Name)+len(c.facts.SortName)+len(c.facts.Biography)+len(c.facts.BirthPlace)+len(c.facts.BiographySource)+len(c.facts.BirthDate)+len(c.facts.DeathDate)))
	for dept := range departments {
		needed += int64(256 + 4*len(dept))
	}
	for dept := range c.departments {
		needed += int64(64 + 4*len(dept))
	}
	for _, w := range known[:min(8, len(known))] {
		needed += int64(256 + 4*len(w.item.Title))
	}
	emitted := 0
	for _, w := range works {
		if a := in.After; a != nil {
			key := rsSQLiteNoCase(w.item.Title)
			if key < a.title || key == a.title && w.item.ID <= a.id {
				continue
			}
		}
		if emitted == in.Limit {
			break
		}
		needed += int64(512 + 4*len(w.item.Title))
		for _, role := range w.roles {
			if p.matches(role, w.item.Kind) {
				needed += int64(128 + 4*(len(role.Role)+len(role.Department)))
			}
		}
		emitted++
	}
	if c.portrait != nil {
		v := c.portrait
		needed += int64(256 + 4*(len(v.Small)+len(v.Large)+len(v.Ambient)+len(v.MIME)))
	}
	if needed > outputBytes {
		return out, governor.ErrSnapshotBudget
	}
	out.Person = rsClonePerson(c.facts)
	out.Portrait = rsClonePortrait(c.portrait)
	out.Total = len(works)
	out.Complete = true
	out.Roles = rsPeopleRoles(c.departments)
	if len(departments) > 1 {
		for dept, ids := range departments {
			out.Groups = append(out.Groups, RSPersonGroup{Department: strings.Clone(dept), Total: len(ids)})
		}
	} else {
		for kind, n := range kinds {
			out.Groups = append(out.Groups, RSPersonGroup{Kind: kind, Total: n})
		}
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		if a.Department != b.Department {
			return a.Department < b.Department
		}
		return a.Kind < b.Kind
	})
	for _, w := range known[:min(8, len(known))] {
		out.KnownFor = append(out.KnownFor, rsCloneTitle(w.item))
	}
	for _, w := range works {
		if a := in.After; a != nil {
			key := rsSQLiteNoCase(w.item.Title)
			if key < a.title || key == a.title && w.item.ID <= a.id {
				continue
			}
		}
		if len(out.Credits) == in.Limit {
			last := out.Credits[len(out.Credits)-1]
			out.Next = &RSPersonPageCursor{identity: identity, person: in.Person, class: in.Class, department: in.Department, kind: in.Kind, limit: in.Limit, title: rsSQLiteNoCase(last.Item.Title), id: last.Item.ID}
			break
		}
		credit := RSPersonCredit{Item: rsCloneTitle(w.item)}
		for _, role := range w.roles {
			if p.matches(role, w.item.Kind) {
				credit.Roles = append(credit.Roles, RSPersonRole{strings.Clone(role.Role), strings.Clone(role.Department)})
			}
		}
		sort.Slice(credit.Roles, func(i, j int) bool {
			a, b := credit.Roles[i], credit.Roles[j]
			if a.Role != b.Role {
				return a.Role < b.Role
			}
			return a.Department < b.Department
		})
		credit.Role = credit.Roles[0].Role
		credit.Department = credit.Roles[0].Department
		for _, role := range credit.Roles {
			if role.Role < credit.Role {
				credit.Role = role.Role
			}
			if role.Department < credit.Department {
				credit.Department = role.Department
			}
		}
		credit.Character = credit.Role
		credit.CreditKind = "crew"
		if rsPeopleCast(credit.Department) {
			credit.CreditKind = "cast"
		}
		out.Credits = append(out.Credits, credit)
	}
	return out, nil
}
func rsPeopleRoles(counts map[string]int) []string {
	roles := make([]string, 0, len(counts))
	for dept := range counts {
		roles = append(roles, dept)
	}
	sort.Slice(roles, func(i, j int) bool {
		if counts[roles[i]] != counts[roles[j]] {
			return counts[roles[i]] > counts[roles[j]]
		}
		return roles[i] < roles[j]
	})
	roles = roles[:min(16, len(roles))]
	for i := range roles {
		roles[i] = strings.Clone(roles[i])
	}
	return roles
}
