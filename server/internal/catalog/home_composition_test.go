package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHomeApprovedVideoCompositionAndBadges(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture("movies", "tv")
	c.Fields(homeItem(t, db, "m1").ID, map[string]any{"added_text": r.Now.Add(-time.Hour).Format(time.RFC3339Nano)})
	c.Fields(homeItem(t, db, "m2").ID, map[string]any{"added_text": r.Now.Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano)})
	c.Fields(homeItem(t, db, "m3").ID, map[string]any{"added_text": r.Now.Add(-14*24*time.Hour - time.Second).Format(time.RFC3339Nano)})
	c.Fields(homeItem(t, db, "m4").ID, map[string]any{"added_text": r.Now.Add(time.Hour).Format(time.RFC3339Nano)})
	homeWatched(t, db, "p", r.Now.Format(time.RFC3339), "m1")
	c.Drain()
	first, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if rowTotal(first) != 6 || len(first.Entries) != 3 || first.ArtworkShape != "poster" || first.SeeAll == nil || first.SeeAll.RowID != "new_on_server" || first.NextCursor == "" {
		t.Fatalf("merged row: %+v", first)
	}
	if first.Entries[0].ID != homeItem(t, db, "m4").Public || first.Entries[1].ID != homeItem(t, db, "m1").Public || first.Entries[2].ID != homeItem(t, db, "m2").Public {
		t.Fatalf("newest order: %+v", first.Entries)
	}
	if first.Entries[0].New || first.Entries[1].New || !first.Entries[2].New {
		t.Fatal("future/watched/boundary badge eligibility")
	}
	second, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 3, Cursor: first.NextCursor})
	if err != nil || second.Start != 3 || rowTotal(second) != 6 || len(second.Entries) != 3 {
		t.Fatalf("page %+v %v", second, err)
	}
	for _, e := range second.Entries {
		if e.New || e.Overview != "" && e.Kind != "episode" {
			t.Fatalf("ordinary row is overhydrated or misbadged %+v", e)
		}
	}
	for _, retired := range []string{"recent_movies", "recent_tv", "continue_listening", "trending"} {
		if _, err = s.HomeSingleRow(r, retired, HomeRowPage{}); !errors.Is(err, ErrHomeRowUnknown) {
			t.Fatalf("retired %s: %v", retired, err)
		}
	}
	revoked := r
	revoked.Viewer.Fence = "revoked"
	if _, err = s.HomeSingleRow(revoked, "new_on_server", HomeRowPage{Limit: 3, Cursor: first.NextCursor}); !errors.Is(err, ErrCursor) {
		t.Fatal("cross-authority continuation", err)
	}
}

