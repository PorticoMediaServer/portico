package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// FullSearchRequest is one public LIVE full-text search read. The query and the
// preview limits come from the owning RD recipe; this driver supplies no SQL,
// plan, bank, stamp or cursor of its own.
type FullSearchRequest struct {
	Request       governor.Request
	Budget        time.Duration
	Foreground    governor.ForegroundRequest
	OutputRequest governor.Request
	View          read.FullSearchInput
	Query         search.Query
	Consume       func(context.Context, read.FullSearchPage) error
}

// fullSearchCore is the values-only per-offer input handed to the shared
// foreground lifecycle. It never carries authority, a plan or a receipt.
type fullSearchCore struct {
	View  read.FullSearchInput
	Query search.Query
}

// FullSearchSummary is bounded scalar observation, never a page or a wire DTO.
type FullSearchSummary struct {
	Hits, Candidates int
	Stamps           bool
	Partial          bool
	Exhausted        bool
	Continuation     bool
	Unavailable      bool
	Disposition      int
}

// FullSearch binds RD's separate full-mode reader to the common foreground
// lifecycle: LIVE admission, both windows, joined consumer and whole budget.
// A nil reader, governor, authority, pool or prepare is an explicit unavailable
// composition (the shared lifecycle emits a failed STUB). A source bank that is
// absent is RD's SearchUnavailable disposition, not a driver error; the caller
// decides whether that is a recorded screening gap.
func FullSearch(r *read.NativeFullSearchReader, g governor.Governor, a *governor.Authority, s *governor.Snapshots, prepare func(context.Context, Offer) (FullSearchRequest, error)) Action {
	inner := func(ctx context.Context, offer Offer) (ForegroundInput[fullSearchCore, read.FullSearchPage], error) {
		if prepare == nil {
			return ForegroundInput[fullSearchCore, read.FullSearchPage]{}, errors.New("full-search composition unavailable")
		}
		in, err := prepare(ctx, offer)
		if err != nil {
			return ForegroundInput[fullSearchCore, read.FullSearchPage]{}, err
		}
		if in.Consume == nil {
			return ForegroundInput[fullSearchCore, read.FullSearchPage]{}, errors.New("full-search consumer unavailable")
		}
		return ForegroundInput[fullSearchCore, read.FullSearchPage]{Request: in.Request, Budget: in.Budget, Foreground: in.Foreground, OutputRequest: in.OutputRequest, Input: fullSearchCore{View: in.View, Query: in.Query}, Consume: in.Consume}, nil
	}
	readPage := func(ctx context.Context, request governor.Request, budget time.Duration, output read.BrowseOutput, in fullSearchCore) (read.FullSearchPage, read.BrowseReadResult, error) {
		page, cost, err := r.Read(ctx, request, budget, output, in.View, in.Query)
		if cost == nil {
			return page, read.BrowseReadResult{}, err
		}
		return page, *cost, err
	}
	summarize := func(p read.FullSearchPage) FullSearchSummary {
		return FullSearchSummary{Hits: len(p.Hits), Candidates: p.NativeCandidates, Stamps: p.Stamp != nil, Partial: p.Partial, Exhausted: p.Exhausted, Continuation: p.Next != nil, Unavailable: p.Disposition == read.SearchUnavailable, Disposition: int(p.Disposition)}
	}
	return foregroundAction("FULL_SEARCH", r != nil, g, a, s, inner, readPage, summarize)
}
