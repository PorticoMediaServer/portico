package read

import (
	"context"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// The work belongs to the artist preparation; it borrows that same lease and
// LIVE/output operation. No provider cache, lookup or second lease is opened.
type rsAlbumProviderWork struct {
	source        []any
	state         []any
	evidence      string
	fact          *RSAlbumProviderFact
	after         int64
	header, ready bool
}

func (r *RSArtistAlbumReader) provider(ctx context.Context, req governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time, cost *BrowseReadResult) error {
	if p.providerWork == nil {
		if e := p.charge(int64(len(p.cards)) * 512); e != nil {
			return e
		}
		p.providerWork = make([]rsAlbumProviderWork, len(p.cards))
		for i := range p.providerWork {
			p.providerWork[i].after = -1
		}
	}
	work := &p.providerWork[p.hydrate]
	id := p.selected[p.hydrate]
	w := &rsByteWindow{source: r.snapshots, preflightCalls: 24, goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	var ordinal int64 = -1
	root := func(query browseQuery) ([]any, error) {
		flat, state, e := rsAlbumProviderSource(query, id, p.input.View.Library)
		if e != nil {
			return nil, e
		}
		if work.source != nil && !equalSource(work.source, flat) {
			return nil, ErrStale
		}
		if work.source == nil {
			if e = p.charge(2048); e != nil {
				return nil, e
			}
			work.source, work.state = flat, state
		}
		return flat, nil
	}
	_, one, e := rsPeoplePublish(ctx, w, req, time.Until(p.lease.Deadline()), out, p.view(at, 6, id), []readStep[struct{}]{
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			source := work.state
			if len(source) == 0 {
				work.ready = true
				return nil
			}
			// Only a currently coherent, complete accepted source yields a fact.
			// Stored/pending observations do not substitute for that source.
			name := "rs.album.provider.header_octets"
			args := []any{id, p.input.View.Library}
			if work.header {
				nextName := "rs.album.provider.next_type"
				nextArgs := []any{work.evidence, work.after}
				if work.after < 0 {
					nextName = "rs.album.provider.first_type"
					nextArgs = nextArgs[:1]
				}
				rows, e := q(nextName, nextArgs...)
				if e != nil {
					return e
				}
				if len(rows) == 0 {
					work.ready = true
					return nil
				}
				ordinal = rows[0][0].(int64)
				if ordinal != work.after+1 {
					return ErrUnqualified
				}
				name = "rs.album.provider.type_octets"
				args = []any{work.evidence, ordinal}
			}
			rows, e := q(name, args...)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			bytes, e := rsPointBytes(rows[0])
			if e != nil {
				return e
			}
			// One byte also funds a legitimate empty secondary label. It is a known
			// whole-row bound, not a maximum-column reservation or a truncation cap.
			return w.arm(max(bytes, 1), 2)
		},
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			if work.ready {
				return nil
			}
			if !work.header {
				rows, e := q("rs.album.provider.header", id, p.input.View.Library)
				if e != nil {
					return e
				}
				if len(rows) != 1 {
					return ErrStale
				}
				row := rows[0]
				fact := rsAlbumProviderValues(row[0].(string), row[1].(string), row[2].(string), row[3].(string), row[4].(string), nil)
				if e = p.charge(512 + artistAlbumWideBytes(reflect.ValueOf(fact)) + 4*int64(len(row[5].(string)))); e != nil {
					return e
				}
				work.evidence = row[5].(string)
				work.fact = &fact
				work.header = true
				return nil
			}
			rows, e := q("rs.album.provider.type", work.evidence, ordinal)
			if e != nil {
				return e
			}
			if len(rows) != 1 || rows[0][0] != ordinal {
				return ErrStale
			}
			value := rows[0][1].(string)
			// Charge old/new slice backing before growing the complete ordered list.
			if e = p.charge(128 + int64(len(work.fact.SecondaryTypes)+1)*32 + 4*int64(len(value))); e != nil {
				return e
			}
			values := make([]string, len(work.fact.SecondaryTypes), len(work.fact.SecondaryTypes)+1)
			copy(values, work.fact.SecondaryTypes)
			work.fact.SecondaryTypes = append(values, value)
			if rsAlbumLabelEqual(value, "Compilation") {
				work.fact.DisplayPrimaryType = "Compilation"
			}
			work.after = ordinal
			return nil
		},
	}, root)
	addSearchReadCost(cost, &one)
	if e != nil {
		return e
	}
	if work.ready {
		card := &p.cards[p.hydrate]
		card.Provider = work.fact
		card.Role = rsAlbumRole(card.PrimaryArtist.ID, p.input.Artist, work.fact)
		// Provider status taxonomy is separate from the accepted observation; this
		// reader does not invent matched/unresolved from a stored evidence row.
		card.MissingCanonicalFacts = []string{"catalog_albums.provider_match_status"}
		p.hydrate++
		p.hydratedArt = false
	}
	return nil
}

