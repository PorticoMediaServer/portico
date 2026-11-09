package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"cmp"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RSAuthorBookReader is an inert trusted-harness reader. Preparation never owns
// a SQL snapshot, renews a lease, or grants cached authorization.
type RSAuthorBookReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
	authority *governor.Authority
}

func NewRSAuthorBookReader(s *governor.Snapshots, a *governor.Authority) (*RSAuthorBookReader, error) {
	if s == nil || a == nil || !s.OwnedBy(a) {
		return nil, ErrInput
	}
	return &RSAuthorBookReader{snapshots: s, pool: s, authority: a}, nil
}

type RSAuthorBookInput struct {
	View   BrowseInput
	Author int64
	Limit  int
	After  *RSAuthorBookCursor
}

// RSAuthorBookCursor continues on the ordered key: the last delivered
// NOCASE-folded title and its entity ID. The identity binds it to the fence,
// author key, source, order and limit that produced it.
type RSAuthorBookCursor struct {
	identity                  [32]byte
	author, library, kind, id int64
	limit                     int
	titleKey                  string
}

// MissingCanonicalFacts identifies facts unavailable in the accepted scalar
// image, rather than manufacturing successful empty provider observations.
type RSAuthorBookCard struct {
	Facts    TitleFacts
	Personal *PersonalState
	// MP/UO own the typed child/source decoder after lock; raw Personal is not
	// Resume. The gap stays explicit until task 13 clears it.
	ResumePending bool

	Artwork               ArtworkFact
	MissingCanonicalFacts []string
}
type RSAuthorBookPage struct {
	Cards             []RSAuthorBookCard
	Total             int
	Next              *RSAuthorBookCursor
	Pending, Complete bool
}
type RSAuthorBookPreparation struct {
	mu               sync.Mutex
	reader           *RSAuthorBookReader
	lease            *governor.PreparationLease
	owner            governor.OwnerID
	input            RSAuthorBookInput
	started          time.Time
	used             int64
	fence            browseFence
	captured, closed bool
	entries          []rsAuthorBookEntry
	key              string
	scan, fileAfter  int64
	files            map[int64]uint8
	authorContext    bool
	start            int

	phase       int
	selected    []int64
	cards       []RSAuthorBookCard
	hydrate     int
	hydratedArt bool
}

func (p *RSAuthorBookPreparation) charge(n int64) error {
	if n < 0 || n > p.lease.PayloadBytes()-p.used {
		return governor.ErrSnapshotBudget
	}
	p.used += n
	return nil
}
func (p *RSAuthorBookPreparation) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.entries = nil
	p.key = ""
	p.files = nil
	p.fence = browseFence{}
	p.cards = nil
	p.selected = nil
	p.input = RSAuthorBookInput{}
	return p.reader.authority.RetirePreparation(p.lease)
}
func (p *RSAuthorBookPreparation) Close() error {
	if p == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}
