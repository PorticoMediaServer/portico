package api

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

func TestLibraryGuideDoesNotNameAHiddenShowOfAVisibleEpisode(t *testing.T) {
	th := newTheatre(t)
	s, owner, ctx := th.s, th.owner.AccessToken, context.Background()
	episode := get[Item](t, s, owner, "/v1/items/"+th.eps[0])
	for _, edit := range []struct{ id, rating string }{{episode.ID, "TV-G"}, {episode.RootID, "NC-17"}} {
		locks, err := s.db.MetadataLocks(ctx, edit.id)
		if err == nil {
			_, err = s.db.EditMetadata(ctx, edit.id, locks.Revision, db.MetadataEdit{ContentRating: &edit.rating})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	ch := makeChannel(t, s, owner, "Shared TV", librarychannels.LibraryChannelBuilder{
		LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{th.libs[db.LibraryShows].PublicID}, Kinds: []string{"show"}}})
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	age := 13
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &age}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)
	if code := status(t, s, "GET", "/v1/items/"+episode.RootID, child.AccessToken, nil); code != 404 {
		t.Fatalf("show should be hidden, got %d", code)
	}
	from, to := s.now(), s.now().Add(3*time.Hour)
	before := programmes(t, s, ch.ID, from, to)
	path := "/v1/live/guide?from=" + from.UTC().Format(time.RFC3339) + "&to=" + to.UTC().Format(time.RFC3339)
	full, limited := get[LiveGuide](t, s, owner, path), get[LiveGuide](t, s, child.AccessToken, path)
	if len(full.Items) != 1 || len(limited.Items) != 1 || limited.Items[0].Channel.ID != ch.ID {
		t.Fatalf("the shared channel is missing: owner %+v, child %+v", full.Items, limited.Items)
	}
	want, got := full.Items[0].Programmes, limited.Items[0].Programmes
	if len(want) != len(got) || len(got) == 0 {
		t.Fatalf("slots changed: owner %d, child %d", len(want), len(got))
	}
	visible, hidden := false, false
	for n, p := range got {
		if p.ID != want[n].ID || !p.StartsAt.Equal(want[n].StartsAt) || !p.EndsAt.Equal(want[n].EndsAt) {
			t.Fatalf("a viewer got a different slot: %+v, %+v", want[n], p)
		}
		switch want[n].ItemID {
		case episode.ID:
			visible = true
			if p.NotAvailable || p.Title != episode.Title || p.EpisodeTitle != "" || p.ItemID != episode.ID {
				t.Fatalf("visible episode borrows a hidden show's name: %+v", p)
			}
		case th.eps[1]:
			hidden = true
			neutral := LiveProgramme{ID: want[n].ID, StartsAt: want[n].StartsAt, EndsAt: want[n].EndsAt, NotAvailable: true}
			if p != neutral {
				t.Fatalf("hidden episode must be a neutral timed entry: %+v", p)
			}
		default:
			t.Fatalf("unexpected programme: %+v", want[n])
		}
	}
	if !visible || !hidden {
		t.Fatalf("need both cases: visible %v, hidden %v", visible, hidden)
	}
	if after := programmes(t, s, ch.ID, from, to); !reflect.DeepEqual(before, after) {
		t.Fatal("reading as another viewer changed the shared schedule")
	}
}
