package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// musicFixture: a v1 fixture whose films play as songs (audio sessions with a
// render plan, spec §18.1).
func musicFixture(t *testing.T, songs int) *v1Fixture {
	t.Helper()
	f := newV1Fixture(t, songs)
	makePlaybackSongs(t, f)
	// Measured facts for every song (the fixture's files are AAC in MP4 as far as
	// the catalog knows, an Apple encode: 2112 priming, 16 padding), and a device
	// whose engine decodes that (spec §3 audioDecode, §18.7).
	addPlaybackRenderFacts(t, f)
	declareDecode(t, f, map[string]any{"codec": "aac", "containers": []string{"mp4", "adts"}, "maxSampleRate": 96000, "maxChannels": 8})
	return f
}

func makePlaybackSongs(t *testing.T, f *v1Fixture) {
	t.Helper()
	original := append([]catalogtest.Item(nil), f.records...)
	artist := f.catalogTest.Artist(f.libraryHandle, "Fixture Artist")
	for i, source := range original {
		item := f.catalogTest.Entity(compactcatalog.Entity{Library: f.libraryHandle, Kind: compactcatalog.Track, Key: "music-track:" + source.Public, Title: fmt.Sprintf("Song %02d", i+1)}, nil)
		f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
			if err := compactcatalog.LinkAssetTx(ctx, tx, item.ID, source.Asset, compactcatalog.Link{}); err != nil {
				return err
			}
			return compactcatalog.SetSongArtistsTx(ctx, tx, item.ID, []int64{artist.ID})
		})
		item.Asset, item.Token = source.Asset, source.Token
		f.records[i] = item
		f.items[i] = item.Public
		f.names[fmt.Sprintf("song%02d", i+1)] = item
	}
	f.names["fixture-artist"] = artist
	f.catalogTest.Drain()
}

func addPlaybackRenderFacts(t *testing.T, f *v1Fixture) {
	t.Helper()
	for _, item := range f.records {
		if _, err := f.db.Exec(`INSERT INTO audio_render_facts(asset_id,size,modified_ns,container,codec,sample_rate,channels,bit_depth,raw_frames,duration_frames,start_frames,end_frames,trim_source,decoder_config,measured_ms)
 SELECT token,size,modified_ns,'mp4','aac',44100,2,0,2112+26460000+16,26460000,2112,16,'itunsmpb','EhA=',1 FROM catalog_assets WHERE token=?`, item.Token); err != nil {
			t.Fatal(err)
		}
	}
}

// declareDecode replaces the device's capability profile with web's plus these
// audioDecode entries.
func declareDecode(t *testing.T, f *v1Fixture, decode ...map[string]any) {
	t.Helper()
	caps := webCapabilities()
	caps["audioDecode"] = decode
	var doc playbackv1.CapabilitiesDocument
	if w := f.raw("GET", "/v1/me/devices/current/capabilities", f.owner.AccessToken, nil, nil); w.Code == 200 {
		_ = json.Unmarshal(w.Body.Bytes(), &doc)
		f.call("PUT", "/v1/me/devices/current/capabilities", map[string]string{"If-Match": playbackv1.ETag(doc.Revision)}, caps, 204, nil)
		return
	}
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, caps, 204, nil)
}

func playable(a *playbackv1.AudioRender) bool {
	return a != nil && (a.Mode == "direct" || a.Mode == "converted")
}

