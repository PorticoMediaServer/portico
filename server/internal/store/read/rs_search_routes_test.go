package read

import (
	"context"
	"errors"
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

	live := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
		if in.Group != RSSearchLiveTV {
			t.Fatal("live group", in.Group)
		}
		return RSSearchGroupPage{Group: RSSearchLiveTV, Hits: []FullSearchHit{{Subject: search.ManifestEntity, ID: 90, Kind: 90, Item: BrowseItem{ID: 90, Title: "channel"}}}}, nil, nil
	}
	routes = NewRSSearchRoutes(nil, catalogue, nil, live, nil)
	result, _, err = routes.Read(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if err != nil || len(result.Page.Groups) != 2 || result.Page.Groups[1].Group != RSSearchLiveTV || len(result.Pending) != 0 {
		t.Fatal(result, err)
	}

	// One owner failure is a named per-group error; the catalogue page stands.
	live = func(context.Context, governor.Request, time.Duration, BrowseOutput, RSSearchRouteRequest) (RSSearchGroupPage, *BrowseReadResult, error) {
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
		cancel()
		if _, _, err := routes.Read(ctx, governor.Request{}, time.Second, BrowseOutput{}, in); !errors.Is(err, context.Canceled) || calls != 0 {
			t.Fatal(err, calls)
		}

		// Cancellation observed after an adapter result suppresses that result.
		in = RSSearchRouteInput{SearchInput: SearchInput{View: BrowseInput{Kind: 1}, Text: "alpha"}}
		routes = NewRSSearchRoutes(nil, func(ctx context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, _ RSSearchRouteRequest) (RSSearchPage, *BrowseReadResult, error) {
			calls++
			cancel()
			return RSSearchPage{Complete: true, Groups: []RSSearchGroupPage{{Group: 1, Hits: []FullSearchHit{rsRouteHit(1, 1)}}}}, nil, nil
		}, nil, nil, nil)
		ctx, cancel = context.WithCancel(t.Context())
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