func (r *RSAuthorBookReader) Begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, reservedBytes int64, in RSAuthorBookInput) (*RSAuthorBookPreparation, *governor.Refusal, error) {
	v := in.View
	if r == nil || ctx == nil || owner == 0 || in.Author <= 0 || in.Limit < 1 || in.Limit > MaxPage || v.Library <= 0 || v.Kind != 13 || v.Database == "" || len(v.Database) > 256 || v.TokenHash == "" || len(v.TokenHash) > 256 || v.Now.IsZero() || v.After != nil || v.KeepArticles || v.policy.recipe != 0 {
		return nil, nil, ErrInput
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%d/%#v", owner, in)))
	d := r.authority.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 74, ID: fmt.Sprintf("rs-author-books:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: reservedBytes, Dependencies: governor.PublicationFootprint{{}}})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	p := &RSAuthorBookPreparation{reader: r, lease: d.Lease, owner: owner, input: in, started: time.Now()}
	if err := p.charge(8192 + 4*int64(len(v.Database)+len(v.TokenHash))); err != nil {
		_ = p.close()
		return nil, nil, err
	}
	p.input.View.Database = strings.Clone(v.Database)
	p.input.View.TokenHash = strings.Clone(v.TokenHash)
	if in.After != nil {
		a := *in.After
		p.input.After = &a
	}
	return p, nil, nil
}
func (r *RSAuthorBookReader) Page(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, p *RSAuthorBookPreparation) (RSAuthorBookPage, BrowseReadResult, error) {
	if r == nil || p == nil || p.reader != r {
		return RSAuthorBookPage{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.owner != request.Owner {
		return RSAuthorBookPage{}, BrowseReadResult{}, ErrInput
	}
	page, cost, err := liveRead(ctx, request, budget, out, r.pool, p.input.View.Now.Add(time.Since(p.started)), func(ctx context.Context, q governor.Request, b time.Duration, at time.Time) (RSAuthorBookPage, BrowseReadResult, error) {
		ctx, cancel := context.WithDeadline(ctx, minTimeRS(time.Now().Add(min(b, time.Second)), p.lease.Deadline()))
		defer cancel()
		return r.poll(ctx, q, out, p, at)
	})
	if err != nil {
		_ = p.close()
	}
	return page, cost, err
}
func (p *RSAuthorBookPreparation) view(at time.Time, kind, id int64) BrowseInput {
	v := p.input.View
	v.Now = at
	v.Kind = kind
	v.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: id}
	return v
}

// fence checks use the author's accepted subject policy at both ends of every
// quantum. The common source root is conservative across all catalogue writes.
func (r *RSAuthorBookReader) fence(ctx context.Context, q governor.Request, out BrowseOutput, p *RSAuthorBookPreparation, at time.Time, cost *BrowseReadResult) error {
	if e := r.authority.CheckPreparation(p.lease); e != nil {
		return e
	}
	f, c, e := rsPeoplePublish(ctx, &rsByteWindow{source: r.snapshots, preflightCalls: 12, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}, q, time.Until(p.lease.Deadline()), out, p.view(at, 13, p.input.Author), []readStep[browseFence]{func(_ context.Context, _ browseQuery, f browseFence, d *browseFence) error { *d = f; return nil }})
	addSearchReadCost(cost, &c)
	if e != nil {
		return e
	}
	if p.captured {
		if f != p.fence {
			return ErrStale
		}
	} else {
		if e = p.charge(rsPeopleFenceBytes(f)); e != nil {
			return e
		}
		p.fence = f
		p.captured = true
	}
	return nil
}
func (p *RSAuthorBookPreparation) identity() [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%#v/%#v/%v", p.fence, struct {
		Owner           governor.OwnerID
		Author, Library int64
		Token           string
		Limit           int
		Key             string
	}{p.owner, p.input.Author, p.input.View.Library, p.input.View.TokenHash, p.input.Limit, p.key}, p.entries)))
}
func (r *RSAuthorBookReader) poll(ctx context.Context, q governor.Request, out BrowseOutput, p *RSAuthorBookPreparation, at time.Time) (page RSAuthorBookPage, cost BrowseReadResult, err error) {
	if err = r.fence(ctx, q, out, p, at, &cost); err != nil {
		return
	}
	if p.phase < 2 {
		err = r.enumerate(ctx, q, out, p, at, &cost)
	} else if p.phase == 2 {
		// The legacy author surface orders its books section by title
		// COLLATE NOCASE <dir>, entity_id <dir>; the series display is task 13's
		// EffectiveSeriesPending gap, not this page's order. NOCASE is the
		// SQLite ASCII-only fold, so titles that differ only in case tie on the
		// entity ID in the requested direction.
		in := p.input
		desc := in.View.Descending
		slices.SortFunc(p.entries, func(a, b rsAuthorBookEntry) int {
			return rsAuthorBookOrder(a, b, desc)
		})
		id := p.identity()
		start := 0
		if a := in.After; a != nil {
			if a.identity != id || a.author != in.Author || a.library != in.View.Library || a.kind != 8 || a.limit != in.Limit {
				return page, cost, ErrStale
			}
			start = len(p.entries)
			for i, entry := range p.entries {
				if rsAuthorBookAfter(entry, a, desc) {
					start = i
					break
				}
			}
		}
		end := min(start+in.Limit, len(p.entries))
		if err = p.charge(int64(end-start) * 4096); err != nil {
			return
		}
		for _, entry := range p.entries[start:end] {
			p.selected = append(p.selected, entry.id)
		}
		p.start = start
		p.cards = make([]RSAuthorBookCard, len(p.selected))
		p.phase = 3
	} else if p.hydrate < len(p.selected) {
		err = r.hydrate(ctx, q, out, p, at, &cost)
	} else {
		p.phase = 4
	}
	if err != nil {
		return
	}
	if err = r.fence(ctx, q, out, p, at, &cost); err != nil {
		return
	}
	if p.phase != 4 {
		page.Pending = true
		return
	}
	needed := int64(4096)
	for _, c := range p.cards {
		needed += authorBookCardBytes(c)
	}
	if needed > out.Request.Need[governor.MemoryBytes]/2 {
		return page, cost, governor.ErrSnapshotBudget
	}
	if err = r.authority.ReadyPreparation(p.lease); err != nil {
		return
	}
	expires, e := time.Parse(time.RFC3339Nano, p.fence.expiresAt)
	if e != nil || !p.input.View.Now.Add(time.Since(p.started)).Before(expires) {
		return page, cost, ErrDenied
	}
	horizon, he := time.Parse(time.RFC3339Nano, p.fence.horizon)
	if he != nil || !p.input.View.Now.Add(time.Since(p.started)).Before(horizon) {
		return page, cost, ErrDenied
	}
	if err = r.authority.HandoffPreparation(p.lease); err != nil {
		return
	}
	page = RSAuthorBookPage{Cards: p.cards, Total: len(p.entries), Complete: true}
	if len(p.selected) > 0 && p.start+len(p.selected) < len(p.entries) {
		last := p.entries[p.start+len(p.selected)-1]
		page.Next = &RSAuthorBookCursor{identity: p.identity(), author: p.input.Author, library: p.input.View.Library, kind: 8, limit: p.input.Limit, titleKey: last.titleKey, id: last.id}
	}
	// Output now owns the charged values; retiring the preparation drops all
	// references without mutating the consumer's copied page.
	err = p.close()
	if err != nil {
		page = RSAuthorBookPage{}
	}
	return
}

