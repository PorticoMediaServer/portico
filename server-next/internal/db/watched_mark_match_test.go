package db_test

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// A person chooses, for one show, the match another show already has: the two become one. A
// profile that had finished the show that remains has not finished what it gained.
func TestWatchedMarkFollowsAChosenMatchThatMerges(t *testing.T) {
	ctx := context.Background()
	p := newMarkProbe(t, "Bluey/Season 1/Bluey - S01E01.mkv", "Bluey 2018/Season 1/Bluey 2018 - S01E02.mkv")
	v, err := p.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	kept, other := p.show(v, "Bluey"), p.show(v, "Bluey 2018")
	if err := p.d.ApplyDetails(ctx, db.Background, kept.ID, db.ItemDetails{Match: db.MatchFound, ProviderKey: "tmdb:82728"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.d.SetState(ctx, v, kept.PublicID, db.StateEdit{Watched: &yes}); err != nil {
		t.Fatal(err)
	}
	remaining, err := p.d.ChangeMatch(ctx, db.User, other.ID, db.ItemDetails{Match: db.MatchChosen, ProviderKey: "tmdb:82728"})
	if err != nil || remaining != kept.PublicID {
		t.Fatalf("the two shows did not become one: %q, %v", remaining, err)
	}
	if p.listedWatched(v, "Bluey") {
		t.Error("a show that gained an unwatched episode by a chosen match is still listed as watched")
	}
	p.check("after the chosen match merged the two")
}

// A person chooses another match for a show, and the new record's rating puts the show within
// a limited profile's reach again, with an episode that arrived meanwhile.
func TestWatchedMarkFollowsAChosenMatch(t *testing.T) {
	ctx := context.Background()
	p := newMarkProbe(t, "Bluey/Season 1/Bluey - S01E01.mkv")
	viewers, err := seed.Profiles(ctx, p.d, 2)
	if err != nil {
		t.Fatal(err)
	}
	everyone, limited := viewers[0], viewers[1] // the second is limited to 13
	yes := true
	show := p.show(everyone, "Bluey")
	p.rate(show, "TV-Y7", 7)
	if _, _, err := p.d.SetState(ctx, limited, show.PublicID, db.StateEdit{Watched: &yes}); err != nil {
		t.Fatal(err)
	}
	seven, seventeen := 7, 17
	for _, to := range []struct {
		rating string
		age    *int
	}{{"TV-MA", &seventeen}, {"TV-Y7", &seven}} {
		if _, err := p.d.ChangeMatch(ctx, db.User, show.ID, db.ItemDetails{Match: db.MatchChosen, ProviderKey: "tmdb:" + to.rating,
			Title: "Bluey", ContentRating: to.rating, RatingAge: to.age}); err != nil {
			t.Fatal(err)
		}
		p.check("after a match with the rating " + to.rating + " was chosen")
		if to.age == &seventeen {
			p.write("Bluey/Season 1/Bluey - S01E02.mkv", 301)
			p.scan()
		}
	}
	if p.listedWatched(limited, "Bluey") {
		t.Error("the limited profile's Watched list shows a show with an episode it has not watched")
	}
}
