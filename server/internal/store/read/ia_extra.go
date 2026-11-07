package read

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// IAExtraInput selects owner membership by exact extra type. Empty ExtraKind
// selects the entity's own extra and complete asset/version/part membership.
// These are evidence reads, never acceptance or public related/detail APIs.
type IAExtraInput struct {
	View      BrowseInput
	Entity    int64
	ExtraKind string
	After     *IAExtraCursor
}
type IAExtraCursor struct {
	fence      browseFence
	entity, id int64
	kind       string
	source     []any
}
type IAExtraMember struct {
	Item, Kind, Revision                              int64
	Owner                                             *int64
	ParentPath, Type, Title, AssetToken, RelativePath string
	Artist, Album, Book, Disc, Track, Part            *int64
	Links                                             []IAAudioLink
}
type IAExtraPage struct {
	Entity                BrowseItem
	Members               []IAExtraMember
	Links                 []IAAudioLink
	Next                  *IAExtraCursor
	Exhausted             bool
	RawRows, RawPositions int
}
type iaExtraCalls struct {
	n           int
	publication bool
}
type iaExtraSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaExtraCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaExtraSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaExtraSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaExtraCalls{})
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return fn(scope)
	}
	// Instrumented test snapshots keep their boundary hooks; production harness
	// composition always supplies the actual pool and this bounded forecast.
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		return pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(min(uint32(4096), r.Budget.RawRows))}, callback)
	}
	return s.base.Read(ctx, r, b, callback)
}
func (s *iaExtraSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	prefix := "foreground.ia.extra."

	octets := readStatementID(prefix + name + "_octets")
	if name == "link" {
		octets = iaLinkOctetsID
	}
	row, e := s.scope.QueryExactBytes(octets, readStatementID(prefix+name), columns, args...)
	s.extra[len(s.extra)-1].n += 2
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += 2 * int64(len(v))
		case []byte:
			s.copied += 3 * int64(len(v))
		}
	}
	if s.copied > s.copyLimit {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}
func (s *iaExtraSnapshots) links(q browseQuery, entity, library int64) ([]IAAudioLink, error) {
	rows, e := q("foreground.ia.extra.links", entity)
	if e != nil {
		return nil, e
	}
	if len(rows) > 16 {
		return nil, ErrUnqualified
	}
	var links []IAAudioLink
	for _, key := range rows {
		row, e := s.point("link", []int{2, 7}, entity, key[0], library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		l := IAAudioLink{Asset: row[0].(int64), Part: row[1].(int64), Edition: row[2].(string), Available: row[3].(int64) != 0, CueOrdinal: iaAudioInt(row[4]), Start: row[5].(float64), Revision: row[8].(int64), Pending: row[9] != nil}
		if row[6] != nil {
			n := row[6].(float64)
			l.End = &n
		}
		if len(row[7].([]byte)) != 16 {
			return nil, ErrUnqualified
		}
		copy(l.Incarnation[:], row[7].([]byte))
		links = append(links, l)
	}
	return links, nil
}

func (r *ForegroundReader) IAExtraMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
	if r == nil {
		return IAExtraPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IAExtraPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaExtraMembership(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaExtraMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
	if in.Entity <= 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IAExtraPage{}, BrowseReadResult{}, ErrInput
	}
	switch in.ExtraKind {
	case "", "trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other":
	default:
		return IAExtraPage{}, BrowseReadResult{}, ErrUnqualified
	}
	s := &iaExtraSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		row, e := s.point("entity", []int{1, 2, 3, 14, 18, 24}, in.Entity, in.View.Library)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if e != nil {
			return nil, e
		}
		if row[7] != in.View.Kind || row[4] != int64(0) {
			return nil, ErrDenied
		}
		source = row
		return row, nil
	}
	steps := []readStep[IAExtraPage]{func(ctx context.Context, q browseQuery, f browseFence, d *IAExtraPage) error {
		if !admittedItem(source, f) {
			return ErrDenied
		}
		// Extra ownership does not establish inherited age policy.
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return ErrUnqualified
		}
		if in.After != nil && (in.After.fence != f || in.After.entity != in.Entity || in.After.kind != in.ExtraKind || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		var e error
		d.Entity, e = itemValue(source)
		return e
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IAExtraPage) error {
		var keys [][]any
		var e error
		after := int64(0)
		if in.After != nil {
			after = in.After.id
		}
		if in.ExtraKind == "" {
			if in.After != nil {
				return ErrInput
			}
			keys, e = q("foreground.ia.extra.self", in.Entity)
			d.Links, e = s.links(q, in.Entity, in.View.Library)
			if e != nil {
				return e
			}
		} else {
			keys, e = q("foreground.ia.extra.owner", in.Entity, in.ExtraKind, after, int64(MaxRaw))
		}
		if e != nil {
			return e
		}
		d.RawRows = len(keys)
		for _, key := range keys {
			id := key[0].(int64)
			after = id
			d.RawPositions++
			row, e := s.point("extra", []int{1, 2, 4, 5, 6, 7}, id)
			if e != nil {
				return e
			}
			if row[8] != in.View.Library {
				return ErrDenied
			}
			if in.ExtraKind != "" && (row[3] != in.Entity || row[4] != in.ExtraKind) {
				return ErrDenied
			}
			ent, e := s.point("entity", []int{1, 2, 3, 14, 18, 24}, id, in.View.Library)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrDenied
			}
			if e != nil {
				return e
			}
			if ent[4] != int64(0) {
				continue
			}
			if row[3] != nil {
				owner, e := s.point("entity", []int{1, 2, 3, 14, 18, 24}, row[3], in.View.Library)
				if errors.Is(e, sql.ErrNoRows) {
					return ErrDenied
				}
				if e != nil {
					return e
				}
				if owner[4] != int64(0) {
					return ErrDenied
				}
			}
			asset, e := q("foreground.ia.extra.asset", row[6], in.View.Library)
			if e != nil {
				return e
			}
			if len(asset) != 1 {
				return ErrDenied
			}
			m := IAExtraMember{Item: id, Kind: ent[7].(int64), Revision: ent[5].(int64), Owner: iaAudioInt(row[3]), ParentPath: row[2].(string), Type: row[4].(string), Title: row[5].(string), AssetToken: row[6].(string), RelativePath: row[7].(string), Artist: iaAudioInt(ent[8]), Album: iaAudioInt(ent[9]), Book: iaAudioInt(ent[10]), Disc: iaAudioInt(ent[11]), Track: iaAudioInt(ent[12]), Part: iaAudioInt(ent[13])}
			m.Links, e = s.links(q, id, in.View.Library)
			if e != nil {
				return e
			}
			found := false
			for _, l := range m.Links {
				if l.Asset == asset[0][0] {
					found = true
				}
			}
			if !found {
				return ErrUnqualified
			}
			d.Members = append(d.Members, m)
			if len(d.Members) == in.View.Limit {
				break
			}
		}
		d.Exhausted = in.ExtraKind == "" || d.RawPositions == len(keys) && len(keys) < MaxRaw
		if !d.Exhausted {
			d.Next = &IAExtraCursor{fence: f, entity: in.Entity, id: after, kind: in.ExtraKind, source: source}
		}
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}
