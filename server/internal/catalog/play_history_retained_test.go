package catalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A removed profile keeps its final display name in direct_profiles; its plays
// stay in the owner's play history. These fixtures model that retained row.
func retainedHistoryFixture(t *testing.T) (*Service, *sql.DB, time.Time) {
	t.Helper()
	db := homeFixture(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, account := range []struct{ id, profile string }{{"owner", "owner-main"}, {"admin", "admin-main"}, {"member", "member-main"}} {
		if _, e := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'',?,1)`, account.id, account.id, account.profile); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id='admin'`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES
	 ('owner-child','owner','Child',0,1),('owner-kid','owner','Kids',0,2),('member-kid','member','Guest',0,1)`); e != nil {
		t.Fatal(e)
	}
	// Logical retirement frees every private resource but keeps the display name.
	if _, e := db.Exec(`UPDATE direct_profiles SET deleted=1,pin_hash=NULL,art='slate',allowed_libraries=NULL,position=0 WHERE id IN('owner-kid','member-kid')`); e != nil {
		t.Fatal(e)
	}
	retainedPlay(t, db, "p-owner", "owner", "owner-main", "owner", "", now.Add(-5*time.Minute))
	retainedPlay(t, db, "p-child", "owner", "owner-child", "owner", "Child", now.Add(-4*time.Minute))
	retainedPlay(t, db, "p-kid", "owner", "owner-kid", "owner", "Kids", now.Add(-3*time.Minute))
	retainedPlay(t, db, "p-member", "member", "member-main", "member", "", now.Add(-2*time.Minute))
	retainedPlay(t, db, "p-guest", "member", "member-kid", "member", "Guest", now.Add(-time.Minute))
	s := New(db)
	s.Clock = func() time.Time { return now }
	return s, db, now
}

func retainedPlay(t *testing.T, db *sql.DB, playback, account, profile, user, profileName string, at time.Time) {
	t.Helper()
	if _, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms,watched_ms,completed)
	 VALUES(?,'local',?,?,?,?,1,'movies',1,'Movie',?,?,1000,1)`, playback, account, profile, user, profileName, at.UnixMilli(), at.UnixMilli()); e != nil {
		t.Fatal(e)
	}
}

func retainedHistoryQuery(audience *HistoryAudience, now time.Time) PlayHistoryQuery {
	return PlayHistoryQuery{Audience: audience, TimeZone: "UTC", Until: now.Format(time.RFC3339Nano), Period: "7d", Limit: 100}
}

func audienceFor(account, profile string) *HistoryAudience {
	return &HistoryAudience{AccountID: account, ProfileID: profile, Epoch: 1}
}

