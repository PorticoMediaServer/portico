package read

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// This file is the inert source-route layer above the merged native search
// composition. It decides which owner adapter must answer a dispatch and then
// calls the caller-supplied adapter. It installs no statement, opens no
// snapshot and contributes no SQL: Live generation/time/name and Reading
// section/publication statements stay with MG and the existing Reading text
// index. In particular it creates no second FTS bank and no alternate
// catalogue-prefix source for Reading full text.

// RSSearchSource names the owner adapter that answers one route.
type RSSearchSource uint8

const (
	RSSearchSourceNone RSSearchSource = iota
	RSSearchSourceTitles
	RSSearchSourceCatalogue
	RSSearchSourceReadingText
	RSSearchSourceLive
	RSSearchSourcePeople
)

func (s RSSearchSource) String() string {
	switch s {
	case RSSearchSourceNone:
		return "none"
	case RSSearchSourceTitles:
		return "titles"
	case RSSearchSourceCatalogue:
		return "catalogue"
	case RSSearchSourceReadingText:
		return "reading-text"
	case RSSearchSourceLive:
		return "live"
	case RSSearchSourcePeople:
		return "people"
	default:
		return "unknown"
	}
}

// Missing owner adapters are named pending, never an empty successful group.
var (
	ErrRSSearchTitlesPending      = errors.New("search title discovery is pending the catalogue title adapter")
	ErrRSSearchCataloguePending   = errors.New("search catalogue composition is pending the native search adapter")
	ErrRSSearchReadingTextPending = errors.New("reading full text is pending the accepted-section text search adapter")
	ErrRSSearchLivePending        = errors.New("live search is pending MG's active-generation channel matcher")
	ErrRSSearchPeoplePending      = errors.New("people search is pending the native people directory adapter")
	ErrRSSearchGroupUnsupported   = errors.New("search group has no published source")
	// ErrRSSearchPreviewOverflow names an all-group preview group that returned
	// more than the bounded eight cards. It is a refusal, never a silent trim of
	// an exact answer.
	ErrRSSearchPreviewOverflow = errors.New("all-group search preview allows at most eight cards per group")
	// ErrRSSearchContinuationUnsupported refuses a catalogue full-search cursor
	// for a source whose page publishes no continuation of its own.
	ErrRSSearchContinuationUnsupported = errors.New("search continuation is not published by this source")
)

// RSSearchGroupError preserves the existing per-group error/success outcome
// when one source of an all-group request fails.
type RSSearchGroupError struct {
	Group RSSearchGroup
	Err   error
}

func (e *RSSearchGroupError) Error() string {
	return fmt.Sprintf("search group %d: %v", e.Group, e.Err)
}
func (e *RSSearchGroupError) Unwrap() error { return e.Err }

// RSSearchRoute is one immutable dispatch decision. Full is true only for a
// selected group's forty-card page; all-group composition is the bounded
// per-group preview (eight cards, Partial=true) described by task14.
type RSSearchRoute struct {
	Mode   search.ReadMode
	Source RSSearchSource
	Group  RSSearchGroup
	Full   bool
}

// RSSearchRouteInput adds the caller's explicit group selection to the public
// dispatch input. Group zero derives the group from View.Kind; a negative group
// is still an explicit request (Live TV or People), never an all-kind search.
// ReadingText selects the existing accepted-section text index instead of the
// catalogue Reading metadata group.
type RSSearchRouteInput struct {
	SearchInput
	Group       RSSearchGroup
	ReadingText bool
	Current     func() bool // optional request-generation guard
}

// RSSearchSourceForGroup maps one explicit group to its owner source. A group
// that RS does not publish is refused rather than answered with an empty page.
func RSSearchSourceForGroup(group RSSearchGroup) (RSSearchSource, error) {
	switch {
	case group == 0:
		return RSSearchSourceCatalogue, nil
	case group == RSSearchLiveTV:
		return RSSearchSourceLive, nil
	case group == RSSearchPeople:
		return RSSearchSourcePeople, nil
	case group == RSSearchReading || group == 21:
		return RSSearchSourceCatalogue, nil
	case group > 0 && group <= 99 && rsSearchPublicGroup(group):
		return RSSearchSourceCatalogue, nil
	default:
		return RSSearchSourceNone, ErrRSSearchGroupUnsupported
	}
}