func TestHomeCausalReasonAndSpotlightAreRealAndBounded(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture("movies", "tv")
	source := homeItem(t, db, "m1")
	c.Exec(`INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,completed) VALUES('watched','p',?,?,?,1,600000,1)`, source.ID, r.Now.Add(-time.Hour).Format(time.RFC3339), r.Now.Format(time.RFC3339))
	c.Fields(source.ID, map[string]any{"overview": strings.Repeat("Description ", 500)})
	c.Drain()
	row, err := s.HomeSingleRow(r, "because_you_watched", HomeRowPage{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if row.Reason == nil || row.Reason.Source.ID != source.Public || row.Reason.Text.Params["title"] != "Movie m1" || row.TitleText.Code != "next.home.becauseYouWatched" || rowTotal(row) < 1 {
		t.Fatalf("causal selection %+v", row)
	}
	for _, entry := range row.Entries {
		if entry.ID == source.Public || entry.Overview != "" {
			t.Fatal("source repeated or overview hydration", entry)
		}
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	a, err := s.WithContext(ctx).HomeManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.HomeManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Spotlight) != 5 || !reflect.DeepEqual(a.Spotlight, b.Spotlight) {
		t.Fatal("spotlight membership/reasons/order are not deterministic")
	}
	seen := map[string]bool{}
	causal := false
	for _, slide := range a.Spotlight {
		if seen[slide.Entry.ID] || len([]rune(slide.Entry.Overview)) > 300 || slide.Reason.Text.Code == "" {
			t.Fatalf("invalid spotlight %+v", slide)
		}
		seen[slide.Entry.ID] = true
		if slide.Reason.Kind == "because_you_watched" {
			causal = true
			if slide.Reason.Source == nil || slide.Reason.Source.ID != source.Public {
				t.Fatal("invented reason")
			}
		}
		if slide.Entry.Kind == "show" && slide.ResumeTarget == nil {
			t.Fatal("available show lacks real play target")
		}
	}
	if !causal {
		t.Fatal("history-backed reason absent")
	}
	overviews := 0
	for _, q := range trace() {
		if strings.Contains(q, "substr(COALESCE(sh.overview,d.overview") {
			overviews++
		}
	}
	if overviews > 5 {
		t.Fatalf("overview hydration %d candidates", overviews)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), `"hero"`) {
		t.Fatal("retired hero serialized")
	}
	c.Exec(`DELETE FROM personal_history WHERE profile_id='p'`)
	empty, err := s.HomeSingleRow(r, "because_you_watched", HomeRowPage{})
	if err != nil || len(empty.Entries) != 0 || empty.Reason != nil {
		t.Fatalf("no history must not fabricate reason: %+v %v", empty, err)
	}
}

func TestHomeFeaturedCollectionAndAudioComposition(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture()
	collection, err := s.CreateCollection("movies", "A real series")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2", "m3", "m4"} {
		if err = s.SetCollectionItem(collection.ID, homeItem(t, db, id).Public, true); err != nil {
			t.Fatal(err)
		}
		c.Fields(homeItem(t, db, id).ID, map[string]any{"poster_url": "/poster/" + id})
	}
	part := homeItem(t, db, "p2")
	book := homeItem(t, db, "book")
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,'book-play')`, part.ID)
	c.Exec(`INSERT INTO progress_activity VALUES('p','books',?,?,'paused')`, part.ID, r.Now.Format(time.RFC3339))
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES('p',?,?,'book-play',120000,0,0)`, book.ID, part.ID)
	c.Drain()
	row, err := s.HomeSingleRow(r, "featured_collection", HomeRowPage{})
	if err != nil {
		t.Fatal(err)
	}
	if row.Collection == nil || row.Collection.Collection.ID != collection.ID || row.Collection.Collection.ItemCount != 4 || len(row.Collection.Members) != 4 || row.Collection.BackdropURL == "" || row.Collection.FirstYear == nil || row.Collection.LastTitle == "" || row.SeeAll.Type != "collection" || row.SeeAll.ResourceID != collection.ID {
		t.Fatalf("featured %+v", row)
	}
	audio, err := s.HomeSingleRow(r, "listen_next", HomeRowPage{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, e := range audio.Entries {
		kinds[e.Kind] = true
		if e.Kind == "book" {
			if e.ID != book.Public || e.Playback == nil || e.Playback.ItemID != part.Public || e.ProgressSeconds == nil || *e.ProgressSeconds != 5520 || e.Duration == nil || *e.Duration != 16200 {
				t.Fatalf("whole-book resume facts %+v", e)
			}
		}
	}
	if !kinds["album"] || !kinds["book"] || len(kinds) != 2 || audio.SubtitleText.Fallback != "Audiobooks and music" || audio.SeeAll == nil {
		t.Fatalf("mixed real audio %+v", audio)
	}
	books, err := s.HomeSingleRow(r, "continue_books", HomeRowPage{})
	if err != nil || rowTotal(books) != 1 || books.Entries[0].Kind != "book" || books.Entries[0].ID != book.Public || !reflect.DeepEqual(books.Platforms, []string{"web", "phone"}) {
		t.Fatalf("books only %+v %v", books, err)
	}
	c.Exec(`DELETE FROM progress_activity WHERE profile_id='p'`)
	empty, err := s.HomeSingleRow(r, "continue_books", HomeRowPage{})
	if err != nil || rowTotal(empty) != 0 {
		t.Fatal("empty conditional books", empty, err)
	}
}

func TestHomeMergedRecentLargeFixturePagesPastTheOldWorkBound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	lib := c.Handle("movies")
	for i := 0; i < 240; i++ {
		item := c.Movie(lib, fmt.Sprintf("/movies/new-%03d.mkv", i), fmt.Sprintf("New %03d", i), 2026)
		c.Fields(item.ID, map[string]any{"added_text": fmt.Sprintf("2026-09-01T00:%02d:%02d.000Z", i/60, i%60)})
	}
	c.Drain()
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	r := homeRequestFixture("movies", "tv")
	row, err := s.WithContext(ctx).HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 7})
	if err != nil || rowTotal(row) != 246 || len(row.Entries) != 7 {
		t.Fatalf("bounded union %+v %v", row, err)
	}
	for _, q := range trace() {
		if strings.Contains(q, "SELECT id,ord FROM (") && !strings.Contains(q, "LIMIT 8") && !strings.Contains(q, "WHERE id IN(SELECT value FROM json_each(?))") {
			t.Fatalf("row hydrated more than its bounded page: %s", q)
		}
	}
}

