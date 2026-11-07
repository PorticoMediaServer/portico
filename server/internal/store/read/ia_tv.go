package read

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IATVInput selects accepted native source facts, not provider/preparation data.
// Children uses direct-parent ID order for an inventory walk, never a public
// display order or a cached episode count. Membership selects one complete
// asset cohort (at most eight episodes), including unavailable links.
type IATVInput struct {
	View        BrowseInput
	Item, Asset int64
	After       *IATVCursor
}
type IATVCursor struct {
	fence      browseFence
	item, last int64
	source     []any
}
type IATVEntity struct {
	BrowseItem
	Kind, Parent, Show, Season                                                int64
	SeasonNumber, EpisodeNumber                                               *int64
	Numbering                                                                 string
	Incarnation                                                               [16]byte
	SourceRevision, HierarchyRevision, SelectionRevision, DescriptiveRevision int64
}
type IATVHierarchy struct {
	Item         IATVEntity
	Show, Season *IATVEntity
}
type IATVMember struct {
	Hierarchy                 IATVHierarchy
	Asset, Part, LinkRevision int64
	Available                 bool
	Start                     float64
	End                       *float64
	Edition                   string
	LinkIncarnation           [16]byte
}
type IATVResult struct {
	Hierarchy             IATVHierarchy
	Children              []IATVEntity
	Members               []IATVMember
	AssetAvailable        bool
	Exhausted             bool
	Next                  *IATVCursor
	RawRows, RawPositions int
}

func (r *ForegroundReader) IATVPoint(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	return r.iaTVLive(ctx, request, budget, output, in, "point")
}
func (r *ForegroundReader) IATVChildren(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	return r.iaTVLive(ctx, request, budget, output, in, "children")
}
func (r *ForegroundReader) IATVEpisodeMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	return r.iaTVLive(ctx, request, budget, output, in, "members")
}
func (r *ForegroundReader) iaTVLive(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput, mode string) (IATVResult, BrowseReadResult, error) {
	if r == nil {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IATVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaTV(ctx, request, budget, output, in, mode)
	})
}

// iaTVScope is private to one invocation of publishRead. It exposes only our
// compiled exact-byte points inside the currently active shared readStep. The
// shared fence/publication protocol still owns every snapshot and final output.
// Hierarchy/link steps use at most three point pairs. The child step has a
// closed forecast of one raw keyset plus at most 64 exact point pairs; numeric
// denials consume raw positions before any text hydration. No scope escapes.
type iaTVScope struct {
	browseSnapshots
	current         *governor.ReadScope
	calls           int
	copied, ceiling int64
	counts          []iaTVSnapshotCalls
	nextQueries     uint16
}

type iaTVSnapshotCalls struct {
	at    time.Time
	calls int
}

