package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

// ForegroundInput contains request metadata only. Prepare allocates no page or
// output grant. Consume uses the protected context, joins encoding/IO/users and
// drops all page references before returning; the shared driver owns retirement.
type ForegroundInput[I, P any] struct {
	Request       governor.Request
	Budget        time.Duration
	Foreground    governor.ForegroundRequest
	OutputRequest governor.Request
	Input         I
	Consume       func(context.Context, P) error
}

type BrowseInput = ForegroundInput[read.BrowseInput, read.BrowsePage]
type DetailInput = ForegroundInput[read.TitleInput, read.TitleDetail]
type PlaybackInput = ForegroundInput[read.TitleInput, read.PlaybackFile]
type ContinueInput = ForegroundInput[read.ContinueInput, read.ContinuePage]

// Summaries contain only bounded scalars, never a page, path, token or cursor.
type BrowseSummary struct {
	Items, RawRows, RawPositions int
	Exhausted, Continuation      bool
}
type DetailSummary struct {
	Children, Images, Cast, Crew, Versions, Streams, Chapters, RawRows int
	File, ChildrenExhausted, CreditsExhausted, Continuation            bool
}
type PlaybackSummary struct{ Streams, Chapters int }
type ContinueSummary = BrowseSummary

// ReadWindow preserves owner evidence with unobserved native facts as null.
// A reservation or returned consumer never proves physical native retirement.
type ReadWindow struct {
	Settings                              *sqliteconn.Settings
	Started, Ended, Deadline, SQLDeadline *time.Time
	Refusal                               *governor.Refusal
	NativeReservedBytes, GoReservedBytes  *int64
}

type ReadEvidence struct {
	Lookup, Publication                     *ReadWindow
	LookupWindows, PublicationWindows       []*ReadWindow
	LookupStatements, PublicationStatements int
	Refusal                                 *governor.Refusal
	Elapsed, AdmissionElapsed               time.Duration
	Attempts                                int
}

// ForegroundResult is recorded after consumption/cleanup. A null Summary means
// no successful read publication; it never invents an empty success on refusal.
// Null error fields mean that phase did not run; an empty string means success.
type ForegroundResult[S any] struct {
	Summary                                        *S
	Evidence                                       *ReadEvidence
	Consumption                                    *read.ForegroundConsumption
	ReadyBudget                                    time.Duration
	ReadyDeadline, ProtectedDeadline, ReadDeadline time.Time
	ReadError, ConsumerError, ScopeError           *string
}

// Browse includes preparation, LIVE waiting, both windows and joined consumer
// cleanup. Run adds original offer lag in ReadyNS; neither is HTTP-ready proof.
func Browse(r *read.BrowseReader, g governor.Governor, a *governor.Authority, s *governor.Snapshots, prepare func(context.Context, Offer) (BrowseInput, error)) Action {
	return foregroundAction("BROWSE", r != nil, g, a, s, prepare, r.Window, func(p read.BrowsePage) BrowseSummary {
		return BrowseSummary{len(p.Items), p.RawRows, p.RawPositions, p.Exhausted, p.Next != nil}
	})
}

func Detail(r *read.ForegroundReader, g governor.Governor, a *governor.Authority, s *governor.Snapshots, prepare func(context.Context, Offer) (DetailInput, error)) Action {
	return foregroundAction("TITLE_DETAIL", r != nil, g, a, s, prepare, r.Detail, func(p read.TitleDetail) DetailSummary {
		v := DetailSummary{Children: len(p.Children), Images: len(p.Images), Cast: len(p.Cast), Crew: len(p.Crew), Versions: len(p.Versions), RawRows: p.RawRows, File: p.File != nil, ChildrenExhausted: p.ChildrenExhausted, CreditsExhausted: p.CreditsExhausted, Continuation: p.Next != nil}
		if p.File != nil {
			v.Streams, v.Chapters = len(p.File.Streams), len(p.File.Chapters)
		}
		return v
	})
}

// PlaybackLookup measures file/stream resolution, never a session start or frame.
func PlaybackLookup(r *read.ForegroundReader, g governor.Governor, a *governor.Authority, s *governor.Snapshots, prepare func(context.Context, Offer) (PlaybackInput, error)) Action {
	return foregroundAction("PLAYBACK_LOOKUP", r != nil, g, a, s, prepare, r.Playback, func(p read.PlaybackFile) PlaybackSummary {
		return PlaybackSummary{len(p.Streams), len(p.Chapters)}
	})
}

