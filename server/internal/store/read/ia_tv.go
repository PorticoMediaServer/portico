package read

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IATVInput selects accepted native source facts for TV preparation. It is not
// an acceptance command and it presents no provider, tuner or playback input.
// Show and Season return one bounded raw cohort; Episode returns one exact
// hierarchy; Asset returns the complete at-most-eight episode cohort that one
// asset belongs to.
type IATVInput struct {
	View  BrowseInput
	Item  int64
	Asset int64
	After *IATVCursor
}

// IATVCursor is a private values-only continuation. Show has two ordered
// streams (seasons, then directly attached episodes); Season has one.
type IATVCursor struct {
	fence     browseFence
	item      int64
	phase     int
	season    int64
	hasSeason bool
	episode   int64
	id        int64
	source    []any
}

// IATVEntity is one accepted native catalogue entity. Children carry the
// fixed-size hierarchy/order fields only; the exact text vector is hydrated
// only for the subject and its ancestors through the byte preflight.
type IATVEntity struct {
	ID, Kind, Parent, Show, Season                                            int64
	SeasonNumber, EpisodeNumber, AdmissionAge                                 *int64
	Revision                                                                  int64
	Retired                                                                   bool
	PublicID                                                                  [16]byte
	Title                                                                     string
	Incarnation                                                               [16]byte
	SourceRevision, HierarchyRevision, SelectionRevision, DescriptiveRevision int64
}
type IATVHierarchy struct {
	Item         IATVEntity
	Show, Season *IATVEntity
}
type IATVLink struct {
	Asset, Part, LinkRevision int64
	Available                 bool
	Start                     float64
	End                       *float64
	Edition                   string
	Incarnation               [16]byte
}
type IATVMember struct {
	Hierarchy IATVHierarchy
	Link      IATVLink
}
type IATVResult struct {
	Hierarchy             IATVHierarchy
	Seasons               []IATVEntity
	Children              []IATVEntity
	Links                 []IATVLink
	Members               []IATVMember
	AssetAvailable        bool
	Exhausted             bool
	Next                  *IATVCursor
	RawRows, RawPositions int
}

// iaTVSnapshots exposes only the current scope to this closed recipe. It keeps
// GV's snapshot lifecycle and native reservation; exact point pairs preflight
// actual octets in the same snapshot before any variable payload. The finite
// forecast raises only the call ceiling, never resource admission.
type iaTVCalls struct {
	n           int
	publication bool
}
type iaTVSnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	extra             []iaTVCalls
	rootCalls         int
	copied, copyLimit int64
}