// PlanRSSearchRoute applies the frozen phase0 script-aware dispatch and the
// merged search_dispatch.go kind rule to a public SearchInput. Latin and other
// non-CJK input of one or two runes, and CJK input of one rune, read the small
// title page. Three Latin/other runes or two CJK runes read full membership.
// An explicit short-context action (ShowMore) always takes the full recipe and
// preserves short context matches. A cleared query and title discovery keep
// requiring one concrete kind; all-kinds is only meaningful for full search.
func PlanRSSearchRoute(in SearchInput) (RSSearchRoute, error) {
	return planRSSearchRoute(RSSearchRouteInput{SearchInput: in})
}

func planRSSearchRoute(in RSSearchRouteInput) (RSSearchRoute, error) {
	if in.View.Kind < 0 || in.View.Kind > 99 {
		return RSSearchRoute{}, ErrInput
	}
	query, err := search.PlanQuery(in.Text, in.ShowMore)
	if err != nil {
		return RSSearchRoute{}, err
	}
	if in.FullAfter != nil && query.Mode() != search.ReadFull {
		return RSSearchRoute{}, ErrInput
	}
	group := in.Group
	if group == 0 {
		group = RSSearchGroup(in.View.Kind)
	} else if in.View.Kind != 0 {
		kind := in.View.Kind
		if kind == 21 {
			kind = 20
		}
		want := group
		if want == 21 {
			want = RSSearchReading
		}
		if want != RSSearchGroup(kind) {
			return RSSearchRoute{}, ErrInput
		}
	}
	if group == 21 {
		group = RSSearchReading
	}
	if query.Mode() != search.ReadFull {
		// Explicit context matching is a full search, never a title-only
		// approximation, and it allows all kinds above. Here at least one
		// concrete positive kind is required.
		if group <= 0 || group > 99 {
			return RSSearchRoute{}, ErrInput
		}
		if query.Mode() == search.ReadNothing {
			return RSSearchRoute{Mode: query.Mode(), Source: RSSearchSourceNone, Group: group}, nil
		}
		return RSSearchRoute{Mode: query.Mode(), Source: RSSearchSourceTitles, Group: group}, nil
	}
	if in.ReadingText {
		if group != RSSearchReading {
			return RSSearchRoute{}, ErrInput
		}
		// The accepted-section text page publishes no continuation of its own;
		// a catalogue full-search cursor must not be forwarded to it.
		if in.FullAfter != nil {
			return RSSearchRoute{}, ErrRSSearchContinuationUnsupported
		}
		return RSSearchRoute{Mode: query.Mode(), Source: RSSearchSourceReadingText, Group: group, Full: true}, nil
	}
	source, err := RSSearchSourceForGroup(group)
	if err != nil {
		return RSSearchRoute{}, err
	}
	if in.FullAfter != nil && source != RSSearchSourceCatalogue {
		return RSSearchRoute{}, ErrRSSearchContinuationUnsupported
	}
	return RSSearchRoute{Mode: query.Mode(), Source: source, Group: group, Full: group != 0}, nil
}

// RSSearchRouteLimit is the bounded card count for one route. It mirrors the
// merged dispatch/task14 limits; it adds no admission or memory quota.
func RSSearchRouteLimit(route RSSearchRoute) int {
	switch {
	case route.Mode == search.ReadTitles:
		return MaxSearchTitles
	case route.Group == 0:
		return 8
	default:
		return 40
	}
}

// RSSearchGroupBudget is the existing per-group Search timeout. The caller's
// budget can shorten it but cannot lengthen it.
const RSSearchGroupBudget = 600 * time.Millisecond

// RSSearchRouteRequest is the bounded input each owner adapter receives.
type RSSearchRouteRequest struct {
	View  BrowseInput
	Query search.Query
	Group RSSearchGroup
	Full  bool
	After *FullSearchCursor
}

// RSSearchCatalogueRead answers the merged catalogue/People composition. It is
// normally backed by RSSearchReader; this layer only selects and bounds it.
type RSSearchCatalogueRead func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error)

// RSSearchReadingTextHit is one accepted-section full-text hit. It carries the
// exact section location and excerpt; it is not a searchable-metadata posting.
type RSSearchReadingTextHit struct {
	ItemID       int64
	SectionPath  string
	SectionIndex int
	Excerpt      string
	MatchStart   int
	MatchLength  int
}