// Spec §18.1 (version 2): an audio presentation says how the client decodes it.
// A device that decodes the file gets it direct, with the exact trim and the
// file's size and prefetch; one that can't gets FLAC or Opus when converting is
// allowed, else "unavailable" with a reason; unmeasured facts are never guessed;
// a video presentation has no plan.
func TestPlaybackV1AudioPresentationSaysHowTheClientDecodes(t *testing.T) {
	f := musicFixture(t, 4)
	start := func(i int, key string) *playbackv1.AudioRender {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": key}, startBody(f.items[i], nil), 201, &s)
		if s.Kind != "audio" || s.Presentation.AudioRender == nil {
			t.Fatalf("audio presentation %+v", s)
		}
		return s.Presentation.AudioRender
	}
	plan := start(0, "render-plan-0000001")
	if plan.Version != 2 || plan.Mode != "direct" || !strings.HasPrefix(plan.URL, "/v1/media/") || !strings.HasSuffix(plan.URL, "/audio") || plan.Codec != "aac" || plan.Container != "mp4" ||
		plan.SampleRate != 44100 || plan.DurationFrames != 26460000 || plan.Trim == nil || *plan.Trim != (playbackv1.AudioRenderTrim{StartFrames: 2112, EndFrames: 16, Source: "itunsmpb"}) || plan.Bytes != 10 || plan.PrefetchBytes != 10 || plan.DecoderConfig != "EhA=" {
		t.Fatalf("direct plan %+v trim %+v", plan, plan.Trim)
	}
	// Opus only: the AAC source is converted to Opus at 48 kHz, exact.
	declareDecode(t, f, map[string]any{"codec": "opus", "containers": []string{"ogg"}})
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`); err != nil {
		t.Fatal(err)
	}
	plan = start(1, "render-plan-0000002")
	if plan.Mode != "converted" || plan.Codec != "opus" || plan.Container != "ogg" || plan.SampleRate != 48000 || plan.DurationFrames != 28800000 || plan.Trim == nil || plan.Trim.StartFrames != 312 || !strings.HasSuffix(plan.URL, "/audio-converted") || plan.Reason == "" {
		t.Fatalf("converted plan %+v", plan)
	}
	// Converting off, and a device that can't decode it: unavailable, and says why.
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	plan = start(2, "render-plan-0000003")
	if plan.Mode != "unavailable" || plan.Reason == "" || plan.URL != "" || plan.Trim != nil {
		t.Fatalf("unavailable plan %+v", plan)
	}
	// Facts not measured (and no measurement here): unavailable, never guessed.
	if _, err := f.db.Exec(`DELETE FROM audio_render_facts`); err != nil {
		t.Fatal(err)
	}
	declareDecode(t, f, map[string]any{"codec": "aac", "containers": []string{"mp4"}})
	plan = start(3, "render-plan-0000004")
	if plan.Mode != "unavailable" || plan.Reason == "" {
		t.Fatalf("an unmeasured track %+v", plan)
	}
	video := newV1Fixture(t, 1)
	var v playbackv1.SessionView
	video.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "render-plan-0000005"}, startBody(video.items[0], nil), 201, &v)
	if v.Presentation.AudioRender != nil {
		t.Fatalf("a video presentation has a render plan: %+v", v.Presentation.AudioRender)
	}
}

// B-10.11: Apple's own renderer has no Opus. Under a bitrate limit below a
// lossless file's, it is given AAC-LC (ADTS) within that limit, with the
// encoder's 1024-frame priming as the trim, instead of no plan at all; a limit
// below what AAC is encoded at is respected by not converting.
func TestPlaybackV1LimitedAppleListeningConvertsToAAC(t *testing.T) {
	f, _ := renderingFixture(t, 3)
	// The songs are 44.1 kHz FLAC at about 900 kbps.
	if _, err := f.db.Exec(`UPDATE audio_render_facts SET container='flac',codec='flac',bit_depth=16,bit_rate=900000,decoder_config='',start_frames=0,end_frames=0,raw_frames=duration_frames,trim_source='flac'`); err != nil {
		t.Fatal(err)
	}
	declareDecode(t, f,
		map[string]any{"codec": "flac", "containers": []string{"flac"}, "maxSampleRate": 192000, "maxChannels": 8, "maxBitDepth": 24, "via": "ExtAudioFile"},
		map[string]any{"codec": "aac", "containers": []string{"mp4", "adts"}, "maxSampleRate": 48000, "maxChannels": 2, "maxBitDepth": 16, "via": "ExtAudioFile"})
	start := func(i int, key string, kbps int) *playbackv1.AudioRender {
		t.Helper()
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": key}, startBody(f.items[i], map[string]any{"quality": map[string]any{"mode": "limit", "maxAudioBitrateKbps": kbps}}), 201, &s)
		return s.Presentation.AudioRender
	}
	plan := start(0, "aac-limit-000000001", 256)
	if plan == nil || plan.Mode != "converted" || plan.Codec != "aac" || plan.Container != "adts" || plan.SampleRate != 44100 || plan.Channels != 2 || plan.DurationFrames != 26460000 ||
		plan.Trim == nil || plan.Trim.StartFrames != 1024 || plan.Trim.Source != "aac-priming" || (1024+plan.DurationFrames+plan.Trim.EndFrames)%1024 != 0 || !strings.HasSuffix(plan.URL, "/audio-converted") {
		t.Fatalf("limited Apple plan %+v trim %+v", plan, plan.Trim)
	}
	stored := func(p *playbackv1.AudioRender) string {
		t.Helper()
		var raw string
		if err := f.db.QueryRow(`SELECT plan_json FROM playback_audio_plans WHERE json_extract(plan_json,'$.id')=?`, p.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if raw := stored(plan); !strings.Contains(raw, `"bitrateBps":256000`) {
		t.Fatalf("the AAC rate stays within the limit: %s", raw)
	}
	plan = start(1, "aac-limit-000000002", 96)
	if raw := stored(plan); plan.Codec != "aac" || !strings.Contains(raw, `"bitrateBps":96000`) {
		t.Fatalf("a lower limit lowers the AAC rate: %+v %s", plan, raw)
	}
	// Within the limit nothing is converted: the FLAC file plays as it is.
	if plan = start(2, "aac-limit-000000003", 2000); plan.Mode != "direct" || plan.Codec != "flac" {
		t.Fatalf("an unlimited Apple plan %+v", plan)
	}
}

// renderingFixture: songs the device decodes (direct plans), converting
// allowed, for spec §18.
func renderingFixture(t *testing.T, songs int) (*v1Fixture, *playback.Service) {
	t.Helper()
	f := musicFixture(t, songs)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`); err != nil {
		t.Fatal(err)
	}
	return f, f.v1.Playback
}

