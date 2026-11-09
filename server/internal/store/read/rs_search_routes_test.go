package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// The phase0 freeze (Q-SEARCH) is restated here as literal expected outcomes,
// not by calling the implementation they verify. Latin/other input of one or
// two runes and CJK input of one rune keep the bounded title page; three
// Latin/other runes or two CJK runes take full membership. This is the
// independent oracle for dispatch parity.
var frozenDispatch = []struct {
	text string
	more bool
	mode search.ReadMode
}{
	{"", false, search.ReadNothing},
	{"   ", false, search.ReadNothing},
	{"a", false, search.ReadTitles},
	{"ab", false, search.ReadTitles},
	{"abc", false, search.ReadFull},
	{"王", false, search.ReadTitles},
	{"王菲", false, search.ReadFull},
	{"a東", false, search.ReadFull},
	{"harbor ad", false, search.ReadFull},
	{"123456789012345", false, search.ReadFull},
	{"12", false, search.ReadTitles},
	{"123", false, search.ReadFull},
	{"a", true, search.ReadFull},
	{"王", true, search.ReadFull},
	{"ab c", true, search.ReadFull},
}

func TestRSSearchRouteFrozenDispatchParity(t *testing.T) {
	for _, tc := range frozenDispatch {
		in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: tc.text, ShowMore: tc.more}}
		route, err := planRSSearchRoute(in)
		if err != nil || route.Mode != tc.mode {
			t.Fatalf("query %q showMore %t: mode %v err %v, want %v", tc.text, tc.more, route.Mode, err, tc.mode)
		}
		switch tc.mode {
		case search.ReadNothing:
			if route.Source != RSSearchSourceNone || route.Group != 1 || route.Full {
				t.Fatal("cleared query took a source", route)
			}
		case search.ReadTitles:
			if route.Source != RSSearchSourceTitles || route.Group != 1 || route.Full {
				t.Fatal("short ordinary input did not stay title discovery", route)
			}
		case search.ReadFull:
			if route.Source != RSSearchSourceCatalogue || route.Group != 1 || !route.Full {
				t.Fatal("full query took the wrong source", route)
			}
		}
	}
	// All kinds is only meaningful for full search. A cleared query and a short
	// ordinary query keep requiring one concrete kind.
	for _, tc := range []struct {
		text string
		more bool
	}{
		{"", false}, {"a", false}, {"ab", false}, {"王", false},
	} {
		in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: tc.text, ShowMore: tc.more}}
		if _, err := planRSSearchRoute(in); !errors.Is(err, ErrInput) {
			t.Fatalf("all-kinds accepted outside full search: %q %v", tc.text, err)
		}
	}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	if route, err := planRSSearchRoute(in); err != nil || route.Source != RSSearchSourceCatalogue || route.Group != 0 || route.Full {
		t.Fatal("all-kind full search must stay the bounded all-group preview", route, err)
	}
}

func TestRSSearchRouteGroupSelection(t *testing.T) {
	for _, tc := range []struct {
		group RSSearchGroup
		want  RSSearchSource
	}{
		{0, RSSearchSourceCatalogue},
		{RSSearchLiveTV, RSSearchSourceLive},
		{RSSearchPeople, RSSearchSourcePeople},
		{RSSearchReading, RSSearchSourceCatalogue},
		{21, RSSearchSourceCatalogue},
		{1, RSSearchSourceCatalogue},
		{2, RSSearchSourceCatalogue},
		{4, RSSearchSourceCatalogue},
		{13, RSSearchSourceCatalogue},
	} {
		got, err := RSSearchSourceForGroup(tc.group)
		if err != nil || got != tc.want {
			t.Fatalf("group %d: %v err %v, want %v", tc.group, got, err, tc.want)
		}
	}
	for _, group := range []RSSearchGroup{3, 9, 11, 12, 22, -3, 100} {
		if _, err := RSSearchSourceForGroup(group); !errors.Is(err, ErrRSSearchGroupUnsupported) {
			t.Fatalf("unpublished group %d accepted: %v", group, err)
		}
	}
	for _, tc := range []struct {
		name string
		in   RSSearchRouteInput
		want RSSearchRoute
		err  error
	}{
		{
			name: "live-full",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha"}, Group: RSSearchLiveTV},
			want: RSSearchRoute{Mode: search.ReadFull, Source: RSSearchSourceLive, Group: RSSearchLiveTV, Full: true},
		},
		{
			name: "people-full",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha"}, Group: RSSearchPeople},
			want: RSSearchRoute{Mode: search.ReadFull, Source: RSSearchSourcePeople, Group: RSSearchPeople, Full: true},
		},
		{
			name: "reading-text",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 20}, Text: "alpha"}, ReadingText: true},
			want: RSSearchRoute{Mode: search.ReadFull, Source: RSSearchSourceReadingText, Group: RSSearchReading, Full: true},
		},
		{
			name: "reading-metadata",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 21}, Text: "alpha"}},
			want: RSSearchRoute{Mode: search.ReadFull, Source: RSSearchSourceCatalogue, Group: RSSearchReading, Full: true},
		},
		{
			name: "live-short-refused",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "a"}, Group: RSSearchLiveTV},
			err:  ErrInput,
		},
		{
			name: "reading-text-wrong-group",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}, ReadingText: true},
			err:  ErrInput,
		},
		{
			name: "explicit-group-mismatch",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 2}, Text: "alpha"}, Group: 1},
			err:  ErrInput,
		},
		{
			name: "continuation-needs-full",
			in:   RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "a", FullAfter: &FullSearchCursor{}}},
			err:  ErrInput,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planRSSearchRoute(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	// A selected group without a published source refuses instead of returning
	// an empty successful page.
	if _, err := planRSSearchRoute(RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 3}, Text: "alpha"}}); !errors.Is(err, ErrRSSearchGroupUnsupported) {
		t.Fatal(err)
	}
}

