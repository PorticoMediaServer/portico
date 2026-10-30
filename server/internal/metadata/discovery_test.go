package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Provider Trending Now. These tests pin the provider-free and policy bounds:
// a feed request happens only for a (provider, media_kind) pair an enabled
// library policy actually names, only after consent, only on the background
// worker's own schedule, and every snapshot is stored atomically and expires
// on its own 48-hour bound. Fakes replace one keyed source each; the real
// transports are pinned in the provider package.

type fakeTrending struct {
	calls   int
	entries []metadataprovider.TrendEntry
	err     error
	before  func()
}

func (f *fakeTrending) Trending(ctx context.Context, language string, pages int) ([]metadataprovider.TrendEntry, error) {
	f.calls++
	if f.before != nil {
		f.before()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func discoveryFixture(t *testing.T) (*Service, *sql.DB, *fakeTrending, *fakeTrending) {
	t.Helper()
	s, db, _ := publicationFixture(t)
	tmdb := &fakeTrending{entries: []metadataprovider.TrendEntry{
		{Provider: "tmdb", Type: "movie", ID: "42"},
		{Provider: "tmdb", Type: "movie", ID: "43"},
	}}
	anilist := &fakeTrending{entries: []metadataprovider.TrendEntry{{Provider: "anilist", Type: "anime", ID: "9"}}}
	s.trendingSources = map[string]trendingSource{
		"tmdb/movie":    tmdb,
		"tmdb/tv":       tmdb,
		"anilist/anime": anilist,
	}
	return s, db, tmdb, anilist
}

// discoveryConfirm confirms consent and enables exactly the named providers on
// the fixture movie library.
func discoveryPolicySet(t *testing.T, s *Service, library string, providers []string, confirmed bool) {
	t.Helper()
	policy, err := s.ScreenPolicy(context.Background(), library)
	if err != nil {
		t.Fatal(err)
	}
	yes := confirmed
	if err = s.UpdateScreenPolicy(context.Background(), library, ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, DisclosureVersion: ScreenDisclosureVersion, Enabled: true, Providers: providers, Language: "en-US", Region: "US", RefreshMode: "replace_unlocked"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryExactProviderPolicyBeforeAnyRequest(t *testing.T) {
	// Anime library with AniList disabled: an owner that enabled only TMDB for
	// anime must not leak an AniList request; movie library with AniList named
	// must not produce a movie-kind AniList feed; TVDB has no documented trend
	// endpoint and contributes nothing.
	s, db, tmdb, anilist := discoveryFixture(t)
	publicationExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('anime-lib','Anime','anime','/owned-anime')`)
	discoveryPolicySet(t, s, "anime-lib", []string{"tmdb"}, true)
	// A movie library cannot name anilist through the owner API (only tmdb/tvdb
	// are offered), so the leak case is a stale or hand-edited policy row.
	discoveryPolicySet(t, s, "lib", []string{"tvdb"}, true)
	publicationExec(t, db, `UPDATE screen_metadata_policies SET providers='["anilist","tvdb"]' WHERE library_id='lib'`)
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 0 || anilist.calls != 0 {
		t.Fatalf("unsupported provider/kind pair was requested: tmdb=%d anilist=%d", tmdb.calls, anilist.calls)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items`); n != "0" {
		t.Fatalf("rows stored without an authorized feed: %s", n)
	}
	// Now the exact policy: a movie library that names tmdb gets the tmdb/movie
	// feed and only that one.
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 1 || anilist.calls != 0 {
		t.Fatalf("feed policy mismatch: tmdb=%d anilist=%d", tmdb.calls, anilist.calls)
	}
}

func TestDiscoveryKindMappingTVFeed(t *testing.T) {
	// A TV library names tmdb: the persisted media_kind is tv while the
	// provider types its entries show, and only show-typed rows are cached.
	s, db, tmdb, _ := discoveryFixture(t)
	publicationExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('tv-lib','TV','tv','/owned-tv')`)
	discoveryPolicySet(t, s, "lib", []string{"tvdb"}, true)
	discoveryPolicySet(t, s, "tv-lib", []string{"tmdb"}, true)
	tmdb.entries = []metadataprovider.TrendEntry{
		{Provider: "tmdb", Type: "show", ID: "7"},
		{Provider: "tmdb", Type: "movie", ID: "8"},
	}
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	ids, e := persistence.DiscoveryProviderIDs(context.Background(), db, "tmdb", "tv", s.publicationTime())
	if e != nil || len(ids) != 1 || ids[0] != "7" {
		t.Fatalf("tv feed roundtrip: %v %v", ids, e)
	}
	// The show library revision moved; the movie library's did not (its policy
	// names tmdb too, so it moved as well — assert the anime fixture library
	// with no enabled provider did not).
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items WHERE media_kind='movie'`); n != "0" {
		t.Fatalf("cross-kind rows leaked: %s", n)
	}
}

func TestDiscoveryConsentGatesFetchAndServing(t *testing.T) {
	s, db, tmdb, _ := discoveryFixture(t)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, false)
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 0 {
		t.Fatal("unconfirmed disclosure sent a provider request")
	}
	// Confirm, fetch, then revoke: the cached rows must stop existing and the
	// next pass must not call the provider again.
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 1 {
		t.Fatalf("expected one authorized fetch, got %d", tmdb.calls)
	}
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, false)
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 1 {
		t.Fatalf("revoked consent still fetched: %d", tmdb.calls)
	}
	if stored, e := persistence.DiscoverySnapshotStored(context.Background(), db); e != nil || stored {
		t.Fatalf("revoked consent kept cached trends: %v %v", stored, e)
	}
	// And nothing due, nothing written: the idle server makes no requests.
	before := tmdb.calls
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != before {
		t.Fatal("idle consent-revoked pass fetched")
	}
}