func grantOf(t *testing.T, plan *playbackv1.AudioRender) string {
	t.Helper()
	parts := strings.Split(plan.URL, "/")
	if plan == nil || len(parts) < 4 {
		t.Fatalf("render url %+v", plan)
	}
	return parts[3]
}

// playingQueue: a queue of the fixture's songs, playing its first.
func playingQueue(t *testing.T, f *v1Fixture, key string) playbackv1.QueueReply {
	t.Helper()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil || !playable(q.Session.Presentation.AudioRender) {
		t.Fatalf("a rendered first session: %+v", q.Session)
	}
	return q
}

func prepareNext(t *testing.T, f *v1Fixture, q playbackv1.QueueReply, key string) playbackv1.PreparedNext {
	t.Helper()
	var p playbackv1.PreparedNext
	f.call("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": key}, map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation}, 200, &p)
	return p
}

// Spec §18.2-18.3 (v2's "private, bounded, transfers only on commit"): the
// prepared next item is private (its first render window only, no media URL,
// no session, not in Now Playing, sharing the slot) until committed; the
// commit moves the queue and starts a session that inherits the state; the
// previous one ends "completed" and its grant is refused; a replay of either
// command answers the same.
func TestPlaybackV1QueueTransitionIsPrivateUntilCommitted(t *testing.T) {
	f, player := renderingFixture(t, 3)
	q := playingQueue(t, f, "transition-queue-00001")
	p := prepareNext(t, f, q, "transition-prep-000001")
	if p.Presentation.URL != "" || !playable(p.Presentation.AudioRender) || p.ItemID != f.items[1] {
		t.Fatalf("prepared %+v", p)
	}
	if expires, _ := time.Parse(time.RFC3339Nano, p.ExpiresAt); time.Until(expires) > 61*time.Second || time.Until(expires) < 50*time.Second {
		t.Fatalf("a preparation lives 60 s: %s", p.ExpiresAt)
	}
	next := grantOf(t, p.Presentation.AudioRender)
	if _, _, _, err := player.ResolveDecodeGrant(next); err != nil {
		t.Fatalf("a prepared item's audio: %v", err)
	}
	if _, _, _, err := player.ResolveGrant(next); err == nil {
		t.Fatal("a prepared item's ordinary media route serves")
	}
	var admin playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &admin)
	if admin.Page.Total != 1 {
		t.Fatalf("Now Playing lists the preparation: %d", admin.Page.Total)
	}
	if again := prepareNext(t, f, q, "transition-prep-000001"); again.Token != p.Token {
		t.Fatalf("a replayed prepare made %s (first %s)", again.Token, p.Token)
	}
	var c playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &c)
	if c.Session == nil || c.Session.ID == q.Session.ID || c.Session.ItemID != f.items[1] || c.Session.State != "playing" || c.Session.Kind != "audio" || c.Queue.Current == nil || c.Queue.Current.Position != 1 {
		t.Fatalf("committed %+v %+v", c.Queue, c.Session)
	}
	if c.Session.Presentation.URL == "" {
		t.Fatal("the committed presentation has no media URL")
	}
	var previous playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+q.Session.ID, nil, nil, 200, &previous)
	if previous.State != "ended" || previous.End == nil || previous.End.Reason != "completed" {
		t.Fatalf("the previous session %+v %+v", previous, previous.End)
	}
	if _, _, _, err := player.ResolveGrant(next); err != nil {
		t.Fatalf("the committed item's media: %v", err)
	}
	if _, _, _, err := player.ResolveDecodeGrant(grantOf(t, q.Session.Presentation.AudioRender)); err == nil {
		t.Fatal("the previous grant still serves")
	}
	var replay playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &replay)
	if replay.Session == nil || replay.Session.ID != c.Session.ID {
		t.Fatalf("a replayed commit made %+v", replay.Session)
	}
}

