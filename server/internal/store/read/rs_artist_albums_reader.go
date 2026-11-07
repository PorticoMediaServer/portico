package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// RSArtistAlbumReader is an inert trusted-harness reader. Preparation never
// owns a SQL snapshot, renews a lease, or grants cached authorization.
type RSArtistAlbumReader struct {
	pool      *governor.Snapshots
	authority *governor.Authority
}

func NewRSArtistAlbumReader(s *governor.Snapshots, a *governor.Authority) (*RSArtistAlbumReader, error) {
	if s == nil || a == nil || !s.OwnedBy(a) {
		return nil, ErrInput
	}
	return &RSArtistAlbumReader{s, a}, nil
}

type RSArtistAlbumInput struct {
	View   BrowseInput
	Artist int64
	Limit  int
	After  *RSArtistAlbumCursor
}
type RSArtistAlbumCursor struct {
	identity                     [32]byte
	artist, library, kind, after int64
	limit                        int
}

// MissingCanonicalFacts identifies facts unavailable in the accepted scalar
// image, rather than manufacturing successful empty provider observations.
type RSArtistAlbumCard struct {
	Facts                 TitleFacts
	PrimaryArtist         TitleFacts
	Artwork               ArtworkFact
	MissingCanonicalFacts []string
}
type RSArtistAlbumPage struct {
	Cards             []RSArtistAlbumCard
	Total             int
	Next              *RSArtistAlbumCursor
	Pending, Complete bool
}
type RSArtistAlbumPreparation struct {
	mu                sync.Mutex
	reader            *RSArtistAlbumReader
	lease             *governor.PreparationLease
	owner             governor.OwnerID
	input             RSArtistAlbumInput
	started           time.Time
	used              int64
	fence             browseFence
	captured, closed  bool
	set               rsArtistAlbumSet
	primary, featured int64
	phase             int
	selected          []int64
	cards             []RSArtistAlbumCard
	hydrate           int
}

func (p *RSArtistAlbumPreparation) charge(n int64) error {
	if n < 0 || n > p.lease.PayloadBytes()-p.used {
		return governor.ErrSnapshotBudget
	}
	p.used += n
	return nil
}
func (p *RSArtistAlbumPreparation) close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	p.set = rsArtistAlbumSet{}
	p.cards = nil
	p.selected = nil
	p.input = RSArtistAlbumInput{}
	return p.reader.authority.RetirePreparation(p.lease)
}
func (p *RSArtistAlbumPreparation) Close() error {
	if p == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.close()
}
func (r *RSArtistAlbumReader) Begin(ctx context.Context, owner governor.OwnerID, deadline time.Time, reservedBytes int64, in RSArtistAlbumInput) (*RSArtistAlbumPreparation, *governor.Refusal, error) {
	v := in.View
	if r == nil || ctx == nil || owner == 0 || in.Artist <= 0 || in.Limit < 1 || in.Limit > MaxPage || v.Library <= 0 || v.Kind != 5 || v.Database == "" || len(v.Database) > 256 || v.TokenHash == "" || len(v.TokenHash) > 256 || v.Now.IsZero() || v.After != nil || v.Descending || v.KeepArticles || v.policy.recipe != 0 {
		return nil, nil, ErrInput
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%d/%#v", owner, in)))
	d := r.authority.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 73, ID: fmt.Sprintf("rs-artist-albums:%x", key)}, Owner: owner, Deadline: deadline, ReservedBytes: reservedBytes, Dependencies: governor.PublicationFootprint{{}}})
	if d.Refusal != nil {
		return nil, d.Refusal, nil
	}
	p := &RSArtistAlbumPreparation{reader: r, lease: d.Lease, owner: owner, input: in, started: time.Now()}
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
func (r *RSArtistAlbumReader) Page(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, p *RSArtistAlbumPreparation) (RSArtistAlbumPage, BrowseReadResult, error) {
	if r == nil || p == nil || p.reader != r {
		return RSArtistAlbumPage{}, BrowseReadResult{}, ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.owner != request.Owner {
		return RSArtistAlbumPage{}, BrowseReadResult{}, ErrInput
	}
	page, cost, err := liveRead(ctx, request, budget, out, r.pool, p.input.View.Now.Add(time.Since(p.started)), func(ctx context.Context, q governor.Request, b time.Duration, at time.Time) (RSArtistAlbumPage, BrowseReadResult, error) {
		ctx, cancel := context.WithDeadline(ctx, minTimeRS(time.Now().Add(min(b, time.Second)), p.lease.Deadline()))
		defer cancel()
		return r.poll(ctx, q, out, p, at)
	})
	if err != nil {
		_ = p.close()
	}
	return page, cost, err
}
func (p *RSArtistAlbumPreparation) view(at time.Time, kind, id int64) BrowseInput {
	v := p.input.View
	v.Now = at
	v.Kind = kind
	v.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: id}
	return v
}