func TestDiscoveryRevocationMidPassStopsRemainingFeeds(t *testing.T) {
	// Two feeds, one pass. The first feed to run revokes the global consent
	// during its fetch; every later feed in the pass must never be dispatched,
	// and the first feed must not commit either — the store's fence re-reads
	// consent inside its own transaction, so a
	// revoke-between-recheck-and-commit publishes nothing. The assertion is
	// order-agnostic: whichever feed is first, the pass issues exactly one
	// request and stores nothing.
	s, db, tmdb, anilist := discoveryFixture(t)
	publicationExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('anime-lib','Anime','anime','/owned-anime')`)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	discoveryPolicySet(t, s, "anime-lib", []string{"anilist"}, true)
	revoke := func() {
		policy, err := s.ScreenPolicy(context.Background(), "lib")
		if err != nil {
			t.Fatal(err)
		}
		if !policy.Confirmed {
			return
		}
		yes := false
		if err = s.UpdateScreenPolicy(context.Background(), "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tmdb.before = revoke
	anilist.before = revoke
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls+anilist.calls != 1 {
		t.Fatalf("a revoked consent did not stop the remaining feeds: tmdb=%d anilist=%d", tmdb.calls, anilist.calls)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items`); n != "0" {
		t.Fatalf("the mid-pass revoke still stored rows: %s", n)
	}
	// Nothing was scheduled either, so the next pass re-decides from scratch.
	if _, ok, e := persistence.LoadDiscoveryRefresh(context.Background(), db, "tmdb", "movie"); e != nil || ok {
		t.Fatalf("the fenced pass wrote scheduling state: %v %v", ok, e)
	}
	if _, ok, e := persistence.LoadDiscoveryRefresh(context.Background(), db, "anilist", "anime"); e != nil || ok {
		t.Fatalf("the fenced pass wrote scheduling state: %v %v", ok, e)
	}
}

