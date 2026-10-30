package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Provider recommendation sources retain their established cache and ranks;
// Home's f47 local weekly source is tested independently below.
func TestProviderRecommendedReusesTheSameRequestTrendingRanking(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest().scoped()
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=1`)
	engineExec(t, f, `INSERT INTO screen_metadata_policies(library_id,providers) VALUES('movies','["tmdb"]') ON CONFLICT(library_id) DO UPDATE SET providers=excluded.providers`)
	for n := 1; n <= 3; n++ {
		engineMeta(t, f, fmt.Sprintf("m%d", n), "tmdb", fmt.Sprint(100+n))
		engineExec(t, f, `INSERT INTO metadata_discovery_items VALUES('tmdb','movie',?,?,?,?)`, fmt.Sprint(100+n), n, r.Now.Add(-time.Hour).Format(time.RFC3339), r.Now.Add(time.Hour).Format(time.RFC3339))
	}
	f.c.Drain()
	expected, err := New(f.db).recommendationCandidates(r, "trending_now", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(expected) != 3 {
		t.Fatalf("fixture trends=%d, want three", len(expected))
	}
	s := New(f.db)
	s.recSources = map[string]homeSource{}
	recommended, err := s.homeEngineSource(r, homeRowSpec{ID: "recommended"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cost := dbwork.Measure(context.Background())
	trending, err := s.WithContext(ctx).homeEngineSource(r, homeRowSpec{ID: "trending_now"})
	if err != nil {
		t.Fatal(err)
	}
	if cost().Statements != 0 {
		t.Fatalf("Trending rescored an existing request ranking with %d statements", cost().Statements)
	}
	if !reflect.DeepEqual(trending.candidates, expected) {
		t.Fatal("reused trend ranking differs from independent provider ranking")
	}
	for _, candidate := range recommended.candidates {
		for _, trend := range expected {
			if candidate.Work == trend.Work {
				t.Fatal("recommended failed to suppress the visible trend preview")
			}
		}
	}
	// A customized order may compose Trending first; either order must produce
	// the same rankings, without changing the live provider order.
	reversed := New(f.db)
	reversed.recSources = map[string]homeSource{}
	first, err := reversed.homeEngineSource(r, homeRowSpec{ID: "trending_now"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reversed.homeEngineSource(r, homeRowSpec{ID: "recommended"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.candidates, trending.candidates) || !reflect.DeepEqual(second.candidates, recommended.candidates) {
		t.Fatal("Home row order changed its rankings")
	}
	// Request entry points allocate a new source map, so revoking consent must
	// remove the old provider rows instead of reusing a previous request's list.
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=0`)
	row, err := New(f.db).homeEngineSource(r, homeRowSpec{ID: "trending_now"})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.candidates) != 0 {
		t.Fatal("a new request reused trends after consent was revoked")
	}
}