// fence checks use the artist's accepted subject policy at both ends of every
// quantum. The common source root is conservative across all catalogue writes.
func (r *RSArtistAlbumReader) fence(ctx context.Context, q governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time, cost *BrowseReadResult) error {
	if e := r.authority.CheckPreparation(p.lease); e != nil {
		return e
	}
	f, c, e := rsPeoplePublish(ctx, &rsByteWindow{source: r.pool, preflightCalls: 12}, q, time.Until(p.lease.Deadline()), out, p.view(at, 5, p.input.Artist), []readStep[browseFence]{func(_ context.Context, _ browseQuery, f browseFence, d *browseFence) error { *d = f; return nil }})
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
func (p *RSArtistAlbumPreparation) identity() [32]byte {
	return [32]byte{}
}
func (r *RSArtistAlbumReader) poll(ctx context.Context, q governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time) (page RSArtistAlbumPage, cost BrowseReadResult, err error) {
	if err = r.fence(ctx, q, out, p, at, &cost); err != nil {
		return
	}
	if p.phase < 2 {
		err = r.enumerate(ctx, q, out, p, at, &cost)
	} else if p.phase == 2 {
		id := p.identity()
		in := p.input
		after := int64(0)
		if a := in.After; a != nil {
			if a.identity != id || a.artist != in.Artist || a.library != in.View.Library || a.kind != 6 || a.limit != in.Limit {
				return page, cost, ErrStale
			}
			after = a.after
		}
		p.set.complete = true
		p.selected, _, err = p.set.page(after, in.Limit)
		if err == nil {
			if err = p.charge(int64(len(p.selected)) * 4096); err == nil {
				p.cards = make([]RSArtistAlbumCard, len(p.selected))
				p.phase = 3
			}
		}
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
		needed += artistAlbumCardBytes(c)
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
	page = RSArtistAlbumPage{Cards: p.cards, Total: len(p.set.ids), Complete: true}
	if len(p.selected) > 0 && p.selected[len(p.selected)-1] < p.set.ids[len(p.set.ids)-1] {
		page.Next = &RSArtistAlbumCursor{identity: p.identity(), artist: p.input.Artist, library: p.input.View.Library, kind: 6, limit: p.input.Limit, after: p.selected[len(p.selected)-1]}
	}
	// Output now owns the charged values; retiring the preparation drops all
	// references without mutating the consumer's copied page.
	err = p.close()
	if err != nil {
		page = RSArtistAlbumPage{}
	}
	return
}
func (r *RSArtistAlbumReader) enumerate(ctx context.Context, q governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time, cost *BrowseReadResult) error {
	_, c, e := rsPeoplePublish(ctx, &rsByteWindow{source: r.pool, preflightCalls: 12}, q, time.Until(p.lease.Deadline()), out, p.view(at, 5, p.input.Artist), []readStep[struct{}]{func(context.Context, browseQuery, browseFence, *struct{}) error { return nil }, func(_ context.Context, query browseQuery, f browseFence, _ *struct{}) error {
		name, after := "rs.audio.primary", p.primary
		if p.phase == 1 {
			name, after = "rs.audio.featured", p.featured
		}
		rows, e := query(name, p.input.Artist, after)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			p.phase++
			return nil
		}
		if len(rows) != 1 {
			return ErrUnqualified
		}
		id := rows[0][0].(int64)
		if p.phase == 0 {
			p.primary = id
		} else {
			p.featured = id
			songs, e := query("rs.audio.song", id)
			if e != nil {
				return e
			}
			if len(songs) != 1 {
				return nil
			}
			s := songs[0]
			if s[1] != p.input.View.Library || s[3] != int64(0) || s[4] == nil {
				return nil
			}
			v := p.view(at, 7, id)
			if _, e = rsDetailSubjectEligibility(query, v, f); errors.Is(e, ErrDenied) {
				return nil
			} else if e != nil {
				return e
			}
			id = s[4].(int64)
		}
		v := p.view(at, 6, id)
		if _, e = rsDetailSubjectEligibility(query, v, f); errors.Is(e, ErrDenied) {
			return nil
		} else if e != nil {
			return e
		}
		rows, e = query("rs.audio.album_artist", id, p.input.View.Library)
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return nil
		}
		// Current primary artist must exist and be live; it is context, not a
		// substitute authority proof for the album's accepted child band.
		if slices.Contains(p.set.ids, id) {
			return nil
		}
		if e = p.charge(int64(len(p.set.ids)+1) * 8); e != nil {
			return e
		}
		ids := make([]int64, len(p.set.ids), len(p.set.ids)+1)
		copy(ids, p.set.ids)
		p.set.ids = ids
		return p.set.add(id)
	}})
	addSearchReadCost(cost, &c)
	return e
}