func TestDiscoveryPolicyChangeMidFlightDiscardsSnapshot(t *testing.T) {
	s, db, tmdb, _ := discoveryFixture(t)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	// The fake flips the owner policy to disabled while the "network request"
	// is in flight. The pre-request check passed, so this is the
	// revoke-between-recheck-and-commit race; the snapshot fence inside the
	// store transaction must refuse the commit.
	tmdb.before = func() {
		policy, err := s.ScreenPolicy(context.Background(), "lib")
		if err != nil {
			t.Fatal(err)
		}
		yes := false
		if err = s.UpdateScreenPolicy(context.Background(), "lib", ScreenPolicyUpdate{ExpectedRevision: policy.Revision, ExpectedConsentRevision: policy.ConsentRevision, ConfirmRemote: &yes, Enabled: true, Providers: []string{"tmdb"}, Language: "en-US", RefreshMode: "replace_unlocked"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if e := s.DiscoveryStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items`); n != "0" {
		t.Fatalf("policy change mid-flight still committed a snapshot: %s rows", n)
	}
	// The scheduling row is untouched, so the next pass re-decides cleanly.
	state, ok, e := persistence.LoadDiscoveryRefresh(context.Background(), db, "tmdb", "movie")
	if e != nil || ok {
		t.Fatalf("mid-flight discard wrote scheduling state: %v %v", state, e)
	}
}

func TestDiscoverySnapshotPersistenceRanksAndRevisions(t *testing.T) {
	s, db, tmdb, anilist := discoveryFixture(t)
	publicationExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('anime-lib','Anime','anime','/owned-anime')`)
	discoveryPolicySet(t, s, "anime-lib", []string{"anilist"}, true)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	tmdb.entries = []metadataprovider.TrendEntry{
		{Provider: "tmdb", Type: "movie", ID: "42"},
		{Provider: "tmdb", Type: "movie", ID: "43"},
		{Provider: "tmdb", Type: "show", ID: "99"},
	}
	anilist.entries = []metadataprovider.TrendEntry{
		{Provider: "anilist", Type: "anime", ID: "9"},
		{Provider: "anilist", Type: "anime", ID: "10"},
		{Provider: "anilist", Type: "show", ID: "8"},
	}
	ctx := context.Background()
	if e := s.DiscoveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	movies, e := persistence.DiscoveryItems(ctx, db, "tmdb", "movie", s.publicationTime())
	if e != nil || len(movies) != 2 {
		t.Fatalf("movie snapshot: %v %v", movies, e)
	}
	for i, entry := range movies {
		if entry.Rank != i+1 || entry.ProviderID == "" || entry.FetchedAt.IsZero() {
			t.Fatalf("rank/provider_id contract broken at %d: %#v", i, entry)
		}
		if !entry.ExpiresAt.Equal(movies[0].ExpiresAt) || entry.ExpiresAt.Sub(movies[0].FetchedAt) != persistence.DiscoveryStaleLimit {
			t.Fatalf("expires_at is not fetched_at+48h: %#v", entry)
		}
	}
	if ids, e := persistence.DiscoveryProviderIDs(ctx, db, "tmdb", "movie", s.publicationTime()); e != nil || len(ids) != 2 || ids[0] != "42" || ids[1] != "43" {
		t.Fatalf("provider ids did not round-trip: %v %v", ids, e)
	}
	anime, e := persistence.DiscoveryItems(ctx, db, "anilist", "anime", s.publicationTime())
	if e != nil || len(anime) != 2 {
		t.Fatalf("anime snapshot: %v %v", anime, e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items WHERE media_kind='anime' AND provider_id='8'`); n != "0" {
		t.Fatalf("wrongly typed feed entry cached: %s", n)
	}
	// Feed commit invalidation: the enabled libraries' revisions moved, the
	// feed's own scheduling state is fresh with a ~6h next attempt.
	if n := publicationScalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='lib'`); n == "1" {
		t.Fatal("movie library revision not invalidated by the feed commit")
	}
	if n := publicationScalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='anime-lib'`); n == "1" {
		t.Fatal("anime library revision not invalidated by the feed commit")
	}
	state, ok, e := persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie")
	if e != nil || !ok || state.Attempts != 0 || state.Status != "fresh" || !state.NextDue.After(s.publicationTime().Add(persistence.DiscoveryRefreshInterval-time.Minute)) {
		t.Fatalf("refresh state after success: %#v ok=%v err=%v", state, ok, e)
	}
	// Nothing due and no policy change: the next idle pass must not fetch.
	before := tmdb.calls + anilist.calls
	if e = s.DiscoveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls+anilist.calls != before {
		t.Fatal("an idle pass re-fetched a fresh feed")
	}
	if state2, _, _ := persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie"); state2.Status != "fresh" {
		t.Fatal("an idle pass rewrote refresh state")
	}
}

func TestDiscoveryFailureRetainsSnapshotAndBacksOff(t *testing.T) {
	s, db, tmdb, _ := discoveryFixture(t)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	ctx := context.Background()
	if e := s.DiscoveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	first, e := persistence.DiscoveryItems(ctx, db, "tmdb", "movie", s.publicationTime())
	if e != nil || len(first) == 0 {
		t.Fatal(first, e)
	}
	// A rate limit with Retry-After keeps the last good snapshot, records the
	// shared cooldown and backs the next attempt off past the Retry-After.
	publicationExec(t, db, `UPDATE metadata_discovery_refresh SET next_attempt='' WHERE provider='tmdb' AND media_kind='movie'`)
	tmdb.err = &metadataprovider.Error{Provider: "tmdb", Status: 429, Code: "request_rejected", RetryAfter: 90 * time.Second}
	tmdb.entries = nil
	if e = s.DiscoveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 2 {
		t.Fatalf("failure pass fetch count: %d", tmdb.calls)
	}
	again, e := persistence.DiscoveryItems(ctx, db, "tmdb", "movie", s.publicationTime())
	if e != nil || len(again) != len(first) || again[0].FetchedAt != first[0].FetchedAt {
		t.Fatalf("a refresh failure disturbed the atomic last-good snapshot: %#v vs %#v", again, first)
	}
	state, ok, e := persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie")
	if e != nil || !ok || state.Attempts != 1 || state.Status != "backoff" {
		t.Fatalf("failure not recorded with bounded backoff: %#v %v", state, e)
	}
	if due, e := persistence.DiscoveryRefreshDue(ctx, db, "tmdb", "movie", s.publicationTime()); e != nil || due {
		t.Fatalf("backoff did not defer the next attempt: %v %v", due, e)
	}
	if due, e := persistence.DiscoveryRefreshDue(ctx, db, "tmdb", "movie", s.publicationTime().Add(91*time.Second)); e != nil || !due {
		t.Fatalf("backoff never becomes due: %v %v", due, e)
	}
	if n := publicationScalar(t, db, `SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider='tmdb'`); n == "" {
		t.Fatal("provider Retry-After did not reach the shared cooldown ledger")
	}
	// Malformed feeds (zero usable entries) are failures, not empty shelves:
	// force the feed due again and take another attempt.
	tmdb.err = nil
	tmdb.entries = nil
	publicationExec(t, db, `UPDATE metadata_discovery_refresh SET next_attempt='' WHERE provider='tmdb' AND media_kind='movie'`)
	if e = s.DiscoveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	if tmdb.calls != 3 {
		t.Fatalf("malformed pass fetch count: %d", tmdb.calls)
	}
	state, ok, e = persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie")
	if e != nil || !ok || state.Attempts != 2 || state.Status != "backoff" {
		t.Fatalf("an empty feed was treated as a snapshot: %#v %v", state, e)
	}
	if again, e := persistence.DiscoveryItems(ctx, db, "tmdb", "movie", s.publicationTime()); e != nil || len(again) != len(first) {
		t.Fatalf("an empty feed disturbed the snapshot: %v %v", again, e)
	}
}

func TestDiscoveryUnavailableFeedAfterSustainedFailures(t *testing.T) {
	s, db, tmdb, _ := discoveryFixture(t)
	discoveryPolicySet(t, s, "lib", []string{"tmdb"}, true)
	tmdb.entries = nil
	tmdb.err = errors.New("provider unreachable")
	ctx := context.Background()
	for i := 0; i < persistence.DiscoveryMaxAttempts; i++ {
		// Force the feed due every pass; production would wait out the backoff.
		publicationExec(t, db, `UPDATE metadata_discovery_refresh SET next_attempt='' WHERE provider='tmdb' AND media_kind='movie'`)
		if e := s.DiscoveryStep(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if tmdb.calls != persistence.DiscoveryMaxAttempts {
		t.Fatalf("attempt budget not honored: %d calls", tmdb.calls)
	}
	state, ok, e := persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie")
	if e != nil || !ok || state.Status != "unavailable" {
		t.Fatalf("sustained failure not parked: %#v %v", state, e)
	}
	// Once parked, the feed is quiet for a day and never churns: bounded
	// daily retry, no wake loop, and no data was ever stored.
	if due, e := persistence.DiscoveryRefreshDue(ctx, db, "tmdb", "movie", s.publicationTime().Add(23*time.Hour)); e != nil || due {
		t.Fatalf("a parked feed woke the worker within its window: %v %v", due, e)
	}
	if due, e := persistence.DiscoveryRefreshDue(ctx, db, "tmdb", "movie", s.publicationTime().Add(25*time.Hour)); e != nil || !due {
		t.Fatalf("a parked feed never retries: %v %v", due, e)
	}
	if n := publicationScalar(t, db, `SELECT count(*) FROM metadata_discovery_items`); n != "0" {
		t.Fatalf("a failing provider stored trend rows: %s", n)
	}
}

func TestDiscoveryStaleSnapshotExpiresAtFortyEightHours(t *testing.T) {
	db, err := persistence.OpenFresh(fmt.Sprintf("%s/discovery-reopen.sqlite", t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publicationExec(t, db, `INSERT INTO libraries(id,name,kind,root) VALUES('lib','Movies','movie','/owned')`)
	ctx := context.Background()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if e := persistence.StoreDiscoverySnapshot(ctx, db, "tmdb", "movie", []string{"42", "43"}, now, nil); e != nil {
		t.Fatal(e)
	}
	if ids, e := persistence.DiscoveryProviderIDs(ctx, db, "tmdb", "movie", now); e != nil || len(ids) != 2 {
		t.Fatal(ids, e)
	}
	// The last-good window is bounded: at 47h the rows still serve, past 48h
	// they are omitted, whatever the refresh state says.
	if ids, e := persistence.DiscoveryProviderIDs(ctx, db, "tmdb", "movie", now.Add(47*time.Hour)); e != nil || len(ids) != 2 {
		t.Fatal(ids, e)
	}
	if ids, e := persistence.DiscoveryProviderIDs(ctx, db, "tmdb", "movie", now.Add(persistence.DiscoveryStaleLimit+time.Second)); e != nil || len(ids) != 0 {
		t.Fatalf("stale snapshot served beyond 48h: %v %v", ids, e)
	}
	if _, _, ok, e := persistence.DiscoverySnapshot(ctx, db, "tmdb", "movie"); e != nil || !ok {
		t.Fatal(e, ok)
	}
	if e := persistence.ClearDiscoverySnapshots(ctx, db); e != nil {
		t.Fatal(e)
	}
	if stored, e := persistence.DiscoverySnapshotStored(ctx, db); e != nil || stored {
		t.Fatalf("clear left cached trends behind: %v %v", stored, e)
	}
	if _, ok, e := persistence.LoadDiscoveryRefresh(ctx, db, "tmdb", "movie"); e != nil || ok {
		t.Fatalf("clear left refresh state behind: %v %v", ok, e)
	}
}
