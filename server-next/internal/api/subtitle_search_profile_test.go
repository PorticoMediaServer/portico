package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles/opensubtitlestest"
)

func TestSubtitleSearchOfAHiddenShowIsUnavailableButKeptSubtitlesPlay(t *testing.T) {
	th := newTheatre(t)
	s, ctx, owner := th.s, context.Background(), th.owner.AccessToken
	episode := get[Item](t, s, owner, "/v1/items/"+th.eps[0])
	provider := opensubtitlestest.New(t)
	s.metadata.UseSubtitles(opensubtitles.New(provider.URL, 0, "key", "owner", "right"))
	kept, err := s.metadata.DownloadSubtitle(ctx, viewerOfToken(t, s, owner), episode.ID, "",
		metadata.SubtitleResult{Ref: "7000001", Language: "eng", Release: "Kept English"})
	if err != nil {
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
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	age := 13
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &age}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)
	get[Item](t, s, child.AccessToken, "/v1/items/"+episode.ID)
	if code := status(t, s, "GET", "/v1/items/"+episode.RootID, child.AccessToken, nil); code != 404 {
		t.Fatalf("show should be hidden, got %d", code)
	}
	before := provider.Requests.Load()
	var problem errorBody
	w := call(t, s, "GET", "/v1/items/"+episode.ID+"/subtitles/search?languages=eng", child.AccessToken, nil, &problem)
	if w.Code != 403 || problem.Error.Code != "not_permitted" || problem.Error.Message != "Not available on this profile" || problem.Error.Retry {
		t.Fatalf("restricted search: %d %s", w.Code, w.Body)
	}
	track := "d" + kept.PublicID
	found := false
	for _, subtitle := range get[ItemSubtitles](t, s, child.AccessToken, "/v1/items/"+episode.ID+"/subtitles").Items {
		found = found || subtitle.ID == track && subtitle.Source == "downloaded"
	}
	if !found {
		t.Fatal("kept subtitle disappeared")
	}
	code, playing := th.start(child.AccessToken, "", PlaybackStart{ItemID: episode.ID, Subtitles: &SubtitlePick{TrackID: &track}})
	if code != 201 || playing.Presentation.Subtitle == nil || playing.Presentation.Subtitle.TrackID != track {
		t.Fatalf("kept subtitle cannot play: %d %+v", code, playing.Presentation)
	}
	if after := provider.Requests.Load(); after != before {
		t.Fatalf("restricted search or kept playback contacted provider: requests %d -> %d", before, after)
	}
	get[SubtitleResults](t, s, owner, "/v1/items/"+episode.ID+"/subtitles/search?languages=eng")
	if provider.Requests.Load() == before {
		t.Fatal("the owner could not search the same episode")
	}
}