func (s *iaTVSnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }
func (s *iaTVSnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.extra = append(s.extra, iaTVCalls{})
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
func (s *iaTVSnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	row, e := s.scope.QueryExactBytes(readStatementID("foreground.ia.tv."+name+"_octets"), readStatementID("foreground.ia.tv."+name), columns, args...)
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

func iaTVNumber(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
func iaTVValue(v any) int64 {
	if v == nil {
		return 0
	}
	return v.(int64)
}

// The entity point column order is fixed by iaTVEntitySQL.
const (
	iaTVColPublicID    = 1
	iaTVColTitle       = 2
	iaTVColIncarnation = 12
)

func iaTVEntity(row []any) (IATVEntity, error) {
	public, ok := row[1].([]byte)
	if !ok || len(public) != 16 {
		return IATVEntity{}, ErrNotReady
	}
	incarnation, ok := row[12].([]byte)
	if !ok || len(incarnation) != 16 {
		return IATVEntity{}, ErrNotReady
	}
	v := IATVEntity{
		ID: row[0].(int64), Title: row[2].(string), Kind: row[3].(int64),
		Parent: iaTVValue(row[4]), Show: iaTVValue(row[5]), Season: iaTVValue(row[6]),
		SeasonNumber: iaTVNumber(row[7]), EpisodeNumber: iaTVNumber(row[8]),
		Retired: row[9].(int64) != 0, AdmissionAge: iaTVNumber(row[10]), Revision: row[11].(int64),
		SourceRevision: iaTVValue(row[13]), HierarchyRevision: iaTVValue(row[14]),
		SelectionRevision: iaTVValue(row[15]), DescriptiveRevision: iaTVValue(row[16]),
	}
	copy(v.PublicID[:], public)
	copy(v.Incarnation[:], incarnation)
	// A shown subject without all four TV revisions is not current accepted
	// evidence, so it cannot authorize a preparation vector.
	if v.SourceRevision <= 0 || v.HierarchyRevision <= 0 || v.SelectionRevision <= 0 || v.DescriptiveRevision <= 0 {
		return IATVEntity{}, ErrNotReady
	}
	return v, nil
}

// IA TV subjects are containers (show, season) or one episode. A container is
// only readable under a fully open profile, matching the merged catalogue
// policy; an episode carries its own admitted age.
func iaTVSubject(item IATVEntity, f browseFence) error {
	if item.Retired {
		return ErrDenied
	}
	if item.Kind != 4 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return ErrUnqualified
	}
	return nil
}
func iaTVEpisodeAge(age *int64, f browseFence) bool {
	if age == nil {
		return f.allowUnrated != 0
	}
	return f.maximumAge < 0 || *age <= f.maximumAge
}

func (s *iaTVSnapshots) show(id, library int64) (IATVEntity, error) {
	row, e := s.point("show", []int{iaTVColPublicID, iaTVColTitle, iaTVColIncarnation}, id, library)
	if errors.Is(e, sql.ErrNoRows) {
		return IATVEntity{}, ErrDenied
	}
	if e != nil {
		return IATVEntity{}, e
	}
	v, e := iaTVEntity(row)
	if e != nil {
		return IATVEntity{}, e
	}
	if v.Kind != 2 || v.Parent != 0 || v.Show != 0 || v.Season != 0 {
		return IATVEntity{}, ErrNotReady
	}
	if v.Retired {
		return IATVEntity{}, ErrDenied
	}
	return v, nil
}
func (s *iaTVSnapshots) season(id, library int64) (IATVEntity, error) {
	row, e := s.point("season", []int{iaTVColPublicID, iaTVColTitle, iaTVColIncarnation}, id, library)
	if errors.Is(e, sql.ErrNoRows) {
		return IATVEntity{}, ErrDenied
	}
	if e != nil {
		return IATVEntity{}, e
	}
	v, e := iaTVEntity(row)
	if e != nil {
		return IATVEntity{}, e
	}
	if v.Kind != 3 || v.Show == 0 || v.Parent != v.Show || v.Season != 0 || v.SeasonNumber == nil {
		return IATVEntity{}, ErrNotReady
	}
	if v.Retired {
		return IATVEntity{}, ErrDenied
	}
	return v, nil
}
func (s *iaTVSnapshots) episode(id, library int64) (IATVEntity, error) {
	row, e := s.point("episode", []int{iaTVColPublicID, iaTVColTitle, iaTVColIncarnation}, id, library)
	if errors.Is(e, sql.ErrNoRows) {
		return IATVEntity{}, ErrDenied
	}
	if e != nil {
		return IATVEntity{}, e
	}
	v, e := iaTVEntity(row)
	if e != nil {
		return IATVEntity{}, e
	}
	if v.Kind != 4 || v.Show == 0 || v.EpisodeNumber == nil {
		return IATVEntity{}, ErrNotReady
	}
	if v.Retired {
		return IATVEntity{}, ErrDenied
	}
	return v, nil
}

// iaTVSeasonWindow reads one season stream. season_number is never null for a
// kind-3 accepted row, so the equality-tail and greater-next seeks are exact.
func iaTVSeasonWindow(q browseQuery, parent int64, after *IATVCursor, limit int) ([][]any, error) {
	if after == nil {
		return q("foreground.ia.tv.show_seasons_first", parent, int64(limit))
	}
	if !after.hasSeason {
		return nil, ErrStale
	}
	raw, e := q("foreground.ia.tv.show_seasons_tail", parent, after.season, after.id, int64(limit))
	if e != nil || len(raw) >= limit {
		return raw, e
	}
	next, e := q("foreground.ia.tv.show_seasons_next", parent, after.season, int64(limit-len(raw)))
	return append(raw, next...), e
}

// iaTVEpisodeWindow reads one episode stream with the merged equality-tail and
// greater-next keysets. episode_number is NOT NULL for an accepted kind-4 row.
func iaTVEpisodeWindow(q browseQuery, prefix string, parent int64, after *IATVCursor, limit int) ([][]any, error) {
	if after == nil {
		return q(prefix+"first", parent, int64(limit))
	}
	raw, e := q(prefix+"tail", parent, after.episode, after.id, int64(limit))
	if e != nil || len(raw) >= limit {
		return raw, e
	}
	next, e := q(prefix+"next", parent, after.episode, int64(limit-len(raw)))
	return append(raw, next...), e
}

func iaTVChild(row []any) IATVEntity {
	return IATVEntity{
		ID: row[0].(int64), Kind: row[2].(int64), Retired: row[3].(int64) != 0,
		AdmissionAge: iaTVNumber(row[4]), Revision: row[5].(int64),
		SeasonNumber: iaTVNumber(row[6]), EpisodeNumber: iaTVNumber(row[7]),
		Season: iaTVValue(row[8]), Parent: iaTVValue(row[9]),
	}
}
func iaTVChildAdmits(row []any, library, kind int64, f browseFence) (IATVEntity, bool, error) {
	if row[1].(int64) != library {
		return IATVEntity{}, false, ErrDenied
	}
	if row[2].(int64) != kind {
		// A wrong kind still consumes and advances the raw continuation.
		return IATVEntity{}, false, nil
	}
	v := iaTVChild(row)
	if v.Kind == 4 && !iaTVEpisodeAge(v.AdmissionAge, f) {
		return IATVEntity{}, false, nil
	}
	return v, !v.Retired, nil
}

func (r *ForegroundReader) IATVShow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IATVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaTVShow(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaTVShow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil || in.View.Kind != 2 || in.Item <= 0 || in.Asset != 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaTVSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var subject IATVEntity
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		v, e := s.show(in.Item, in.View.Library)
		if e != nil {
			return nil, e
		}
		subject = v
		source = []any{v.PublicID, v.Incarnation, v.HierarchyRevision, v.SourceRevision, v.SelectionRevision, v.DescriptiveRevision}
		return source, nil
	}
	steps := []readStep[IATVResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		if len(source) == 0 {
			return ErrUnqualified
		}
		if e := iaTVSubject(subject, f); e != nil {
			return e
		}
		if in.After != nil && (in.After.fence != f || in.After.item != in.Item || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		d.Hierarchy = IATVHierarchy{Item: subject}
		return ctx.Err()
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		if in.After != nil && (in.After.fence != f || in.After.item != in.Item || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		visible := len(d.Seasons) + len(d.Children)
		cursor := in.After
		if cursor == nil || cursor.phase == 0 {
			raw, e := iaTVSeasonWindow(q, in.Item, cursor, MaxRaw)
			if e != nil {
				return e
			}
			d.RawRows += len(raw)
			for _, row := range raw {
				if e := ctx.Err(); e != nil {
					return e
				}
				if row[1].(int64) != in.View.Library || row[2].(int64) != 3 {
					return ErrDenied
				}
				if row[6] == nil {
					return ErrNotReady
				}
				d.RawPositions++
				d.Next = &IATVCursor{fence: f, item: in.Item, phase: 0, season: row[6].(int64), hasSeason: true, id: row[0].(int64), source: source}
				if row[3].(int64) != 0 {
					continue
				}
				d.Seasons = append(d.Seasons, iaTVChild(row))
				visible++
				if visible == in.View.Limit {
					return ctx.Err()
				}
			}
			if len(raw) == MaxRaw {
				return ctx.Err()
			}
			// Seasons are exhausted. The same call continues with the direct
			// episode stream, whose fresh first page is the exact boundary.
			cursor = nil
			d.Next = nil
		}
		raw, e := iaTVEpisodeWindow(q, "foreground.ia.tv.show_episodes_", in.Item, cursor, MaxRaw)
		if e != nil {
			return e
		}
		d.RawRows += len(raw)
		for _, row := range raw {
			if e := ctx.Err(); e != nil {
				return e
			}
			v, ok, e := iaTVChildAdmits(row, in.View.Library, 4, f)
			if e != nil {
				return e
			}
			d.RawPositions++
			d.Next = &IATVCursor{fence: f, item: in.Item, phase: 1, episode: iaTVValue(row[7]), id: row[0].(int64), source: source}
			if !ok {
				continue
			}
			d.Children = append(d.Children, v)
			visible++
			if visible == in.View.Limit {
				return ctx.Err()
			}
		}
		if len(raw) == MaxRaw {
			return ctx.Err()
		}
		d.Next = nil
		d.Exhausted = true
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

func (r *ForegroundReader) IATVSeason(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IATVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaTVSeason(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaTVSeason(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil || in.View.Kind != 3 || in.Item <= 0 || in.Asset != 0 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaTVSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var season, show IATVEntity
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		v, e := s.season(in.Item, in.View.Library)
		if e != nil {
			return nil, e
		}
		parent, e := s.show(v.Show, in.View.Library)
		if e != nil {
			return nil, e
		}
		season, show = v, parent
		source = []any{v.PublicID, v.Incarnation, v.HierarchyRevision, v.SourceRevision, v.SelectionRevision, v.DescriptiveRevision, parent.PublicID, parent.Incarnation, parent.HierarchyRevision}
		return source, nil
	}
	steps := []readStep[IATVResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		if len(source) == 0 {
			return ErrUnqualified
		}
		if e := iaTVSubject(season, f); e != nil {
			return e
		}
		if e := iaTVSubject(show, f); e != nil {
			return e
		}
		if season.Show != show.ID || season.Parent != show.ID {
			return ErrNotReady
		}
		if in.After != nil && (in.After.fence != f || in.After.item != in.Item || !equalSource(in.After.source, source)) {
			return ErrStale
		}
		d.Hierarchy = IATVHierarchy{Item: season, Show: &show}
		return ctx.Err()
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		raw, e := iaTVEpisodeWindow(q, "foreground.ia.tv.season_episodes_", in.Item, in.After, MaxRaw)
		if e != nil {
			return e
		}
		d.RawRows = len(raw)
		for _, row := range raw {
			if e := ctx.Err(); e != nil {
				return e
			}
			v, ok, e := iaTVChildAdmits(row, in.View.Library, 4, f)
			if e != nil {
				return e
			}
			d.RawPositions++
			d.Next = &IATVCursor{fence: f, item: in.Item, phase: 0, episode: iaTVValue(row[7]), id: row[0].(int64), source: source}
			if !ok {
				continue
			}
			d.Children = append(d.Children, v)
			if len(d.Children) == in.View.Limit {
				return ctx.Err()
			}
		}
		if len(raw) == MaxRaw {
			return ctx.Err()
		}
		d.Next = nil
		d.Exhausted = true
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

func (r *ForegroundReader) IATVEpisode(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IATVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaTVEpisode(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaTVEpisode(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil || in.View.Kind != 4 || in.Item <= 0 || in.Asset != 0 || in.After != nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaTVSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var item, show IATVEntity
	var season *IATVEntity
	var source []any
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		v, e := s.episode(in.Item, in.View.Library)
		if e != nil {
			return nil, e
		}
		parent, e := s.show(v.Show, in.View.Library)
		if e != nil {
			return nil, e
		}
		item, show, season = v, parent, nil
		source = []any{v.PublicID, v.Incarnation, v.HierarchyRevision, v.SourceRevision, v.SelectionRevision, v.DescriptiveRevision, parent.PublicID, parent.Incarnation, parent.HierarchyRevision}
		if v.Season != 0 {
			seasonRow, e := s.season(v.Season, in.View.Library)
			if e != nil {
				return nil, e
			}
			season = &seasonRow
			source = append(source, seasonRow.PublicID, seasonRow.Incarnation, seasonRow.HierarchyRevision)
		}
		return source, nil
	}
	steps := []readStep[IATVResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		if len(source) == 0 {
			return ErrUnqualified
		}
		if e := iaTVSubject(show, f); e != nil {
			return e
		}
		if item.Season != 0 {
			if season == nil || season.Show != show.ID || season.Parent != show.ID || season.Season != 0 || season.SeasonNumber == nil || item.Parent != season.ID {
				return ErrNotReady
			}
		} else if item.Parent != show.ID {
			return ErrNotReady
		}
		if !iaTVEpisodeAge(item.AdmissionAge, f) {
			return ErrDenied
		}
		d.Hierarchy = IATVHierarchy{Item: item, Show: &show, Season: season}
		return ctx.Err()
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		rows, e := q("foreground.ia.tv.links", in.Item, in.View.Library)
		if e != nil {
			return e
		}
		d.RawRows = len(rows)
		if len(rows) > 16 {
			return ErrUnqualified
		}
		for _, row := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			link, e := s.link(in.Item, row[0].(int64), in.View.Library)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrDenied
			}
			if e != nil {
				return e
			}
			d.RawPositions++
			d.Links = append(d.Links, link)
		}
		d.Exhausted = true
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

func (r *ForegroundReader) IATVAssetMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (IATVResult, BrowseReadResult, error) {
		in.View.Now = at
		return r.iaTVAssetMembership(ctx, request, budget, output, in)
	})
}
func (r *ForegroundReader) iaTVAssetMembership(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in IATVInput) (IATVResult, BrowseReadResult, error) {
	if r == nil || in.View.Kind != 4 || in.Asset <= 0 || in.Item != 0 || in.After != nil || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return IATVResult{}, BrowseReadResult{}, ErrInput
	}
	s := &iaTVSnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	var asset IATVAsset
	root := func(q browseQuery) ([]any, error) {
		s.rootCalls++
		s.extra[len(s.extra)-1].publication = s.rootCalls%2 == 0
		v, e := s.asset(in.Asset, in.View.Library)
		if e != nil {
			return nil, e
		}
		asset = v
		return []any{v.Incarnation, v.Revision, v.Available}, nil
	}
	steps := []readStep[IATVResult]{func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		d.AssetAvailable = asset.Available != 0
		return ctx.Err()
	}, func(ctx context.Context, q browseQuery, f browseFence, d *IATVResult) error {
		rows, e := q("foreground.ia.tv.members", in.Asset, in.View.Library)
		if e != nil {
			return e
		}
		d.RawRows = len(rows)
		// The whole raw cohort is read before any visibility predicate. A ninth
		// row refuses the complete unit even if later rows would be denied.
		if len(rows) > 8 {
			return ErrUnqualified
		}
		for _, row := range rows {
			if e := ctx.Err(); e != nil {
				return e
			}
			h, e := s.hierarchy(row[0].(int64), in.View.Library, f)
			if e != nil {
				return e
			}
			if h.Item.Kind != 4 {
				return ErrDenied
			}
			link, e := s.link(h.Item.ID, in.Asset, in.View.Library)
			if errors.Is(e, sql.ErrNoRows) {
				return ErrDenied
			}
			if e != nil {
				return e
			}
			d.RawPositions++
			d.Members = append(d.Members, IATVMember{Hierarchy: h, Link: link})
		}
		d.Exhausted = true
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

// hierarchy validates one episode's complete accepted ancestors. A duplicate
// or cross-linked parent is ErrNotReady; a missing row in this library is
// ErrDenied. It is used only by the complete asset membership unit.
func (s *iaTVSnapshots) hierarchy(id, library int64, f browseFence) (IATVHierarchy, error) {
	item, e := s.episode(id, library)
	if e != nil {
		return IATVHierarchy{}, e
	}
	show, e := s.show(item.Show, library)
	if e != nil {
		return IATVHierarchy{}, e
	}
	h := IATVHierarchy{Item: item, Show: &show}
	if item.Season != 0 {
		season, e := s.season(item.Season, library)
		if e != nil {
			return IATVHierarchy{}, e
		}
		if season.Show != show.ID || season.Parent != show.ID || item.Parent != season.ID {
			return IATVHierarchy{}, ErrNotReady
		}
		h.Season = &season
	} else if item.Parent != show.ID {
		return IATVHierarchy{}, ErrNotReady
	}
	if !iaTVEpisodeAge(item.AdmissionAge, f) {
		return IATVHierarchy{}, ErrDenied
	}
	return h, nil
}

func (s *iaTVSnapshots) link(entity, asset, library int64) (IATVLink, error) {
	row, e := s.point("link", []int{6, 7}, entity, asset, library)
	if e != nil {
		return IATVLink{}, e
	}
	if len(row[7].([]byte)) != 16 {
		return IATVLink{}, ErrNotReady
	}
	l := IATVLink{Asset: row[1].(int64), Part: row[2].(int64), Available: row[3].(int64) != 0, Start: row[4].(float64), Edition: row[6].(string), LinkRevision: row[8].(int64)}
	if row[5] != nil {
		n := row[5].(float64)
		l.End = &n
	}
	copy(l.Incarnation[:], row[7].([]byte))
	return l, nil
}

type IATVAsset struct {
	Available   int64
	Revision    int64
	Incarnation [16]byte
}

func (s *iaTVSnapshots) asset(id, library int64) (IATVAsset, error) {
	row, e := s.point("asset", []int{4}, id, library)
	if errors.Is(e, sql.ErrNoRows) {
		return IATVAsset{}, ErrDenied
	}
	if e != nil {
		return IATVAsset{}, e
	}
	if len(row[4].([]byte)) != 16 {
		return IATVAsset{}, ErrNotReady
	}
	v := IATVAsset{Available: row[1].(int64), Revision: row[2].(int64)}
	copy(v.Incarnation[:], row[4].([]byte))
	return v, nil
}