// Spec §18.3 (v2's "fences expiry, source, generation and owner policy"): a
// commit re-checks everything; any change cancels the preparation with the
// reason, and the queue doesn't move.
func TestPlaybackV1QueueTransitionFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name, code, reason string
		change             func(f *v1Fixture, q playbackv1.QueueReply)
	}{
		{"session paused", "prepared_canceled", "session_changed", func(f *v1Fixture, q playbackv1.QueueReply) {
			f.call("PATCH", "/v1/playback/sessions/"+q.Session.ID, map[string]string{"If-Match": q.Session.Revision}, map[string]any{"state": "paused"}, 200, nil)
		}},
		{"queue changed", "prepared_canceled", "queue_changed", func(f *v1Fixture, q playbackv1.QueueReply) {
			f.call("PATCH", "/v1/queues/"+q.Queue.ID, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"repeat": "all"}, 200, nil)
		}},
		{"converting turned off", "prepared_canceled", "policy_changed", func(f *v1Fixture, q playbackv1.QueueReply) {
			if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
				t.Fatal(err)
			}
		}},
		{"source changed", "prepared_canceled", "source_changed", func(f *v1Fixture, q playbackv1.QueueReply) {
			updatePlaybackAssetSize(t, f, f.records[1])
		}},
		{"expired", "prepared_expired", "", func(f *v1Fixture, q playbackv1.QueueReply) {
			f.v1.Now = func() time.Time { return time.Now().Add(61 * time.Second) }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := renderingFixture(t, 3)
			if tc.reason == "policy_changed" {
				// Converting matters only to a converted plan: a device that decodes FLAC but not AAC.
				declareDecode(t, f, map[string]any{"codec": "flac", "containers": []string{"flac"}})
			}
			q := playingQueue(t, f, "fence-queue-0000001")
			p := prepareNext(t, f, q, "fence-prepare-000001")
			tc.change(f, q)
			w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":commit-next", f.owner.AccessToken, nil, map[string]any{"token": p.Token})
			if !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || tc.reason != "" && !strings.Contains(w.Body.String(), `"reason":"`+tc.reason+`"`) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			f.v1.Now = nil
			var now playbackv1.QueueView
			f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &now)
			if now.Current == nil || now.Current.Position != 0 {
				t.Fatalf("the queue moved: %+v", now.Current)
			}
		})
	}
}