// Only the owner's main profile reads retained plays of removed profiles, and
// it reads them marked with the name the profile had when it was removed.
func TestPlayHistoryOwnerMainSeesRemovedProfileRowsMarked(t *testing.T) {
	s, _, now := retainedHistoryFixture(t)
	q := retainedHistoryQuery(audienceFor("owner", "owner-main"), now)

	page, e := s.PlayHistory(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if page.Total == nil || *page.Total != 5 || len(page.Entries) != 5 {
		t.Fatalf("owner history: total=%v entries=%d", page.Total, len(page.Entries))
	}
	profiles := map[string]int{}
	for _, entry := range page.Entries {
		profiles[entry.Profile]++
	}
	if profiles["Kids (removed)"] != 1 || profiles["Guest (removed)"] != 1 || profiles["Child"] != 1 || profiles[""] != 2 {
		t.Fatalf("owner profile attribution: %v", profiles)
	}

	summary, e := s.PlaySummary(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if summary.Plays != 5 || summary.People != 2 {
		t.Fatalf("owner summary: %+v", summary)
	}
	active := map[string]int64{}
	for _, person := range summary.MostActive {
		active[person.Name] = person.Plays
	}
	if active["owner · Kids (removed)"] != 1 || active["member · Guest (removed)"] != 1 || active["owner · Child"] != 1 {
		t.Fatalf("owner activity names: %v", active)
	}

	people, e := s.DashboardPeople(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if len(people.Items) != 2 {
		t.Fatalf("owner people: %+v", people.Items)
	}
	for _, person := range people.Items {
		switch person.AccountID {
		case "owner":
			if person.Plays != 3 || strings.Join(person.Profiles, "|") != "Child|Kids (removed)" {
				t.Fatalf("owner people row: %+v", person)
			}
		case "member":
			if person.Plays != 2 || strings.Join(person.Profiles, "|") != "Guest (removed)" {
				t.Fatalf("member people row: %+v", person)
			}
		default:
			t.Fatalf("unexpected person: %+v", person)
		}
	}

	history, e := s.DashboardHistory(context.Background(), q, "movies")
	if e != nil {
		t.Fatal(e)
	}
	var plays int64
	for _, bucket := range history.Buckets {
		for _, kind := range bucket.Kinds {
			plays += kind.Plays
		}
	}
	if plays != 5 || history.Top == nil {
		t.Fatalf("owner activity buckets: %d", plays)
	}
}

// An admin, a member, the owner's own child profile, another account's profile
// and a stale principal all keep the ordinary history: live rows only, unmarked.
func TestPlayHistoryOthersNeverSeeRemovedProfileRows(t *testing.T) {
	s, _, now := retainedHistoryFixture(t)
	for name, audience := range map[string]*HistoryAudience{
		"admin main":        audienceFor("admin", "admin-main"),
		"member main":       audienceFor("member", "member-main"),
		"owner child":       audienceFor("owner", "owner-child"),
		"other profile":     audienceFor("member", "member-kid"),
		"stale owner epoch": {AccountID: "owner", ProfileID: "owner-main", Epoch: 2},
		"no audience":       nil,
	} {
		q := retainedHistoryQuery(audience, now)
		page, e := s.PlayHistory(context.Background(), q)
		if e != nil {
			t.Fatal(name, e)
		}
		if page.Total == nil || *page.Total != 3 || len(page.Entries) != 3 {
			t.Fatalf("%s history: total=%v entries=%d", name, page.Total, len(page.Entries))
		}
		profiles := map[string]int{}
		for _, entry := range page.Entries {
			if strings.Contains(entry.Profile, "(removed)") {
				t.Fatalf("%s saw removed profile: %+v", name, entry)
			}
			profiles[entry.Profile]++
		}
		if profiles["Child"] != 1 || profiles[""] != 2 {
			t.Fatalf("%s attribution: %v", name, profiles)
		}

		summary, e := s.PlaySummary(context.Background(), q)
		if e != nil {
			t.Fatal(name, e)
		}
		if summary.Plays != 3 || summary.People != 2 {
			t.Fatalf("%s summary: %+v", name, summary)
		}
		for _, person := range summary.MostActive {
			if strings.Contains(person.Name, "(removed)") {
				t.Fatalf("%s activity: %v", name, person)
			}
		}

		people, e := s.DashboardPeople(context.Background(), q)
		if e != nil {
			t.Fatal(name, e)
		}
		for _, person := range people.Items {
			if strings.Contains(strings.Join(person.Profiles, "|"), "(removed)") {
				t.Fatalf("%s people: %+v", name, person)
			}
			want := int64(1)
			if person.AccountID == "owner" {
				want = 2
			}
			if person.Plays != want {
				t.Fatalf("%s people plays: %+v", name, person)
			}
		}

		history, e := s.DashboardHistory(context.Background(), q, "movies")
		if e != nil {
			t.Fatal(name, e)
		}
		var plays int64
		for _, bucket := range history.Buckets {
			for _, kind := range bucket.Kinds {
				plays += kind.Plays
			}
		}
		if plays != 3 {
			t.Fatalf("%s activity buckets: %d", name, plays)
		}
	}
}

// Only a local profile can be removed. A Hosted row that happens to carry the
// same account and profile ids keeps its stored attribution: it is neither
// marked "(removed)" nor withheld from an ordinary audience.
func TestRetainedProfileMarkingIsLocalAuthorityOnly(t *testing.T) {
	s, db, now := retainedHistoryFixture(t)
	if _, e := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms,completed)
	 VALUES('p-hosted','hosted','owner','owner-kid','host','Hosted Kid',1,'movies',1,'Movie',?,?,1)`, now.Add(-30*time.Second).UnixMilli(), now.UnixMilli()); e != nil {
		t.Fatal(e)
	}

	ownerPage, e := s.PlayHistory(context.Background(), retainedHistoryQuery(audienceFor("owner", "owner-main"), now))
	if e != nil {
		t.Fatal(e)
	}
	if ownerPage.Total == nil || *ownerPage.Total != 6 {
		t.Fatalf("owner total: %v", ownerPage.Total)
	}
	ownerProfiles := map[string]int{}
	for _, entry := range ownerPage.Entries {
		ownerProfiles[entry.Profile]++
	}
	if ownerProfiles["Hosted Kid"] != 1 || ownerProfiles["Kids (removed)"] != 1 {
		t.Fatalf("owner attribution: %v", ownerProfiles)
	}

	otherPage, e := s.PlayHistory(context.Background(), retainedHistoryQuery(audienceFor("admin", "admin-main"), now))
	if e != nil {
		t.Fatal(e)
	}
	if otherPage.Total == nil || *otherPage.Total != 4 || len(otherPage.Entries) != 4 {
		t.Fatalf("other total: %v entries=%d", otherPage.Total, len(otherPage.Entries))
	}
	otherProfiles := map[string]int{}
	for _, entry := range otherPage.Entries {
		if strings.Contains(entry.Profile, "(removed)") {
			t.Fatalf("other saw removed marking: %+v", entry)
		}
		otherProfiles[entry.Profile]++
	}
	if otherProfiles["Hosted Kid"] != 1 {
		t.Fatalf("hosted row was hidden: %v", otherProfiles)
	}
	summary, e := s.PlaySummary(context.Background(), retainedHistoryQuery(audienceFor("admin", "admin-main"), now))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, person := range summary.MostActive {
		if person.Name == "host · Hosted Kid" {
			found = true
		}
		if strings.Contains(person.Name, "(removed)") {
			t.Fatalf("other summary: %v", summary.MostActive)
		}
	}
	if summary.Plays != 4 || !found {
		t.Fatalf("other summary: %+v", summary)
	}
}

// Retained history never feeds recommendations: the cross-profile readers run
// only for a live contributor, so a removed profile cannot contribute plays.
func TestRemovedProfileNeverFeedsOtherProfilesRecommendations(t *testing.T) {
	s, db, now := retainedHistoryFixture(t)
	removed := identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "owner-kid"}
	live := identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "member-main"}
	for _, viewer := range []identity.Viewer{removed, live} {
		storePreference(t, db, viewer, map[string]any{"privacy.showActivityToMembers": true, "privacy.pauseWatchHistory": false})
	}
	if sharing, e := s.profileSharesActivity(identity.PersonalKey(removed), now); e != nil || sharing {
		t.Fatalf("removed profile shared activity: %v %v", sharing, e)
	}
	if sharing, e := s.profileSharesActivity(identity.PersonalKey(live), now); e != nil || !sharing {
		t.Fatalf("live profile did not share activity: %v %v", sharing, e)
	}
	e := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		contributor, e := s.WithContext(ctx).listeningContributor(identity.PersonalKey(removed), "movies")
		if e != nil {
			return e
		}
		if contributor != nil {
			t.Fatalf("removed profile read as a recommendation contributor")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
