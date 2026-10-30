package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// §5.5: a text subtitle is a WebVTT sidecar under the presentation's grant;
// switching it keeps the generation; off removes it; burning a text track is
// refused (only image and styled tracks burn).
func TestPlaybackV1SubtitleSidecars(t *testing.T) {
	f, subs, principal, source := v1SubtitleFixture(t)
	body := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	var err error
	receipt, err := subs.Mutate(context.Background(), principal, f.items[0], subtitles.Mutation{OperationID: "upload-english", SourceID: source, Scope: "personal", Format: "srt", Language: "en", Title: "English", Rights: "Owner transcript", OffsetUS: "0", Content: &body})
	if err != nil {
		t.Fatalf("subtitle upload failed: %v", err)
	}
	var options playbackv1.Options
	f.call("GET", "/v1/items/"+f.items[0]+"/playback-options", nil, nil, 200, &options)
	found := false
	for _, s := range options.Versions[0].Subtitles {
		found = found || s.ID == receipt.ResourceID && s.Language == "en"
	}
	if !found {
		t.Fatalf("uploaded subtitle missing from options: %+v", options.Versions[0].Subtitles)
	}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "subtitle-key-0000001"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": receipt.ResourceID}}), 201, &s)
	if len(s.Presentation.Subtitles) != 1 || s.Presentation.Subtitles[0].TrackID != receipt.ResourceID || !strings.HasPrefix(s.Presentation.Subtitles[0].URL, "/v1/media/") {
		t.Fatalf("subtitles %+v", s.Presentation.Subtitles)
	}
	if !strings.HasSuffix(s.Presentation.Subtitles[0].URL, ".vtt") {
		t.Fatal("WebVTT URL has no WebVTT suffix")
	}
	payload := f.raw("GET", s.Presentation.Subtitles[0].URL, "", nil, nil)
	if payload.Code != 200 || !strings.HasPrefix(payload.Header().Get("Content-Type"), "text/vtt") || !strings.HasPrefix(payload.Body.String(), "WEBVTT") {
		t.Fatalf("WebVTT delivery: %d %s", payload.Code, payload.Body.String())
	}
	var off playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": s.Revision}, map[string]any{"subtitles": map[string]any{"trackId": nil}}, 200, &off)
	if len(off.Presentation.Subtitles) != 0 || off.Presentation.Generation != s.Presentation.Generation {
		t.Fatalf("off %+v", off.Presentation)
	}
	if w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": off.Revision}, map[string]any{"subtitles": map[string]any{"trackId": "nope"}}); w.Code != 400 {
		t.Fatalf("unknown subtitle: %d", w.Code)
	}
	if w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": off.Revision}, map[string]any{"subtitles": map[string]any{"trackId": receipt.ResourceID, "delivery": "burn"}}); w.Code != 400 {
		t.Fatalf("burn-in: %d", w.Code)
	}
}

func v1SubtitleFixture(t *testing.T) (*v1Fixture, *subtitles.Service, identity.Principal, string) {
	t.Helper()
	f := newV1Fixture(t, 1)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := subtitles.New(subtitles.Options{DB: f.db, Directory: filepath.Join(root, "subtitles"), Storage: storage.New(binary), HelperBinary: binary,
		Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { subs.Close() })
	player := playback.New(f.db)
	player.ConfigureSubtitleDelivery(subs)
	f.v1.Subtitles = subs
	f.handler = New(Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Playback: player, Subtitles: subs, PlaybackV1: f.v1})
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return f, subs, principal, f.records[0].Token
}