// Spec §18.3: a replan of the private presentation between prepare and commit
// changes the bytes the preparation described. Commit keeps the exact prepared
// generation and refuses the drift instead of borrowing the newer plan.
func TestPlaybackV1CommitNextRefusesPreparedGenerationDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, drift := range []string{"generation", "stopped"} {
		t.Run(drift, func(t *testing.T) {
			f, _ := renderingFixture(t, 3)
			q := playingQueue(t, f, "drift-queue-0000001")
			p := prepareNext(t, f, q, "drift-prepare-000001")
			if p.Presentation.Generation < 1 {
				t.Fatalf("prepared presentation carries no exact generation: %+v", p.Presentation)
			}
			// The replay of the same preparation is the stored view, and it conforms to
			// the published prepare-next response.
			w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": "drift-prepare-000001"}, map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation})
			if w.Code != 200 {
				t.Fatalf("preparation replay: %d %s", w.Code, w.Body.String())
			}
			assertSpecResponse(t, "POST", "/v1/queues/{id}:prepare-next", w)
			var media string
			if err := f.db.QueryRow(`SELECT media_session_id FROM playback_v1_prepared WHERE token=?`, p.Token).Scan(&media); err != nil {
				t.Fatal(err)
			}
			change := `UPDATE playback_sessions SET generation=generation+1 WHERE id=?`
			if drift == "stopped" {
				change = `UPDATE playback_sessions SET state='stopped' WHERE id=?`
			}
			if _, err := f.db.Exec(change, media); err != nil {
				t.Fatal(err)
			}
			w = f.raw("POST", "/v1/queues/"+q.Queue.ID+":commit-next", f.owner.AccessToken, nil, map[string]any{"token": p.Token})
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"prepared_canceled"`) || !strings.Contains(w.Body.String(), `"reason":"source_changed"`) {
				t.Fatalf("a replanned preparation committed: %d %s", w.Code, w.Body.String())
			}
			var now playbackv1.QueueView
			f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &now)
			if now.Current == nil || now.Current.Position != 0 {
				t.Fatalf("the queue moved: %+v", now.Current)
			}
		})
	}
}

func updatePlaybackAssetSize(t *testing.T, f *v1Fixture, item catalogtest.Item) {
	t.Helper()
	var size int64
	if err := f.db.QueryRow(`SELECT size FROM catalog_assets WHERE id=?`, item.Asset).Scan(&size); err != nil {
		t.Fatal(err)
	}
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"size": size + 1})
	})
}

// Spec §18.2: a preparation needs a rendered current session of the caller's,
// at its generation, with something after it.
func TestPlaybackV1PrepareNextRefusals(t *testing.T) {
	f, _ := renderingFixture(t, 1)
	q := playingQueue(t, f, "refusal-queue-000001")
	w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": "refusal-prepare-00001"}, map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "queue_ended") {
		t.Fatalf("nothing next: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": "refusal-prepare-00002"}, map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation + 1})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"reason":"session_changed"`) {
		t.Fatalf("another generation: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", f.owner.AccessToken, map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": "refusal-prepare-00003"}, map[string]any{"sessionId": "ps_other", "sessionGeneration": 1})
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"reason":"session_not_current"`) {
		t.Fatalf("another session: %d %s", w.Code, w.Body.String())
	}
}

// Spec §18.5: a completion after the track reported "ended" still starts the
// next entry; the successor inherits the play state and quality request; the
// queue says what plays next (through the fence) and the post-play policy.
func TestPlaybackV1AdvanceAfterEndedInheritsAndSaysWhatsNext(t *testing.T) {
	f := musicFixture(t, 3)
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "advance-ended-0000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(f.items...)}},
		"startPlayback": map[string]any{"state": "paused", "quality": map[string]any{"mode": "limit", "maxVideoBitrateKbps": 4000}}}, 201, &q)
	if q.Queue.Next == nil || !q.Queue.Next.Available || q.Queue.Next.Reason != "ready" || q.Queue.Next.EntryID == nil || *q.Queue.Next.EntryID != q.Window[1].EntryID || q.Queue.PostPlay == nil {
		t.Fatalf("what's next %+v %+v", q.Queue.Next, q.Queue.PostPlay)
	}
	h := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
		return f.raw(method, path, f.owner.AccessToken, nil, body)
	})
	h.report(q.Session.ID, q.Session.Presentation.Generation, map[string]any{"state": "ended", "positionMs": 600_000})
	var ended playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+q.Session.ID, nil, nil, 200, &ended)
	if ended.State != "ended" {
		t.Fatalf("the track didn't end: %+v", ended)
	}
	var head playbackv1.QueueView
	f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &head)
	var next playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":advance", map[string]string{"If-Match": head.Revision}, map[string]any{"reason": "completion"}, 200, &next)
	if next.Session == nil || next.Session.ItemID != f.items[1] || next.Session.State != "paused" {
		t.Fatalf("completion after ended: %+v", next.Session)
	}
	var request string
	if err := f.db.QueryRow(`SELECT request FROM playback_v1_sessions WHERE id=?`, next.Session.ID).Scan(&request); err != nil || !strings.Contains(request, `"limit"`) {
		t.Fatalf("the quality request wasn't inherited: %s %v", request, err)
	}
	var last playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":advance", map[string]string{"If-Match": next.Queue.Revision}, map[string]any{"reason": "next"}, 200, &last)
	if last.Queue.Next == nil || last.Queue.Next.Available || last.Queue.Next.Reason != "end" || last.Queue.Next.EntryID != nil {
		t.Fatalf("at the end: %+v", last.Queue.Next)
	}
}

// Spec §18.1/18.3 (v2's "render metadata is pinned to the presentation"): what
// commits is the plan prepared, whatever the tags say afterwards.
func TestPlaybackV1CommittedPlanIsThePreparedOne(t *testing.T) {
	f, _ := renderingFixture(t, 2)
	q := playingQueue(t, f, "pinned-plan-queue-001")
	p := prepareNext(t, f, q, "pinned-plan-prepare-1")
	if _, err := f.db.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES(?,?,'replaygain_track_gain','embedded','-12 dB')`, f.library, f.records[1].Token); err != nil {
		t.Fatal(err)
	}
	var c playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &c)
	got, want := c.Session.Presentation.AudioRender, p.Presentation.AudioRender
	if got == nil || got.URL != want.URL || got.ID != want.ID || got.DurationFrames != want.DurationFrames || (got.Gain == nil) != (want.Gain == nil) {
		t.Fatalf("committed plan %+v, prepared %+v", got, want)
	}
}

// Spec §18.2 (v2's worker-driven expiry): past 60 s the sweep cancels a
// preparation and its private grant serves nothing, even unasked.
func TestPlaybackV1ExpiredPreparationIsSwept(t *testing.T) {
	f, player := renderingFixture(t, 2)
	q := playingQueue(t, f, "swept-prep-queue-0001")
	p := prepareNext(t, f, q, "swept-prep-prepare-01")
	f.v1.Now = func() time.Time { return time.Now().Add(61 * time.Second) }
	f.v1.SweepQueues(context.Background())
	f.v1.Now = nil
	var state, reason string
	if err := f.db.QueryRow(`SELECT state,cancel_reason FROM playback_v1_prepared WHERE token=?`, p.Token).Scan(&state, &reason); err != nil || state != "canceled" || reason != "expired" {
		t.Fatalf("after the sweep: %s %s %v", state, reason, err)
	}
	if _, _, _, err := player.ResolveDecodeGrant(grantOf(t, p.Presentation.AudioRender)); err == nil {
		t.Fatal("a swept preparation still serves")
	}
}

// Spec §18.1/18.2 over HTTP: before commit a prepared item's audio route
// serves at most prefetchBytes in total, then 403 prepared_limit; a converted
// stream serves only from frame 0. (Only the gates: the fixture's files aren't
// audio, and there's no source storage here.)
func TestPlaybackV1AudioRouteCapsAPrivatePresentation(t *testing.T) {
	f, _ := renderingFixture(t, 2)
	q := playingQueue(t, f, "audio-route-queue-001")
	p := prepareNext(t, f, q, "audio-route-prep-0001")
	plan := p.Presentation.AudioRender
	if plan.PrefetchBytes != 10 {
		t.Fatalf("prefetch %d", plan.PrefetchBytes)
	}
	// The whole 10-byte file is within the budget; the storage-less fixture then fails to open it.
	if w := f.raw("GET", plan.URL, "", map[string]string{"Range": "bytes=0-9"}, nil); w.Code == 403 {
		t.Fatalf("the first bytes of a prepared item: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("GET", plan.URL, "", map[string]string{"Range": "bytes=0-0"}, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "prepared_limit") {
		t.Fatalf("past the prefetch budget: %d %s", w.Code, w.Body.String())
	}
	// A committed item has no budget.
	var c playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &c)
	if w := f.raw("GET", c.Session.Presentation.AudioRender.URL, "", map[string]string{"Range": "bytes=0-0"}, nil); w.Code == 403 {
		t.Fatalf("a committed item's audio: %d %s", w.Code, w.Body.String())
	}
}

// Spec §18 and F-title S20: the server says it offers queue transitions, and
// that v1 plays music, so a music app can take the one-request path.
func TestPlaybackV1CapabilitiesAdvertiseMusicAndTransitions(t *testing.T) {
	f := newV1Fixture(t, 1)
	var caps struct {
		Features map[string]string `json:"features"`
	}
	f.call("GET", "/v1/capabilities", nil, nil, 200, &caps)
	if caps.Features["queueTransitions"] != "enabled" || !strings.Contains(","+caps.Features["playback_v1_kinds"]+",", ",song,") {
		t.Fatalf("features %+v", caps.Features)
	}
}

// F-title S20: playing a whole artist (albums in order) is one request, and the
// first song plays in under 2 s even for a large artist.
func TestPlaybackV1ArtistPlaysInOneRequestQuickly(t *testing.T) {
	testtier.Media(t, "a large artist fixture projected per test")
	f := newV1Fixture(t, 1)
	artist := createLargePlaybackArtist(t, f)
	started := time.Now()
	var q playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "artist-one-request-01"}, map[string]any{"segments": []any{map[string]any{"source": containerSource("artist", artist.Public)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	elapsed := time.Since(started)
	if q.Session == nil || q.Session.Kind != "audio" || q.Queue.Total != 5000 {
		t.Fatalf("artist play: total %d session %+v", q.Queue.Total, q.Session)
	}
	t.Logf("5,000-song artist: first song in %s", elapsed.Round(time.Millisecond))
	if elapsed > 2*time.Second {
		t.Fatalf("the first song took %s", elapsed)
	}
}

func createLargePlaybackArtist(t *testing.T, f *v1Fixture) catalogtest.Item {
	t.Helper()
	var root string
	if err := f.db.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, f.libraryHandle).Scan(&root); err != nil {
		t.Fatal(err)
	}
	asset := f.records[0].Asset
	var artistID int64
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		artistID, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryHandle, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey("big-artist"), Title: "Big Artist"})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, artistID, map[string]any{"local_key": "big-artist"}); err != nil {
			return err
		}
		for albumIndex := 0; albumIndex < 500; albumIndex++ {
			local := fmt.Sprintf("big-album-%04d", albumIndex)
			albumID, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryHandle, Kind: compactcatalog.Album, Parent: artistID, Key: compactcatalog.AlbumKey(local), Title: fmt.Sprintf("Album %04d", albumIndex), Year: 1970 + albumIndex%50})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetFactsTx(ctx, tx, albumID, map[string]any{"artist_id": artistID, "local_key": local}); e != nil {
				return e
			}
			for track := 0; track < 10; track++ {
				n := albumIndex*10 + track
				path := filepath.Join(root, "large-artist", fmt.Sprintf("Song %05d.mp3", n))
				id, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryHandle, Kind: compactcatalog.Track, Parent: albumID, Key: compactcatalog.ItemKey(root, path, 0), Title: fmt.Sprintf("Song %05d", n)})
				if e != nil {
					return e
				}
				if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"album_id": albumID, "track_number": track + 1}); e != nil {
					return e
				}
				if e = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); e != nil {
					return e
				}
				if e = compactcatalog.SetSongArtistsTx(ctx, tx, id, []int64{artistID}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	artist := catalogtest.Item{ID: artistID, Public: f.catalogTest.Public(artistID)}
	f.names["big-artist"] = artist
	f.catalogTest.Drain()
	return artist
}

