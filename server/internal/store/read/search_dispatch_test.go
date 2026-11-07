package read

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// These controls use real GV admission/custody, but SYNTHETIC title outcomes.
// No native title/FTS executor or source/authority/ready qualification is implied.
func searchDispatcherFixture(t *testing.T, capacity int, titles TitleSearchRead) (*browseFixture, *search.SearchRequests, *SearchDispatcher) {
	t.Helper()
	f, _ := newLiveFixture(t)
	requests, err := search.NewSearchRequests(capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(requests.Close)
	d, err := NewSearchDispatcher(requests, f.governor, f.authority, f.snapshots, titles)
	if err != nil {
		t.Fatal(err)
	}
	return f, requests, d
}

func dispatcherInput(f *browseFixture, text string) SearchInput {
	v := f.input
	v.Limit = MaxSearchTitles
	return SearchInput{View: v, Session: "session", Text: text}
}

func TestSearchDispatcherModesAndTypedOutcomes(t *testing.T) {
	synctest.Test(t, testSearchDispatcherModesAndTypedOutcomes)
}

func testSearchDispatcherModesAndTypedOutcomes(t *testing.T) {
	var calls int
	f, _, d := searchDispatcherFixture(t, 2, nil)
	d.titles = func(ctx context.Context, purpose governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, q search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		calls++
		if q.Mode() != search.ReadTitles || q.Fold() != "a" || budget <= 0 {
			t.Error("wrong title dispatch")
		}
		assertLiveUsage(t, f, 1, 0)
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(purpose.Deadline) {
			t.Error("original read deadline lost")
		}
		return TitleSearchPage{Disposition: SearchPending}, nil, nil
	}
	for _, tc := range []struct {
		text   string
		more   bool
		mode   search.ReadMode
		state  SearchDisposition
		reason SearchReason
	}{
		{"", false, search.ReadNothing, SearchResults, SearchNoReason},
		{"a", false, search.ReadTitles, SearchPending, SearchSourcePending},
		{"alpha", false, search.ReadFull, SearchUnavailable, SearchFullModeUnsupported},
		{"a", true, search.ReadFull, SearchUnavailable, SearchFullModeUnsupported},
	} {
		q, r, o := liveRequests("dispatch-" + tc.text)
		in := dispatcherInput(f, tc.text)
		in.ShowMore = tc.more
		published := false
		result, err := d.Consume(t.Context(), q, r, time.Second, o, in, func(ctx context.Context, response SearchResponse) (func(context.Context) error, error) {
			assertLiveUsage(t, f, 1, 0)
			if response.Mode != tc.mode || response.Disposition != tc.state || response.Reason != tc.reason || len(response.Items) != 0 || response.Generation == 0 {
				t.Error("wrong typed response", response)
			}
			return func(context.Context) error { assertLiveUsage(t, f, 1, 0); published = true; return nil }, nil
		})
		if err != nil || !published || !result.Published || result.Read == nil || result.Read.LookupStatements != 5 || result.Read.PublicationStatements != 5 || len(result.Read.LookupWindows) != 1 || len(result.Read.PublicationWindows) != 1 || result.Disposition != tc.state || result.Consumption == nil || result.Read.AdmissionElapsed != result.Consumption.AdmissionElapsed || result.Consumption.AdmissionElapsed < 0 || result.Consumption.Elapsed < result.Consumption.AdmissionElapsed || result.Elapsed < result.Consumption.Elapsed {
			t.Fatal(result, err)
		}
		assertLiveUsage(t, f, 0, 0)
	}
	if calls != 1 {
		t.Fatal("full mode approximated by title source", calls)
	}
	d.titles = nil
	q, r, o := liveRequests("missing-title-source")
	result, err := d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(_ context.Context, p SearchResponse) (func(context.Context) error, error) {
		if p.Disposition != SearchUnavailable || p.Reason != SearchTitleSourceUnavailable {
			t.Error(p)
		}
		return func(context.Context) error { return nil }, nil
	})
	if err != nil || !result.Published {
		t.Fatal(result, err)
	}
	items := []BrowseItem{{ID: 1, Title: "synthetic"}}
	d.titles = func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		return TitleSearchPage{Disposition: SearchResults, Items: items}, nil, nil
	}
	q, r, o = liveRequests("owned-title-page")
	result, err = d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(_ context.Context, p SearchResponse) (func(context.Context) error, error) {
		items[0].Title = "source reused its slice"
		if p.Disposition != SearchResults || len(p.Items) != 1 || p.Items[0].Title != "synthetic" {
			t.Error("title response aliases source page", p)
		}
		return func(context.Context) error { return nil }, nil
	})
	if err != nil || !result.Published {
		t.Fatal(result, err)
	}
	assertLiveUsage(t, f, 0, 0)

	d.titles = func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		t.Error("title source entered without LIVE admission")
		return TitleSearchPage{}, nil, nil
	}
	q, r, o = liveRequests("expired-admission")
	q.ReadyDeadline = time.Now().Add(-time.Second)
	result, err = d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
		t.Error("refused admission reached payload consumer")
		return nil, nil
	})
	if !errors.Is(err, governor.ErrBusy) || result.Published || result.Consumption == nil || result.Consumption.Refusal == nil {
		t.Fatal(result, err)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestSearchDispatcherSuppressesAfterEncodingAndRetainsCustody(t *testing.T) {
	synctest.Test(t, testSearchDispatcherSuppressesAfterEncodingAndRetainsCustody)
}

func testSearchDispatcherSuppressesAfterEncodingAndRetainsCustody(t *testing.T) {
	f, requests, d := searchDispatcherFixture(t, 2, func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		return TitleSearchPage{Disposition: SearchResults, Items: []BrowseItem{{ID: 1, Title: "synthetic"}}}, nil, nil
	})
	prepared, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var oldPublished atomic.Bool
	var oldErr error
	var oldResult SearchDispatchResult
	go func() {
		defer close(done)
		q, r, o := liveRequests("old-search")
		oldResult, oldErr = d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
			close(prepared)
			<-release
			return func(context.Context) error { oldPublished.Store(true); return nil }, nil
		})
	}()
	var released bool
	defer func() {
		if !released {
			close(release)
		}
		<-done
	}()
	select {
	case <-prepared:
	case <-done:
		t.Fatal("old request failed before encoding", oldErr)
	}
	q, r, o := liveRequests("new-search")
	newResult, err := d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "b"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	})
	if err != nil || !newResult.Published {
		t.Fatal(newResult, err)
	}
	assertLiveUsage(t, f, 1, 0) // old encoder remains charged after cancellation
	if oldPublished.Load() {
		t.Fatal("old response published during preparation")
	}
	close(release)
	released = true
	<-done
	if !errors.Is(oldErr, ErrSearchSuperseded) || oldPublished.Load() || oldResult.Published || newResult.Generation <= oldResult.Generation {
		t.Fatal(oldResult, newResult, oldErr)
	}
	assertLiveUsage(t, f, 0, 0)
	// Invalid raw input does not replace the current valid identity.
	active, err := requests.Begin(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Finish()
	q, r, o = liveRequests("invalid-search")
	_, err = d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, strings.Repeat("x", 513)), func(context.Context, SearchResponse) (func(context.Context) error, error) {
		t.Error("invalid query consumed")
		return nil, nil
	})
	if !errors.Is(err, search.ErrQuery) || !active.Current() {
		t.Fatal("invalid input superseded", err)
	}
}