// §5.5 burn-in: a styled track the device can't draw is burned into the video.
// The session keeps its id; the presentation gets a new generation and URL,
// and its decision says "burn". Off again returns to a plain stream (another
// generation). The same track with delivery embeddedClient burns nothing.
func v1BurnInFixture(t *testing.T, preferences ...playback.DeliveryPreferenceSource) (*v1Fixture, string) {
	t.Helper()
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	facts, err := decoder.ProbeToolchain(context.Background(), ffmpeg)
	if err != nil {
		t.Fatalf("FFmpeg probe failed: %v", err)
	}
	if !facts.Encoders["libx264"] || !facts.Encoders["aac"] || !facts.Muxers["hls"] || !facts.Muxers["mp4"] {
		decodertest.Unavailable(t, "burn-in planning needs the H.264/AAC HLS toolchain capability")
	}
	// This test plans the burn (the protocol and the delivery plan), it never
	// runs the encoder, so a libass FFmpeg isn't required to exercise it; the
	// rendering itself is covered by playback's ordinary burn-in runtime test.
	facts.Filters["ass"] = true
	previous := decoder.CurrentToolchain()
	decoder.ConfigureToolchain(facts)
	t.Cleanup(func() { decoder.RestoreToolchain(previous) })
	f, subs, principal, source := v1SubtitleFixture(t)
	// Burn-in is an HLS conversion: this server needs its HLS producer.
	hls, err := playback.NewHLS(context.Background(), f.db, filepath.Join(t.TempDir(), "hls"), ffmpeg)
	if err != nil {
		t.Fatalf("create HLS producer: %v", err)
	}
	player := playback.New(f.db)
	player.ConfigureSubtitleDelivery(subs)
	player.ConfigureHLS(hls)
	f.v1.Playback = player
	for _, source := range preferences {
		player.ConfigureDelivery(nil, source, nil, "")
		if f.v1.Playback != player {
			f.v1.Playback.ConfigureDelivery(nil, source, nil, "")
		}
	}
	f.handler = New(Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Playback: player, Subtitles: subs, PlaybackV1: f.v1})
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	styled := "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:02.00,Default,,0,0,0,,{\\b1}Hello\n"
	var receipt subtitles.Receipt
	receipt, err = subs.Mutate(context.Background(), principal, f.items[0], subtitles.Mutation{OperationID: "upload-styled", SourceID: source, Scope: "personal", Format: "ass", Language: "en", Title: "Styled", Rights: "Owner transcript", OffsetUS: "0", Content: &styled, Render: "styled"})
	if err != nil {
		t.Fatalf("subtitle upload failed: %v", err)
	}
	return f, receipt.ResourceID
}