// Spec §18.1 gains, to the ReplayGain 2.0 reference (−18 LUFS): ReplayGain tags
// as they are; R128 tags converted (+5 dB); without tags the server's loudness
// analysis (−18 − integrated loudness, true peak); album values once every
// track of the album is measured (duration-weighted energy mean, peak = max).
func TestPlaybackV1AudioPlanGains(t *testing.T) {
	f := musicFixture(t, 4)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	tag := func(i int, field, value string) {
		exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES(?,?,?,'embedded',?)`, f.library, f.records[i].Token, field, value)
	}
	tag(0, "replaygain_track_gain", "-6.2 dB")
	tag(0, "replaygain_track_peak", "0.98")
	tag(1, "r128_track_gain", "-1024")
	// Songs 2 and 3 are one album, measured only by analysis.
	artist := f.catalogTest.Artist(f.libraryHandle, "Gain Artist")
	album := f.catalogTest.Album(artist, "Analysis Album", 2020)
	for i, lufs := range map[int]float64{2: -14, 3: -20} {
		f.catalogTest.Fields(f.records[i].ID, map[string]any{"album_id": album.ID, "track_number": i - 1})
		f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetSongArtistsTx(ctx, tx, f.records[i].ID, []int64{artist.ID})
		})
		exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms)
 VALUES(?,?,?,'1','1',1,1,'loudness','EBU-R128','','',600000000,?,1)`, "r"+fmt.Sprint(i), "o"+fmt.Sprint(i), f.records[i].Token, fmt.Sprintf(`{"algorithm":"EBU-R128","integratedLUFS":%g,"truePeakLinear":%g}`, lufs, 0.5+float64(i)/10))
	}
	f.catalogTest.Drain()
	gain := func(i int) *playbackv1.AudioRenderGain {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": fmt.Sprintf("gain-start-%08d", i)}, startBody(f.items[i], nil), 201, &s)
		if s.Presentation.AudioRender == nil || s.Presentation.AudioRender.Gain == nil {
			t.Fatalf("song %d: no gains %+v", i, s.Presentation.AudioRender)
		}
		return s.Presentation.AudioRender.Gain
	}
	near := func(p *float64, want float64) bool { return p != nil && math.Abs(*p-want) < 1e-9 }
	if g := gain(0); !near(g.TrackDB, -6.2) || !near(g.TrackPeak, 0.98) || g.Source != "tags" {
		t.Fatalf("ReplayGain %+v", g)
	}
	if g := gain(1); !near(g.TrackDB, 1) || g.Source != "tags" {
		t.Fatalf("R128 -1024 (-4 dB at -23 LUFS) is +1 dB at -18: %+v", g)
	}
	albumGain := 10 * math.Log10((math.Pow(10, -1.4)+math.Pow(10, -2.0))/2)
	if g := gain(2); !near(g.TrackDB, -4) || !near(g.TrackPeak, 0.7) || !near(g.AlbumDB, -18-albumGain) || !near(g.AlbumPeak, 0.8) || g.Source != "analysis" {
		t.Fatalf("analysis %+v (album %v)", g, albumGain)
	}
}

