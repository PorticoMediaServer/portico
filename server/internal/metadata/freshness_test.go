package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// conditionalFixtureProvider is the same fake as screenFixtureProvider plus the conditional
// call, so a test can see which of the two a refresh used and what validators it sent.
type conditionalFixtureProvider struct {
	record          metadataprovider.ScreenRecord
	searches        int
	details         int
	conditional     int
	notModified     bool
	sentETag        string
	returnedETag    string
	failConditional error
}

func (p *conditionalFixtureProvider) SearchScreen(context.Context, string, string, int, string, string) ([]metadataprovider.ScreenRecord, error) {
	p.searches++
	return []metadataprovider.ScreenRecord{p.record}, nil
}

func (p *conditionalFixtureProvider) ScreenDetails(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error) {
	p.details++
	return p.record, nil
}

func (p *conditionalFixtureProvider) ScreenDetailsConditional(_ context.Context, _, _, _, _ string, in metadataprovider.Conditional) (metadataprovider.ScreenRecord, metadataprovider.Conditional, error) {
	p.conditional++
	p.sentETag = in.ETag
	if p.failConditional != nil {
		return metadataprovider.ScreenRecord{}, metadataprovider.Conditional{}, p.failConditional
	}
	out := metadataprovider.Conditional{ETag: p.returnedETag}
	if out.ETag == "" {
		out.ETag = in.ETag
	}
	if p.notModified {
		return metadataprovider.ScreenRecord{}, out, metadataprovider.ErrNotModified
	}
	return p.record, out, nil
}

// fetches is every request that would have left this machine.
func (p *conditionalFixtureProvider) fetches() int { return p.searches + p.details + p.conditional }

func freshnessFixture(t *testing.T) (*Service, *sql.DB, *conditionalFixtureProvider, catalogtest.Item) {
	t.Helper()
	s, db, base := screenFixture(t)
	item := publicationFixtureMovie(t, db)
	p := &conditionalFixtureProvider{record: base.record, returnedETag: `W/"v2"`}
	s.screenProviders = map[string]ScreenProvider{"tmdb": p}
	screenConfirm(t, s)
	return s, db, p, item
}

// settleScreenPublication runs the worker until the item is published and stops changing, so a
// test can start counting requests from a settled library rather than from the middle of one.
func settleScreenPublication(t *testing.T, s *Service, db *sql.DB, item catalogtest.Item) {
	t.Helper()
	ctx := context.Background()
	// The first pass searches and publishes; the pass after it is the first refresh of the
	// accepted identity, which is what records the document's freshness. A settled library is
	// past both.
	for range 6 {
		if e := s.ScreenStep(ctx); e != nil {
			t.Fatal(e)
		}
		settled := publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID) == "matched" &&
			publicationScalar(t, db, `SELECT count(*) FROM metadata_document_freshness`) != "0"
		if settled {
			if publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID) != "42" {
				t.Fatal("settled without an accepted identity")
			}
			return
		}
		publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
	}
	t.Fatal("the item never settled")
}