func TestRSSearchRoutesNamedSourcesAndPending(t *testing.T) {
	base := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "a"}}
	var titleKind int64
	routes := NewRSSearchRoutes(func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in BrowseInput, q search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		titleKind = in.Kind
		if q.Mode() != search.ReadTitles {
			t.Fatal("title adapter saw another mode", q.Mode())
		}
		return TitleSearchPage{Disposition: SearchResults, Items: []BrowseItem{{ID: 7, Title: "title"}}}, nil, nil
	}, nil, nil, nil, nil)
	result, cost, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, base)
	if err != nil || cost != nil || result.Route.Source != RSSearchSourceTitles || titleKind != 1 || len(result.Titles) != 1 || result.Titles[0].ID != 7 {
		t.Fatal(result, titleKind, err)
	}

	// Explicit short-context action is a full search. The title source must not
	// be used and short context matches stay on the membership route.
	calledTitle := false
	calledCatalogue := false
	routes = NewRSSearchRoutes(func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		calledTitle = true
		return TitleSearchPage{}, nil, nil
	}, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		calledCatalogue = true
		if in.Group != 1 || !in.Full {
			t.Fatal("wrong explicit context route", in)
		}
		return RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}, nil, nil, nil)
	in := base
	in.ShowMore = true
	result, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || calledTitle || !calledCatalogue || result.Route.Mode != search.ReadFull || len(result.Page.Groups) != 1 {
		t.Fatal(result, calledTitle, calledCatalogue, err)
	}

	// Reading full text uses the accepted-section text adapter, never the
	// catalogue prefix postings.
	catalogueCalled, readingCalled := false, false
	routes = NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		catalogueCalled = true
		return RSSearchPage{}, nil, nil
	}, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in RSSearchRouteRequest) (RSSearchReadingTextPage, *BrowseReadResult, error) {
		readingCalled = true
		if in.Group != RSSearchReading {
			t.Fatal("reading adapter group", in.Group)
		}
		return RSSearchReadingTextPage{Hits: []RSSearchReadingTextHit{{ItemID: 9, SectionPath: "chapter1", SectionIndex: 0, Excerpt: "text", MatchLength: 1}}}, nil, nil
	}, nil, nil)
	result, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 20}, Text: "alpha"}, ReadingText: true})
	if err != nil || catalogueCalled || !readingCalled || len(result.Reading.Hits) != 1 || result.Route.Source != RSSearchSourceReadingText {
		t.Fatal(result, catalogueCalled, readingCalled, err)
	}

	for _, tc := range []struct {
		name   string
		routes *RSSearchRoutes
		in     RSSearchRouteInput
		want   error
	}{
		{"titles", NewRSSearchRoutes(nil, nil, nil, nil, nil), base, ErrRSSearchTitlesPending},
		{"catalogue", NewRSSearchRoutes(nil, nil, nil, nil, nil), RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}}, ErrRSSearchCataloguePending},
		{"reading", NewRSSearchRoutes(nil, nil, nil, nil, nil), RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 20}, Text: "alpha"}, ReadingText: true}, ErrRSSearchReadingTextPending},
		{"live", NewRSSearchRoutes(nil, nil, nil, nil, nil), RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha"}, Group: RSSearchLiveTV}, ErrRSSearchLivePending},
		{"people", NewRSSearchRoutes(nil, nil, nil, nil, nil), RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha"}, Group: RSSearchPeople}, ErrRSSearchPeoplePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := tc.routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, tc.in); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}

	// A cleared query intentionally has no source and is not a missing adapter.
	result, _, err = NewRSSearchRoutes(nil, nil, nil, nil, nil).Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}}})
	if err != nil || result.Route.Source != RSSearchSourceNone || len(result.Titles) != 0 || len(result.Page.Groups) != 0 {
		t.Fatal(result, err)
	}
}

func TestRSSearchRoutesAllGroupsAndPerGroupErrors(t *testing.T) {
	catalogue := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		if in.Group != 0 || in.Full {
			t.Fatal("all-group route", in)
		}
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	routes := NewRSSearchRoutes(nil, catalogue, nil, nil, nil)
	result, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Page.Groups) != 1 || len(result.Pending) != 1 || result.Pending[0] != RSSearchLiveTV {
		t.Fatal("nil Live adapter must be named pending, not an empty group", result, err)
	}

	live := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: []FullSearchHit{{Subject: search.ManifestEntity, ID: 90, Kind: 90, Item: BrowseItem{ID: 90, Title: "channel"}}}}, nil, nil
	}
	routes = NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	result, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Page.Groups) != 2 || result.Page.Groups[1].Group != RSSearchLiveTV || len(result.Pending) != 0 {
		t.Fatal(result, err)
	}

	// One owner failure is a named per-group error; the catalogue page stands.
	live = func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		return RSSearchGroupPage{}, nil, ErrDenied
	}
	routes = NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	result, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Errors) != 1 || result.Errors[0].Group != RSSearchLiveTV || !errors.Is(result.Errors[0].Err, ErrDenied) || len(result.Page.Groups) != 1 {
		t.Fatal(result, err)
	}

	// A selected group keeps its error attached to that group.
	routes = NewRSSearchRoutes(nil, nil, nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		return RSSearchGroupPage{}, nil, ErrDenied
	}, nil)
	_, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha"}, Group: RSSearchLiveTV})
	var grouped *RSSearchGroupError
	if !errors.As(err, &grouped) || grouped.Group != RSSearchLiveTV || !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}