// Recheck every retained source at delivery; a cached observation never
// authorizes itself. These fixed source points join the original global
// footprint and current artist/principal fence in the shared two-view driver.
func (r *RSArtistAlbumReader) providerFences(ctx context.Context, req governor.Request, out BrowseOutput, p *RSArtistAlbumPreparation, at time.Time, cost *BrowseReadResult) error {
	root := func(q browseQuery) ([]any, error) {
		if len(p.providerWork) != len(p.cards) {
			return nil, ErrUnqualified
		}
		for i, work := range p.providerWork {
			if !work.ready || work.source == nil {
				return nil, ErrUnqualified
			}
			flat, _, e := rsAlbumProviderSource(q, p.selected[i], p.input.View.Library)
			if e != nil {
				return nil, e
			}
			if !equalSource(work.source, flat) {
				return nil, ErrStale
			}
		}
		return nil, nil
	}
	w := &rsByteWindow{source: r.snapshots, preflightCalls: uint16(4*len(p.cards) + 16), goCopyBytes: out.Request.Need[governor.MemoryBytes] / 2}
	_, c, e := rsPeoplePublish(ctx, w, req, time.Until(p.lease.Deadline()), out, p.view(at, 5, p.input.Artist), []readStep[struct{}]{func(context.Context, browseQuery, browseFence, *struct{}) error { return nil }}, root)
	addSearchReadCost(cost, &c)
	return e
}

// Fixed entity identity remains fenced even when the exact provider slot does
// not exist. Other providers/roles are not substitutes for the primary release.
func rsAlbumProviderSource(q browseQuery, id, library int64) ([]any, []any, error) {
	entity, e := q("rs.detail.source", id, library, int64(6))
	if e != nil {
		return nil, nil, e
	}
	if len(entity) != 1 {
		return nil, nil, ErrDenied
	}
	slot, e := q("rs.album.provider.slot", id)
	if e != nil {
		return nil, nil, e
	}
	if len(slot) > 1 {
		return nil, nil, ErrUnqualified
	}
	state, e := q("rs.album.provider.state", id, library)
	if e != nil {
		return nil, nil, e
	}
	if len(state) > 1 || len(state) == 1 && len(state[0]) != 18 {
		return nil, nil, ErrUnqualified
	}
	flat := append([]any(nil), entity[0]...)
	if len(slot) > 0 {
		flat = append(flat, slot[0]...)
	}
	// Consent is an independent exact singleton point. Joining the singleton
	// lets ANALYZE prefer a table SCAN; the point remains SEARCH in both profiles.
	runtime, e := q("rs.album.provider.runtime", int64(1))
	if e != nil {
		return nil, nil, e
	}
	if len(runtime) > 1 || len(runtime) == 1 && len(runtime[0]) != 2 {
		return nil, nil, ErrUnqualified
	}
	flat = append(flat, int64(len(runtime)))
	if len(runtime) == 1 {
		flat = append(flat, runtime[0]...)
	}
	if len(state) == 0 {
		return flat, nil, nil
	}
	flat = append(flat, state[0]...)
	if len(runtime) == 0 {
		return flat, nil, nil
	}
	epoch, ok := runtime[0][0].(int64)
	online, ok2 := runtime[0][1].(int64)
	if !ok || !ok2 || epoch < 0 || online < 0 || online > 1 {
		return nil, nil, ErrUnqualified
	}
	if online != 1 || state[0][11] != epoch {
		return flat, nil, nil
	}
	return flat, state[0], nil
}
