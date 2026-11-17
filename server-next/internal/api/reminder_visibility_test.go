package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"testing"
	"time"
)

func TestReminderDoesNotKeepAProgrammeHiddenByNewLimits(t *testing.T) {
	s, owner, _ := recordable(t)
	id := programmeID(t, s, owner, "A Grim Film")
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 5).Session.AccessToken, ProfileSelect{}, &selected)
	if code := status(t, s, "PUT", "/v1/live/reminders/"+id, selected.AccessToken, nil); code != 204 {
		t.Fatal(code)
	}
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 13}, nil)
	if got := get[Page[LiveReminderOut]](t, s, selected.AccessToken, "/v1/live/reminders"); len(got.Items) != 0 {
		t.Errorf("hidden reminder remains: %+v", got.Items)
	}
}

func TestLibraryReminderUsesCurrentVisibleProgrammeTitle(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	ctx := context.Background()
	episode := get[Item](t, s, owner, "/v1/items/"+th.eps[0])
	edit := func(id, rating string) {
		t.Helper()
		locks, err := s.db.MetadataLocks(ctx, id)
		if err == nil {
			_, err = s.db.EditMetadata(ctx, id, locks.Revision, db.MetadataEdit{ContentRating: &rating})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	edit(episode.ID, "TV-G")
	ch := makeChannel(t, s, owner, "TV", librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{th.libs[db.LibraryShows].PublicID},
		Kinds: []string{"show"}}})
	from, to := s.now(), s.now().Add(3*time.Hour)
	guide := get[LiveGuide](t, s, owner, "/v1/live/guide?from="+from.UTC().Format(time.RFC3339)+"&to="+to.UTC().Format(time.RFC3339))
	id := ""
	for _, row := range guide.Items {
		if row.Channel.ID == ch.ID {
			for _, p := range row.Programmes {
				if p.ItemID == episode.ID {
					id = p.ID
					break
				}
			}
		}
	}
	if id == "" {
		t.Fatal("episode not in guide")
	}
	if code := status(t, s, "PUT", "/v1/live/reminders/"+id, owner, nil); code != 204 {
		t.Fatal(code)
	}
	edit(episode.RootID, "NC-17")
	main := get[Page[Profile]](t, s, owner, "/v1/profiles").Items[0].ID
	call(t, s, "PUT", "/v1/profiles/"+main+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: new(13), AllowUnrated: true}, nil)
	got := get[Page[LiveReminderOut]](t, s, owner, "/v1/live/reminders")
	if len(got.Items) != 1 || got.Items[0].Title != episode.Title {
		t.Errorf("hidden show in reminder: %+v", got.Items)
	}
	edit(episode.ID, "TV-MA")
	if got := get[Page[LiveReminderOut]](t, s, owner, "/v1/live/reminders"); len(got.Items) != 0 {
		t.Errorf("hidden title remains: %+v", got.Items)
	}
}