func TestRSSearchRoutesCancellationGenerationAndBudget(t *testing.T) {
	// A virtual clock makes the group deadline exact: the route derives it from
	// time.Now inside the bubble, so elapsed real time cannot leak into the
	// assertion.
	synctest.Test(t, func(t *testing.T) {
		in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}}
		var calls int
		routes := NewRSSearchRoutes(nil, func(ctx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok || deadline.After(time.Now().Add(RSSearchGroupBudget)) {
				t.Fatal("group budget was not applied")
			}
			<-ctx.Done()
			return RSSearchPage{}, nil, ctx.Err()
		}, nil, nil, nil)

		// Cancellation before preparation opens no adapter.
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		cancel()
		if _, _, err := routes.Read(ctx, governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatal(err, calls)
		}

		// Cancellation observed after an adapter result suppresses that result.
		// The adapter cancels the *second* context, so name it separately: vet
		// cannot follow a cancel reachable only from a callback.
		in = RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}}
		ctx, cancelResult := context.WithCancel(t.Context())
		t.Cleanup(cancelResult)
		routes = NewRSSearchRoutes(nil, func(ctx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			calls++
			cancelResult()
			return RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
		}, nil, nil, nil)
		result, _, err := routes.Read(ctx, governor.Request{}, time.Second, BrowseOutput{}, in)
		if !errors.Is(err, context.Canceled) || len(result.Page.Groups) != 0 {
			t.Fatal("cancelled result escaped", result, err)
		}

		// The request-generation guard refuses before and after preparation.
		calls = 0
		current := false
		routes = NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			calls++
			return RSSearchPage{Complete: true}, nil, nil
		}, nil, nil, nil)
		in.Current = func() bool { return current }
		if _, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrSearchSuperseded) || calls != 0 {
			t.Fatal(err, calls)
		}
		current = true
		calls = 0
		guard := func() bool { return calls == 0 }
		in.Current = guard
		if _, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, ErrSearchSuperseded) || calls != 1 {
			t.Fatal("superseded prepared result escaped", err, calls)
		}

		// The caller's shorter budget wins and the 600ms group ceiling still holds.
		for _, budget := range []time.Duration{100 * time.Millisecond, 5 * time.Second} {
			start := time.Now()
			var got time.Duration
			routes = NewRSSearchRoutes(nil, func(ctx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("adapter has no deadline")
				}
				got = deadline.Sub(start)
				return RSSearchPage{Complete: true}, nil, nil
			}, nil, nil, nil)
			in.Current = nil
			if _, _, err := routes.Read(t.Context(), governor.Request{}, budget, BrowseOutput{}, in); err != nil {
				t.Fatal(err)
			}
			if got <= 0 || got > min(budget, RSSearchGroupBudget) {
				t.Fatal("wrong group deadline", got, budget)
			}
		}
	})
}

