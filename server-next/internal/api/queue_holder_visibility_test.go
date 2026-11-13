package api

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func TestQueueDoesNotNameAHiddenShowOfAVisibleEpisode(t *testing.T) {
	th := newTheatre(t)
	ctx, s, owner := context.Background(), th.s, th.owner.AccessToken
	episode := get[Item](t, s, owner, "/v1/items/"+th.eps[0])
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	age := 13
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &age}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)
	v := viewerOfToken(t, s, child.AccessToken)
	// History already holds the show's name when its rating later changes.
	if _, err := s.db.SaveProgress(ctx, db.User, []db.Progress{{Viewer: v, ItemID: episode.ID, PlayID: "hidden-show",
		At: th.clock.Now(), PositionMS: hour, WatchedMS: hour}}); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct{ id, rating string }{{episode.ID, "TV-G"}, {episode.RootID, "NC-17"}} {
		locks, err := s.db.MetadataLocks(ctx, edit.id)
		if err == nil {
			_, err = s.db.EditMetadata(ctx, edit.id, locks.Revision, db.MetadataEdit{ContentRating: &edit.rating})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, hidden := range []string{episode.RootID, episode.ParentID} {
		if w := call(t, s, "GET", "/v1/items/"+hidden, child.AccessToken, nil, nil); w.Code != 404 {
			t.Fatalf("holder should be hidden, got %d", w.Code)
		}
	}
	var made QueueCreated
	if w := call(t, s, "POST", "/v1/queues", child.AccessToken,
		QueueCreate{Source: playqueue.QueueSource{Items: []string{episode.ID}}}, &made); w.Code != 201 {
		t.Fatalf("creating the visible episode's queue: %d %s", w.Code, w.Body)
	}
	var page QueueEntries
	w := call(t, s, "GET", "/v1/queues/"+made.Queue.ID+"/entries", child.AccessToken, nil, &page)
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].ItemID != episode.ID || len(made.Window) != 1 {
		t.Fatalf("visible episode missing from its queue: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), `"rootTitle"`) || strings.Contains(w.Body.String(), `"parentTitle"`) ||
		made.Window[0].RootTitle != "" || made.Window[0].ParentTitle != "" {
		t.Fatalf("queue names a hidden holder: %s; initial window %+v", w.Body, made.Window)
	}
	// The same visible episode must not leak its holders through the other small projections.
	if detail := get[Item](t, s, child.AccessToken, "/v1/items/"+episode.ID); detail.Root != nil || detail.Parent != nil {
		t.Fatalf("detail names a hidden holder: %+v", detail)
	}
	item, err := s.db.Item(ctx, v, episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	places, err := s.db.TitlePlaces(ctx, v, []int64{item.ID})
	if err != nil || places[item.ID].ParentTitle != "" {
		t.Fatalf("download label names the hidden show: %+v, %v", places, err)
	}
	titles, err := s.db.VisibleTitles(ctx, v, []int64{item.ID})
	if err != nil || titles[item.ID] != episode.Title {
		t.Fatalf("playback label names the hidden show: %+v, %v", titles, err)
	}
	playing, err := s.db.PlaybackItem(ctx, v, episode.ID)
	if err != nil || playing.Root != nil || playing.Parent != nil {
		t.Fatalf("playback item names a hidden holder: %+v, %v", playing, err)
	}
	// The other device can play as an unrestricted profile; its cached show name is still hidden here.
	th.play(owner, episode.ID)
	streams, err := s.compatSessions(newRequest("GET", "/Sessions", child.AccessToken, nil), Caller{UserID: v.UserID, ProfileID: v.ProfileID, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	playingNow := streams.([]map[string]any)[0]["NowPlayingItem"].(map[string]any)
	if _, leaked := playingNow["SeriesName"]; leaked {
		t.Fatalf("compatible session names the hidden show: %+v", playingNow)
	}
	if history := get[HistoryPage](t, s, child.AccessToken, "/v1/history"); len(history.Items) != 1 || history.Items[0].Subtitle != "" {
		t.Fatalf("history names the hidden show: %+v", history.Items)
	}
}