// RSSearchReadingTextPage is a bounded page of the existing Reading text index.
// Pending with no hits is work in progress, never a zero-match answer.
type RSSearchReadingTextPage struct {
	Hits      []RSSearchReadingTextHit
	HasMore   bool
	Pending   bool
	Exhausted bool
}

type RSSearchReadingTextRead func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchReadingTextPage, *BrowseReadResult, error)

// RSSearchLiveRead is the MG seam. MG owns channel hiding, name/time matching
// and the active generation; RS must not synthesize those facts.
type RSSearchLiveRead func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error)

// RSSearchPeopleRead is the task16 directory seam for an explicit People action.
type RSSearchPeopleRead func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error)

// RSSearchRoutes is inert trusted composition. Nil adapters are refused by name
// when a route needs them; they never become an empty successful group.
type RSSearchRoutes struct {
	Titles    TitleSearchRead
	Catalogue RSSearchCatalogueRead
	Reading   RSSearchReadingTextRead
	Live      RSSearchLiveRead
	People    RSSearchPeopleRead
}

func NewRSSearchRoutes(titles TitleSearchRead, catalogue RSSearchCatalogueRead, reading RSSearchReadingTextRead, live RSSearchLiveRead, people RSSearchPeopleRead) *RSSearchRoutes {
	return &RSSearchRoutes{Titles: titles, Catalogue: catalogue, Reading: reading, Live: live, People: people}
}

// RSSearchRoutesResult keeps each route's own shape rather than flattening a
// group page into one global list. Rank, membership, per-group status and the
// bounded Reading excerpts therefore survive delivery. Pending names sources
// that were deliberately not queried; Errors holds per-group failures from an
// all-group request.
type RSSearchRoutesResult struct {
	Route   RSSearchRoute
	Titles  []BrowseItem
	Page    RSSearchPage
	Reading RSSearchReadingTextPage
	Pending []RSSearchGroup
	Errors  []RSSearchGroupError
}