// ContinueWatching is the own-state leaf recipe, measured against B-R02.
func ContinueWatching(r *read.ForegroundReader, g governor.Governor, a *governor.Authority, s *governor.Snapshots, prepare func(context.Context, Offer) (ContinueInput, error)) Action {
	return foregroundAction("CONTINUE_WATCHING", r != nil, g, a, s, prepare, r.ContinueWatching, func(p read.ContinuePage) ContinueSummary {
		return ContinueSummary{len(p.Items), p.RawRows, p.RawPositions, p.Exhausted, p.Next != nil}
	})
}

func foregroundAction[I, P, S any](phase string, available bool, g governor.Governor, a *governor.Authority, snapshots *governor.Snapshots, prepare func(context.Context, Offer) (ForegroundInput[I, P], error), readPage func(context.Context, governor.Request, time.Duration, read.BrowseOutput, I) (P, read.BrowseReadResult, error), summarize func(P) S) Action {
	return func(ctx context.Context, offer Offer, emit func(Observation) error) error {
		if !available || g == nil || a == nil || snapshots == nil || prepare == nil {
			return preparationError(emit, errors.New("foreground composition unavailable"))
		}
		start := time.Now()
		in, err := prepare(ctx, offer)
		if err != nil {
			return preparationError(emit, err)
		}
		if in.Consume == nil {
			return preparationError(emit, errors.New("foreground consumer unavailable"))
		}
		out := ForegroundResult[S]{ReadyBudget: in.Budget, ReadyDeadline: in.Foreground.ReadyDeadline, ProtectedDeadline: in.Foreground.ProtectedDeadline, ReadDeadline: in.Request.Deadline}
		consumption, err := read.ConsumeForeground(ctx, g, a, snapshots, in.Foreground, in.OutputRequest, func(ctx context.Context, output read.BrowseOutput) error {
			page, result, err := readPage(ctx, in.Request, in.Budget, output, in.Input)
			out.Evidence = readEvidence(result)
			out.ReadError = errorFact(err)
			if err != nil || result.Refusal != nil {
				if result.Refusal != nil {
					err = errors.Join(err, errActionRefused)
				}
				return err
			}
			summary := summarize(page)
			out.Summary = &summary
			err = in.Consume(ctx, page)
			out.ConsumerError = errorFact(err)
			return err
		})
		out.Consumption, out.ScopeError = &consumption, errorFact(err)
		o := observedCall(phase, start, time.Now())
		o.Result = out
		if err != nil {
			o.CallError = err.Error()
		}
		o.RefusalError = consumption.Refusal != nil || out.Evidence != nil && out.Evidence.Refusal != nil
		return errors.Join(err, emit(o))
	}
}

func errorFact(err error) *string {
	v := ""
	if err != nil {
		v = err.Error()
	}
	return &v
}

func timeFact(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}

func readWindow(v governor.SnapshotResult) *ReadWindow {
	if v == (governor.SnapshotResult{}) {
		return nil
	}
	w := &ReadWindow{Started: timeFact(v.Started), Ended: timeFact(v.Ended), Deadline: timeFact(v.Deadline), SQLDeadline: timeFact(v.SQLDeadline), Refusal: v.Refusal}
	if v.Settings != (sqliteconn.Settings{}) {
		w.Settings = &v.Settings
	}
	if v.NativeReservedBytes != 0 {
		w.NativeReservedBytes = &v.NativeReservedBytes
	}
	if v.GoReservedBytes != 0 {
		w.GoReservedBytes = &v.GoReservedBytes
	}
	return w
}

func readEvidence(v read.BrowseReadResult) *ReadEvidence {
	e := &ReadEvidence{Lookup: readWindow(v.Lookup), Publication: readWindow(v.Publication), LookupStatements: v.LookupStatements, PublicationStatements: v.PublicationStatements, Refusal: v.Refusal, Elapsed: v.Elapsed, AdmissionElapsed: v.AdmissionElapsed, Attempts: v.Attempts}
	for _, w := range v.LookupWindows {
		e.LookupWindows = append(e.LookupWindows, readWindow(w))
	}
	for _, w := range v.PublicationWindows {
		e.PublicationWindows = append(e.PublicationWindows, readWindow(w))
	}
	return e
}
