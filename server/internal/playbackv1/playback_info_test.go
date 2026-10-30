package playbackv1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSessionPlaybackComparisonUsesAuthorizedCommittedGeneration(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("ja", "en", "always")
	session := f.start("playback-comparison-start", StartRequest{ItemID: f.items[0].Public})
	ctx := context.Background()
	view, err := f.service.Get(ctx, f.caller, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	info := view.PlaybackInfo
	if info == nil || info.Source.Video == nil || info.Source.Audio == nil || info.Delivered.Video == nil || info.Source.Video.Codec != "h264" || info.Source.Video.Profile != "High" || info.Source.Audio.Codec != "aac" || info.Route != "original" || info.Basis != "committed_delivery_plan" {
		t.Fatalf("actual session comparison %+v err=%v", info, err)
	}
	if view.ObservedPlayback != nil {
		t.Fatal("source/target became observed output")
	}
	if info.Source.Subtitles == nil || info.Source.Subtitles.Language != "en" || info.Source.Subtitles.Format != "srt" || info.Source.Subtitles.Origin != "upload" || info.Delivered.Subtitles.Rendering != "externalText" {
		t.Fatalf("actual pinned subtitle %+v", info)
	}
	original, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE catalog_assets SET width=640,height=360 WHERE token=?`, f.items[0].Token)
	again, err := f.service.Get(ctx, f.caller, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := json.Marshal(again.PlaybackInfo)
	if string(actual) != string(original) {
		t.Fatal("catalogue refresh changed committed comparison")
	}

	// A selected resource does not grant access to another viewer's current
	// private metadata if its ownership changes after the selection.
	f.catalog.Exec(`UPDATE subtitle_resources SET owner='different-viewer' WHERE id=?`, f.tracks[0]["en"])
	private, err := f.service.Get(ctx, f.caller, session.ID)
	if err != nil || private.PlaybackInfo == nil || private.PlaybackInfo.Source.Subtitles != nil {
		t.Fatal("private resource metadata crossed viewer authority", err)
	}
	denied := errors.New("profile can no longer view title")
	blocked := f.caller
	blocked.AuthorizeItem = func(context.Context, string) error { return denied }
	if _, err = f.service.Get(ctx, blocked, session.ID); !errors.Is(err, denied) {
		t.Fatal("ordinary profile authorization bypassed", err)
	}
	f.catalog.Exec(`UPDATE playback_sessions SET generation=generation+1 WHERE id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`, session.ID)
	stale, err := f.service.Get(ctx, f.caller, session.ID)
	if err != nil || stale.PlaybackInfo != nil {
		t.Fatal("media generation mismatch supplied comparison", stale.PlaybackInfo, err)
	}
}
