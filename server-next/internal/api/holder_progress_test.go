package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestShowAndSeasonProgressFollowsVisibleEpisodes(t *testing.T) {
	th := newTheatre(t)
	ctx, token := context.Background(), th.owner.AccessToken
	lib := th.libs[db.LibraryShows]
	check := func(want int) {
		t.Helper()
		for _, kind := range []string{"show", "season"} {
			query := fmt.Sprintf("/v1/items?library=%s&kind=%s&total=true", lib.PublicID, kind)
			for _, flag := range []bool{true, false} {
				n := want
				if !flag {
					n = 1 - n
				}
				if page := get[ItemPage](t, th.s, token, fmt.Sprintf("%s&inProgress=%t", query, flag)); *page.Total != n {
					t.Fatalf("%s inProgress=%t: %+v, want %d", kind, flag, page, n)
				}
			}
		}
	}
	edit := func(item string, body map[string]any) {
		t.Helper()
		th.clock.Set(th.clock.Now().Add(time.Millisecond))
		if code := status(t, th.s, "PUT", "/v1/items/"+item+"/state", token, body); code != 200 {
			t.Fatal(code)
		}
	}
	check(0)
	edit(th.eps[0], map[string]any{"positionMs": 60_000})
	check(1)
	page := get[ItemPage](t, th.s, token, "/v1/items?library="+lib.PublicID+"&kind=show,season&inProgress=true&limit=1&total=true")
	last := get[ItemPage](t, th.s, token, "/v1/items?library="+lib.PublicID+"&kind=show,season&inProgress=true&limit=1&cursor="+page.NextCursor)
	if *page.Total != 2 || page.NextCursor == "" || len(last.Items) != 1 || last.Items[0].ID == page.Items[0].ID || last.NextCursor != "" {
		t.Fatal("holder progress does not page through both distinct holders")
	}
	edit(th.eps[0], map[string]any{"positionMs": 0})
	check(0)
	edit(th.eps[0], map[string]any{"watched": true})
	check(1)
	edit(th.eps[1], map[string]any{"watched": true})
	check(0)
	edit(th.eps[1], map[string]any{"watched": false})
	check(1)

	other, err := th.s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "another viewer"})
	if err != nil {
		t.Fatal(err)
	}
	place := int64(60_000)
	if _, _, err := th.s.db.SetState(ctx, other, th.eps[0], db.StateEdit{PositionMS: &place}); err != nil {
		t.Fatal(err)
	}
	locks, err := th.s.db.MetadataLocks(ctx, th.eps[0])
	if err != nil {
		t.Fatal(err)
	}
	rating := "NC-17"
	if _, err := th.s.db.EditMetadata(ctx, th.eps[0], locks.Revision, db.MetadataEdit{ContentRating: &rating}); err != nil {
		t.Fatal(err)
	}
	profiles, err := th.s.db.Profiles(ctx, other.UserID)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("other profiles: %v, %v", profiles, err)
	}
	age := 13
	if _, err := th.s.db.SetProfileRestrictions(ctx, other.UserID, profiles[0].PublicID,
		db.Restrictions{AllLibraries: true, MaxRatingAge: &age, AllowUnrated: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := th.s.db.Item(ctx, other, th.eps[0]); err != db.ErrNotFound {
		t.Fatalf("episode was not hidden: %v", err)
	}
	for _, kind := range []db.ItemKind{db.KindShow, db.KindSeason} {
		q := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}}
		all, err := th.s.db.Items(ctx, other, q)
		if err != nil || len(all.Items) != 1 {
			t.Fatalf("the holder with a visible episode is hidden: %+v, %v", all, err)
		}
		q.Conditions = []db.Condition{{Field: "inProgress", Values: []string{"true"}}}
		page, err := th.s.db.Items(ctx, other, q)
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("another profile's watched state or a hidden episode made %s in progress: %+v, %v", kind, page, err)
		}
	}
}