// Read routes and executes one dispatch under the caller's original request.
// The returned error is a named pending/refusal or a per-group error; a nil
// error with an empty result means an intentionally cleared query.
func (r *RSSearchRoutes) Read(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSSearchRouteInput) (RSSearchRoutesResult, *BrowseReadResult, error) {
	var result RSSearchRoutesResult
	if ctx == nil || budget <= 0 {
		return result, nil, ErrInput
	}
	if err := rssRouteCurrent(ctx, in); err != nil {
		return result, nil, err
	}
	route, err := planRSSearchRoute(in)
	if err != nil {
		return result, nil, err
	}
	result.Route = route
	req := RSSearchRouteRequest{View: in.View, Query: search.Query{}, Group: route.Group, Full: route.Full}
	planned, err := search.PlanQuery(in.Text, in.ShowMore)
	if err != nil {
		return result, nil, err
	}
	req.Query = planned
	req.After = in.FullAfter
	if route.Source == RSSearchSourceNone {
		return result, nil, nil
	}
	groupBudget := min(budget, RSSearchGroupBudget)
	if groupBudget <= 0 {
		return result, nil, ErrInput
	}

	switch route.Source {
	case RSSearchSourceTitles:
		if r == nil || r.Titles == nil {
			return result, nil, ErrRSSearchTitlesPending
		}
		groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
		defer cancel()
		page, cost, err := r.Titles(groupCtx, request, time.Until(deadline), output, req.View, req.Query)
		if err != nil {
			return result, nil, rssRouteGroupError(route.Group, err)
		}
		owned, err := ownSearchPage(page, MaxSearchTitles)
		if err != nil {
			return result, nil, err
		}
		if err = rssRouteCurrent(groupCtx, in); err != nil {
			return result, nil, err
		}
		result.Titles = owned.Items
		return result, cost, nil
	case RSSearchSourceCatalogue:
		if r == nil || r.Catalogue == nil {
			return result, nil, ErrRSSearchCataloguePending
		}
		groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
		defer cancel()
		page, cost, err := r.Catalogue(groupCtx, request, time.Until(deadline), output, req)
		if err != nil {
			return result, nil, rssRouteGroupError(route.Group, err)
		}
		if err = rssValidateRSSearchPage(page, route); err != nil {
			return result, nil, err
		}
		if err = rssRouteCurrent(groupCtx, in); err != nil {
			return result, nil, err
		}
		result.Page = page
		if route.Group == 0 {
			// A pending catalogue page carries no prefix or groups at all. Live
			// is not polled behind it, so the page keeps the validator's pending
			// shape and never becomes "pending with a Live group".
			if page.Pending {
				return result, cost, nil
			}
			if cost == nil {
				cost = &BrowseReadResult{}
			}
			result.Pending, result.Errors = r.appendLive(ctx, request, budget, output, req, &result, cost)
			// A request generation that moved on while Live ran must not let the
			// superseded Live group escape with the catalogue page.
			if err = rssRouteCurrent(ctx, in); err != nil {
				result.Page, result.Pending, result.Errors = RSSearchPage{}, nil, nil
				return result, cost, err
			}
		}
		return result, cost, nil
	case RSSearchSourceReadingText:
		if r == nil || r.Reading == nil {
			return result, nil, ErrRSSearchReadingTextPending
		}
		groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
		defer cancel()
		page, cost, err := r.Reading(groupCtx, request, time.Until(deadline), output, req)
		if err != nil {
			return result, nil, rssRouteGroupError(route.Group, err)
		}
		if err = rssValidateReadingTextPage(page, route); err != nil {
			return result, nil, err
		}
		if err = rssRouteCurrent(groupCtx, in); err != nil {
			return result, nil, err
		}
		result.Reading = page
		return result, cost, nil
	case RSSearchSourceLive:
		if r == nil || r.Live == nil {
			return result, nil, ErrRSSearchLivePending
		}
		groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
		defer cancel()
		page, cost, err := r.Live(groupCtx, request, time.Until(deadline), output, req)
		if err != nil {
			return result, nil, rssRouteGroupError(route.Group, err)
		}
		if err = rssValidateGroupPage(page, route); err != nil {
			return result, nil, err
		}
		if err = rssRouteCurrent(groupCtx, in); err != nil {
			return result, nil, err
		}
		result.Page = RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{page}}
		return result, cost, nil
	case RSSearchSourcePeople:
		if r == nil || r.People == nil {
			return result, nil, ErrRSSearchPeoplePending
		}
		groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
		defer cancel()
		page, cost, err := r.People(groupCtx, request, time.Until(deadline), output, req)
		if err != nil {
			return result, nil, rssRouteGroupError(route.Group, err)
		}
		if err = rssValidateGroupPage(page, route); err != nil {
			return result, nil, err
		}
		if err = rssRouteCurrent(groupCtx, in); err != nil {
			return result, nil, err
		}
		result.Page = RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{page}}
		return result, cost, nil
	default:
		return result, nil, ErrInput
	}
}

// appendLive adds the explicitly requested Live group to an all-group request.
// Without MG's adapter it is named pending; a failing owner adapter is kept as
// a per-group error while every catalogue group still stands.
//
// The catalogue's continuity cursor belongs to the catalogue section only. The
// all-group Live preview is always its first-page preview, so the cursor is
// cleared here rather than forwarded to an adapter that publishes no
// continuation of its own; accepting it in plan and forwarding it here would
// hand Live a catalogue *FullSearchCursor.
//
// Live is a second source under the caller's request. It gets its own bounded
// group deadline and, whenever the caller holds a real foreground read scope,
// its own output grant: the caller's BrowseOutput authorizes exactly one public
// read operation, so handing it to a second source would refuse every Live read
// with governor.ErrGrant. The Live cost (windows, statements, refusal) is added
// to the composite cost, never discarded.
func (r *RSSearchRoutes) appendLive(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, req RSSearchRouteRequest, result *RSSearchRoutesResult, cost *BrowseReadResult) (pending []RSSearchGroup, groupErrors []RSSearchGroupError) {
	req.Group = RSSearchLiveTV
	req.After = nil
	if r == nil || r.Live == nil {
		return []RSSearchGroup{RSSearchLiveTV}, nil
	}
	groupCtx, cancel, deadline := rssGroupDeadline(ctx, request, budget)
	defer cancel()
	liveOutput := output
	if output.live != nil {
		derived, refusal, release, err := output.live.derive(groupCtx, rssDerivedOutputRequest(output.Request, deadline))
		if refusal != nil {
			// A second-source admission refusal is the composite read's refusal,
			// exactly as ConsumeForeground records its own admission refusal.
			cost.Refusal = refusal
		}
		if err != nil {
			return nil, []RSSearchGroupError{{Group: RSSearchLiveTV, Err: err}}
		}
		// A retirement failure means physical output custody is uncertain. It is
		// surfaced as this group's error rather than discarded.
		defer func() {
			if rerr := release(); rerr != nil {
				groupErrors = append(groupErrors, RSSearchGroupError{Group: RSSearchLiveTV, Err: rerr})
			}
		}()
		liveOutput = derived
	}
	page, liveCost, err := r.Live(groupCtx, request, time.Until(deadline), liveOutput, req)
	addSearchReadCost(cost, liveCost)
	if err != nil {
		return nil, []RSSearchGroupError{{Group: RSSearchLiveTV, Err: err}}
	}
	if err = rssValidatePreviewGroupPage(page); err != nil {
		return nil, []RSSearchGroupError{{Group: RSSearchLiveTV, Err: err}}
	}
	result.Page.Groups = append(result.Page.Groups, page)
	return nil, nil
}