// rsAuthorBookEntry is one complete, deduplicated membership row. Its key is
// the same ASCII-only NOCASE fold SQLite applies to the legacy title order; the
// raw title stays available for facts and the oracle.
type rsAuthorBookEntry struct {
	id       int64
	title    string
	titleKey string
}

// rsAuthorBookOrder sorts exactly like the legacy author books section:
// title COLLATE NOCASE <dir>, entity_id <dir>.
func rsAuthorBookOrder(a, b rsAuthorBookEntry, descending bool) int {
	c := rsListeningKeyCompare(a.titleKey, b.titleKey)
	if c != 0 {
		if descending {
			return -c
		}
		return c
	}
	if descending {
		return cmp.Compare(b.id, a.id)
	}
	return cmp.Compare(a.id, b.id)
}

// rsAuthorBookAfter reports whether e lies strictly after the cursor key in the
// requested direction.
func rsAuthorBookAfter(e rsAuthorBookEntry, a *RSAuthorBookCursor, descending bool) bool {
	c := rsListeningKeyCompare(e.titleKey, a.titleKey)
	if c == 0 {
		c = cmp.Compare(e.id, a.id)
	}
	if descending {
		return c < 0
	}
	return c > 0
}

// First enumerate the complete current file stream once. A fixed-size map
// records structural and visible membership; wide book text is read only from
// exact admitted points in the subsequent complete book stream.
func (r *RSAuthorBookReader) enumerate(ctx context.Context, q governor.Request, out BrowseOutput, p *RSAuthorBookPreparation, at time.Time, cost *BrowseReadResult) error {
	w := &rsByteWindow{source: r.snapshots, preflightCalls: 20, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	var next int64
	_, c, e := rsPeoplePublish(ctx, w, q, time.Until(p.lease.Deadline()), out, p.view(at, 13, p.input.Author), []readStep[struct{}]{
		func(_ context.Context, query browseQuery, _ browseFence, _ *struct{}) error {
			name, id := "rs.audio.author_key_octets", p.input.Author
			if p.key != "" {
				if p.phase == 0 {
					return nil
				}
				rows, e := query("rs.audio.books", p.input.View.Library, p.scan)
				if e != nil {
					return e
				}
				if len(rows) == 0 {
					return nil
				}
				next = rows[0][0].(int64)
				id = next
				name = "rs.author.book_octets"
			}
			rows, e := query(name, id, p.input.View.Library)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				return nil
			}
			n, e := rsPointBytes(rows[0])
			if e != nil {
				return e
			}
			// The book payload scope also checks its subject admission proof.
			// That projection contains exactly one checked 16-byte incarnation;
			// its optional publication phase is an enum of at most nine bytes.
			// Reserve the largest actual point or fixed proof row, not just text.
			if name == "rs.author.book_octets" {
				n = max(n, 16)
			}
			return w.arm(n, 8)
		}, func(_ context.Context, query browseQuery, f browseFence, _ *struct{}) error {
			if p.key == "" {
				rows, e := query("rs.audio.author_key", p.input.Author, p.input.View.Library)
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrDenied
				}
				key := rsListeningAuthorKey(rows[0][0].(string))
				if key == "" {
					return ErrDenied
				}
				if e = p.charge(4 * int64(len(key))); e != nil {
					return e
				}
				p.key = key
				return nil
			}
			if p.phase == 0 {
				rows, e := query("rs.author.files", p.input.View.Library, p.fileAfter)
				if e != nil {
					return e
				}
				if len(rows) == 0 {
					p.phase = 1
					return nil
				}
				row := rows[0]
				p.fileAfter = row[0].(int64)
				if row[1] == nil || row[2] != int64(0) {
					return nil
				}
				book := row[1].(int64)
				flags := uint8(1)
				itemFence := f
				itemFence.container = 0
				if _, e = rsDetailSubjectEligibility(query, p.view(at, 9, p.fileAfter), itemFence); e == nil {
					flags |= 2
				} else if !errors.Is(e, ErrDenied) {
					return e
				}
				if p.files[book] == 0 {
					if e = p.charge(256); e != nil {
						return e
					}
					if p.files == nil {
						p.files = make(map[int64]uint8)
					}
				}
				p.files[book] |= flags
				return nil
			}
			if next == 0 {
				if !p.authorContext {
					return ErrDenied
				}
				p.phase = 2
				return nil
			}
			p.scan = next
			rows, e := query("rs.author.book", p.scan, p.input.View.Library)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return nil
			}
			row := rows[0]
			if rsListeningAuthorKey(row[0].(string)) != p.key {
				return nil
			}
			// Any current book by this author proves the author context exists,
			// exactly as the legacy author group survives while its books section
			// can still be empty. File presence only decides membership.
			p.authorContext = true
			if p.files[p.scan]&1 == 0 {
				return nil
			}
			if p.files[p.scan]&2 == 0 {
				return nil
			}
			if _, e = rsDetailSubjectEligibility(query, p.view(at, 8, p.scan), f); errors.Is(e, ErrDenied) {
				return nil
			} else if e != nil {
				return e
			}
			title := row[1].(string)
			if e = p.charge(256 + 4*int64(len(title)) + int64(len(p.entries)+1)*64); e != nil {
				return e
			}
			entries := make([]rsAuthorBookEntry, len(p.entries), len(p.entries)+1)
			copy(entries, p.entries)
			p.entries = append(entries, rsAuthorBookEntry{p.scan, title, rbPersonalASCIIFold(title)})
			return nil
		}})
	addSearchReadCost(cost, &c)
	return e
}