func TestRSSearchRoutesBoundedPages(t *testing.T) {
	emptyOK := func(n int) []FullSearchHit {
		hits := make([]FullSearchHit, n)
		for i := range hits {
			hits[i] = rsRouteHit(1, int64(i+1))
		}
		return hits
	}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}}
	for _, tc := range []struct {
		name  string
		page  RSSearchPage
		valid bool
	}{
		{"selected-forty", RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: emptyOK(40)}}}, true},
		{"selected-forty-one", RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: emptyOK(41)}}}, false},
		{"wrong-group", RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 2, Hits: emptyOK(1)}}}, false},
		{"both-pending-and-complete", RSSearchPage{Pending: true, Complete: true}, false},
		{"neither-state", RSSearchPage{}, false},
		{"pending-with-prefix", RSSearchPage{Pending: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: emptyOK(1)}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := tc.page
			routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
				return page, nil, nil
			}, nil, nil, nil)
			_, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, ErrInput) {
				t.Fatal(err)
			}
		})
	}
	// The all-group preview is eight cards per group, not the selected forty.
	allIn := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	for _, n := range []int{8, 9} {
		page := RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: emptyOK(n)}}}
		routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			return page, nil, nil
		}, nil, nil, nil)
		_, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, allIn)
		if n == 8 && err != nil || n == 9 && !errors.Is(err, ErrInput) {
			t.Fatal(n, err)
		}
	}
	// Title discovery keeps its twenty-title bound and never carries items on a
	// pending page.
	titles := make([]BrowseItem, MaxSearchTitles+1)
	for i := range titles {
		titles[i] = BrowseItem{ID: int64(i + 1), Title: "t"}
	}
	titleIn := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "a"}}
	for _, page := range []TitleSearchPage{
		{Disposition: SearchResults, Items: titles},
		{Disposition: SearchPending, Items: titles[:1]},
	} {
		routes := NewRSSearchRoutes(func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
			return page, nil, nil
		}, nil, nil, nil, nil)
		if _, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, titleIn); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
	// Reading text pages are bounded and a pending page has no prefix.
	readingIn := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 20}, Text: "alpha"}, ReadingText: true}
	readingHit := RSSearchReadingTextHit{ItemID: 1, SectionPath: "s", Excerpt: "x", MatchLength: 1}
	readingHits := func(n int) []RSSearchReadingTextHit {
		hits := make([]RSSearchReadingTextHit, n)
		for i := range hits {
			hits[i] = readingHit
			hits[i].ItemID = int64(i + 1)
		}
		return hits
	}
	for _, tc := range []struct {
		page  RSSearchReadingTextPage
		valid bool
	}{
		{RSSearchReadingTextPage{Hits: readingHits(40)}, true},
		{RSSearchReadingTextPage{Hits: readingHits(41)}, false},
		{RSSearchReadingTextPage{Pending: true, Hits: []RSSearchReadingTextHit{readingHit}}, false},
	} {
		page := tc.page
		routes := NewRSSearchRoutes(nil, nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchReadingTextPage, *BrowseReadResult, error) {
			return page, nil, nil
		}, nil, nil)
		_, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, readingIn)
		if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}

	// An over-budget or invalid query never reaches a source.
	routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		t.Error("invalid query opened a source")
		return RSSearchPage{}, nil, nil
	}, nil, nil, nil)
	if _, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: strings.Repeat("x", 513)}}); !errors.Is(err, search.ErrQuery) {
		t.Fatal(err)
	}
}

func rsRouteHit(group RSSearchGroup, id int64) FullSearchHit {
	h := FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: 1, Kind: int64(group), Item: BrowseItem{ID: id, Title: "hit"}}
	if group == RSSearchPeople {
		h.Subject = search.ManifestPerson
		h.Kind = 0
		h.Tier = 3
		h.Item = BrowseItem{}
		h.PersonName = "person"
	}
	return h
}

func rsRouteLiveHits(n int) []FullSearchHit {
	hits := make([]FullSearchHit, n)
	for i := range hits {
		hits[i] = rsRouteHit(RSSearchLiveTV, int64(i+1))
	}
	return hits
}

// An all-group Live preview is the first page of the Live group: it receives the
// caller's view and planned query, and never the catalogue's continuation
// cursor.
func assertAllGroupLiveRoute(t *testing.T, in SearchInput, got RSSearchRouteRequest) {
	t.Helper()
	planned, err := search.PlanQuery(in.Text, in.ShowMore)
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != planned {
		t.Fatalf("all-group Live query %v, want %v", got.Query, planned)
	}
	if !reflect.DeepEqual(got.View, in.View) {
		t.Fatalf("all-group Live view %+v, want %+v", got.View, in.View)
	}
	if got.Group != RSSearchLiveTV {
		t.Fatalf("all-group Live group %d", got.Group)
	}
	if got.After != nil {
		t.Fatalf("all-group Live preview received a catalogue cursor %v", got.After)
	}
}

// Each group starts its own bounded window: Live must not be charged the time
// catalogue already spent inside the same 600ms ceiling.
func TestRSSearchRoutesAllGroupLiveGetsItsOwnGroupDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
		routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			time.Sleep(500 * time.Millisecond)
			return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
		}, nil, func(ctx context.Context, _ governor.Request, budget time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
			assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("Live group has no deadline")
			}
			if span := deadline.Sub(time.Now()); span < 550*time.Millisecond || span > RSSearchGroupBudget || budget < 550*time.Millisecond {
				t.Fatalf("Live group inherited catalogue's spent budget: span %v budget %v", span, budget)
			}
			return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
		}, nil)
		result, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
		if err != nil || len(result.Page.Groups) != 2 {
			t.Fatal(result, err)
		}
	})
}

// A real foreground read scope authorizes exactly ONE public read operation. The
// all-group route must therefore hand Live its own output grant: reusing the
// catalogue's output makes every real MG Live adapter fail with governor.ErrGrant.
func TestRSSearchRoutesAllGroupLiveGetsItsOwnOutput(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("rs-route-live-output")
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	var grants []*governor.Grant
	// Both fakes perform the same single-use begin a real public reader does, so
	// a shared output is refused instead of being silently tolerated.
	begin := func(ctx context.Context, req governor.Request, out BrowseOutput) error {
		if out.live == nil {
			t.Fatal("route did not pass foreground custody")
		}
		if err := out.live.begin(ctx, out, req, f.snapshots); err != nil {
			return err
		}
		out.live.reads.Done()
		grants = append(grants, out.Grant)
		return nil
	}
	catalogue := func(ctx context.Context, req governor.Request, _ time.Duration, out BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		if err := begin(ctx, req, out); err != nil {
			return RSSearchPage{}, nil, err
		}
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	live := func(ctx context.Context, req governor.Request, _ time.Duration, out BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		if err := begin(ctx, req, out); err != nil {
			return RSSearchGroupPage{}, nil, err
		}
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, &BrowseReadResult{LookupStatements: 1}, nil
	}
	routes := NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	var result RSSearchRoutesResult
	var cost *BrowseReadResult
	consumption, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, custody BrowseOutput) error {
		var e error
		result, cost, e = routes.Read(ctx, request, time.Second, custody, in)
		return e
	})
	if err != nil || consumption.Refusal != nil {
		t.Fatal(err, consumption.Refusal)
	}
	if len(result.Page.Groups) != 2 || result.Page.Groups[1].Group != RSSearchLiveTV || len(result.Errors) != 0 {
		t.Fatal("Live group did not survive its own output", result, result.Errors)
	}
	if len(grants) != 2 || grants[0] == nil || grants[1] == nil || grants[0] == grants[1] {
		t.Fatal("each source must hold its own output grant", grants)
	}
	if cost == nil || cost.LookupStatements != 1 {
		t.Fatal("Live cost missing from the composite", cost)
	}
	assertLiveUsage(t, f, 0, 0)
}

