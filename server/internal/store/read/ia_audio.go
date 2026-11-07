package read

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IAAudioInput selects accepted membership facts, not an acceptance command.
// Kinds are canonical: artist=5, album=6, song=7, book=8, audiobook_file=9.
// Album and Book remain separate identities; no edition, CUE range or part is
// collapsed. A non-exhausted window must be continued before using a full cohort.
type IAAudioInput struct {
	View   BrowseInput
	Entity int64
	After  *IAAudioCursor
}
type IAAudioCursor struct {
	fence              browseFence
	entity             int64
	disc, position, id int64
	source             []any
}
type IAAudioMember struct {
	ID, Kind, Revision                     int64
	Artist, Album, Book, Disc, Track, Part *int64
	Links                                  []IAAudioLink
}
type IAAudioLink struct {
	Asset, Part, Revision int64
	Edition               string
	Available             bool
	CueOrdinal            *int64
	Start                 float64
	End                   *float64
	Incarnation           [16]byte
	Pending               bool
}
type IAAudioPage struct {
	Entity                BrowseItem
	Kind                  int64
	Members               []IAAudioMember
	Links                 []IAAudioLink
	Artists               []int64
	Next                  *IAAudioCursor
	Exhausted             bool
	RawRows, RawPositions int
}

// iaAudioSnapshots exposes only the current scope to this closed recipe. It
// preserves GV's snapshot lifecycle and native reservation; exact point pairs
// preflight actual octets in that same snapshot before any variable payload.
// The finite forecast raises only the call ceiling, never resource admission.
type iaAudioCalls struct {
	n           int
	publication bool
}
type iaAudioSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaAudioCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaAudioSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaAudioSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaAudioCalls{})
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
func (s *iaAudioSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	octets := readStatementID("foreground.ia.audio." + name + "_octets")
	if name == "link" {
		octets = iaLinkOctetsID
	}
	_ = octets // DELIBERATE MUTANT: bypass the compiled native point preflight.
	values, e := s.scope.Query(readStatementID("foreground.ia.audio."+name), args...)
	var row []any
	if e == nil {
		if len(values) != 1 {
			return nil, sql.ErrNoRows
		}
		row = values[0]
	}
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
func iaAudioInt(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
func iaAudioMember(row []any) IAAudioMember {
	return IAAudioMember{ID: row[0].(int64), Kind: row[1].(int64), Revision: row[4].(int64), Artist: iaAudioInt(row[6]), Album: iaAudioInt(row[7]), Book: iaAudioInt(row[8]), Disc: iaAudioInt(row[9]), Track: iaAudioInt(row[10]), Part: iaAudioInt(row[11])}
}
func (s *iaAudioSnapshots) links(q browseQuery, entity, library int64) ([]IAAudioLink, error) {
	rows, e := q("foreground.ia.audio.links", entity)
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

func iaAudioArtists(q browseQuery, song, library int64, f browseFence) ([]int64, error) {
	rows, e := q("foreground.ia.audio.song_artists", song)
	if e != nil {
		return nil, e
	}
	if len(rows) > 64 {
		return nil, ErrUnqualified
	}
	var ids []int64
	for _, row := range rows {
		if row[1] != library || row[2] != int64(5) {
			return nil, ErrDenied
		}
		if row[3] != int64(0) {
			continue
		}
		if f.maximumAge >= 0 || f.allowUnrated == 0 {
			return nil, ErrUnqualified
		}
		ids = append(ids, row[0].(int64))
	}
	return ids, nil
}

// IAAudioMembership is harness-only and requires ConsumeForeground custody.
// Values disclose no SQL, connection, writer or provider capability.
func (r *ForegroundReader) IAAudioMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAAudioInput) (IAAudioPage, BrowseReadResult, error) {
	if r == nil {
		return IAAudioPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IAAudioPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaAudioMembership(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaAudioMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IAAudioInput) (IAAudioPage, BrowseReadResult, error) {
	if r == nil || in.Entity <= 0 || in.View.Kind < 5 || in.View.Kind > 9 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IAAudioPage{}, BrowseReadResult{}, ErrInput
	}
	s := &iaAudioSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		row, e := s.point("entity", []int{1, 2, 3, 14, 18}, in.Entity, in.View.Library)
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
	steps := []readStep[IAAudioPage]{func(ctx context.Context, q browseQuery, f browseFence, d *IAAudioPage) error {
		if !admittedItem(source, f) {
			return ErrDenied
		}
		if (in.View.Kind == 5 || in.View.Kind == 6 || in.View.Kind == 8) && (f.maximumAge >= 0 || f.allowUnrated == 0) {
			return ErrUnqualified
		}
		if in.After != nil && (in.After.fence != f || in.After.entity != in.Entity || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		var e error
		d.Entity, e = itemValue(source)
		d.Kind = in.View.Kind
		return errors.Join(e, ctx.Err())
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IAAudioPage) error {
		disc, pos, id := int64(-1<<63), int64(-1<<63), int64(0)
		if in.After != nil {
			disc, pos, id = in.After.disc, in.After.position, in.After.id
		}
		if in.View.Kind == 7 || in.View.Kind == 9 {
			var e error
			d.Links, e = s.links(q, in.Entity, in.View.Library)
			if e == nil && in.View.Kind == 7 {
				d.Artists, e = iaAudioArtists(q, in.Entity, in.View.Library, f)
			}
			d.Exhausted = true
			return e
		}
		kind := int64(6)
		var rows [][]any
		var e error
		if in.View.Kind == 5 {
			rows, e = q("foreground.ia.audio.artist", in.Entity, id, int64(MaxRaw))
		} else {
			name := "album"
			kind = 7
			if in.View.Kind == 8 {
				name = "book"
				kind = 9
			}
			prefix := "foreground.ia.audio." + name
			if in.After == nil {
				rows, e = q(prefix+".first", in.Entity, int64(MaxRaw))
			} else {
				rows, e = q(prefix+".tail", in.Entity, disc, pos, id, int64(MaxRaw))
				if e == nil && len(rows) < MaxRaw {
					var more [][]any
					more, e = q(prefix+".next", in.Entity, disc, pos, int64(MaxRaw-len(rows)))
					rows = append(rows, more...)
				}
				if e == nil && len(rows) < MaxRaw {
					var more [][]any
					more, e = q(prefix+".disc", in.Entity, disc, int64(MaxRaw-len(rows)))
					rows = append(rows, more...)
				}
			}
		}
		if e != nil {
			return e
		}
		d.RawRows = len(rows)
		for _, row := range rows {
			// Advance on every RAW position, including retired, wrong-kind and hidden.
			id, disc, pos = row[0].(int64), row[12].(int64), row[13].(int64)
			d.RawPositions++
			if row[2] != in.View.Library {
				return ErrDenied
			}
			if row[1] != kind || row[3] != int64(0) {
				continue
			}
			if row[5] == nil {
				if f.allowUnrated == 0 {
					continue
				}
			} else if f.maximumAge >= 0 && row[5].(int64) > f.maximumAge {
				continue
			}
			m := iaAudioMember(row)
			if kind == 7 || kind == 9 {
				m.Links, e = s.links(q, m.ID, in.View.Library)
				if e != nil {
					return e
				}
			}
			d.Members = append(d.Members, m)
			if len(d.Members) == in.View.Limit {
				break
			}
		}
		d.Exhausted = d.RawPositions == len(rows) && len(rows) < MaxRaw
		if !d.Exhausted {
			d.Next = &IAAudioCursor{fence: f, entity: in.Entity, disc: disc, position: pos, id: id, source: source}
		}
		return ctx.Err()
	}}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, root)
	// Exact point pairs bypass browseQuery, but remain native registered queries.
	// Each attempt has two business windows followed by fresh publication.
	for _, calls := range s.extra {
		if calls.publication {
			result.PublicationStatements += calls.n
		} else {
			result.LookupStatements += calls.n
		}
	}
	return page, result, e
}
