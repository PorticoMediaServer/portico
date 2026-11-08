package read

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// One LIVE operation owns every snapshot, retry, retained draft and final check.
// These counters/deadline are never reset at a phase or retry boundary.
type savedViewSession struct {
	r                              *SavedViewReader
	ctx                            context.Context
	request                        governor.Request
	output                         BrowseOutput
	started, deadline, clock       time.Time
	copied, copyLimit, nativeLimit int64
	cost                           BrowseReadResult
}

func newSavedViewSession(r *SavedViewReader, ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in BrowseInput) (*savedViewSession, error) {
	if r == nil || r.snapshots == nil || ctx == nil || out.live == nil || budget <= 0 || in.Now.IsZero() || in.Limit < 1 || in.Limit > MaxPage || len(in.Database) == 0 || len(in.Database) > 256 || len(in.TokenHash) == 0 || len(in.TokenHash) > 256 {
		return nil, ErrInput
	}
	native := r.snapshots.NativeRowReservation()
	goBytes := request.Need[governor.MemoryBytes] - native
	if goBytes <= 0 || out.Request.Need[governor.MemoryBytes]/2 < goBytes {
		return nil, governor.ErrSnapshotBudget
	}
	now := time.Now()
	deadline := earliest(now.Add(min(budget, time.Second)), request.Deadline, out.Request.Deadline)
	if d, ok := ctx.Deadline(); ok {
		deadline = earliest(deadline, d)
	}
	return &savedViewSession{r: r, ctx: ctx, request: request, output: out, started: now, deadline: deadline, clock: in.Now, copyLimit: goBytes, nativeLimit: native}, nil
}
func (s *savedViewSession) reserve(n int64) error {
	if n < 0 || n > s.copyLimit-s.copied {
		return governor.ErrSnapshotBudget
	}
	s.copied += n
	return nil
}
func (s *savedViewSession) charge(rows [][]any) error {
	for _, row := range rows {
		n := int64(64 + len(row)*32)
		for _, v := range row {
			switch x := v.(type) {
			case string:
				n += 2 * int64(len(x))
			case []byte:
				n += 3 * int64(len(x))
			}
		}
		if e := s.reserve(n); e != nil {
			return e
		}
	}
	return nil
}

type savedViewForecastSnapshots interface {
	ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
}

func (s *savedViewSession) window(final bool, maxQueries uint16, fn func(browseQuery) error) error {
	if !time.Now().Before(s.deadline) {
		return context.DeadlineExceeded
	}
	ctx := s.ctx
	if final {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithDeadline(ctx, s.deadline)
	defer cancel()
	request := s.request
	request.Deadline = s.deadline
	native := s.r.snapshots.NativeRowReservation()
	if maxQueries > 8 {
		// Whole-window variable bytes cannot exceed half the ORIGINAL Go copy
		// allowance (every returned byte is charged twice). This is a legal total
		// envelope, not columns*max; payloads still require exact native byte points.
		native = min(native, s.copyLimit/2)
		// Preserve the original request ceiling. The governor funds this variable
		// row bound, fixed native decode bookkeeping and the explicit ORIGINAL
		// Go-copy allowance inside it; native+copy alone omits that overhead.
	}
	s.nativeLimit = native
	callback := func(scope *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) {
			if final {
				s.cost.PublicationStatements++
			} else {
				s.cost.LookupStatements++
			}
			if e := s.output.live.checkContext(scope.Context()); e != nil {
				return nil, e
			}
			rows, e := scope.Query(readStatementID(name), args...)
			if e != nil {
				return nil, e
			}
			if e = s.charge(rows); e != nil {
				return nil, e
			}
			return rows, nil
		}
		return fn(q)
	}
	var result governor.SnapshotResult
	var e error
	if maxQueries > 8 {
		forecast, ok := s.r.snapshots.(savedViewForecastSnapshots)
		if !ok {
			return ErrUnqualified
		}
		result, e = forecast.ReadForecasted(ctx, request, time.Until(s.deadline), governor.SnapshotForecast{NativeRowBytes: native, MaxQueries: maxQueries, GoCopyBytes: s.copyLimit}, callback)
	} else {
		result, e = s.r.snapshots.Read(ctx, request, time.Until(s.deadline), callback)
	}
	if final {
		s.cost.Publication = result
		s.cost.PublicationWindows = append(s.cost.PublicationWindows, result)
	} else {
		s.cost.Lookup = result
		s.cost.LookupWindows = append(s.cost.LookupWindows, result)
	}
	if result.Refusal != nil {
		s.cost.Refusal = result.Refusal
		return governor.ErrBusy
	}
	return e
}
func (s *savedViewSession) input(in BrowseInput) BrowseInput {
	in.Now = s.clock.Add(time.Since(s.started))
	return in
}
func (s *savedViewSession) finish(f browseFence, prior, final error) error {
	if errors.Is(final, ErrDenied) || errors.Is(final, ErrUnqualified) || errors.Is(final, ErrStale) {
		return final
	}
	if e := errors.Join(prior, final, s.ctx.Err()); e != nil {
		return e
	}
	if !time.Now().Before(s.deadline) {
		return context.DeadlineExceeded
	}
	at := s.clock.Add(time.Since(s.started))
	expires, e1 := time.Parse(time.RFC3339Nano, f.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, f.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		return ErrDenied
	}
	return s.output.live.checkContext(s.ctx)
}
func (s *savedViewSession) result() BrowseReadResult {
	s.cost.Elapsed = time.Since(s.started)
	return s.cost
}