func (s *iaTVScope) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	before := s.calls
	queries := s.nextQueries
	s.nextQueries = 0
	callback := func(scope *governor.ReadScope) error {
		s.current = scope
		defer func() { s.current = nil }()
		return fn(scope)
	}
	var result governor.SnapshotResult
	var e error
	if queries > 0 {
		pool, ok := s.browseSnapshots.(interface {
			ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
		})
		if !ok {
			return result, ErrUnqualified
		}
		result, e = pool.ReadForecasted(ctx, r, b, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: queries}, callback)
	} else {
		result, e = s.browseSnapshots.Read(ctx, r, b, callback)
	}
	s.counts = append(s.counts, iaTVSnapshotCalls{result.Started, s.calls - before})
	return result, e
}
func (s *iaTVScope) exact(name string, cols []int, args ...any) ([]any, error) {
	if s.current == nil {
		return nil, governor.ErrSnapshotClosed
	}
	s.calls += 2
	octets := readStatementID("foreground.ia.tv." + name + "_octets")
	if name == "link" {
		octets = iaLinkOctetsID
	}
	row, e := s.current.QueryExactBytes(octets, readStatementID("foreground.ia.tv."+name), cols, args...)
	if errors.Is(e, sql.ErrNoRows) {
		s.calls-- // The length point found no row; no payload statement ran.
		return nil, ErrDenied
	}
	if e != nil {
		return nil, e
	}
	s.copied += 64 + int64(len(row))*32
	for _, v := range row {
		switch v := v.(type) {
		case string:
			s.copied += int64(len(v)) * 2
		case []byte:
			s.copied += int64(len(v)) * 3
		}
	}
	if s.copied > s.ceiling {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}
func iaTVNullable(v any) int64 {
	if v == nil {
		return 0
	}
	return v.(int64)
}
func iaTVNumber(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
func (s *iaTVScope) entity(id, library int64, f browseFence) (IATVEntity, error) {
	row, e := s.exact("entity", []int{1, 2, 9, 13}, id, library)
	if e != nil {
		return IATVEntity{}, e
	}
	if row[10].(int64) != 0 || row[12] == nil && f.allowUnrated == 0 || row[12] != nil && f.maximumAge >= 0 && row[12].(int64) > f.maximumAge {
		return IATVEntity{}, ErrDenied
	}
	kind := row[3].(int64)
	if kind < 2 || kind > 4 {
		return IATVEntity{}, ErrDenied
	}
	if kind != 4 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return IATVEntity{}, ErrUnqualified
	}
	if len(row[1].([]byte)) != 16 || row[13] == nil || len(row[13].([]byte)) != 16 {
		return IATVEntity{}, ErrNotReady
	}
	v := IATVEntity{BrowseItem: BrowseItem{ID: id, Title: row[2].(string), Revision: row[11].(int64)}, Kind: kind, Parent: iaTVNullable(row[4]), Show: iaTVNullable(row[5]), Season: iaTVNullable(row[6]), SeasonNumber: iaTVNumber(row[7]), EpisodeNumber: iaTVNumber(row[8]), SourceRevision: iaTVNullable(row[14]), HierarchyRevision: iaTVNullable(row[15]), SelectionRevision: iaTVNullable(row[16]), DescriptiveRevision: iaTVNullable(row[17])}
	if v.SourceRevision <= 0 || v.HierarchyRevision <= 0 || v.SelectionRevision <= 0 || v.DescriptiveRevision <= 0 {
		return IATVEntity{}, ErrNotReady
	}
	copy(v.PublicID[:], row[1].([]byte))
	copy(v.Incarnation[:], row[13].([]byte))
	if row[9] != nil {
		v.Numbering = row[9].(string)
	}
	return v, nil
}
func (s *iaTVScope) hierarchy(id, library int64, f browseFence) (IATVHierarchy, error) {
	item, e := s.entity(id, library, f)
	if e != nil {
		return IATVHierarchy{}, e
	}
	h := IATVHierarchy{Item: item}
	if item.Kind == 2 {
		if item.Parent != 0 || item.Show != 0 || item.Season != 0 {
			return IATVHierarchy{}, ErrNotReady
		}
		return h, nil
	}
	show, e := s.entity(item.Show, library, f)
	if e != nil {
		return IATVHierarchy{}, e
	}
	if show.Kind != 2 || show.Parent != 0 || show.Show != 0 || show.Season != 0 {
		return IATVHierarchy{}, ErrNotReady
	}
	h.Show = &show
	if item.Kind == 3 {
		if item.Parent != show.ID || item.Season != 0 || item.SeasonNumber == nil {
			return IATVHierarchy{}, ErrNotReady
		}
		return h, nil
	}
	if item.EpisodeNumber == nil {
		return IATVHierarchy{}, ErrNotReady
	}
	if item.Season != 0 {
		season, e := s.entity(item.Season, library, f)
		if e != nil {
			return IATVHierarchy{}, e
		}
		if season.Kind != 3 || season.Show != show.ID || season.Parent != show.ID || item.Parent != season.ID || season.SeasonNumber == nil {
			return IATVHierarchy{}, ErrNotReady
		}
		h.Season = &season
	} else if item.Parent != show.ID {
		return IATVHierarchy{}, ErrNotReady
	}
	return h, nil
}

func (r *ForegroundReader) iaTV(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput, mode string) (IATVResult, BrowseReadResult, error) {
	if r == nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles || (mode != "members" && (in.Item <= 0 || in.Asset != 0)) || (mode == "members" && (in.Asset <= 0 || in.Item != 0 || in.View.Kind != 4)) || (mode != "children" && in.After != nil) || in.View.Kind < 2 || in.View.Kind > 4 {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaTVScope{browseSnapshots: r.snapshots, ceiling: output.Request.Need[governor.MemoryBytes] / 4}
	var keys [][]any
	var capturedSource []any
	source := func(q browseQuery) ([]any, error) {
		name, id := "source", in.Item
		if mode == "members" {
			name, id = "asset_source", in.Asset
		}

		columns := []int{12, 13}
		if mode == "members" {
			columns = []int{4}
		}
		row, e := s.exact(name, columns, id, in.View.Library)
		if e != nil {
			return nil, e
		}
		if in.After != nil && !equalSource(in.After.source, row) {
			return nil, ErrStale
		}
		capturedSource = row
		return row, nil
	}
	steps := []readStep[IATVResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		keys = nil
		if mode == "members" {
			d.AssetAvailable = capturedSource[1].(int64) != 0
		}
		if in.After != nil && (in.After.fence != f || in.After.item != in.Item) {
			return ErrStale
		}
		return ctx.Err()
	}}
	if mode != "members" {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
			h, e := s.hierarchy(in.Item, in.View.Library, f)
			if e != nil {
				return e
			}
			if h.Item.Kind != in.View.Kind {
				return ErrDenied
			}
			d.Hierarchy = h
			d.Exhausted = true
			if mode == "children" {
				s.nextQueries = 1 + 2*MaxRaw
			}
			return ctx.Err()
		})
	}
	if mode != "point" {
		steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
			var e error
			if mode == "members" {
				keys, e = q("foreground.ia.tv.members", in.Asset)
			} else {
				if d.Hierarchy.Item.Kind == 4 {
					return ErrInput
				}
				last := int64(0)
				if in.After != nil {
					last = in.After.last
				}
				keys, e = q("foreground.ia.tv.children", in.Item, last)
			}
			if e != nil {
				return e
			}
			d.RawRows = len(keys)
			d.Exhausted = len(keys) < MaxRaw
			if mode == "members" {
				if len(keys) > 8 {
					return ErrUnqualified
				}
				d.Exhausted = true
				return ctx.Err()
			}
			for _, raw := range keys {
				if e := ctx.Err(); e != nil {
					return e
				}
				id := raw[0].(int64)
				d.RawPositions++
				d.Next = &IATVCursor{fence: f, item: in.Item, last: id, source: capturedSource}
				if raw[3].(int64) != in.View.Library {
					return ErrDenied
				}
				if raw[1].(int64) != 0 || raw[2] == nil && f.allowUnrated == 0 || raw[2] != nil && f.maximumAge >= 0 && raw[2].(int64) > f.maximumAge {
					continue
				}
				v, e := s.entity(id, in.View.Library, f)
				if e != nil {
					return e
				}
				show := in.Item
				if d.Hierarchy.Item.Kind == 3 {
					show = d.Hierarchy.Item.Show
				}
				if v.Parent != in.Item || (v.Kind != 3 && v.Kind != 4) || v.Show != show || d.Hierarchy.Item.Kind == 3 && (v.Kind != 4 || v.Season != in.Item) {
					return ErrNotReady
				}
				d.Children = append(d.Children, v)
				if len(d.Children) == in.View.Limit {
					break
				}
			}
			d.Exhausted = d.RawPositions == len(keys) && len(keys) < MaxRaw
			if d.Exhausted {
				d.Next = nil
			}
			return ctx.Err()
		})
		if mode == "members" {
			for group := 0; group < 8; group++ {
				group := group
				steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
					if group >= len(keys) {
						return nil
					}
					id := keys[group][0].(int64)
					h, e := s.hierarchy(id, in.View.Library, f)
					if e != nil {
						return e
					}
					if h.Item.Kind != 4 {
						return ErrDenied
					}
					d.Members = append(d.Members, IATVMember{Hierarchy: h, Asset: in.Asset})
					d.RawPositions++
					return ctx.Err()
				})
			}
		}
		if mode == "members" {
			for group := 0; group < 3; group++ {
				group := group
				steps = append(steps, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
					for pos := group * 3; pos < min(group*3+3, len(d.Members)); pos++ {
						m := &d.Members[pos]
						v, e := s.exact("link", []int{6, 7}, m.Hierarchy.Item.ID, in.Asset, in.View.Library)
						if e != nil {
							return e
						}
						m.Part = v[2].(int64)
						m.Available = v[3].(int64) != 0
						m.Start = v[4].(float64)
						if v[5] != nil {
							end := v[5].(float64)
							m.End = &end
						}
						m.Edition = v[6].(string)
						if len(v[7].([]byte)) != 16 {
							return ErrNotReady
						}
						copy(m.LinkIncarnation[:], v[7].([]byte))
						m.LinkRevision = v[8].(int64)
					}
					return ctx.Err()
				})
			}
		}

	}
	page, result, e := publishRead(ctx, s, request, budget, output, in.View, steps, source)
	for _, count := range s.counts {
		publication := false
		for _, window := range result.PublicationWindows {
			if window.Started == count.at {
				publication = true
				break
			}
		}
		if publication {
			result.PublicationStatements += count.calls
		} else {
			result.LookupStatements += count.calls
		}
	}
	return page, result, e
}