func authorBookCardBytes(c RSAuthorBookCard) int64 {
	return 8192 + artistAlbumWideBytes(reflect.ValueOf(c))
}
func (r *RSAuthorBookReader) hydrate(ctx context.Context, q governor.Request, out BrowseOutput, p *RSAuthorBookPreparation, at time.Time, cost *BrowseReadResult) error {
	id := p.selected[p.hydrate]
	f := &ForegroundReader{snapshots: r.snapshots, pool: r.pool}
	card := &p.cards[p.hydrate]
	// The page direction only orders membership; the shared facts, art and
	// personal recipes refuse a descending request and keep their canonical
	// detail semantics.
	v := p.view(at, 8, id)
	v.Descending = false
	// Private recipes stay inside the caller's single LIVE operation. Each poll
	// performs one hydration recipe, under the original preparation deadline.
	var c BrowseReadResult
	var e error
	if card.Facts.Book == nil {
		var facts TitleFacts
		facts, c, e = f.facts(ctx, q, time.Until(p.lease.Deadline()), out, TitleFactsInput{View: v, Item: id})
		if e == nil {
			e = p.charge(authorBookCardBytes(RSAuthorBookCard{Facts: facts}))
			if e == nil {
				card.Facts = facts
			}
		}
	} else if !p.hydratedArt {
		card.Artwork, c, e = f.artwork(ctx, q, time.Until(p.lease.Deadline()), out, ArtworkInput{View: v, Item: id, Role: "poster", PageSlot: true})
		if e == nil {
			e = p.charge(authorBookCardBytes(*card))
			if e == nil {
				// Effective series and typed resume are explicit owner-ABI gaps
				// (task 13), not empty successful observations. The names are the
				// accepted scalar/view columns those owners will supply.
				card.MissingCanonicalFacts = []string{"personal_items.resume_item_public_id/resume_asset_id", "listening_book_context.series_index"}
				card.ResumePending = true
				p.hydratedArt = true
			}
		}
	} else {
		var state PersonalState
		state, c, e = f.uoPersonalState(ctx, q, time.Until(p.lease.Deadline()), out, PersonalStateInput{View: v, Item: id})
		if e == nil {
			e = p.charge(512)
			if e == nil {
				card.Personal = &state
				p.hydrate++
				p.hydratedArt = false
			}
		}
	}

	addSearchReadCost(cost, &c)
	return e
}