// The heart of it: a row woken again for a local reason does not call the provider, because
// the document it would ask for was fetched minutes ago. Before this, every wake was a request.
func TestAFreshDocumentIsNotRefetched(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	settleScreenPublication(t, s, db, item)
	if p.details == 0 && p.searches == 0 {
		t.Fatal("the first publication did not fetch")
	}
	firstPass := p.fetches()
	// Ten wakes for local reasons — a scan, a sidecar, a policy edit — and not one request.
	for range 10 {
		publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
		if e := s.ScreenStep(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if p.fetches() != firstPass {
		t.Fatalf("a fresh document was refetched: %d searches, %d details, %d conditional", p.searches, p.details, p.conditional)
	}
	// And the row is still published from the remembered document, not left unfinished.
	if publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID) != "matched" {
		t.Fatal("skipping the fetch left the row unsettled")
	}
}

// Outside the window the provider is asked, but conditionally, and an unchanged document is a
// 304 that costs no body and starts the window again.
func TestAStaleDocumentIsAskedForConditionally(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	settleScreenPublication(t, s, db, item)
	// The first fetch was unconditional and recorded no validator, so give it one, as a real
	// provider's response would have.
	if e := s.noteDocumentChecked(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), documentValidators{ETag: `W/"v1"`}, true, s.publicationTime().UTC().Add(-90*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	p.notModified = true
	// Settling already asked the provider itself once (an admitted refresh never uses the
	// response cache), so count from here.
	before := p.conditional
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.conditional != before+1 {
		t.Fatalf("a stale document was not asked for conditionally: %d details, %d conditional", p.details, p.conditional)
	}
	if p.sentETag != `W/"v1"` {
		t.Fatal("the stored validator was not sent", p.sentETag)
	}
	// The 304 renews the window, so the next wake asks nothing at all.
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.conditional != before+1 {
		t.Fatal("a 304 did not renew the freshness window", p.conditional)
	}
	if publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID) != "42" {
		t.Fatal("the publication was lost across a not-modified refresh")
	}
}

// A person asking for a refresh is never held back by the rule: it exists to stop the server
// asking on its own.
func TestAnOwnerRefreshForgetsFreshness(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	settleScreenPublication(t, s, db, item)
	before := p.fetches()
	if e := s.QueueRefresh(item.Public); e != nil {
		t.Fatal(e)
	}
	if publicationScalar(t, db, `SELECT count(*) FROM metadata_document_freshness`) != "0" {
		t.Fatal("an owner refresh left the document fresh")
	}
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.fetches() <= before {
		t.Fatalf("an owner refresh did not reach the provider: %d searches, %d details, %d conditional", p.searches, p.details, p.conditional)
	}
}

// The budget is what stops a library-wide wake from becoming a stampede. A refresh that cannot
// be afforded republishes what is held rather than queueing a request for later.
func TestTheRefreshBudgetBoundsRefetchingAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	settleScreenPublication(t, s, db, item)
	// Spend the hour. Settling the library already spent one refresh, so the count is what
	// the cap allows in total, not what this loop asks for. The clock is the service's own,
	// because that is what the worker spends against.
	now := s.publicationTime().UTC()
	admitted := 0
	for range refreshBudgetHour + 10 {
		ok, e := s.admitRefresh(ctx, "tmdb", now)
		if e != nil {
			t.Fatal(e)
		}
		if !ok {
			break
		}
		admitted++
	}
	if admitted == 0 || admitted >= refreshBudgetHour {
		t.Fatal("the budget did not stop at its cap", admitted)
	}
	if ok, e := s.admitRefresh(ctx, "tmdb", now); e != nil || ok {
		t.Fatal("the budget admitted more than its cap", ok, e)
	}
	// A different provider has its own allowance.
	if ok, e := s.admitRefresh(ctx, "tvdb", now); e != nil || !ok {
		t.Fatal("one provider's budget bound another", ok, e)
	}
	// Durable: the caps are in the database, so a restart cannot hand out a second allowance.
	if publicationScalar(t, db, `SELECT spent FROM metadata_refresh_budget WHERE provider='tmdb' AND span='hour'`) != "240" {
		t.Fatal("the budget is not durable")
	}
	remaining, e := s.RefreshBudgetRemaining(ctx, "tmdb", now)
	if e != nil || remaining["hour"] != 0 || remaining["day"] != refreshBudgetDay-refreshBudgetHour {
		t.Fatal(remaining, e)
	}
	// An untouched provider still has its whole allowance; one provider cannot spend another's.
	if untouched, err := s.RefreshBudgetRemaining(ctx, "musicbrainz", now); err != nil || untouched["hour"] != refreshBudgetHour {
		t.Fatal(untouched, err)
	}
	// A stale document with no allowance republishes what is held rather than asking.
	if e = s.noteDocumentChecked(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), documentValidators{}, true, now.Add(-90*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	before := p.fetches()
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.fetches() != before {
		t.Fatalf("a refresh was made with no allowance left: %d searches, %d details, %d conditional", p.searches, p.details, p.conditional)
	}
	if publicationScalar(t, db, `SELECT status FROM screen_metadata_work WHERE target_id=?`, item.ID) != "matched" {
		t.Fatal("a budgeted-out row was left unsettled")
	}
	// The window rolls forward on its own an hour later.
	if ok, err := s.admitRefresh(ctx, "tmdb", now.Add(61*time.Minute)); err != nil || !ok {
		t.Fatal("the hourly window never reopened", ok, err)
	}
}

// A first fetch is never budgeted: the item somebody just added is what they are waiting for.
func TestAFirstFetchIsNeverBudgeted(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	now := s.publicationTime().UTC()
	for range refreshBudgetHour {
		if ok, e := s.admitRefresh(ctx, "tmdb", now); e != nil {
			t.Fatal(e)
		} else if !ok {
			break
		}
	}
	if ok, e := s.admitRefresh(ctx, "tmdb", now); e != nil || ok {
		t.Fatal("the allowance was not spent", ok, e)
	}
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if p.details != 1 {
		t.Fatal("a first fetch was refused by the refresh budget", p.details)
	}
	if publicationScalar(t, db, `SELECT provider_id FROM screen_metadata_work WHERE target_id=?`, item.ID) != "42" {
		t.Fatal("a newly added item was not published")
	}
}

// The windows themselves: what a document is, and why it is being looked at.
func TestFreshnessWindowsByKindAndPriority(t *testing.T) {
	for _, tc := range []struct {
		provider, kind string
		priority       RefreshPriority
		want           time.Duration
	}{
		{"tmdb", "movie", RefreshOrdinary, freshnessMovie},
		{"tmdb", "show", RefreshOrdinary, freshnessShow},
		{"tmdb", "episode", RefreshOrdinary, freshnessEpisode},
		{"anilist", "anime", RefreshOrdinary, freshnessShow},
		{"musicbrainz", "release", RefreshOrdinary, freshnessReleaseData},
		{"acoustid", "recording", RefreshOrdinary, freshnessRecording},
		{"tvdb", "unknown-kind", RefreshOrdinary, freshnessDefault},
		// A series that is still airing is the one case a fortnight is too long for, and a
		// recently added item is the one anybody is most likely about to open.
		{"tmdb", "show", RefreshAiring, freshnessAiringShow},
		{"tmdb", "movie", RefreshRecentlyAdded, freshnessRecent},
		// Priority only ever shortens: a fingerprint mapping that was added yesterday still
		// does not need asking about every hour.
		{"acoustid", "recording", RefreshRecentlyAdded, freshnessRecent},
	} {
		if got := documentFreshness(tc.provider, tc.kind, tc.priority); got != tc.want {
			t.Errorf("%s/%s priority %d: got %s, want %s", tc.provider, tc.kind, tc.priority, got, tc.want)
		}
	}
}

// A provider that refuses is backed off exponentially with jitter proportional to the delay,
// and its own Retry-After is a floor that is never undercut — the rule the Hosted control
// plane uses, which metadata's hand-rolled, jitter-free backoff did not.
func TestProviderBackoffIsJitteredAndNeverUndercutsRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	seenDistinct := map[time.Time]bool{}
	for range 40 {
		at := providerRetryAt(now, 30*time.Second, 3, 8, time.Time{})
		if at.Before(now.Add(4*time.Minute)) || at.After(now.Add(6*time.Minute)) {
			t.Fatal("outside the delay plus half again", at.Sub(now))
		}
		seenDistinct[at] = true
	}
	if len(seenDistinct) < 20 {
		t.Fatal("a fleet-wide failure would come back as one wave", len(seenDistinct))
	}
	floor := now.Add(time.Hour)
	at := providerRetryAt(now, 30*time.Second, 1, 8, floor)
	if at.Before(floor) || at.After(floor.Add(time.Minute)) {
		t.Fatal("a provider's Retry-After was undercut or wildly overshot", at.Sub(now))
	}
	// The shift is capped, so a long-failing provider does not retreat to the heat death.
	capped := providerRetryAt(now, time.Second, 99, 6, time.Time{})
	if capped.After(now.Add(2 * time.Minute)) {
		t.Fatal("the backoff shift is not capped", capped.Sub(now))
	}
}