// The exact compiled owner/shared points reserve from their actual native byte
// lengths BEFORE payload query/allocation, then compare every returned byte.
func (s *savedViewSession) resource(q browseQuery, f browseFence, id string) (savedViewResource, error) {
	facts, e := q("rb.saved.view.candidate", id, f.token)
	if e != nil {
		return savedViewResource{}, e
	}
	if len(facts) != 1 || len(facts[0]) != 7 || facts[0][0] != int64(0) || facts[0][1] != nil {
		return savedViewResource{}, ErrDenied
	}
	return s.resourceFromFacts(q, f, id, facts[0])
}
func (s *savedViewSession) resourceFromFacts(q browseQuery, f browseFence, id string, facts []any) (savedViewResource, error) {
	role := savedViewRowRole(facts[2:6])
	if role == "" {
		return savedViewResource{}, ErrDenied
	}
	suffix := "shared"
	args := []any{"local", f.account, f.profile, id}
	if role == "owner" {
		suffix = "owner"
		args = []any{id, "local", f.account, f.profile}
	}
	sizes, e := q("rb.saved.view.resource."+suffix+".bytes", args...)
	if e != nil {
		return savedViewResource{}, e
	}
	if len(sizes) != 1 || len(sizes[0]) != 3 {
		return savedViewResource{}, ErrDenied
	}
	var bytes int64
	for _, v := range sizes[0] {
		n, ok := v.(int64)
		if !ok || n < 0 || n > s.nativeLimit-bytes {
			return savedViewResource{}, governor.ErrSnapshotBudget
		}
		bytes += n
	}
	if bytes > (s.copyLimit-s.copied-192)/2 {
		return savedViewResource{}, governor.ErrSnapshotBudget
	}
	rows, e := q("rb.saved.view.resource."+suffix, args...)
	if e != nil {
		return savedViewResource{}, e
	}
	if len(rows) != 1 || len(rows[0]) != 4 || rows[0][3] != facts[6] {
		return savedViewResource{}, ErrStale
	}
	row := rows[0]
	for i := 0; i < 3; i++ {
		v, ok := row[i].(string)
		if !ok || int64(len(v)) != sizes[0][i].(int64) {
			return savedViewResource{}, governor.ErrSnapshotBudget
		}
	}
	return savedViewResource{fence: f, id: id, name: row[0].(string), summary: row[1].(string), definition: row[2].(string), revision: row[3].(int64), role: role}, nil
}