func TestHomeNewCompositionReappliesCurrentRestriction(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	adult := homeItem(t, db, "m5")
	c.Attributes(adult.ID, "contentRating", "R")
	c.Exec(`INSERT OR REPLACE INTO content_rating_ages(value_key,minimum_age)VALUES('r',17)`)
	c.Drain()
	age := 13
	r := homeRequestFixture("movies")
	r.Viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &age}
	if err := s.RebuildVisibilityClass(context.Background(), "movies", r.Viewer.Restrictions); err != nil {
		t.Fatal(err)
	}
	row, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range row.Entries {
		if e.ID == adult.Public {
			t.Fatal("denied title in merged recent")
		}
	}
}

func TestHomeCommunityUsesThirtyDaysAndPreservesPrivacyThreshold(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest("movies")
	r.CommunityActivity = true
	engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["movies"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, f.db)
	for i, key := range []string{persistence.PersonalOwnerKey("local", "a", "p"), persistence.PersonalOwnerKey("local", "b", "q"), persistence.PersonalOwnerKey("local", "c", "r")} {
		at := r.Now.Add(-20 * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
		engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed)VALUES(?,?,?,?,?,1,600000,0,1)`, fmt.Sprint(i), key, f.items["m1"].ID, at, at)
	}
	f.c.Drain()
	row, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1})
	if err != nil || rowTotal(row) != 1 || row.Entries[0].ID != f.items["m1"].Public || row.Period == nil || row.Period.Days != 30 || row.Period.Metric != "distinct_eligible_viewers" || row.Title != "Popular on this server" {
		t.Fatalf("30-day ranked row %+v %v", row, err)
	}
	expired := r.Now.Add(-30*24*time.Hour - time.Second).Format("2006-01-02T15:04:05.000Z")
	engineExec(t, f, `UPDATE personal_history SET updated_at=? WHERE id='2'`, expired)
	empty, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{})
	if err != nil || rowTotal(empty) != 0 {
		t.Fatal("two eligible viewers disclosed popularity", empty, err)
	}
	engineExec(t, f, `UPDATE personal_history SET updated_at=? WHERE id='2'`, r.Now.Format("2006-01-02T15:04:05.000Z"))
	storePreference(t, f.db, identity.Viewer{Authority: "local", AccountID: "b", ProfileID: "q"}, map[string]any{"privacy.pauseWatchHistory": true})
	empty, err = s.HomeSingleRow(r, "community_watching", HomeRowPage{})
	if err != nil || rowTotal(empty) != 0 {
		t.Fatal("paused viewer contributed", empty, err)
	}
}

func TestHomeCommunityRanksOnlyVideoBeforeTotalsAndPaging(t *testing.T) {
	f := recommendationFixtureDB(t)
	s := New(f.db)
	r := engineRequest()
	r.CommunityActivity = true
	engineExec(t, f, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('a','a',x'00','p',1),('b','b',x'00','q',1),('c','c',x'00','r',1);UPDATE direct_memberships SET allowed_libraries='["movies","tv","music","books"]' WHERE account_id IN('b','c')`)
	shareFixtureProfiles(t, f.db)
	at := r.Now.Add(-time.Hour).Format("2006-01-02T15:04:05.000Z")
	for i, key := range []string{persistence.PersonalOwnerKey("local", "a", "p"), persistence.PersonalOwnerKey("local", "b", "q"), persistence.PersonalOwnerKey("local", "c", "r")} {
		for _, name := range []string{"m1", "e101", "t1", "p1", "m2"} {
			if name == "m2" && i == 2 {
				continue // Two viewers remain below the unchanged privacy threshold.
			}
			engineExec(t, f, `INSERT INTO personal_history(id,profile_id,item_id,started_at,updated_at,sequence,position,unit,completed)VALUES(?,?,?,?,?,1,600000,0,1)`, fmt.Sprintf("%d-%s", i, name), key, f.items[name].ID, at, at)
		}
	}
	f.c.Drain()
	first, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1})
	if err != nil || rowTotal(first) != 2 || len(first.Entries) != 1 || first.Start != 0 || first.NextCursor == "" || !first.HasMore || first.Period == nil || first.Period.Days != 30 {
		t.Fatalf("first video rank: %+v %v", first, err)
	}
	second, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || rowTotal(second) != 2 || second.Start != 1 || len(second.Entries) != 1 || second.NextCursor != "" || second.HasMore {
		t.Fatalf("second video rank: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, row := range []HomeRow{first, second} {
		for _, entry := range row.Entries {
			if entry.Kind != "movie" && entry.Kind != "show" {
				t.Fatalf("square audio work in poster rank: %+v", entry)
			}
			seen[entry.ID] = true
		}
	}
	if len(seen) != 2 || !seen[f.items["m1"].Public] || !seen[f.items["show"].Public] {
		t.Fatalf("video membership: %v", seen)
	}
	// Home narrows its source; the shared engine still admits eligible audio.
	shared, err := s.recommendationCandidates(r, "community_watching", "")
	if err != nil || !recHas(shared, f.items["album"].Public) || !recHas(shared, f.items["book"].Public) {
		t.Fatalf("shared audio recommendation behavior changed: %+v %v", shared, err)
	}
	// Audio changes cannot change this Home row's selected membership/fingerprint.
	engineExec(t, f, `DELETE FROM personal_history WHERE item_id IN(?,?)`, f.items["t1"].ID, f.items["p1"].ID)
	unchanged, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || rowTotal(unchanged) != 2 || unchanged.Start != 1 || len(unchanged.Entries) != 1 || unchanged.Entries[0].ID != second.Entries[0].ID {
		t.Fatalf("audio altered video paging: %+v %v", unchanged, err)
	}
	engineExec(t, f, `DELETE FROM personal_history WHERE id='2-m1'`)
	if _, err = s.HomeSingleRow(r, "community_watching", HomeRowPage{Limit: 1, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("changed eligible video membership did not fence the cursor", err)
	}
	private, err := s.HomeSingleRow(r, "community_watching", HomeRowPage{})
	if err != nil || rowTotal(private) != 1 || len(private.Entries) != 1 || private.Entries[0].ID != f.items["show"].Public {
		t.Fatalf("subthreshold video leaked: %+v %v", private, err)
	}
}

func TestHomeBooksContinuationFiltersMusicBeforeItsPageLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	c := catalogtest.New(t, db)
	s := New(db)
	r := homeRequestFixture("music", "books")
	part := homeItem(t, db, "p2")
	book := homeItem(t, db, "book")
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id)VALUES('p',?,120000,0,'book')`, part.ID)
	c.Exec(`INSERT INTO progress_activity VALUES('p','books',?,?,'paused')`, part.ID, r.Now.Add(-2*time.Hour).Format(time.RFC3339))
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES('p',?,?,'book',120000,0,0)`, book.ID, part.ID)
	for i := 0; i < 45; i++ {
		song := c.Song(homeItem(t, db, "album"), i+10, fmt.Sprintf("/music/busy-%02d.flac", i), fmt.Sprintf("Busy %02d", i))
		c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id)VALUES('p',?,120000,0,'music')`, song.ID)
		c.Exec(`INSERT INTO progress_activity VALUES('p','music',?,?,'paused')`, song.ID, r.Now.Add(-time.Duration(i)*time.Minute).Format(time.RFC3339))
	}
	c.Drain()
	row, err := s.HomeSingleRow(r, "continue_books", HomeRowPage{Limit: 12})
	if err != nil || rowTotal(row) != 1 || len(row.Entries) != 1 || row.Entries[0].Kind != "book" || len(row.Resumes) != 1 || row.Resumes[0].Target.ID != part.Public {
		t.Fatalf("music consumed book candidate budget: %+v %v", row, err)
	}
}

func TestHomeSpotlightDoesNotInventMissingOverview(t *testing.T) {
	db := homeFixture(t)
	c := catalogtest.New(t, db)
	c.Exec(`UPDATE catalog_item_details SET overview=''`)
	c.Exec(`UPDATE catalog_shows SET overview=''`)
	c.Drain()
	doc, err := New(db).HomeRows(homeRequestFixture("movies", "tv"))
	if err != nil || len(doc.Spotlight) == 0 {
		t.Fatal(doc, err)
	}
	for _, spot := range doc.Spotlight {
		if spot.Entry.Overview != "" {
			t.Fatal("missing source metadata was fabricated", spot.Entry)
		}
		if spot.Reason.Text.Code == "" {
			t.Fatal("missing real selection reason", spot)
		}
	}
}
