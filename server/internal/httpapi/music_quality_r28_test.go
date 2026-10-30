package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"testing"
)

func TestMusicQualityR28HTTPSourceAndSession(t *testing.T) {
	f := musicFixture(t, 1)
	headers := map[string]string{playback.TransportClassHeader: "cellular", DeviceClassHeader: "mobile"}
	read := func(want playbackv1.Quality) {
		t.Helper()
		var out playbackv1.Options
		f.call("GET", "/v1/items/"+f.items[0]+"/playback-options", headers, nil, 200, &out)
		if out.Preferred == nil || out.Preferred.Quality != want || len(out.Qualities) != 4 {
			t.Fatal(out)
		}
	}
	read(playbackv1.Quality{Mode: playbackv1.QualityLimit, MaxAudioBitrateKbps: 320})
	f.call("PATCH", "/v1/preferences", nil, map[string]any{"scope": "profile-device-class", "deviceClass": "mobile", "expectedRevision": 1, "idempotencyKey": "music-original-http", "values": map[string]any{"music.qualityMobile": "original"}}, 200, nil)
	read(playbackv1.Quality{Mode: playbackv1.QualityOriginal})
	headers["Idempotency-Key"] = "music-start-original-http"
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", headers, startBody(f.items[0], nil), 201, &session)
	if session.Presentation.Quality == nil || session.Presentation.Quality.Mode != playbackv1.QualityOriginal || session.Presentation.AudioRender == nil || session.Presentation.AudioRender.Mode != "direct" {
		t.Fatal(session)
	}
}

func TestMusicSourceBitrateRequiresMeasuredMediaR36(t *testing.T) {
	f := musicFixture(t, 2)
	if _, err := f.db.Exec(`UPDATE audio_render_facts SET duration_frames=22050,raw_frames=2112+22050+16,bit_rate=0`); err != nil {
		t.Fatal(err)
	}
	var first playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "music-unknown-media-bitrate"}, startBody(f.items[0], nil), 201, &first)
	if first.Presentation.AudioRender.SourceBitrateBPS != 0 {
		t.Fatal("file bytes mistaken for measured media bitrate", first.Presentation.AudioRender)
	}
	if _, err := f.db.Exec(`UPDATE audio_render_facts SET bit_rate=192000`); err != nil {
		t.Fatal(err)
	}
	var next playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "music-measured-media-bitrate"}, startBody(f.items[1], nil), 201, &next)
	if next.Presentation.AudioRender.SourceBitrateBPS != 192000 {
		t.Fatal(next.Presentation.AudioRender)
	}
}