// A client that lists what its own music engine decodes in its capability
// document gets direct plans for what it decodes.
func TestPlaybackV1ClientProfileDecodeListChoosesDirect(t *testing.T) {
	f := newV1Fixture(t, 1)
	makePlaybackSongs(t, f)
	addPlaybackRenderFacts(t, f)
	caps := webCapabilities()
	caps["audioDecode"] = []map[string]any{{"codec": "aac", "containers": []string{"mp4", "adts"}, "maxSampleRate": 96000, "maxChannels": 2, "via": "webcodecs"}}
	if w := f.raw("PUT", "/v1/me/devices/current/capabilities", f.owner.AccessToken, map[string]string{"Content-Type": "application/json"}, caps); w.Code != 204 {
		t.Fatalf("capability document %d %s", w.Code, w.Body)
	}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "profile-decode-00001"}, startBody(f.items[0], nil), 201, &s)
	if plan := s.Presentation.AudioRender; plan == nil || plan.Mode != "direct" || plan.Codec != "aac" {
		t.Fatalf("plan %+v", s.Presentation.AudioRender)
	}
	// A malformed entry is refused, not half-read.
	bad := webCapabilities()
	bad["audioDecode"] = []map[string]any{{"codec": "aac", "containers": []string{}}}
	if w := f.raw("PUT", "/v1/me/devices/current/capabilities", f.owner.AccessToken, map[string]string{"Content-Type": "application/json"}, bad); w.Code < 400 {
		t.Fatalf("an empty container list was accepted: %d", w.Code)
	}
}