// The all-group preview is eight cards per group. A Live adapter that returns a
// selected-group-sized (or any over-limit) page is refused by name, never
// silently trimmed into an exact-looking answer.
func TestRSSearchRoutesAllGroupLivePreviewLimit(t *testing.T) {
	catalogue := func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	for _, tc := range []struct {
		hits int
		ok   bool
	}{
		{hits: 8, ok: true},
		{hits: 9},
		{hits: 40},
	} {
		page := RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(tc.hits)}
		routes := NewRSSearchRoutes(nil, catalogue, nil, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
			assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
			return page, nil, nil
		}, nil)
		result, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
		if err != nil {
			t.Fatal(tc.hits, err)
		}
		if tc.ok {
			if len(result.Page.Groups) != 2 || len(result.Errors) != 0 {
				t.Fatal(tc.hits, result, result.Errors)
			}
			continue
		}
		if len(result.Page.Groups) != 1 || len(result.Errors) != 1 ||
			result.Errors[0].Group != RSSearchLiveTV || !errors.Is(result.Errors[0].Err, ErrRSSearchPreviewOverflow) {
			t.Fatal(tc.hits, result, result.Errors)
		}
	}
}

// A pending catalogue page carries neither groups nor a Live prefix: pending
// with a group is exactly the shape rssValidateRSSearchPage refuses.
func TestRSSearchRoutesAllGroupPendingCatalogue(t *testing.T) {
	cost := &BrowseReadResult{LookupStatements: 2}
	liveCalled := false
	routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Pending: true}, cost, nil
	}, nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		liveCalled = true
		return RSSearchGroupPage{Group: RSSearchLiveTV}, nil, nil
	}, nil)
	result, got, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}})
	if err != nil || got != cost || liveCalled {
		t.Fatal(err, got, liveCalled)
	}
	if !result.Page.Pending || result.Page.Complete || len(result.Page.Groups) != 0 || len(result.Pending) != 0 {
		t.Fatal("pending all-group page must carry no groups", result)
	}
}

// Live's cost is part of the composite: dropping the second source's windows,
// statements or refusal would under-report the read and hide a governor refusal.
func TestRSSearchRoutesAllGroupLiveCost(t *testing.T) {
	window := governor.SnapshotResult{Started: time.Now()}
	refusal := &governor.Refusal{Code: governor.Capacity}
	catalogueCost := &BrowseReadResult{LookupStatements: 2, PublicationStatements: 3, Attempts: 1, LookupWindows: []governor.SnapshotResult{window}}
	liveCost := &BrowseReadResult{LookupStatements: 5, PublicationStatements: 7, Attempts: 3, LookupWindows: []governor.SnapshotResult{window, window}, PublicationWindows: []governor.SnapshotResult{window}, Refusal: refusal}
	page := RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, catalogueCost, nil
	}, nil, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		return page, liveCost, nil
	}, nil)
	result, cost, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Page.Groups) != 2 {
		t.Fatal(err, result)
	}
	if cost.LookupStatements != 7 || cost.PublicationStatements != 10 || cost.Attempts != 3 ||
		len(cost.LookupWindows) != 3 || len(cost.PublicationWindows) != 1 || cost.Refusal != refusal {
		t.Fatalf("composite cost dropped Live work: %+v", cost)
	}

	// A failing Live source still contributes its cost and refusal.
	cost.Refusal = nil
	liveCost.Refusal = refusal
	routes = NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Complete: true, Partial: true}, &BrowseReadResult{LookupStatements: 1}, nil
	}, nil, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		return RSSearchGroupPage{}, liveCost, governor.ErrBusy
	}, nil)
	result, cost, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Errors) != 1 || !errors.Is(result.Errors[0].Err, governor.ErrBusy) {
		t.Fatal(err, result.Errors)
	}
	if cost.LookupStatements != 6 || cost.Refusal != refusal {
		t.Fatalf("failed group cost dropped: %+v", cost)
	}
}

// A request generation that moves on while Live runs cannot let its group escape
// with the catalogue page.
func TestRSSearchRoutesAllGroupSupersededAfterLive(t *testing.T) {
	calls := 0
	liveCalled := false
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}, nil, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		liveCalled = true
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
	}, nil)
	in.Current = func() bool { calls++; return calls <= 2 }
	result, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrSearchSuperseded) || !liveCalled || len(result.Page.Groups) != 0 {
		t.Fatal("superseded Live group escaped", result, err)
	}
}

