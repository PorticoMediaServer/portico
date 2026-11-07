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
		rows, e := query("rs.album.provider.state", id, p.input.View.Library)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 || len(rows[0]) != 24 {
			return nil, ErrUnqualified
		}
		if work.source != nil && !equalSource(work.source, rows[0]) {
			return nil, ErrStale
		}
		if work.source == nil {
			if e = p.charge(2048); e != nil {
				return nil, e
			}
			work.source = rows[0]
		}
		return rows[0], nil
	}
	_, one, e := rsPeoplePublish(ctx, w, req, time.Until(p.lease.Deadline()), out, p.view(at, 6, id), []readStep[struct{}]{
		func(_ context.Context, q browseQuery, _ browseFence, _ *struct{}) error {
			source := work.source
			// Missing accepted head or current provider permission has no provider
			// fact. A stored/pending observation is never substituted for that head.
			if source[7] != int64(1) || source[8] != int64(1) || source[9] != int64(1) || source[18] != int64(1) || source[19] != int64(1) || source[20] != int64(1) {
				work.ready = true
				return nil
			}
			if source[23] != int64(1) {
				return ErrUnqualified
			}
			name := "rs.album.provider.header_octets"
			args := []any{id, p.input.View.Library}
			if work.header {
				rows, e := q("rs.album.provider.next_type", id, p.input.View.Library, work.after)
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
				args = append(args, ordinal)
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
				if e = p.charge(512 + artistAlbumWideBytes(reflect.ValueOf(fact))); e != nil {
					return e
				}
				work.fact = &fact
				work.header = true
				return nil
			}
			rows, e := q("rs.album.provider.type", id, p.input.View.Library, ordinal)
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
