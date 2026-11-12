package api

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// A programme's page and the owner's tuner clashes say what the asking profile may do with the
// booking, as the recordings read does: the booker may change and cancel it, another profile
// that only sees it may do neither, and the owner may do both.
func TestTheProgrammePageAndTheClashesSayWhoMayChangeABooking(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	folder := t.TempDir()
	options := s.dvrOptions()
	options.Folder, options.Floor = filepath.Join(folder, "Recorded TV"), 0
	s.setDVROptions(options)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("One tuner"), TunerCount: new(1),
		Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/recordable.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid)

	late, grim := programmeID(t, s, owner, "Late News"), programmeID(t, s, owner, "A Grim Film")
	call(t, s, "POST", "/v1/dvr/recordings", onKid.AccessToken, DvrRecordingBook{Programme: late}, nil)
	call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: grim}, nil)

	if got := get[LiveProgrammeDetail](t, s, onKid.AccessToken, "/v1/live/programmes/"+late); got.Recording == nil || !got.Recording.MayChange ||
		!got.Recording.MayCancel {
		t.Fatalf("the booker's programme page reads %+v", got.Recording)
	}
	if got := get[LiveProgrammeDetail](t, s, owner, "/v1/live/programmes/"+late); got.Recording == nil || !got.Recording.MayChange ||
		!got.Recording.MayCancel {
		t.Fatalf("the owner's programme page reads %+v", got.Recording)
	}
	if got := get[LiveProgrammeDetail](t, s, onKid.AccessToken, "/v1/live/programmes/"+grim); got.Recording == nil || got.Recording.MayChange ||
		got.Recording.MayCancel {
		t.Fatalf("another profile's booking on the kid's programme page reads %+v", got.Recording)
	}
	clashes := get[DvrTuners](t, s, owner, "/v1/admin/dvr/tuners").Conflicts
	if len(clashes) != 1 || !clashes[0].MayChange || !clashes[0].MayCancel {
		t.Fatalf("the owner's clashes read %+v", clashes)
	}
}
