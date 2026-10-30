package playbackv1

import (
	"context"
	"errors"
	"testing"
)

func TestQualityDefaultsR25CallerAndOptions(t *testing.T) {
	ctx := context.Background()
	for _, audio := range []bool{false, true} {
		f := newCarryFixture(t, audio)
		f.service.Playback = nil // Real catalogue/options projection; no media runtime.
		for _, transport := range []string{"wifi", "unknown", "cellular"} {
			for _, saved := range []string{"", "original", "automatic"} {
				c := f.caller
				c.TransportClass = transport
				c.SavedQuality = func(context.Context) (string, error) { return saved, nil }
				want := Quality{Mode: QualityOriginal}
				if saved == "automatic" {
					want.Mode = QualityAutomatic
				}
				if saved == "" && transport == "cellular" && !audio {
					want = Quality{Mode: QualityFixed, ID: "720p-high"}
				}
				if saved == "" && transport == "cellular" && audio {
					want = Quality{Mode: QualityLimit, MaxAudioBitrateKbps: 320}
				}
				if got := c.defaultQuality(ctx, map[bool]string{false: "movie", true: "song"}[audio]); got != want {
					t.Fatal(audio, transport, saved, got, want)
				}
				view, err := f.service.Options(ctx, c.Principal, f.items[0].Public, Preview{}, nil, Reach{SavedQuality: saved, TransportClass: transport})
				if err != nil || view.Preferred == nil || view.Preferred.Quality != want {
					t.Fatal(audio, transport, saved, view.Preferred, err)
				}
				// An explicit session/preview Original beats a cellular default.
				view, err = f.service.Options(ctx, c.Principal, f.items[0].Public, Preview{Quality: Quality{Mode: QualityOriginal}}, nil, Reach{TransportClass: transport})
				if err != nil || view.Plan != nil && view.Plan.Quality.Mode != QualityOriginal {
					t.Fatal(view, err)
				}
				choice, _, err := f.service.choice(view, Preview{Quality: Quality{Mode: QualityOriginal}}, Reach{TransportClass: transport})
				if err != nil || choice.QualityID != "original" {
					t.Fatal(choice, err)
				}
			}
		}
		c := f.caller
		c.TransportClass = "cellular"
		c.SavedQuality = func(context.Context) (string, error) { return "", errors.New("read failed") }
		if got := c.defaultQuality(ctx, map[bool]string{false: "movie", true: "song"}[audio]); got.Mode != QualityOriginal {
			t.Fatal(got)
		}
	}
}

// Exercise the actual item/queue start and replay writes with a source already
// below the cellular ceiling. Conversion is disabled; no media worker is needed.
func TestQualityDefaultsR25StartOverrideAndReplay(t *testing.T) {
	f := newCarryFixture(t, false)
	f.catalog.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`)
	f.catalog.Exec(`UPDATE asset_streams SET detail_json=json_set(detail_json,'$.width',640,'$.height',360,'$.bitRate',800000) WHERE type='video'`)
	f.caller.TransportClass = "cellular"
	first := f.start("cellular-default", StartRequest{ItemID: f.items[0].Public})
	if got := f.stored(first.ID).Quality; got.Mode != QualityFixed || got.ID != "720p-high" {
		t.Fatal(got)
	}
	f.caller.SavedQuality = func(context.Context) (string, error) { return "original", nil }
	replay := f.start("cellular-default", StartRequest{ItemID: f.items[0].Public})
	if replay.ID != first.ID || f.stored(replay.ID).Quality.ID != "720p-high" {
		t.Fatal("replay reread defaults", replay)
	}
	f.caller.SavedQuality = nil
	for _, quality := range []Quality{{Mode: QualityOriginal}, {Mode: QualityAutomatic}} {
		session := f.start("explicit-"+quality.Mode, StartRequest{ItemID: f.items[0].Public, Quality: &quality})
		if got := f.stored(session.ID).Quality; got != quality {
			t.Fatal("explicit session choice replaced", got, quality)
		}
	}
	queued := f.queue()
	if queued.Session == nil {
		t.Fatal("queue did not start")
	}
	if got := f.stored(queued.Session.ID).Quality; got.Mode != QualityFixed || got.ID != "720p-high" {
		t.Fatal("queue ignored cellular default", got)
	}
}
