package api

import "testing"

func TestAControllerSeesThePlayingVersionAndTracks(t *testing.T) {
	th := newTheatre(t)
	phone := th.owner.AccessToken
	tv := onMain(t, th.s, signIn(t, th.s, 1)).AccessToken
	var opened ReceiverOpen
	call(t, th.s, "PUT", "/v1/receivers/current", tv, nil, &opened)
	version := th.options(tv, th.film, "").Versions[0]
	audio, subtitle := version.Audio[1].ID, version.Subtitles[0].ID
	code, playing := th.start(tv, "", PlaybackStart{ItemID: th.film, VersionID: version.ID,
		Audio: &AudioPick{TrackID: audio}, Subtitles: &SubtitlePick{TrackID: &subtitle}})
	if code != 201 {
		t.Fatalf("TV start: %d", code)
	}
	// Both existing controller reads must describe the other device, not a local default.
	streams := func(token string) []AccountStream {
		t.Helper()
		account := get[AccountPlayback](t, th.s, token, "/v1/playback/sessions")
		receivers := get[Page[ReceiverItem]](t, th.s, token, "/v1/receivers")
		if len(account.Items) != 1 || len(receivers.Items) != 1 || receivers.Items[0].Playing == nil {
			t.Fatalf("controller lists: %+v, %+v", account, receivers)
		}
		return []AccountStream{account.Items[0], *receivers.Items[0].Playing}
	}
	for _, stream := range streams(phone) {
		if stream.VersionID != version.ID || stream.AudioTrackID != audio || stream.Subtitle == nil || stream.Subtitle.TrackID != subtitle {
			t.Fatalf("playing choices: %+v", stream)
		}
	}
	_, request := th.ask(phone, opened.ID, "control-tracks", ReceiverAsk{Kind: "control"})
	th.answer(tv, request.ID, ReceiverAnswer{Answer: "allowed"})
	change := PlaybackChange{Audio: &AudioPick{TrackID: version.Audio[0].ID}, Subtitles: &SubtitlePick{}}
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+playing.ID, phone, change, nil); w.Code != 200 {
		t.Fatalf("controller change: %d %s", w.Code, w.Body)
	}
	for _, stream := range streams(phone) {
		if stream.AudioTrackID != audio || stream.Subtitle == nil || stream.Subtitle.TrackID != subtitle {
			t.Fatalf("a pending command was reported as already playing: %+v", stream)
		}
	}
	// Once the playing device applies it, its session knows the effective new picks.
	if w := call(t, th.s, "PATCH", "/v1/playback/sessions/"+playing.ID, tv, change, nil); w.Code != 200 {
		t.Fatalf("TV applies change: %d %s", w.Code, w.Body)
	}
	for _, stream := range streams(phone) {
		if stream.VersionID != version.ID || stream.AudioTrackID != version.Audio[0].ID || stream.Subtitle != nil {
			t.Fatalf("new audio and subtitles off: %+v", stream)
		}
	}
	var profile Profile
	call(t, th.s, "POST", "/v1/profiles", phone, ProfileCreate{Name: "No films"}, &profile)
	call(t, th.s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", phone, Restrictions{}, nil)
	var restricted Session
	call(t, th.s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, th.s, 2).Session.AccessToken, ProfileSelect{}, &restricted)
	for _, stream := range streams(restricted.AccessToken) {
		if stream.ItemID != "" || stream.VersionID != "" || stream.AudioTrackID != "" || stream.Subtitle != nil {
			t.Fatalf("a hidden title leaked playback choices: %+v", stream)
		}
	}
}