// Count all retained wide fields, including nested album/artist facts. Native
// octet preflight remains in the private Facts/art recipes before copying.
func artistAlbumCardBytes(c RSArtistAlbumCard) int64 {
	return 8192 + artistAlbumWideBytes(reflect.ValueOf(c))
}
func artistAlbumWideBytes(v reflect.Value) int64 {
	switch v.Kind() {
	case reflect.String:
		return 4 * int64(v.Len())
	case reflect.Pointer:
		if !v.IsNil() {
			return 64 + artistAlbumWideBytes(v.Elem())
		}
	case reflect.Struct:
		var n int64
		for i := 0; i < v.NumField(); i++ {
			n += artistAlbumWideBytes(v.Field(i))
		}
		return n
	case reflect.Slice:
		var n int64
		for i := 0; i < v.Len(); i++ {
			n += 64 + artistAlbumWideBytes(v.Index(i))
		}
		return n
	}
	return 0
}
func (r *RSArtistAlbumReader) hydrate(ctx context.Context, q governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time, cost *BrowseReadResult) error {
	id := p.selected[p.hydrate]
	f := &ForegroundReader{snapshots: r.pool, pool: r.pool}
	card := &p.cards[p.hydrate]
	// Private recipes stay inside the caller's single LIVE operation. Each poll
	// performs one hydration recipe, under the original preparation deadline.
	var c BrowseReadResult
	var e error
	if card.Facts.Album == nil {
		var value TitleFacts
		value, c, e = f.facts(ctx, q, time.Until(p.lease.Deadline()), out, TitleFactsInput{View: p.view(at, 6, id), Item: id})
		if e == nil {
			e = p.charge(artistAlbumCardBytes(RSArtistAlbumCard{Facts: value}))
			if e == nil {
				card.Facts = value
			}
		}
	} else if card.PrimaryArtist.Item.ID == 0 {
		// Artist context may be hidden by its own subject policy: never expose it.
		var value TitleFacts
		value, c, e = f.facts(ctx, q, time.Until(p.lease.Deadline()), out, TitleFactsInput{View: p.view(at, 5, card.Facts.Album.Artist), Item: card.Facts.Album.Artist})
		if e == nil {
			e = p.charge(artistAlbumCardBytes(RSArtistAlbumCard{PrimaryArtist: value}))
			if e == nil {
				card.PrimaryArtist = value
			}
		}
	} else {
		card.Artwork, c, e = f.artwork(ctx, q, time.Until(p.lease.Deadline()), out, ArtworkInput{View: p.view(at, 6, id), Item: id, Role: "poster", PageSlot: true})
		if e == nil {
			card.MissingCanonicalFacts = []string{"catalog_albums.provider_match_status", "catalog_albums.release_id", "catalog_albums.release_group_id", "catalog_albums.provider_observed_at", "albumRoleSQL"}
			p.hydrate++
		}
	}
	addSearchReadCost(cost, &c)
	return e
}
