package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"cmp"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RSAuthorBookReader is an inert trusted-harness reader. Preparation never
// owns a SQL snapshot, renews a lease, or grants cached authorization.
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
type RSAuthorBookCursor struct {
	identity                     [32]byte
	author, library, kind, after int64
	limit                        int
}

// MissingCanonicalFacts identifies facts unavailable in the accepted scalar
// image, rather than manufacturing successful empty provider observations.
type RSAuthorBookCard struct {
	Facts    TitleFacts
	Personal *PersonalState

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
	if r == nil || ctx == nil || owner == 0 || in.Author <= 0 || in.Limit < 1 || in.Limit > MaxPage || v.Library <= 0 || v.Kind != 13 || v.Database == "" || len(v.Database) > 256 || v.TokenHash == "" || len(v.TokenHash) > 256 || v.Now.IsZero() || v.After != nil || v.Descending || v.KeepArticles || v.policy.recipe != 0 {
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
		slices.SortFunc(p.entries, func(a, b rsAuthorBookEntry) int {
			if n := cmp.Compare(a.position, b.position); n != 0 {
				return n
			}
			if n := cmp.Compare(a.title, b.title); n != 0 {
				return n
			}
			return cmp.Compare(a.id, b.id)
		})
		id := p.identity()
		in := p.input
		after := int64(0)
		if a := in.After; a != nil {
			if a.identity != id || a.author != in.Author || a.library != in.View.Library || a.kind != 8 || a.limit != in.Limit {
				return page, cost, ErrStale
			}
			after = a.after
		}

		offset := int(after)
		if offset < 0 || offset > len(p.entries) {
			return page, cost, ErrStale
		}
		end := min(offset+in.Limit, len(p.entries))
		if err = p.charge(int64(end-offset) * 4096); err != nil {
			return
		}
		for _, entry := range p.entries[offset:end] {
			p.selected = append(p.selected, entry.id)
		}
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

	page = RSAuthorBookPage{Cards: p.cards, Total: len(p.entries), Complete: true}
	offset := 0
	if p.input.After != nil {
		offset = int(p.input.After.after)
	}
	if offset+len(p.selected) < len(p.entries) {
		page.Next = &RSAuthorBookCursor{identity: p.identity(), author: p.input.Author, library: p.input.View.Library, kind: 8, limit: p.input.Limit, after: int64(offset + len(p.selected))}
	}

	return
}

type rsAuthorBookEntry struct {
	id       int64
	title    string
	position float64
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
			if rsListeningAuthorKey(row[0].(string)) != p.key || p.files[p.scan]&1 == 0 {
				return nil
			}
			p.authorContext = true
			if p.files[p.scan]&2 == 0 {
				return nil
			}
			if _, e = rsDetailSubjectEligibility(query, p.view(at, 8, p.scan), f); errors.Is(e, ErrDenied) {
				return nil
			} else if e != nil {
				return e
			}
			pos := float64(0)
			if row[2] != nil {
				pos = rsAuthorSQLiteReal(row[2].(string))
			}
			if pos <= 0 {
				pos = 999999
			}
			if e = p.charge(256 + 4*int64(len(row[1].(string))) + int64(len(p.entries)+1)*64); e != nil {
				return e
			}
			entries := make([]rsAuthorBookEntry, len(p.entries), len(p.entries)+1)
			copy(entries, p.entries)
			p.entries = append(entries, rsAuthorBookEntry{p.scan, row[1].(string), pos})
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
	var c BrowseReadResult
	var e error
	if card.Facts.Book == nil {
		var v TitleFacts
		v, c, e = f.facts(ctx, q, time.Until(p.lease.Deadline()), out, TitleFactsInput{View: p.view(at, 8, id), Item: id})
		if e == nil {
			e = p.charge(authorBookCardBytes(RSAuthorBookCard{Facts: v}))
			if e == nil {
				card.Facts = v
			}
		}
	} else if !p.hydratedArt {
		card.Artwork, c, e = f.artwork(ctx, q, time.Until(p.lease.Deadline()), out, ArtworkInput{View: p.view(at, 8, id), Item: id, Role: "poster", PageSlot: true})
		if e == nil {
			e = p.charge(authorBookCardBytes(*card))
			if e == nil {
				card.MissingCanonicalFacts = []string{"catalog_books.provider_match_status", "book_resume.item_id/position", "catalog_book_context.series_index"}
				p.hydratedArt = true
			}
		}
	} else {
		var state PersonalState
		state, c, e = f.uoPersonalState(ctx, q, time.Until(p.lease.Deadline()), out, PersonalStateInput{View: p.view(at, 8, id), Item: id})
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

// Publish encodes and delivers a ready draft inside the existing LIVE/output
// custody. The original preparation stays charged until the consumer returns.
// Page is a trusted preparation API; only Publish delivers public bytes.
func (r *RSAuthorBookReader) Publish(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, p *RSAuthorBookPreparation, encode func(RSAuthorBookPage) ([]byte, error), consume func([]byte) error) (bool, error) {
	if r == nil || p == nil || p.reader != r || encode == nil || consume == nil {
		return false, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.owner != req.Owner {
		return false, ErrInput
	}
	_, _, err := liveRead(ctx, req, budget, out, r.pool, p.input.View.Now.Add(time.Since(p.started)), func(ctx context.Context, q governor.Request, b time.Duration, at time.Time) (struct{}, BrowseReadResult, error) {
		page, cost, e := r.poll(ctx, q, out, p, at)
		if e != nil {
			return struct{}{}, cost, e
		}
		if page.Pending {
			return struct{}{}, cost, nil
		}
		bytes, e := encode(page)
		if e != nil {
			return struct{}{}, cost, e
		}
		if int64(len(bytes)) > out.Request.Need[governor.MemoryBytes]/2 {
			return struct{}{}, cost, governor.ErrSnapshotBudget
		}
		if e = p.charge(int64(len(bytes))); e != nil {
			return struct{}{}, cost, e
		}
		if e = r.fence(ctx, q, out, p, p.input.View.Now.Add(time.Since(p.started)), &cost); e != nil {
			return struct{}{}, cost, e
		}
		if e = r.authority.HandoffPreparation(p.lease); e != nil {
			return struct{}{}, cost, e
		}
		e = consume(bytes)
		return struct{}{}, cost, e
	})
	done := p.phase == 4
	if err != nil || done {
		err = errors.Join(err, p.close())
	}
	return done, err
}

// rsAuthorSQLiteReal implements SQLite REAL coercion's ASCII numeric prefix:
// trailing text is ignored and an incomplete exponent is excluded. NULL is
// handled by the caller; the original text remains unchanged in typed Facts.
func rsAuthorSQLiteReal(s string) float64 {
	start := 0
	for start < len(s) && strings.ContainsRune(" \t\n\r\v\f", rune(s[start])) {
		start++
	}
	i := start
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0
	}
	end := i
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i > exp {
			end = i
		}
	}
	n, _ := strconv.ParseFloat(s[start:end], 64)
	return n
}