func TestPlaybackV1SubtitleBurnIn(t *testing.T) {
	f, trackID := v1BurnInFixture(t)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`); err != nil {
		t.Fatal(err)
	}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "burn-key-00000000001"}, startBody(f.items[0], nil), 201, &s)
	var burned playbackv1.SessionView
	w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": s.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID}})
	if w.Code != 200 {
		t.Fatalf("burn: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &burned); err != nil {
		t.Fatal(err)
	}
	p := burned.Presentation
	if burned.ID != s.ID || p.Generation <= s.Presentation.Generation || p.URL == s.Presentation.URL || p.Mode != "stream" || p.Decision.Video == nil || p.Decision.Video.Action != "burn" || len(p.Subtitles) != 0 {
		t.Fatalf("burned presentation %+v (was %+v)", p, s.Presentation)
	}
	var plain playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": burned.Revision}, map[string]any{"subtitles": map[string]any{"trackId": nil}}, 200, &plain)
	if plain.Presentation.Generation <= p.Generation || plain.Presentation.Decision.Video != nil && plain.Presentation.Decision.Video.Action == "burn" {
		t.Fatalf("off after burn %+v", plain.Presentation)
	}
	var drawn playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": plain.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID, "delivery": "embeddedClient"}}, 200, &drawn)
	if drawn.Presentation.Generation != plain.Presentation.Generation {
		t.Fatalf("embeddedClient burned: %+v", drawn.Presentation)
	}
	// A styled track asked for as a sidecar is its plain words: no conversion.
	var words playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": drawn.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID, "delivery": "sidecar"}}, 200, &words)
	if words.Presentation.Generation != drawn.Presentation.Generation || words.Presentation.Subtitle == nil || words.Presentation.Subtitle.Delivery != "sidecar" ||
		len(words.Presentation.Subtitles) != 1 || words.Presentation.Subtitles[0].Format != "webvtt" || words.Presentation.Decision.Video != nil && words.Presentation.Decision.Video.Action == "burn" {
		t.Fatalf("a styled track as a sidecar: %+v", words.Presentation)
	}
}

// With "Styled subtitles" off, an engine that cannot draw the style shows the
// track's words beside the stream: no burn-in, so no conversion is needed.
func TestPlaybackV1StyledOffShowsPlainWordsWithoutConverting(t *testing.T) {
	f, trackID := v1BurnInFixture(t, func(*sql.Tx, identity.Viewer, string) (playback.DeliveryPreferenceReader, error) {
		return operations.PreferenceValues{"playback.keepSubtitleStyling": false}, nil
	})
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "styled-off-0000000001"}, startBody(f.items[0], nil), 201, &s)
	var words playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": s.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID}}, 200, &words)
	p := words.Presentation
	if p.Generation != s.Presentation.Generation || p.Subtitle == nil || p.Subtitle.TrackID != trackID || p.Subtitle.Delivery != "sidecar" || len(p.Subtitles) != 1 || p.Subtitles[0].Format != "webvtt" {
		t.Fatalf("styled off: %+v", p)
	}
	// The viewer can still insist on the picture; with converting off that is refused.
	if w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, f.owner.AccessToken, map[string]string{"If-Match": words.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID, "delivery": "burn"}}); w.Code != 403 {
		t.Fatalf("an explicit burn with converting off: %d %s", w.Code, w.Body.String())
	}
	// A start that names the track plans no conversion for it either.
	var started playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "styled-off-0000000002"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": trackID}}), 201, &started)
	if q := started.Presentation; q.Subtitle == nil || q.Subtitle.Delivery != "sidecar" || len(q.Subtitles) != 1 || q.Decision.Video != nil && q.Decision.Video.Action == "burn" {
		t.Fatalf("styled off at start: %+v", q)
	}
}

func TestPlaybackV1SubtitleBurnInRefusesDisabledConversion(t *testing.T) {
	f, trackID := v1BurnInFixture(t)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "burn-denied-00000001"}, startBody(f.items[0], nil), 201, &session)
	w := f.raw("PATCH", "/v1/playback/sessions/"+session.ID, f.owner.AccessToken, map[string]string{"If-Match": session.Revision}, map[string]any{"subtitles": map[string]any{"trackId": trackID}})
	if w.Code != 403 || v1Code(w) != "transcode_not_allowed" {
		t.Fatalf("burn with conversion disabled: %d %s", w.Code, w.Body.String())
	}
	var unchanged playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+session.ID, nil, nil, 200, &unchanged)
	if unchanged.Revision != session.Revision || unchanged.Presentation.Generation != session.Presentation.Generation {
		t.Fatal("refused burn changed the session", unchanged)
	}
}

func TestPlaybackV1SubtitleOriginalUsesGrantRangesAndRevocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, subs, principal, source := v1SubtitleFixture(t)
	body := "[Script Info]\nScriptType: v4.00+\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:02.00,Default,,0,0,0,,Styled\r\n"
	receipt, e := subs.Mutate(context.Background(), principal, f.items[0], subtitles.Mutation{OperationID: "original-ass", SourceID: source, Scope: "personal", Format: "ass", Language: "en", Title: "English", Rights: "Own transcript", OffsetUS: "0", Content: &body})
	if e != nil {
		t.Fatal(e)
	}
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "original-key-00001"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": receipt.ResourceID, "delivery": "embeddedClient"}}), 201, &session)
	if len(session.Presentation.Subtitles) != 1 || session.Presentation.Subtitles[0].Format != "ass" {
		t.Fatal(session.Presentation.Subtitles)
	}
	file := session.Presentation.Subtitles[0]
	if !strings.HasPrefix(file.URL, "/v1/media/subtitles/") || file.FontsURL != strings.TrimSuffix(file.URL, "/original.ass")+"/fonts" {
		t.Fatal("original or font list escaped the subtitle namespace", file)
	}
	full := f.raw("GET", file.URL, "", nil, nil)
	if full.Code != 200 || full.Body.String() != body {
		t.Fatalf("original %d %s", full.Code, full.Body.String())
	}
	partial := f.raw("GET", file.URL, "", map[string]string{"Range": "bytes=0-7"}, nil)
	if partial.Code != 206 || partial.Body.String() != body[:8] || partial.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range %d %s", partial.Code, partial.Body.String())
	}
	head := f.raw("HEAD", file.URL, "", nil, nil)
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatalf("original HEAD %d %s", head.Code, head.Body.String())
	}
	var fonts subtitles.FontFiles
	f.call("GET", file.FontsURL, nil, nil, 200, &fonts)
	// The media grant is tied to the session generation, not the bearer credential.
	grant, _, ok := strings.Cut(strings.TrimPrefix(file.URL, "/v1/media/subtitles/"), "/")
	if !ok {
		t.Fatal("original URL has no grant")
	}
	result, e := f.db.Exec(`UPDATE playback_sessions SET state='stopped' WHERE grant_token=?`, grant)
	if e != nil {
		t.Fatal(e)
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		t.Fatal("grant did not address the playback session", n, e)
	}
	revoked := f.raw("GET", file.URL, "", nil, nil)
	if revoked.Code == 200 || revoked.Code == 206 {
		t.Fatal("stopped session retained subtitle access")
	}
}