// rssGroupDeadline bounds one source's own work. Each group starts a fresh
// 600ms window; the caller's request/context deadline still caps the total, so
// the Live group is not charged the time catalogue already spent.
func rssGroupDeadline(ctx context.Context, request governor.Request, budget time.Duration) (context.Context, context.CancelFunc, time.Time) {
	deadline := time.Now().Add(min(budget, RSSearchGroupBudget))
	if !request.Deadline.IsZero() && request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	groupCtx, cancel := context.WithDeadline(ctx, deadline)
	return groupCtx, cancel, deadline
}

// rssDerivedOutputRequest derives one source's output budget from the caller's
// output request: same owner/class and the same bounded memory-only profile.
// derive further clamps it to the parent output's own deadline.
func rssDerivedOutputRequest(base governor.Request, deadline time.Time) governor.Request {
	derived := base
	derived.Deadline = deadline
	return derived
}

// derive mints a second, independent one-public-read scope from this foreground
// scope. It does not weaken the rule that ONE output authorizes ONE read: the
// derived scope carries its own output grant and its own single-use flag, while
// the LIVE grant still proves the caller's original foreground admission. The
// returned release joins the derived reads and retires its grant; callers must
// invoke it before returning to the foreground consumer, and its retirement
// error is the caller's to surface.
//
// The doubled reservation is deliberate until MG's Live adapter defines a
// shared-output API. The parent grant stays held while the derived grant is a
// second, independent Foreground admission: each source is admission-checked
// against MaxGrantsPerClass and the memory partition, the parent is never
// charged zero, and a full ledger refuses the second source as a named
// per-group Live error instead of silently reusing the parent's authorization.
func (s *foregroundOutput) derive(ctx context.Context, outputRequest governor.Request) (BrowseOutput, *governor.Refusal, func() error, error) {
	if s == nil || ctx == nil {
		return BrowseOutput{}, nil, nil, governor.ErrGrant
	}
	if outputRequest.Owner != s.request.Owner || outputRequest.Class != governor.Foreground || outputRequest.Writer ||
		outputRequest.Need[governor.MemoryBytes] != 8<<20 || outputRequest.Deadline.IsZero() ||
		outputRequest.Deadline.After(s.request.ProtectedDeadline) || len(outputRequest.IO) != 0 || len(outputRequest.Footprint) != 0 {
		return BrowseOutput{}, nil, nil, ErrInput
	}
	for resource, n := range outputRequest.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return BrowseOutput{}, nil, nil, ErrInput
		}
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return BrowseOutput{}, nil, nil, governor.ErrGrant
	}
	authority, live, request, pool, started, admission := s.authority, s.live, s.request, s.pool, s.started, s.admission
	parentOutput := s.outputRequest
	s.mu.Unlock()
	// The derived output may not outlive the parent output it is minted from,
	// even when the group's own window is longer.
	if parentOutput.Deadline.Before(outputRequest.Deadline) {
		outputRequest.Deadline = parentOutput.Deadline
	}
	decision := authority.TryContext(ctx, outputRequest)
	if decision.Refusal != nil || decision.Grant == nil {
		// Return the refusal beside ErrBusy so the caller can record its code and
		// RetryAt, as ConsumeForeground does for its own admission refusals.
		return BrowseOutput{}, decision.Refusal, nil, governor.ErrBusy
	}
	child := &foregroundOutput{
		authority: authority, pool: pool, live: live, request: request,
		output: decision.Grant, outputRequest: outputRequest, ctx: ctx, started: started, admission: admission,
	}
	release := func() error {
		child.mu.Lock()
		child.closed = true
		child.mu.Unlock()
		child.reads.Wait()
		return authority.Retired(decision.Grant)
	}
	return BrowseOutput{Authority: authority, Grant: decision.Grant, Request: outputRequest, live: child}, nil, release, nil
}