// Validators are stored as the provider gave them, and a 304 that repeats only the ETag does
// not lose a Last-Modified that is still valid.
func TestValidatorsArePersistedAndNotLostOnANotModified(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := freshnessFixture(t)
	now := s.publicationTime().UTC()
	if e := s.noteDocumentChecked(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), documentValidators{ETag: `W/"v1"`, LastModified: "Wed, 17 Sep 2026 00:00:00 GMT"}, true, now); e != nil {
		t.Fatal(e)
	}
	state, e := s.documentState(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"))
	if e != nil || !state.Found || state.ETag != `W/"v1"` || state.LastModified != "Wed, 17 Sep 2026 00:00:00 GMT" {
		t.Fatal(state, e)
	}
	fresh, _, e := s.documentFresh(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), RefreshOrdinary, now.Add(time.Hour))
	if e != nil || !fresh {
		t.Fatal("an hour-old movie document was called stale", e)
	}
	if fresh, _, e = s.documentFresh(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), RefreshOrdinary, now.Add(freshnessMovie+time.Minute)); e != nil || fresh {
		t.Fatal("a document past its window was called fresh", e)
	}
	// A document nothing has fetched is never fresh, which is what makes a first fetch
	// unconditional and unbudgeted.
	if fresh, state, e = s.documentFresh(ctx, "tmdb", "movie", "99", RefreshOrdinary, now); e != nil || fresh || state.Found {
		t.Fatal("an unknown document was called fresh", e)
	}
	if e = s.ForgetDocumentFreshness(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA")); e != nil {
		t.Fatal(e)
	}
	if state, e = s.documentState(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA")); e != nil || state.Found {
		t.Fatal("the document was not forgotten", state, e)
	}
}

// A provider error on a conditional refresh is still an error: it must not be mistaken for
// "not modified" and quietly renew the window.
func TestAFailedConditionalRefreshDoesNotRenewTheWindow(t *testing.T) {
	ctx := context.Background()
	s, db, p, item := freshnessFixture(t)
	settleScreenPublication(t, s, db, item)
	stale := s.publicationTime().UTC().Add(-90 * 24 * time.Hour)
	if e := s.noteDocumentChecked(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"), documentValidators{ETag: `W/"v1"`}, true, stale); e != nil {
		t.Fatal(e)
	}
	p.failConditional = errors.New("provider unavailable")
	publicationExec(t, db, `UPDATE screen_metadata_work SET status='pending',lease='',lease_until='' WHERE target_id=?`, item.ID)
	if e := s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	state, e := s.documentState(ctx, "tmdb", "movie", screenDocumentResource("42", "en-US", "CA"))
	if e != nil || !state.Found {
		t.Fatal(state, e)
	}
	if !state.CheckedAt.Equal(stale.Truncate(time.Second)) {
		t.Fatal("a failed refresh renewed the freshness window", state.CheckedAt, stale)
	}
}
