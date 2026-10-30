package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"testing"
)

func TestMusicQualityR28DefaultsAndOptions(t *testing.T) {
	for _, kind := range []string{"song", "track", "audiobook_file", "movie"} {
		for _, transport := range []string{"wifi", "ethernet", "unknown", "satellite", "cellular", "mobile", "wwan", " CELLULAR "} {
			want := Quality{Mode: QualityOriginal}
			if playback.NormalizeTransportClass(transport) == "cellular" {
				if musicKind(kind) {
					want = Quality{Mode: QualityLimit, MaxAudioBitrateKbps: 320}
				} else if kind == "movie" {
					want = Quality{Mode: QualityFixed, ID: "720p-high"}
				}
			}
			if got := defaultQuality("", "", transport, kind); got != want {
				t.Fatal(kind, transport, got, want)
			}
			for _, saved := range playback.SavedQualities() {
				if got := defaultQuality(saved, "", transport, kind); got != savedQuality(saved) {
					t.Fatal("saved choice replaced", kind, transport, saved, got)
				}
			}
			if musicKind(kind) {
				for _, p := range qualityladder.Music() {
					q := defaultQuality("", p.ID, transport, kind)
					if err := q.Validate("quality"); err != nil {
						t.Fatal(q, err)
					}
					choice := playback.V1Choice{}
					q.apply(&choice)
					if p.AudioKbps > 0 && (!choice.Limit || choice.MaxAudioBitrateBPS != p.AudioKbps*1000 || choice.MaxVideoBitrateBPS != 0 || choice.MaxVideoHeight != 0) {
						t.Fatal(choice, p)
					}
				}
			}
		}
	}
	for _, bps := range []int{0, 96000, 192000, 320000, 500000} {
		menu := musicQualityOptions(SourceFacts{BitrateKbps: bps / 1000})
		if len(menu) != 4 || menu[0].Mode != QualityOriginal {
			t.Fatal(menu)
		}
		for i, p := range qualityladder.Music()[1:] {
			q := menu[i+1]
			if p.ID == "cd-quality" {
				if q.Mode != QualityCD {
					t.Fatal(q)
				}
				continue
			}
			if q.Mode != QualityLimit || q.ID != "" || q.MaxAudioBitrateKbps != p.AudioKbps || q.SameAsOriginal != (bps > 0 && bps <= p.AudioKbps*1000) {
				t.Fatal(bps, q, p)
			}
		}
	}
}

