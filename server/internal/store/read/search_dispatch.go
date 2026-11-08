package read

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

var ErrSearchSuperseded = errors.New("search response is no longer current")

const MaxSearchTitles = 20

type SearchDisposition uint8

const (
	SearchResults SearchDisposition = iota + 1
	SearchPending
	SearchUnavailable
)

type SearchReason uint8

const (
	SearchNoReason SearchReason = iota
	SearchTitleSourceUnavailable
	SearchFullModeUnsupported
	SearchSourcePending
)

type SearchInput struct {
	View          BrowseInput
	Session, Text string
	ShowMore      bool
	FullAfter     *FullSearchCursor
}

// TitleSearchPage is a draft executor seam, not native serving authority.
// Pending/unavailable pages contain NO items and make no empty/exhaustive claim.
type TitleSearchPage struct {
	Disposition SearchDisposition
	Items       []BrowseItem
}

// TitleSearchRead comes only from trusted harness composition. A future native
// implementation must use the SAME pool's public LIVE read, current authority,
// accepted bank/profile/source and HN fixed-slot attestation. It must join all
// work and close native windows before returning owned bounded values. No such
// executor is installed by this package; nil produces typed unavailable.
type TitleSearchRead func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error)

type SearchResponse struct {
	Generation  uint64
	Mode        search.ReadMode
	Disposition SearchDisposition
	Reason      SearchReason
	Items       []BrowseItem
	Full        *FullSearchPage
}

// SearchConsumer prepares bounded encoding without publishing or retaining
// page references beyond its returned publisher. The driver checks Current
// AFTER preparation, just before invoking Publish, then joins that publisher.
// Both phases must honour ctx and join owned users; no work/reference escapes.
// Generation must survive delivery so clients can reject already-in-flight
// older responses. Supersession cannot retract bytes already sent.
type SearchConsumer func(context.Context, SearchResponse) (publish func(context.Context) error, err error)

type SearchDispatchResult struct {
	Generation  uint64
	Mode        search.ReadMode
	Disposition SearchDisposition
	Reason      SearchReason
	Published   bool
	Read        *BrowseReadResult
	Consumption *ForegroundConsumption
	Elapsed     time.Duration
}

// SearchDispatcher uses the existing captured shared permission statements;
// it installs no native title executor or product runtime registration.
// Composition owns/funds SearchRequests metadata separately and closes/drains
// it after active dispatch calls return. GV alone owns payload/LIVE custody.
type SearchDispatcher struct {
	requests  *search.SearchRequests
	governor  governor.Governor
	authority *governor.Authority
	pool      *governor.Snapshots
	titles    TitleSearchRead
	full      FullSearchRead
}

func NewSearchDispatcher(requests *search.SearchRequests, g governor.Governor, a *governor.Authority, pool *governor.Snapshots, titles TitleSearchRead) (*SearchDispatcher, error) {
	if requests == nil || a == nil || !a.Owns(g) || pool == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	return &SearchDispatcher{requests: requests, governor: g, authority: a, pool: pool, titles: titles}, nil
}

