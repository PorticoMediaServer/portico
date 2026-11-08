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
		return RSSearchRoute{Mode: query.Mode(), Source: RSSearchSourceReadingText, Group: group, Full: true}, nil
	}
	source, err := RSSearchSourceForGroup(group)
	if err != nil {
		return RSSearchRoute{}, err
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
	groupCtx, cancel := context.WithTimeout(ctx, groupBudget)
	defer cancel()

	switch route.Source {
	case RSSearchSourceTitles:
		if r == nil || r.Titles == nil {
			return result, nil, ErrRSSearchTitlesPending
		}
		page, cost, err := r.Titles(groupCtx, request, groupBudget, output, req.View, req.Query)
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
		page, cost, err := r.Catalogue(groupCtx, request, groupBudget, output, req)
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
			result.Pending, result.Errors = r.appendLive(groupCtx, request, groupBudget, output, req, &result)
		}
		return result, cost, nil
	case RSSearchSourceReadingText:
		if r == nil || r.Reading == nil {
			return result, nil, ErrRSSearchReadingTextPending
		}
		page, cost, err := r.Reading(groupCtx, request, groupBudget, output, req)
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
		page, cost, err := r.Live(groupCtx, request, groupBudget, output, req)
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
		page, cost, err := r.People(groupCtx, request, groupBudget, output, req)
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
func (r *RSSearchRoutes) appendLive(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, req RSSearchRouteRequest, result *RSSearchRoutesResult) ([]RSSearchGroup, []RSSearchGroupError) {
	req.Group = RSSearchLiveTV
	if r == nil || r.Live == nil {
		return []RSSearchGroup{RSSearchLiveTV}, nil
	}
	page, _, err := r.Live(ctx, request, budget, output, req)
	if err != nil {
		return nil, []RSSearchGroupError{{Group: RSSearchLiveTV, Err: err}}
	}
	if err = rssValidateGroupPage(page, RSSearchRoute{Mode: search.ReadFull, Source: RSSearchSourceLive, Group: RSSearchLiveTV}); err != nil {
		return nil, []RSSearchGroupError{{Group: RSSearchLiveTV, Err: err}}
	}
	result.Page.Groups = append(result.Page.Groups, page)
	return nil, nil
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
	if page.Group != route.Group {
		return ErrInput
	}
	if len(page.Hits) > RSSearchRouteLimit(route) {
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