func rssRouteCurrent(ctx context.Context, in RSSearchRouteInput) error {
	if ctx == nil {
		return ErrInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if in.Current != nil && !in.Current() {
		return ErrSearchSuperseded
	}
	return nil
}

func rssRouteGroupError(group RSSearchGroup, err error) error {
	if err == nil {
		return nil
	}
	var grouped *RSSearchGroupError
	if errors.As(err, &grouped) {
		return err
	}
	return &RSSearchGroupError{Group: group, Err: err}
}

func rssValidateRSSearchPage(page RSSearchPage, route RSSearchRoute) error {
	if page.Pending && page.Complete || !page.Pending && !page.Complete {
		return ErrInput
	}
	if page.Pending && len(page.Groups) != 0 {
		return ErrInput
	}
	limit := RSSearchRouteLimit(route)
	for _, group := range page.Groups {
		if !rsSearchPublicGroup(group.Group) {
			return ErrInput
		}
		if route.Group != 0 && group.Group != route.Group {
			return ErrInput
		}
		if len(group.Hits) > limit {
			return ErrInput
		}
		for _, hit := range group.Hits {
			if hit.ID <= 0 || rsSearchHitGroup(hit) != group.Group {
				return ErrInput
			}
		}
	}
	return nil
}

func rssValidateGroupPage(page RSSearchGroupPage, route RSSearchRoute) error {
	return rssValidateGroupPageLimit(page, route.Group, RSSearchRouteLimit(route), ErrInput)
}

// rssValidatePreviewGroupPage validates one all-group preview: the same bounded
// group shape at the preview limit (eight cards), with a named refusal when the
// owner adapter returns more. An exact answer is refused, never silently cut.
func rssValidatePreviewGroupPage(page RSSearchGroupPage) error {
	return rssValidateGroupPageLimit(page, RSSearchLiveTV, 8, ErrRSSearchPreviewOverflow)
}

func rssValidateGroupPageLimit(page RSSearchGroupPage, group RSSearchGroup, limit int, overflow error) error {
	if page.Group != group {
		return ErrInput
	}
	if len(page.Hits) > limit {
		if overflow != nil {
			return overflow
		}
		return ErrInput
	}
	for _, hit := range page.Hits {
		if hit.ID <= 0 {
			return ErrInput
		}
		// MG owns the Live hit shape. RS must not infer a native kind for a
		// group whose generation/channel facts are not landed; it validates
		// only the bounded page and group identity for that adapter.
		if page.Group != RSSearchLiveTV && rsSearchHitGroup(hit) != page.Group {
			return ErrInput
		}
	}
	return nil
}

func rssValidateReadingTextPage(page RSSearchReadingTextPage, route RSSearchRoute) error {
	if page.Pending && (page.Exhausted || len(page.Hits) != 0) {
		return ErrInput
	}
	if len(page.Hits) > RSSearchRouteLimit(route) {
		return ErrInput
	}
	for _, hit := range page.Hits {
		if hit.ItemID <= 0 || hit.SectionIndex < 0 || hit.SectionIndex > 4095 ||
			len(hit.SectionPath) == 0 || len(hit.SectionPath) > 1024 || !utf8.ValidString(hit.SectionPath) ||
			!utf8.ValidString(hit.Excerpt) || hit.MatchStart < 0 || hit.MatchLength < 1 {
			return ErrInput
		}
	}
	return nil
}