func TestMusicQualityR28StartReplayAndOverrides(t *testing.T) {
	f := newCarryFixture(t, true)
	f.catalog.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`)
	f.caller.TransportClass = "cellular"
	first := f.start("music-mobile-default", StartRequest{ItemID: f.items[0].Public})
	want := Quality{Mode: QualityLimit, MaxAudioBitrateKbps: 320}
	if got := f.stored(first.ID).Quality; got != want {
		t.Fatal(got)
	}
	if first.Presentation.AudioRender == nil || first.Presentation.AudioRender.Mode != "direct" {
		t.Fatal("source below ceiling was converted", first.Presentation)
	}
	f.caller.SavedMusicQuality = func(context.Context) (string, error) { return "original", nil }
	replay := f.start("music-mobile-default", StartRequest{ItemID: f.items[0].Public})
	if replay.ID != first.ID || f.stored(replay.ID).Quality != want {
		t.Fatal("replay re-read defaults", replay)
	}
	saved := f.start("music-saved-original", StartRequest{ItemID: f.items[0].Public})
	if f.stored(saved.ID).Quality.Mode != QualityOriginal {
		t.Fatal(saved)
	}
	for _, q := range []Quality{{Mode: QualityOriginal}, {Mode: QualityLimit, MaxAudioBitrateKbps: 96}} {
		f.caller.SavedMusicQuality = func(context.Context) (string, error) { return "256", nil }
		session := f.start("music-explicit-"+q.Mode, StartRequest{ItemID: f.items[0].Public, Quality: &q})
		if f.stored(session.ID).Quality != q {
			t.Fatal("session override lost", session)
		}
	}
	f.caller.SavedMusicQuality = nil
	queued := f.queue()
	if queued.Session == nil || f.stored(queued.Session.ID).Quality != want {
		t.Fatal("queue default lost", queued)
	}
	c := f.caller
	c.SavedMusicQuality = func(context.Context) (string, error) { return "", errors.New("read failed") }
	if got := c.defaultQuality(context.Background(), "song"); got.Mode != QualityOriginal {
		t.Fatal("failed read selected mobile", got)
	}
	// The options producer uses the same quality resolution as starts.
	for _, savedMusic := range []string{"", "original", "128"} {
		view, err := f.service.Options(context.Background(), f.caller.Principal, f.items[0].Public, Preview{}, nil, Reach{TransportClass: "cellular", SavedMusicQuality: savedMusic})
		if err != nil || view.Preferred == nil || view.Preferred.Quality != defaultQuality("", savedMusic, "cellular", "song") || len(view.Qualities) != 4 {
			t.Fatal(view, err)
		}
		if view.Qualities[2].ID != "" || view.Qualities[2].MaxAudioBitrateKbps != 320 {
			t.Fatal(view.Qualities)
		}
	}
}

func TestMusicQualityR28RenderRetainsOwnerAudioCeiling(t *testing.T) {
	f := newCarryFixture(t, true)
	f.caller.TransportClass = "cellular"
	f.catalog.Exec(`UPDATE audio_render_facts SET bit_rate=192000`)
	f.catalog.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`)
	cfg := playback.DefaultDeliveryConfiguration()
	cfg.MaxAudioBitrateBPS = 96000
	f.service.Playback.ConfigureDelivery(musicDeliverySettings{cfg}, func(tx *sql.Tx, viewer identity.Viewer, device string) (playback.DeliveryPreferenceReader, error) {
		return nil, nil
	}, nil, "")
	caps := playback.BaselineCapabilityDocument()
	caps.Engines.Native.Evidence = playback.EvidenceDeclared
	caps.AudioDecode = []playback.ClientAudioDecode{{Codec: "aac", Containers: []string{"mp4", "adts"}, MaxSampleRate: 96000, MaxChannels: 2}}
	if _, err := f.service.Playback.PutCapabilities(context.Background(), f.caller.Principal, f.caller.DeviceID, -1, caps); err != nil {
		t.Fatal(err)
	}
	session := f.start("music-owner-audio-ceiling", StartRequest{ItemID: f.items[0].Public})
	if session.Presentation.AudioRender == nil || session.Presentation.AudioRender.Mode != "converted" {
		t.Fatal("renderer bypassed narrower owner ceiling", session.Presentation)
	}
	media, err := loadRow(context.Background(), f.catalog.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := f.catalog.DB.QueryRow(`SELECT plan_json FROM playback_audio_plans WHERE session_id=?`, media.media).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan playback.AudioPlan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Convert == nil || plan.Convert.BitrateBPS > 96000 {
		t.Fatal(plan)
	}
}

type musicDeliverySettings struct {
	cfg playback.DeliveryConfiguration
}

func (s musicDeliverySettings) Delivery() playback.DeliveryConfiguration { return s.cfg }

func TestMusicQualityR28SourceEquivalenceDoesNotRoundDown(t *testing.T) {
	f := newCarryFixture(t, true)
	f.service.Playback = nil
	f.catalog.Exec(`UPDATE catalog_assets SET size=24000001,duration=600`)
	view, err := f.service.Options(context.Background(), f.caller.Principal, f.items[0].Public, Preview{}, nil, Reach{})
	if err != nil || len(view.Qualities) != 4 {
		t.Fatal(view, err)
	}
	if view.Qualities[2].SameAsOriginal {
		t.Fatal("source just above 320kbps rounded down", view.Qualities)
	}
	f.catalog.Exec(`UPDATE catalog_assets SET size=24000000`)
	view, err = f.service.Options(context.Background(), f.caller.Principal, f.items[0].Public, Preview{}, nil, Reach{})
	if err != nil || !view.Qualities[2].SameAsOriginal {
		t.Fatal("source at ceiling was not equivalent", view, err)
	}
}