// The full-mode catalogue cursor is forwarded, and sources with no published
// continuation refuse it by name instead of silently ignoring it.
func TestRSSearchRoutesForwardCursorAndRefuseUnsupported(t *testing.T) {
	cursor := &FullSearchCursor{query: "alpha"}
	var got *FullSearchCursor
	routes := NewRSSearchRoutes(nil, func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		got = in.After
		return RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}, nil, nil, nil)
	if _, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha", FullAfter: cursor}}); err != nil || got != cursor {
		t.Fatal("catalogue cursor not forwarded", got, err)
	}
	for _, tc := range []struct {
		name string
		in   RSSearchRouteInput
	}{
		{"reading-text", RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 20}, Text: "alpha", FullAfter: cursor}, ReadingText: true}},
		{"live", RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha", FullAfter: cursor}, Group: RSSearchLiveTV}},
		{"people", RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{}, Text: "alpha", FullAfter: cursor}, Group: RSSearchPeople}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := planRSSearchRoute(tc.in); !errors.Is(err, ErrRSSearchContinuationUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

// Neither the caller's request/output nor the planned query/AFTER may be zeroed
// on the way to a source; each fake records exactly what it was given.
func TestRSSearchRoutesForwardRequestOutputAndQuery(t *testing.T) {
	request := governor.Request{Key: governor.JobKey{Domain: 7, ID: "forward"}, Owner: 3, Class: governor.Foreground, Deadline: time.Now().Add(time.Minute)}
	grant := &governor.Grant{}
	output := BrowseOutput{Authority: nil, Grant: grant, Request: governor.Request{Key: governor.JobKey{Domain: 8, ID: "output"}, Owner: 3, Class: governor.Foreground, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: request.Deadline}}
	view := BrowseInput{Database: "db", TokenHash: "token", Library: 1, Kind: 1, Limit: 8}
	planned, err := search.PlanQuery("alpha", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		in      RSSearchRouteInput
		group   RSSearchGroup
		source  RSSearchSource
		cursor  *FullSearchCursor
		wantErr error
	}{
		{name: "catalogue", in: RSSearchRouteInput{SearchInput: SearchInput{View: view, Text: "alpha"}}, group: 1, source: RSSearchSourceCatalogue},
		{name: "catalogue-after", in: RSSearchRouteInput{SearchInput: SearchInput{View: view, Text: "alpha", FullAfter: &FullSearchCursor{}}}, group: 1, source: RSSearchSourceCatalogue},
		{name: "reading-text", in: RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Database: "db", TokenHash: "token", Library: 1, Kind: 20, Limit: 8}, Text: "alpha"}, ReadingText: true}, group: RSSearchReading, source: RSSearchSourceReadingText},
		{name: "live", in: RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Database: "db", TokenHash: "token", Library: 1, Limit: 8}, Text: "alpha"}, Group: RSSearchLiveTV}, group: RSSearchLiveTV, source: RSSearchSourceLive},
		{name: "people", in: RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Database: "db", TokenHash: "token", Library: 1, Limit: 8}, Text: "alpha"}, Group: RSSearchPeople}, group: RSSearchPeople, source: RSSearchSourcePeople},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sawReq governor.Request
			var sawOut BrowseOutput
			var sawAfter *FullSearchCursor
			check := func(req governor.Request, out BrowseOutput, in RSSearchRouteRequest) {
				sawReq, sawOut, sawAfter = req, out, in.After
				if !reflect.DeepEqual(req, request) {
					t.Fatal("request zeroed or replaced", req)
				}
				if !reflect.DeepEqual(out, output) {
					t.Fatal("output zeroed or replaced", out)
				}
				if in.Query != planned || in.Group != tc.group || in.View.Kind != tc.in.View.Kind || !in.Full {
					t.Fatal("route request incomplete", in, in.Query.Mode())
				}
				wantAfter := tc.in.FullAfter
				if in.After != wantAfter {
					t.Fatal("AFTER cursor dropped", in.After, wantAfter)
				}
			}
			catalogue := func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
				check(req, out, in)
				return RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: in.Group, Hits: []FullSearchHit{rsRouteHit(in.Group, 1)}}}}, nil, nil
			}
			reading := func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in RSSearchRouteRequest) (RSSearchReadingTextPage, *BrowseReadResult, error) {
				check(req, out, in)
				return RSSearchReadingTextPage{Hits: []RSSearchReadingTextHit{{ItemID: 1, SectionPath: "s", Excerpt: "x", MatchLength: 1}}}, nil, nil
			}
			live := func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
				check(req, out, in)
				return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
			}
			people := func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
				check(req, out, in)
				return RSSearchGroupPage{Group: RSSearchPeople, Hits: []FullSearchHit{rsRouteHit(RSSearchPeople, 1)}}, nil, nil
			}
			routes := NewRSSearchRoutes(nil, catalogue, reading, live, people)
			result, _, err := routes.Read(t.Context(), request, time.Second, output, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if result.Route.Source != tc.source || result.Route.Group != tc.group {
				t.Fatal(result.Route)
			}
			if sawReq.Key != request.Key || sawOut.Grant != grant || sawAfter != tc.in.FullAfter {
				t.Fatal("source saw a zeroed request/output/AFTER", sawReq, sawOut, sawAfter)
			}
		})
	}
}