func TestSearchDispatcherFailureRetiresAfterPublishAndFinish(t *testing.T) {
	synctest.Test(t, testSearchDispatcherFailureRetiresAfterPublishAndFinish)
}

func testSearchDispatcherFailureRetiresAfterPublishAndFinish(t *testing.T) {
	f, requests, d := searchDispatcherFixture(t, 1, nil)
	prepareErr := errors.New("encoding failed")
	q, r, o := liveRequests("encode-fail")
	_, err := d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) { return nil, prepareErr })
	if !errors.Is(err, prepareErr) {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	panicValue := &struct{ label string }{"publish panic"}
	func() {
		defer func() {
			if recover() != panicValue {
				t.Error("primary panic changed")
			}
		}()
		q, r, o := liveRequests("publish-panic")
		_, _ = d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
			return func(context.Context) error { assertLiveUsage(t, f, 1, 0); panic(panicValue) }, nil
		})
	}()
	assertLiveUsage(t, f, 0, 0)
	active, err := requests.Begin(t.Context(), "probe")
	if err != nil {
		t.Fatal("Finish leaked metadata after panic", err)
	}
	active.Finish()
}

func TestSearchDispatcherRejectsPartialOrOversizedTitleResults(t *testing.T) {
	synctest.Test(t, testSearchDispatcherRejectsPartialOrOversizedTitleResults)
}