func (d *SearchDispatcher) Consume(ctx context.Context, live governor.ForegroundRequest, request governor.Request, budget time.Duration, output governor.Request, in SearchInput, consume SearchConsumer) (out SearchDispatchResult, err error) {
	started := time.Now()
	defer func() { out.Elapsed = time.Since(started) }()
	if d == nil || ctx == nil || consume == nil || budget <= 0 ||
		in.View.Now.IsZero() || in.View.Limit < 1 || in.View.Limit > MaxSearchTitles ||
		in.View.After != nil || in.View.Descending || in.View.KeepArticles ||
		len(in.View.Database) == 0 || len(in.View.Database) > 256 ||
		len(in.View.TokenHash) == 0 || len(in.View.TokenHash) > 256 ||
		in.View.Library <= 0 || in.View.Kind < 0 || in.View.Kind > 99 ||
		request.Writer || request.Deadline.IsZero() || request.Key != live.Key ||
		request.Owner != live.Owner || request.Class != governor.Foreground ||
		request.Need[governor.Reader] != 1 || request.Need[governor.MemoryBytes] != 12<<20 {
		return out, ErrInput
	}
	query, err := search.PlanQuery(in.Text, in.ShowMore)
	if err != nil {
		return out, err // invalid input never supersedes a valid active request
	}
	if in.FullAfter != nil && query.Mode() == search.ReadTitles {
		return out, ErrInput
	}
	// All-kinds (Kind 0) is only meaningful for full search; title discovery
	// keeps requiring one concrete kind.
	if in.View.Kind == 0 && query.Mode() == search.ReadTitles {
		return out, ErrInput
	}
	var after *FullSearchCursor
	if query.Mode() == search.ReadFull {
		after = cloneFullCursor(in.FullAfter)
	}
	out.Mode = query.Mode()
	active, err := d.requests.Begin(ctx, in.Session)
	if err != nil {
		return out, err
	}
	defer active.Finish() // AFTER shared output/LIVE cleanup and both consumers
	out.Generation = active.Generation()
	// Preserve the trusted request-entry clock through metadata preparation;
	// the public LIVE reader accounts for admission/execution from here onward.
	view := in.View
	view.Database, view.TokenHash = strings.Clone(view.Database), strings.Clone(view.TokenHash)
	in = SearchInput{} // retain neither raw query/session backing nor browse cursors
	consumption, err := ConsumeForeground(active.Context(), d.governor, d.authority, d.pool, live, output, func(ctx context.Context, custody BrowseOutput) error {
		view.Now = view.Now.Add(custody.live.started.Sub(started))
		deadline := earliest(live.ReadyDeadline, live.ProtectedDeadline, request.Deadline, started.Add(min(budget, time.Second)))
		readCtx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		purpose := request
		purpose.Deadline = deadline
		out.Read = &BrowseReadResult{Attempts: 1, AdmissionElapsed: custody.live.admission}
		defer func() { out.Read.Elapsed = time.Since(started) }()
		initial, e := d.permissionFence(readCtx, purpose, deadline, custody, view, false, out.Read)
		if e != nil {
			return e
		}
		// Prepare-only consumers cannot publish until a NEW shared fence has
		// checked the actual principal, including both original birth witnesses.
		// Cancellation may not suppress an observed denial; validation remains
		// bounded by the ORIGINAL operation deadline and authorizes no business.
		finish := func(prior error) error {
			validation, stop := context.WithDeadline(context.WithoutCancel(readCtx), deadline)
			defer stop()
			current, e := d.permissionFence(validation, purpose, deadline, custody, view, true, out.Read)
			if e == nil && current != initial {
				e = ErrStale
			}
			return searchFenceError(errors.Join(prior, readCtx.Err()), e, out.Read)
		}
		page := TitleSearchPage{Disposition: SearchResults}
		var fullPage *FullSearchPage
		reason := SearchNoReason
		switch query.Mode() {
		case search.ReadNothing:
			// A valid cleared query still supersedes old work and sends generation.
		case search.ReadFull:
			if d.full == nil {
				page.Disposition, reason = SearchUnavailable, SearchFullModeUnsupported
				break
			}
			if e := readCtx.Err(); e != nil {
				return finish(e)
			}
			p, cost, e := d.full(readCtx, purpose, time.Until(deadline), custody, FullSearchInput{View: view, After: after}, query)
			addSearchReadCost(out.Read, cost)
			if e != nil {
				return finish(e)
			}
			if e = readCtx.Err(); e != nil {
				return finish(e)
			}
			owned, e := ownFullSearchPage(p, view.Limit)
			if e != nil {
				return finish(e)
			}
			fullPage = &owned
			page.Disposition = owned.Disposition
			if owned.Disposition == SearchPending {
				reason = SearchSourcePending
			} else if owned.Disposition == SearchUnavailable {
				reason = SearchFullModeUnsupported
			}
		case search.ReadTitles:
			if d.titles == nil {
				page.Disposition, reason = SearchUnavailable, SearchTitleSourceUnavailable
				break
			}
			if e := readCtx.Err(); e != nil {
				return finish(e)
			}
			var e error
			var native *BrowseReadResult
			page, native, e = d.titles(readCtx, purpose, time.Until(deadline), custody, view, query)
			addSearchReadCost(out.Read, native)
			if e != nil {
				return finish(e)
			}
			if e = readCtx.Err(); e != nil {
				return finish(e)
			}
			switch page.Disposition {
			case SearchPending:
				reason = SearchSourcePending
			case SearchUnavailable:
				reason = SearchTitleSourceUnavailable
			}
		default:
			return finish(ErrInput)
		}
		owned, e := ownSearchPage(page, view.Limit)
		if e != nil {
			return finish(e)
		}
		out.Disposition, out.Reason = owned.Disposition, reason
		response := SearchResponse{Generation: out.Generation, Mode: out.Mode, Disposition: owned.Disposition, Reason: reason, Items: owned.Items, Full: fullPage}
		check := func() error {
			if !active.Current() {
				return errors.Join(ErrSearchSuperseded, ctx.Err())
			}
			return errors.Join(ctx.Err(), readCtx.Err(), custody.live.checkContext(readCtx))
		}
		if e = check(); e != nil {
			return finish(e)
		}
		publish, e := consume(ctx, response)
		if e != nil {
			return finish(e)
		}
		if publish == nil {
			return finish(ErrInput)
		}
		if e = finish(check()); e != nil {
			return e
		}
		if e = check(); e != nil {
			return e
		}
		if e = publish(ctx); e != nil {
			return e
		}
		if e = check(); e != nil {
			return e
		}
		out.Published = true
		return nil
	})
	if consumption.Refusal == nil && out.Read != nil {
		consumption.Refusal = out.Read.Refusal
	}
	out.Consumption = &consumption
	if err != nil {
		out.Published = false
	}
	return out, err
}

func ownSearchPage(page TitleSearchPage, limit int) (TitleSearchPage, error) {
	if page.Disposition < SearchResults || page.Disposition > SearchUnavailable || len(page.Items) > limit || page.Disposition != SearchResults && len(page.Items) != 0 {
		return TitleSearchPage{}, ErrInput
	}
	for _, item := range page.Items {
		if item.ID <= 0 || len(item.Title) > 64<<10 || !utf8.ValidString(item.Title) {
			return TitleSearchPage{}, ErrInput
		}
	}
	owned := TitleSearchPage{Disposition: page.Disposition}
	for _, item := range page.Items {
		item.Title = strings.Clone(item.Title)
		owned.Items = append(owned.Items, item)
	}
	return owned, nil
}