// B7 (F-web): queue entries say which album a song is on, and where, so a client
// can join an album's tracks gaplessly instead of crossfading them.
func TestPlaybackV1QueueEntriesPlaceSongsOnTheirAlbum(t *testing.T) {
	f := musicFixture(t, 2)
	artist := f.catalogTest.Artist(f.libraryHandle, "Queue Artist")
	album := f.catalogTest.Album(artist, "Queue Album", 2020)
	for i, item := range f.records {
		f.catalogTest.Fields(item.ID, map[string]any{"album_id": album.ID, "disc_number": 1, "track_number": i + 1})
		f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetSongArtistsTx(ctx, tx, item.ID, []int64{artist.ID})
		})
	}
	f.catalogTest.Drain()
	q := playingQueue(t, f, "album-entries-queue-01")
	var page playbackv1.EntryPage
	f.call("GET", "/v1/queues/"+q.Queue.ID+"/window?before=0&after=5", nil, nil, 200, &page)
	if len(page.Items) != 2 || page.Items[0].AlbumID != album.Public || page.Items[0].Disc != 1 || page.Items[0].Track != 1 || page.Items[1].Track != 2 {
		t.Fatalf("entries %+v", page.Items)
	}
}

// A direct plan in a container with no index of its own (MP3, ADTS, Ogg) names
// the server's seek index when the file has one: byte offsets with the raw
// frame each starts at, served on the plan's grant. Nothing is converted for a
// part-way start: the converted route never serves a direct plan, whatever the
// owner allows and whatever the device decodes.
func TestPlaybackV1DirectPlanNamesItsSeekIndex(t *testing.T) {
	f := musicFixture(t, 3)
	if _, err := f.db.Exec(`UPDATE audio_render_facts SET container='mp3',codec='mp3',decoder_config='',start_frames=1105,end_frames=0,raw_frames=1105+duration_frames,trim_source='lame'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`); err != nil {
		t.Fatal(err)
	}
	index := audiofacts.EncodeSeekIndex(&audiofacts.SeekIndex{Offsets: []int64{4096, 52_000, 99_000}, Frames: []int64{0, 132_480, 264_960}})
	if _, err := f.db.Exec(`INSERT INTO audio_seek_index(asset_id,size,modified_ns,entries) SELECT token,size,modified_ns,? FROM catalog_assets WHERE token=?`, index, f.records[0].Token); err != nil {
		t.Fatal(err)
	}
	mp3 := map[string]any{"codec": "mp3", "containers": []string{"mp3"}}
	declareDecode(t, f, mp3, map[string]any{"codec": "flac", "containers": []string{"flac"}, "maxSampleRate": 96000})
	start := func(i int, key string) *playbackv1.AudioRender {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": key}, startBody(f.items[i], nil), 201, &s)
		return s.Presentation.AudioRender
	}
	plan := start(0, "seek-index-0000001")
	if plan.Mode != "direct" || plan.SeekIndex == nil || !strings.HasSuffix(plan.SeekIndex.URL, "/audio-index") {
		t.Fatalf("seek index %+v", plan.SeekIndex)
	}
	var got struct {
		Version int     `json:"version"`
		Offsets []int64 `json:"offsets"`
		Frames  []int64 `json:"frames"`
	}
	w := f.raw("GET", plan.SeekIndex.URL, "", nil, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Version != 1 || len(got.Offsets) != 3 || got.Offsets[1] != 52_000 || got.Frames[2] != 264_960 {
		t.Fatalf("index route: %d %s", w.Code, w.Body.String())
	}
	// No conversion is offered or served for a direct plan's part-way start.
	if w := f.raw("GET", strings.TrimSuffix(plan.URL, "/audio")+"/audio-converted?fromFrame=441000", "", nil, nil); w.Code != 404 {
		t.Fatalf("a direct plan is served converted: %d", w.Code)
	}
	// A file without an index (not measured with one, or it changed since) names none.
	if plan = start(1, "seek-index-0000002"); plan.Mode != "direct" || plan.SeekIndex != nil {
		t.Fatalf("no index: %+v", plan.SeekIndex)
	}
	if w := f.raw("GET", strings.TrimSuffix(plan.URL, "/audio")+"/audio-index", "", nil, nil); w.Code != 404 {
		t.Fatalf("a plan without an index serves one: %d", w.Code)
	}
	// The index goes with the file: once the asset changes, it is not served.
	if _, err := f.db.Exec(`UPDATE audio_seek_index SET modified_ns=modified_ns+1`); err != nil {
		t.Fatal(err)
	}
	if plan = start(0, "seek-index-0000004"); plan.SeekIndex != nil {
		t.Fatalf("a stale index is named: %+v", plan.SeekIndex)
	}
}