func testSearchDispatcherRejectsPartialOrOversizedTitleResults(t *testing.T) {
	f, _, d := searchDispatcherFixture(t, 1, nil)
	for _, page := range []TitleSearchPage{
		{Disposition: SearchPending, Items: []BrowseItem{{ID: 1, Title: "partial"}}},
		{Disposition: SearchResults, Items: make([]BrowseItem, MaxSearchTitles+1)},
		{Disposition: SearchResults, Items: []BrowseItem{{ID: 1, Title: "\xff"}}},
	} {
		d.titles = func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
			return page, nil, nil
		}
		q, r, o := liveRequests("invalid-title-page")
		result, err := d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
			t.Error("invalid title page consumed")
			return nil, nil
		})
		if !errors.Is(err, ErrInput) || result.Published {
			t.Fatal(result, err)
		}
		assertLiveUsage(t, f, 0, 0)
	}
}

func TestSearchDispatcherCancellationAndRevocationBeforePublish(t *testing.T) {
	synctest.Test(t, testSearchDispatcherCancellationAndRevocationBeforePublish)
}

func testSearchDispatcherCancellationAndRevocationBeforePublish(t *testing.T) {
	f, _, d := searchDispatcherFixture(t, 1, nil)
	for _, kind := range []string{"cancel-read", "revoke-encoded"} {
		ctx, cancel := context.WithCancel(t.Context())
		var output BrowseOutput
		d.titles = func(_ context.Context, _ governor.Request, _ time.Duration, custody BrowseOutput, _ BrowseInput, _ search.Query) (TitleSearchPage, *BrowseReadResult, error) {
			output = custody
			if kind == "cancel-read" {
				cancel()
			}
			return TitleSearchPage{Disposition: SearchResults, Items: []BrowseItem{{ID: 1, Title: "synthetic"}}}, nil, nil
		}
		q, r, o := liveRequests(kind)
		published := false
		result, err := d.Consume(ctx, q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
			if kind == "cancel-read" {
				t.Error("cancelled read prepared a response")
			}
			f.governor.Cancel(output.Grant)
			return func(context.Context) error { published = true; return nil }, nil
		})
		cancel()
		if err == nil || published || result.Published {
			t.Fatal(kind, result, err)
		}
		if kind == "cancel-read" && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if kind == "revoke-encoded" && !errors.Is(err, governor.ErrGrant) {
			t.Fatal(err)
		}
		assertLiveUsage(t, f, 0, 0)
	}
}

func TestSearchPermissionFencesIncludePublicExecutorCost(t *testing.T) {
	synctest.Test(t, testSearchPermissionFencesIncludePublicExecutorCost)
}

func testSearchPermissionFencesIncludePublicExecutorCost(t *testing.T) {
	f, _, d := searchDispatcherFixture(t, 1, nil)
	d.titles = func(ctx context.Context, purpose governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, _ search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		// An actual public reader still consumes the same scope's one operation;
		// the dispatcher's serial fence-only windows neither deadlock it nor
		// substitute LIVE for its permission checks. This is not a title search.
		page, cost, err := f.reader.Window(ctx, purpose, budget, output, in)
		return TitleSearchPage{Disposition: SearchResults, Items: page.Items}, &cost, err
	}
	q, r, o := liveRequests("public-title-executor")
	result, err := d.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
		return func(context.Context) error { return nil }, nil
	})
	if err != nil || !result.Published || result.Read == nil || result.Read.LookupStatements != 11 || result.Read.PublicationStatements != 10 || len(result.Read.LookupWindows) != 2 || len(result.Read.PublicationWindows) != 2 {
		t.Fatal("executor/fence work lost or duplicated", result.Read, err)
	}
	assertLiveUsage(t, f, 0, 0)
}
