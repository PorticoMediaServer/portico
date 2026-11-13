package api

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// Use the same booking writes as the recorder. Book ahead so its background worker
// cannot claim a fixture between booking and marking its capture complete.
func groupBooking(t *testing.T, s *Server, token string, ch db.LiveChannel, title, key string, age *int) db.Recording {
	t.Helper()
	ctx := context.Background()
	claims, err := s.tokens.CheckAccess(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.db.ProfileNames(ctx, claims.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := s.db.BookAiring(ctx, db.Booking{ProfileID: claims.ProfileID, ProfileName: names.Name, ProfilePub: names.PublicID,
		ChannelID: ch.ID, ChannelName: ch.Name, Title: title, ProgrammeKey: key, RatingAge: age,
		StartsAt: s.now().Add(time.Hour), EndsAt: s.now().Add(2 * time.Hour), Now: s.now()})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func finishGroupBooking(t *testing.T, s *Server, rec db.Recording, state string) {
	t.Helper()
	if err := s.db.RecordingFinished(context.Background(), rec.ID, state, "", s.now()); err != nil {
		t.Fatal(err)
	}
}

func TestDvrGroupsMatchTheRecordingsScopeAndVisibility(t *testing.T) {
	s, owner, members := seededWith(t, 50, 3)
	ctx := context.Background()
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Aerial"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "1|News|News")}})
	private := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Private"), Audience: new("owner"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "2|Private|Private")}})
	lostSource := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Later removed"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "3|News|Gone")}})
	ownerClaims, err := s.tokens.CheckAccess(owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	channel := func(source string) db.LiveChannel {
		public := get[LiveChannelPage](t, s, owner, "/v1/live/channels?source="+source).Items[0].ID
		ch, err := s.db.LiveChannelByID(ctx, db.Viewer{UserID: ownerClaims.UserID, ProfileID: ownerClaims.ProfileID}, public)
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}
	ch, hidden, lost := channel(src.ID), channel(private.ID), channel(lostSource.ID)
	for n, token := range []string{owner, members[0], members[1]} {
		rec := groupBooking(t, s, token, ch, "News", fmt.Sprint(n), nil)
		finishGroupBooking(t, s, rec, db.RecordingRecorded)
	}
	rec := groupBooking(t, s, owner, ch, "Shared", "shared", nil)
	groupBooking(t, s, members[0], ch, "Shared", "shared", nil)
	finishGroupBooking(t, s, rec, db.RecordingPartial)
	if err := s.db.ChangeRecording(ctx, rec.PublicID, db.RecordingChange{Keep: new(true)}, s.now()); err != nil {
		t.Fatal(err)
	}
	finishGroupBooking(t, s, groupBooking(t, s, owner, hidden, "Private", "private", nil), db.RecordingRecorded)
	finishGroupBooking(t, s, groupBooking(t, s, owner, ch, "Adult", "adult", new(18)), db.RecordingRecorded)
	// A paused/missing channel remains visible to its original booker, not other requesters.
	finishGroupBooking(t, s, groupBooking(t, s, members[0], lost, "Own missing", "missing-own", nil), db.RecordingRecorded)
	missing := groupBooking(t, s, owner, lost, "Requested missing", "missing-shared", nil)
	groupBooking(t, s, members[0], lost, "Requested missing", "missing-shared", nil)
	finishGroupBooking(t, s, missing, db.RecordingRecorded)
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+lostSource.ID, owner, LiveSourceSave{Paused: new(true)}); code != 200 {
		t.Fatal(code)
	}
	// Link a real catalogue title so the group's watched count uses the viewer's own state/visibility.
	item := get[ItemPage](t, s, owner, "/v1/items?library="+libraryOfKind(t, s, owner, "movies")+"&kind=movie&limit=1").Items[0]
	detail, err := s.db.Item(ctx, db.Viewer{UserID: ownerClaims.UserID, ProfileID: ownerClaims.ProfileID}, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.RecordingTitle(ctx, rec.ID, detail.ID); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{owner, members[0]} {
		claims, _ := s.tokens.CheckAccess(token, time.Now())
		if _, err := s.db.SaveRule(ctx, db.RecordingRule{ProfileID: claims.ProfileID, SourceID: ch.SourceID, ChannelID: ch.ID, Title: "News"}, s.now()); err != nil {
			t.Fatal(err)
		}
	}
	// Compare against the existing read, including requesters, ratings, and owner-only sources.
	for _, token := range []string{owner, members[0], members[1], members[2]} {
		for _, scope := range []string{"", "&scope=everyone"} {
			recordings := get[Page[DvrRecording]](t, s, token, "/v1/dvr/recordings?state=recorded"+scope).Items
			want := map[string][4]int{}
			for _, r := range recordings {
				n := want[r.Title]
				n[0]++
				if r.Keep {
					n[1]++
				}
				if r.State == "partial" {
					n[2]++
				}
				if r.ItemID != "" && !r.Watched {
					n[3]++
				}
				want[r.Title] = n
			}
			groups := get[Page[DvrGroup]](t, s, token, "/v1/dvr/groups?sort=title"+scope)
			got := map[string][4]int{}
			for _, g := range groups.Items {
				got[g.Title] = [4]int{g.Recordings, g.Kept, g.Exceptions, g.Unwatched}
				if g.Key != g.Title || g.Recording.Title != g.Title || g.Recording.ID == "" {
					t.Fatalf("group has no genuine newest recording: %+v", g)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("groups%s differ from recordings: got %v want %v", scope, got, want)
			}
			overview := get[DvrOverview](t, s, token, "/v1/dvr?"+scope)
			if overview.Groups != len(want) || overview.Recorded != len(recordings) {
				t.Fatalf("overview%s: %+v, want %d/%d", scope, overview, len(want), len(recordings))
			}
			if token == owner {
				wantRules := 1
				if scope != "" {
					wantRules = 2
				}
				if overview.Rules != wantRules {
					t.Fatalf("rules%s: %d want %d", scope, overview.Rules, wantRules)
				}
			}
		}
	}
	if code := status(t, s, "GET", "/v1/dvr/groups?sort=unknown", owner, nil); code != 400 {
		t.Fatalf("unknown sort answered %d", code)
	}
}

func TestDvrGroupsPageAndAnExactGroupBeyondTheOldClientCap(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	ctx := context.Background()
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Aerial"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "1|News|News")}})
	public := get[LiveChannelPage](t, s, owner, "/v1/live/channels?source="+src.ID).Items[0].ID
	ch, err := s.db.LiveChannelByID(ctx, db.Viewer{UserID: 1, ProfileID: 1}, public)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 1075; n++ {
		title := "Long show"
		if n < 60 {
			title = fmt.Sprintf("Show %02d", n)
		}
		rec := groupBooking(t, s, owner, ch, title, fmt.Sprint(n), nil)
		finishGroupBooking(t, s, rec, db.RecordingRecorded)
	}
	for _, sort := range []string{"title", "recent", "oldest"} {
		seen := map[string]bool{}
		cursor := ""
		for {
			page := get[Page[DvrGroup]](t, s, owner, "/v1/dvr/groups?sort="+sort+"&cursor="+url.QueryEscape(cursor))
			for _, g := range page.Items {
				if seen[g.Key] {
					t.Fatalf("%s repeated group %s", sort, g.Key)
				}
				seen[g.Key] = true
				if g.Title == "Long show" && g.Recordings != 1015 {
					t.Fatalf("group capped: %+v", g)
				}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if len(seen) != 61 {
			t.Fatalf("%s read %d groups, want61", sort, len(seen))
		}
	}
	rows := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=recorded&group="+url.QueryEscape("Long show"))
	if len(rows.Items) != 50 || rows.NextCursor == "" {
		t.Fatalf("exact group does not page: %+v", rows)
	}
	for _, r := range rows.Items {
		if r.Title != "Long show" {
			t.Fatalf("group includes %q", r.Title)
		}
	}
	overview := get[DvrOverview](t, s, owner, "/v1/dvr")
	if overview.Groups != 61 || overview.Recorded != 1075 {
		t.Fatalf("overview capped: %+v", overview)
	}
}

func TestRecordingPagesAndCursorsIgnoreHiddenRows(t *testing.T) {
	s, owner, members := seededWith(t, 50, 1)
	ctx := context.Background()
	channel := func(audience string) db.LiveChannel {
		src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new(audience), Audience: new(audience),
			Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "1|News|News")}})
		public := get[LiveChannelPage](t, s, owner, "/v1/live/channels?source="+src.ID).Items[0].ID
		claims, err := s.tokens.CheckAccess(owner, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ch, err := s.db.LiveChannelByID(ctx, db.Viewer{UserID: claims.UserID, ProfileID: claims.ProfileID}, public)
		if err != nil {
			t.Fatal(err)
		}
		return ch
	}
	shown, hidden := channel("everyone"), channel("owner")
	for n := 0; n < 51; n++ {
		rec := groupBooking(t, s, members[0], shown, "News", fmt.Sprintf("made-%d", n), nil)
		finishGroupBooking(t, s, rec, db.RecordingRecorded)
		groupBooking(t, s, members[0], shown, "News", fmt.Sprintf("next-%d", n), nil)
	}
	// Record page boundaries as well as IDs: a hidden row must not supply a cursor,
	// shorten a page, or create an extra empty page in any of the three lists.
	pages := func(state string) []string {
		t.Helper()
		var out []string
		cursor := ""
		for page := 0; page < 4; page++ {
			got := get[Page[DvrRecording]](t, s, members[0], "/v1/dvr/recordings?scope=everyone&state="+state+"&cursor="+url.QueryEscape(cursor))
			for _, rec := range got.Items {
				out = append(out, rec.ID)
			}
			out = append(out, "cursor="+got.NextCursor)
			if got.NextCursor == "" {
				return out
			}
			cursor = got.NextCursor
		}
		t.Fatal("recordings did not finish paging")
		return nil
	}
	states := []string{"recorded", "history", "upcoming"}
	before := map[string][]string{}
	for _, state := range states {
		before[state] = pages(state)
		if len(before[state]) != 53 { // 51 recordings and two page boundaries
			t.Fatalf("%s fixture has %d IDs and boundaries, want 53", state, len(before[state]))
		}
	}
	for n := 0; n < 60; n++ {
		rec := groupBooking(t, s, owner, hidden, "A hidden title", fmt.Sprintf("hidden-made-%d", n), nil)
		finishGroupBooking(t, s, rec, db.RecordingRecorded)
		groupBooking(t, s, owner, hidden, "A hidden title", fmt.Sprintf("hidden-next-%d", n), nil)
	}
	for _, state := range states {
		if after := pages(state); !reflect.DeepEqual(after, before[state]) {
			t.Errorf("%s pages or cursors changed after hidden rows were added", state)
		}
		for _, title := range []string{"A hidden title", "No such title"} {
			got := get[Page[DvrRecording]](t, s, members[0], "/v1/dvr/recordings?scope=everyone&state="+state+"&group="+url.QueryEscape(title))
			if len(got.Items) != 0 || got.NextCursor != "" {
				t.Errorf("%s revealed hidden or absent group %q: %+v", state, title, got)
			}
		}
	}
}