func TestHomeHiddenTrendingDoesNotSuppressRecommended(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest().scoped()
	r.HiddenRowIDs = []string{"trending_now"}
	s := New(f.db)
	s.recSources = map[string]homeSource{}
	expected, err := s.modelledCandidates(r, "recommended")
	if err != nil {
		t.Fatal(err)
	}
	source, err := s.homeEngineSource(r, homeRowSpec{ID: "recommended"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.candidates, expected) {
		t.Fatal("hidden Trending changed Recommended")
	}
	if _, ok := s.recSources["trending_now:"+idsJSON(r.Libraries)]; ok {
		t.Fatal("hidden Trending was unnecessarily scored")
	}
}

// collections f47 and Root's 22 October ruling supersede the provider feed for
// Home only. These are real normalized history records in a fresh baseline;
// no production response, rank or activity is fabricated by the test server.
func TestHomeWeeklyTrendingUsesOnlyGenuineEligibleLocalSignals(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest("movies")
	r.CommunityActivity = true
	for _, letter := range []string{"a", "b", "c", "d"} {
		engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, letter, letter, "profile-"+letter)
		shareFixtureProfiles(t, f.db)
	}
	engineExec(t, f, `UPDATE direct_memberships SET allowed_libraries='["movies"]'`)
	record := func(actor, item, tag string, at time.Time, completed int, position int64) {
		t.Helper()
		key := persistence.PersonalOwnerKey("local", actor, "profile-"+actor)
		engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed)VALUES(?,?,?,?,?,1,?,0,?)`, tag, key, f.items[item].ID, at.UTC().Format("2006-01-02T15:04:05.000Z"), at.UTC().Format("2006-01-02T15:04:05.000Z"), position, completed)
	}
	for _, actor := range []string{"a", "b", "c", "d"} {
		record(actor, "m1", "one-"+actor, r.Now.Add(-2*time.Hour), 1, 300000)
		if actor != "d" {
			record(actor, "m2", "two-"+actor, r.Now.Add(-time.Hour), 1, 300000)
		}
		record(actor, "m3", "old-"+actor, r.Now.Add(-8*24*time.Hour), 1, 300000)
		record(actor, "m4", "future-"+actor, r.Now.Add(time.Hour), 1, 300000)
		record(actor, "m5", "tiny-"+actor, r.Now.Add(-time.Hour), 0, 1)
	}
	for i := 0; i < 5; i++ {
		record("a", "m2", fmt.Sprint("replay-", i), r.Now.Add(-30*time.Minute), 1, 300000)
	}
	f.c.Drain()
	s := New(f.db)
	check := func(want ...string) HomeRow {
		t.Helper()
		row, err := s.HomeSingleRow(r, "trending_now", HomeRowPage{Limit: 60})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, entry := range row.Entries {
			got = append(got, entry.ID)
		}
		expected := []string{}
		for _, key := range want {
			expected = append(expected, f.items[key].Public)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatal("weekly activity membership/rank", got, expected)
		}
		raw, _ := json.Marshal(row)
		for _, private := range []string{"profile-a", "profile-b", "profile-c", "profile-d", "viewers", "accountId", "profileId"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private activity fact left source", private)
			}
		}
		return row
	}
	row := check("m1", "m2")
	if row.Why == nil || row.Why.Fallback != "What people on this server are watching most this week." {
		t.Fatal("f47 copy", row.Why)
	}
	t.Log("genuine seven-day normalized signals produce four-profile work before three-profile work; replays, eight-day, future and tiny signals do not add titles or inflate rank")
	// A privacy opt-out is applied immediately, including to an open cursor.
	storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "d", ProfileID: "profile-d"}, map[string]any{"privacy.showActivityToMembers": false})
	check("m2", "m1") // same three viewers; newest eligible activity wins.
	storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "c", ProfileID: "profile-c"}, map[string]any{"privacy.pauseWatchHistory": true})
	check() // fewer than three eligible people for either work.
	storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "d", ProfileID: "profile-d"}, map[string]any{"privacy.showActivityToMembers": true})
	storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "c", ProfileID: "profile-c"}, map[string]any{"privacy.pauseWatchHistory": false})
	f.c.Attributes(f.items["m1"].ID, "label", "adult")
	f.c.Drain()
	r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"adult"}}
	check("m2")
	r.Viewer.Restrictions = identity.ContentRestrictions{}
	engineExec(t, f, `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id IN('c','d')`)
	check() // current library admission, not the historical admission.
	engineExec(t, f, `UPDATE direct_memberships SET allowed_libraries='["movies"]'`)
	engineExec(t, f, `INSERT INTO profile_restrictions(profile_id,blocked_labels)VALUES('profile-c','["adult"]'),('profile-d','["adult"]')`)
	check("m2") // current contributor policy cannot lend hidden title signals.
	engineExec(t, f, `DELETE FROM profile_restrictions`)
	limits := access.DefaultLimits()
	limits.Tags.DeniedLabels = []string{"adult"}
	body, _ := json.Marshal(limits)
	for _, actor := range []string{"c", "d"} {
		engineExec(t, f, `INSERT INTO access_limits(account_id,revision,body,updated_ms)VALUES(?,1,?,0)`, actor, string(body))
	}
	check("m2") // account ceilings narrow the contributor's own profile policy.
	engineExec(t, f, `DELETE FROM access_limits`)
	engineExec(t, f, `DELETE FROM personal_history`)
	// Provider discovery and owner ratings never substitute for empty activity.
	engineExec(t, f, `UPDATE screen_metadata_consent SET confirmed=1`)
	engineExec(t, f, `INSERT INTO screen_metadata_policies(library_id,providers)VALUES('movies','["tmdb"]') ON CONFLICT(library_id)DO UPDATE SET providers=excluded.providers`)
	engineMeta(t, f, "m1", "tmdb", "101")
	engineExec(t, f, `INSERT INTO metadata_discovery_items VALUES('tmdb','movie','101',1,?,?)`, r.Now.Add(-time.Hour).Format(time.RFC3339), r.Now.Add(time.Hour).Format(time.RFC3339))
	check()
	provider, err := s.recommendationCandidates(r.scoped(), "trending_now", "")
	if err != nil || len(provider) != 1 {
		t.Fatal("provider discovery was rewritten", provider, err)
	}
}

func TestHomeRecommendationWhyCarriesOnlyCurrentPersonalSeeds(t *testing.T) {
	f := recommendationFixtureDB(t)
	r := engineRequest("movies")
	engineWatched(t, f, "p", r.Now.Add(-time.Hour).Format(time.RFC3339), "m1")
	f.c.Drain()
	s := New(f.db)
	row, err := s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 1})
	if err != nil || len(row.WhyTitles) != 1 || row.WhyTitles[0].ID != f.items["m1"].Public || row.Why == nil || row.Why.Params["titles"] != "Movie m1" {
		t.Fatal(row, err)
	}
	r.Viewer.Profile = "other"
	row, err = s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 1})
	if err != nil || len(row.WhyTitles) != 0 {
		t.Fatal("another profile's reason", row, err)
	}
	r.Viewer.Profile = "p"
	r.Viewer.Restrictions = identity.ContentRestrictions{BlockedLabels: []string{"hidden"}}
	f.c.Attributes(f.items["m1"].ID, "label", "hidden")
	f.c.Drain()
	row, err = s.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 1})
	if err != nil || len(row.WhyTitles) != 0 {
		t.Fatal("hidden seed in reason", row, err)
	}
}