type SavedViewExecutionInput struct {
	View     BrowseInput
	Resource string
	After    *SavedViewExecutionCursor
}
type SavedViewExecutionPage struct {
	Page             BrowsePage
	Next             *SavedViewExecutionCursor
	ResourceRevision int64
}

// Execute publishes only after the actual resource, principal birth, complete
// stored bytes and catalogue/policy fence agree in fresh first/final snapshots.
// Library/kind are resolved from the actual stored definition; caller hints
// cannot select the execution target.
// Caller query/sort/account/profile never supplies execution authority.
func (r *SavedViewReader) Execute(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in SavedViewExecutionInput) (SavedViewExecutionPage, BrowseReadResult, error) {
	if r == nil || len(in.Resource) > 256 || in.View.After != nil {
		return SavedViewExecutionPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (SavedViewExecutionPage, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r, ctx, request, budget, out, in.View)
		if e != nil {
			return SavedViewExecutionPage{}, BrowseReadResult{}, e
		}
		var page SavedViewExecutionPage
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			page = SavedViewExecutionPage{}
			effective := in.View
			var resource savedViewResource
			var f browseFence
			var filter rbScalarFilter
			e = s.window(false, 11, func(q browseQuery) error {
				var e error
				f, e = readSavedViewPrincipal(q, s.input(effective))
				if e != nil {
					return e
				}
				resource, e = s.resource(q, f, in.Resource)
				if e != nil {
					return e
				}
				if e = s.reserve((1 << 20) + 8*int64(len(resource.definition))); e != nil {
					return e
				}
				definition, e := savedViewStoredDefinition(resource)
				if e != nil {
					return e
				}
				if definition.Pivot != "movies" || len(definition.LibraryID) == 0 || len(definition.LibraryID) > 256 {
					return ErrUnqualified
				}
				mapped, e := q("rb.saved.view.library", definition.LibraryID)
				if e != nil {
					return e
				}
				if len(mapped) != 1 {
					return ErrDenied
				}
				effective.Library = mapped[0][0].(int64)
				effective.Kind = 1
				f, e = readBrowseFence(q, s.input(effective))
				if e != nil {
					return e
				}
				resource.fence = f
				effective, filter, e = savedViewExecutionPlan(resource, effective)
				if e != nil {
					return e
				}
				// Direction/article bits are query provenance, never principal facts.
				// Replace caller preferences with the actual validated stored definition.
				f.descending, f.keepArticles = effective.Descending, effective.KeepArticles
				resource.fence = f
				if in.After != nil {
					if in.After.resource != resource {
						return ErrStale
					}
					effective.After = in.After.browse
					if effective.After == nil || effective.After.fence != f {
						return ErrStale
					}
				}
				return nil
			})
			ready := e == nil
			if ready {
				e = s.window(false, 0, func(q browseQuery) error {
					var e error
					page.Page, e = readSavedViewScalarPage(ctx, q, effective, f, filter)
					if e == nil {
						page.ResourceRevision = resource.revision
						if page.Page.Next != nil {
							page.Next = &SavedViewExecutionCursor{resource, page.Page.Next}
						}
					}
					return e
				})
			}
			prior := e
			if ready {
				final := s.window(true, 0, func(q browseQuery) error {
					current, e := readBrowseFence(q, s.input(effective))
					if e != nil {
						return e
					}
					now, e := s.resource(q, current, in.Resource)
					if e != nil {
						return e
					}
					if current != f || now != resource {
						return ErrStale
					}
					return nil
				})
				e = s.finish(f, prior, final)
			}
			if e == nil {
				return page, s.result(), nil
			}
			if !errors.Is(e, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(e, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			e = errors.Join(e, governor.ErrBusy)
		}
		return SavedViewExecutionPage{}, s.result(), e
	})
}
