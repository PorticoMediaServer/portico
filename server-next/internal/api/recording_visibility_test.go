package api

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"testing"
)

func TestRecordingsRespectCurrentItemAccess(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Aerial"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "1|News|News")}})
	public := get[LiveChannelPage](t, s, owner, "/v1/live/channels").Items[0].ID
	ch, err := s.db.LiveChannelByID(context.Background(), viewerOfToken(t, s, owner), public)
	if err != nil {
		t.Fatal(err)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 5).Session.AccessToken, ProfileSelect{}, &selected)
	rec := groupBooking(t, s, selected.AccessToken, ch, "Heat", "heat", nil)
	item, err := s.db.Item(context.Background(), viewerOfToken(t, s, owner), th.film)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.RecordingTitle(context.Background(), rec.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	finishGroupBooking(t, s, rec, db.RecordingRecorded)
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 6}, nil)
	if code := status(t, s, "GET", "/v1/items/"+th.film, selected.AccessToken, nil); code != 404 {
		t.Fatal(code)
	}
	if w := call(t, s, "GET", "/v1/dvr/recordings/"+rec.PublicID, selected.AccessToken, nil, nil); w.Code != 404 {
		t.Errorf("hidden recording: %d %s", w.Code, w.Body)
	}
	if got := get[Page[DvrGroup]](t, s, selected.AccessToken, "/v1/dvr/groups"); len(got.Items) != 0 {
		t.Errorf("hidden recording groups: %+v", got.Items)
	}
	if got := get[DvrOverview](t, s, selected.AccessToken, "/v1/dvr"); got.Recorded != 0 || got.Groups != 0 {
		t.Errorf("hidden counts: %+v", got)
	}
	if code := status(t, s, "PATCH", "/v1/dvr/recordings/"+rec.PublicID, selected.AccessToken, DvrRecordingChange{Keep: new(true)}); code != 404 {
		t.Errorf("hidden edit: HTTP %d", code)
	}
	if code := status(t, s, "DELETE", "/v1/dvr/recordings/"+rec.PublicID, selected.AccessToken, nil); code != status(t, s, "DELETE", "/v1/dvr/recordings/rec_absent", selected.AccessToken, nil) {
		t.Errorf("hidden delete: HTTP %d", code)
	}
	call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": nil}, nil)
	if code := status(t, s, "GET", "/v1/dvr/recordings/"+rec.PublicID, selected.AccessToken, nil); code != 200 {
		t.Errorf("refused mutation lost recording: HTTP %d", code)
	}

}