// The catalogue's full-search cursor belongs to the catalogue section only. An
// all-group request may forward it to the catalogue, but the Live preview is
// always its first page: forwarding the cursor would hand Live a catalogue
// *FullSearchCursor. Live must still receive the caller's view and query.
func TestRSSearchRoutesAllGroupLiveIgnoresCatalogueCursor(t *testing.T) {
	cursor := &FullSearchCursor{query: "alpha"}
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Database: "db", TokenHash: "token", Library: 1}, Text: "alpha", FullAfter: cursor}}
	var catalogueAfter *FullSearchCursor
	var liveAfter *FullSearchCursor
	var liveView BrowseInput
	var liveQuery search.Query
	var liveGroup RSSearchGroup
	catalogue := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, catalogueIn RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		catalogueAfter = catalogueIn.After
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	live := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		liveAfter, liveView, liveQuery, liveGroup = liveIn.After, liveIn.View, liveIn.Query, liveIn.Group
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
	}
	routes := NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	result, _, err := routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Page.Groups) != 2 || len(result.Errors) != 0 {
		t.Fatal(result, err)
	}
	if catalogueAfter != cursor {
		t.Fatal("catalogue did not receive its own cursor", catalogueAfter)
	}
	if liveAfter != nil {
		t.Fatalf("all-group Live received the catalogue cursor: %+v", liveAfter)
	}
	if liveGroup != RSSearchLiveTV || liveView != in.View || liveQuery.Mode() != search.ReadFull {
		t.Fatalf("all-group Live request lost identity: group %d view %+v query %v", liveGroup, liveView, liveQuery.Mode())
	}
}

// A per-source sub-deadline is capped by both the caller's request deadline and
// the caller's context deadline, for the catalogue as well as the all-group Live
// source. The virtual clock keeps the exact min() observable.
func TestRSSearchRoutesSourceDeadlineCaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
		check := func(name string, requestDeadline time.Time, ctx context.Context, want time.Time) {
			t.Helper()
			var catalogueDeadline, liveDeadline time.Time
			routes := NewRSSearchRoutes(nil, func(groupCtx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
				var ok bool
				if catalogueDeadline, ok = groupCtx.Deadline(); !ok {
					t.Fatal(name, ": catalogue group has no deadline")
				}
				return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
			}, nil, func(groupCtx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
				assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
				var ok bool
				if liveDeadline, ok = groupCtx.Deadline(); !ok {
					t.Fatal(name, ": Live group has no deadline")
				}
				return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
			}, nil)
			if _, _, err := routes.Read(ctx, governor.Request{Deadline: requestDeadline}, time.Second, BrowseOutput{}, in); err != nil {
				t.Fatal(name, err)
			}
			if !catalogueDeadline.Equal(want) || !liveDeadline.Equal(want) {
				t.Fatalf("%s: catalogue %v Live %v, want both capped at %v", name, catalogueDeadline, liveDeadline, want)
			}
		}
		requestCap := time.Now().Add(150 * time.Millisecond)
		check("request-deadline", requestCap, t.Context(), requestCap)
		ctxCap := time.Now().Add(90 * time.Millisecond)
		ctx, cancel := context.WithDeadline(t.Context(), ctxCap)
		defer cancel()
		check("context-deadline", time.Time{}, ctx, ctxCap)
	})
}

// Parent cancellation must reach the in-flight Live source, not merely fail the
// next generation check.
func TestRSSearchRoutesAllGroupParentCancelStopsLive(t *testing.T) {
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	routes := NewRSSearchRoutes(nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}, nil, func(liveCtx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		close(started)
		<-liveCtx.Done()
		return RSSearchGroupPage{}, nil, liveCtx.Err()
	}, nil)
	type outcome struct {
		result RSSearchRoutesResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, _, err := routes.Read(ctx, governor.Request{}, time.Second, BrowseOutput{}, in)
		done <- outcome{result: result, err: err}
	}()
	<-started
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) {
		t.Fatal("cancelled parent did not stop the read", got.err)
	}
	if len(got.result.Page.Groups) != 0 || len(got.result.Errors) != 0 || len(got.result.Pending) != 0 {
		t.Fatal("cancelled Live group escaped", got.result)
	}
}

// A failing Live source under real foreground custody must still retire its own
// output grant and reads: the ledger returns exactly to its baseline.
func TestRSSearchRoutesAllGroupLiveErrorRetiresCustody(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("rs-route-live-error")
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	var grants []*governor.Grant
	begin := func(ctx context.Context, req governor.Request, out BrowseOutput) error {
		if out.live == nil {
			t.Fatal("route did not pass foreground custody")
		}
		if err := out.live.begin(ctx, out, req, f.snapshots); err != nil {
			return err
		}
		out.live.reads.Done()
		grants = append(grants, out.Grant)
		return nil
	}
	catalogue := func(ctx context.Context, req governor.Request, _ time.Duration, out BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		if err := begin(ctx, req, out); err != nil {
			return RSSearchPage{}, nil, err
		}
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	live := func(ctx context.Context, req governor.Request, _ time.Duration, out BrowseOutput, liveIn RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		assertAllGroupLiveRoute(t, in.SearchInput, liveIn)
		if err := begin(ctx, req, out); err != nil {
			return RSSearchGroupPage{}, nil, err
		}
		return RSSearchGroupPage{}, &BrowseReadResult{LookupStatements: 2}, ErrDenied
	}
	routes := NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	var result RSSearchRoutesResult
	var cost *BrowseReadResult
	consumption, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, custody BrowseOutput) error {
		var e error
		result, cost, e = routes.Read(ctx, request, time.Second, custody, in)
		return e
	})
	if err != nil || consumption.Refusal != nil {
		t.Fatal(err, consumption.Refusal)
	}
	if len(result.Page.Groups) != 1 || result.Page.Groups[0].Group != 1 || len(result.Errors) != 1 ||
		result.Errors[0].Group != RSSearchLiveTV || !errors.Is(result.Errors[0].Err, ErrDenied) {
		t.Fatal("failing Live group did not stay a per-group error", result, result.Errors)
	}
	if len(grants) != 2 || grants[0] == nil || grants[1] == nil || grants[0] == grants[1] {
		t.Fatal("each source must hold its own output grant", grants)
	}
	if cost == nil || cost.LookupStatements != 2 {
		t.Fatal("failed Live cost missing from the composite", cost)
	}
	assertLiveUsage(t, f, 0, 0)
}

// A full Foreground ledger refuses the derived Live output as a named per-group
// error, propagates the admission refusal into the composite cost, and leaves
// the catalogue groups standing.
func TestRSSearchRoutesAllGroupLiveDeriveBusy(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, request, output := liveRequests("rs-route-live-busy")
	in := RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 0}, Text: "alpha"}}
	var filler []*governor.Grant
	retire := func() {
		for _, grant := range filler {
			if err := f.authority.Retired(grant); err != nil {
				t.Error(err)
			}
		}
		filler = nil
	}
	liveCalled := false
	catalogue := func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
		// The parent already holds LIVE and output, so fourteen zero-need grants
		// reach the fixture's MaxGrantsPerClass without touching the memory
		// partition.
		for len(filler) < 14 {
			decision := f.governor.Try(governor.Request{
				Key: governor.JobKey{Domain: 75, ID: "rs-route-busy-filler"}, Owner: 1,
				Class: governor.Foreground, Deadline: time.Now().Add(time.Minute),
			})
			if decision.Refusal != nil || decision.Grant == nil {
				t.Fatalf("filler admission refused: %+v", decision.Refusal)
			}
			filler = append(filler, decision.Grant)
		}
		return RSSearchPage{Complete: true, Partial: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
	}
	routes := NewRSSearchRoutes(nil, catalogue, nil, func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		liveCalled = true
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: rsRouteLiveHits(1)}, nil, nil
	}, nil)
	var result RSSearchRoutesResult
	var cost *BrowseReadResult
	consumption, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, custody BrowseOutput) error {
		var e error
		result, cost, e = routes.Read(ctx, request, time.Second, custody, in)
		return e
	})
	if err != nil || consumption.Refusal != nil {
		t.Fatal(err, consumption.Refusal)
	}
	if liveCalled {
		t.Fatal("Live ran without its own output grant")
	}
	if len(result.Page.Groups) != 1 || result.Page.Groups[0].Group != 1 {
		t.Fatal("catalogue groups did not survive the Live refusal", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Group != RSSearchLiveTV || !errors.Is(result.Errors[0].Err, governor.ErrBusy) {
		t.Fatal("derive refusal was not a named per-group Live error", result.Errors)
	}
	if cost == nil || cost.Refusal == nil || cost.Refusal.Code != governor.Capacity || cost.Refusal.RetryAt.IsZero() {
		t.Fatalf("derive refusal not propagated into cost: %+v", cost)
	}
	retire()
	assertLiveUsage(t, f, 0, 0)
}

// The derived output never outlives the parent output it was minted from, even
// when the group window and the protected request are longer.
func TestRSSearchRoutesDerivedOutputDeadlineClamped(t *testing.T) {
	f, _ := newLiveFixture(t)
	q, _, output := liveRequests("rs-route-derived-deadline")
	output.Deadline = time.Now().Add(150 * time.Millisecond)
	var derivedDeadline time.Time
	var derivedGrant *governor.Grant
	consumption, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, custody BrowseOutput) error {
		derived, refusal, release, err := custody.live.derive(ctx, governor.Request{
			Key: governor.JobKey{Domain: 76, ID: "rs-route-derived"}, Owner: output.Owner,
			Class: governor.Foreground, Need: governor.Amounts{governor.MemoryBytes: 8 << 20},
			Deadline: time.Now().Add(2 * time.Second),
		})
		if err != nil || refusal != nil {
			return errors.Join(err, governor.ErrBusy)
		}
		derivedDeadline, derivedGrant = derived.Request.Deadline, derived.Grant
		return release()
	})
	if err != nil || consumption.Refusal != nil {
		t.Fatal(err, consumption.Refusal)
	}
	if !derivedDeadline.Equal(output.Deadline) {
		t.Fatalf("derived deadline %v not clamped to parent output %v", derivedDeadline, output.Deadline)
	}
	if derivedGrant == nil {
		t.Fatal("derive returned no output grant")
	}
	assertLiveUsage(t, f, 0, 0)
}
