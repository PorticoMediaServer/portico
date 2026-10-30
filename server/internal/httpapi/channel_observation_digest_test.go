package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// countingChannelEngine stands in for the linear runtime and counts the channel
// reads a request makes. A changed observation must read the channel once (the
// tag's own snapshot); an unchanged one must still read once and never fall back
// to Get, which reads it twice.
type countingChannelEngine struct {
	recordingChannelEngine
	mu    sync.Mutex
	reads int64
	state playback.ChannelState
	fail  error
}

func (e *countingChannelEngine) ReadTx(context.Context, *sql.Tx, identity.Principal, string) (playback.ChannelState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reads++
	if e.fail != nil {
		return playback.ChannelState{}, e.fail
	}
	return e.state, nil
}

func (e *countingChannelEngine) failWith(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail = err
}

func (e *countingChannelEngine) count() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reads
}

func (e *countingChannelEngine) set(state playback.ChannelState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
}

func (e *countingChannelEngine) get() playback.ChannelState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// A channel observation ack carries the exact view tag without reading the whole
// session: every tick costs one maintained-state read and sends no Session while
// nothing changed; every real change (observed facts, lease, programme/stream
// state) changes the tag and sends a Session built from that same read; a
// refused or failed channel read falls back to the ordinary Get.
func TestChannelObservationTagUsesOneReadAndSendsOnlyChangedViews(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := memberWithSettledCatalogue(t, f)
	channelFixture(t, f)
	// Pin the service clock so the observation batch worker never applies a
	// sample: this test is about the ack's own read, not the writer.
	now := time.UnixMilli(1_800_000_000_000)
	f.v1.Now = func() time.Time { return now }
	engine := &countingChannelEngine{}
	engine.set(playback.ChannelState{
		Name: "News",
		Media: playback.LinearMedia{
			BufferGeneration: "1", StreamURL: "/v1/media/linear/channel/1/token/index.m3u8",
			OriginMS: 1_000_000, WindowStartUS: "1000000", WindowEndUS: "60000000", LiveEdgeUS: "58000000",
			State: "active", Engine: playback.EngineNative,
			Clocks: []playback.LinearClock{{StartUS: "1000000", EndUS: "60000000", OffsetUS: "0"}},
		},
		Programme: &playback.LinearProgramme{ID: "programme-1", ItemID: "item", Title: "News at six", StartMS: 1_000, EndMS: 2_000},
	})
	f.v1.Channels = engine

	var started playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "channel-tag-start-000001"},
		map[string]any{"channelId": "library:channel", "state": "playing"}, 201, &started)
	if started.ID == "" || started.Kind != "channel" || started.Presentation.Linear == nil || started.Presentation.Generation != 1 {
		t.Fatalf("channel start %+v", started)
	}
	// The observed-output join needs the session's channel row and buffer
	// ordinal; the start engine records the row without a buffer.
	if _, err := f.db.Exec(`UPDATE playback_channel_sessions SET buffer_ordinal=1,status='active' WHERE session_id=?`, started.ID); err != nil {
		t.Fatal(err)
	}
	device := memberDeviceID(t, f, member)

	var grant playbackv1.ObserverGrant
	f.callAs(member, "POST", "/v1/playback/sessions/"+started.ID+"/observer", nil,
		map[string]any{"generation": started.Presentation.Generation}, 200, &grant)
	held := ""
	observe := func(seq int64) playbackv1.ObservationAck {
		t.Helper()
		var ack playbackv1.ObservationAck
		body := map[string]any{
			"epoch": grant.Epoch, "seq": seq, "generation": 1, "state": "playing",
			"positionMs": seq * 1000, "rate": 1, "monotonicMs": seq * 1000,
			"playClockMs": seq * 1000, "seekSerial": 0,
		}
		if held != "" {
			body["viewTag"] = held
		}
		f.callAs(member, "PUT", "/v1/playback/sessions/"+started.ID+"/observation", nil, body, 200, &ack)
		if ack.ViewTag != "" {
			held = ack.ViewTag
		}
		return ack
	}

	reads := engine.count()
	first := observe(1)
	if first.Session == nil || first.ViewTag == "" || first.Session.Presentation.Linear == nil || first.Session.Presentation.Linear.Name != "News" {
		t.Fatalf("first ack did not carry the channel view: %+v", first)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("first observation read the channel %d times, want 1", got)
	}

	// A tick that changes no source must not rebuild the view.
	reads = engine.count()
	same := observe(2)
	if same.Session != nil || same.ViewTag != first.ViewTag {
		t.Fatalf("unchanged tick sent a view: tag %q -> %q session %+v", first.ViewTag, same.ViewTag, same.Session)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("unchanged observation read the channel %d times, want 1 (Get would be 2)", got)
	}

	// Observed facts are part of the view: a new snapshot changes the tag and
	// the Session, while a new sequence of the same facts does not.
	seedObserved := func(seq int64, kbps int) {
		t.Helper()
		seedChannelObserved(t, f, started.ID, device, seq, now, &playback.ObservedPlayback{DeliveredBitrateKbps: kbps})
	}
	reads = engine.count()
	seedObserved(10, 8_000)
	observed := observe(3)
	if observed.Session == nil || observed.ViewTag == first.ViewTag || observed.Session.ObservedPlayback == nil || observed.Session.ObservedPlayback.DeliveredBitrateKbps != 8_000 {
		t.Fatalf("changed observed facts did not produce the view: %+v", observed)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("observed change read the channel %d times, want 1", got)
	}
	reads = engine.count()
	seedObserved(11, 8_000) // same facts, new sequence
	sameObserved := observe(4)
	if sameObserved.Session != nil || sameObserved.ViewTag != observed.ViewTag {
		t.Fatalf("a new sequence of the same observed facts churned the tag: %q -> %q", observed.ViewTag, sameObserved.ViewTag)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("same observed facts read the channel %d times, want 1", got)
	}

	// The lease is in the view: a renewal must reach the player.
	renewed := now.Add(60 * time.Second).UnixMilli()
	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, renewed, started.ID); err != nil {
		t.Fatal(err)
	}
	reads = engine.count()
	lease := observe(5)
	if lease.Session == nil || lease.ViewTag == observed.ViewTag || lease.Session.Lease.ExpiresAt != time.UnixMilli(renewed).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("lease renewal did not produce the view: %+v", lease)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("lease change read the channel %d times, want 1", got)
	}

	// A programme/name change from the channel runtime changes the view.
	state := engine.get()
	state.Name = "News at seven"
	engine.set(state)
	reads = engine.count()
	changed := observe(6)
	if changed.Session == nil || changed.ViewTag == lease.ViewTag || changed.Session.Presentation.Linear == nil || changed.Session.Presentation.Linear.Name != "News at seven" {
		t.Fatalf("channel state change did not produce the view: %+v", changed)
	}
	if got := engine.count() - reads; got != 1 {
		t.Fatalf("channel change read the channel %d times, want 1", got)
	}
	// The ack's Session is the same snapshot's view: a fresh GET of the same
	// unchanged state must encode identically.
	var readBack playbackv1.SessionView
	f.callAs(member, "GET", "/v1/playback/sessions/"+started.ID, nil, nil, 200, &readBack)
	sent, err := json.Marshal(*changed.Session)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(readBack)
	if err != nil {
		t.Fatal(err)
	}
	if string(sent) != string(got) {
		t.Fatalf("ack view differs from GET for the same state:\n ack %s\n get %s", sent, got)
	}

	// A channel read that refuses or fails is never turned into a partial tag:
	// the full read produces the view (defaulted here) exactly as before.
	engine.failWith(errors.New("channel state unavailable"))
	reads = engine.count()
	failed := observe(7)
	if failed.Session == nil || failed.Session.Presentation.Linear != nil {
		t.Fatalf("failed channel read did not take the full-read path: %+v", failed)
	}
	if got := engine.count() - reads; got != 3 {
		t.Fatalf("fallback read the channel %d times, want 3 (the failed exact read plus Get's two)", got)
	}
	engine.failWith(nil)

	// The producer itself refuses a wrong owner and a lapsed lease: both must
	// return an error so the route takes Get, never a stale tag.
	var account, profile, authority string
	if err := f.db.QueryRow(`SELECT account_id,profile_id,authority FROM playback_v1_sessions WHERE id=?`, started.ID).
		Scan(&account, &profile, &authority); err != nil {
		t.Fatal(err)
	}
	caller := playbackv1.Caller{Principal: identity.Principal{Viewer: identity.Viewer{AccountID: account, ProfileID: profile, Authority: authority}}, DeviceID: device}
	if _, _, err := f.v1.ChannelObservationView(context.Background(), caller, started.ID, "held"); err != nil {
		t.Fatalf("unchanged direct read: %v", err)
	}
	wrong := caller
	wrong.DeviceID = "another-device"
	if _, _, err := f.v1.ChannelObservationView(context.Background(), wrong, started.ID, "held"); err == nil {
		t.Fatal("another device received a channel tag")
	}
	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, now.UnixMilli()-1, started.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.v1.ChannelObservationView(context.Background(), caller, started.ID, "held"); err == nil {
		t.Fatal("a lapsed lease received a tag instead of the Get fallback")
	}
}

// seedChannelObserved writes one observed-output snapshot for a channel session
// as a batch would, with the sequence the v1 row must carry for the read.
func seedChannelObserved(t *testing.T, f *v1Fixture, session, device string, seq int64, now time.Time, observed *playback.ObservedPlayback) {
	t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE playback_v1_sessions SET last_seq=? WHERE id=?`, seq, session); err != nil {
		t.Fatal(err)
	}
	if err := playback.StoreObservedPlaybackTx(context.Background(), tx, session, device, 1, 1, seq, now.UnixMilli(), observed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The one-read producer must serialize exactly the view Get produces for the
// same state: a complete channel (programme, next, seek, confirmed position,
// clocks, tuner deadline and observed output) and an ended session.
func TestChannelObservationViewMatchesGetJSON(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := memberWithSettledCatalogue(t, f)
	channelFixture(t, f)
	now := time.UnixMilli(1_800_000_000_000)
	f.v1.Now = func() time.Time { return now }
	deadline := now.Add(90 * time.Second).UnixMilli()
	confirmed := int64(4_321_000)
	position := "2000000"
	seekID := "sk_1234567890123456"
	engine := &countingChannelEngine{}
	engine.set(playback.ChannelState{
		Channel:   playback.LinearReference{Kind: "library-channel", ChannelID: "channel", Generation: "generation"},
		Name:      "News",
		Programme: &playback.LinearProgramme{ID: "programme-1", ItemID: "item", Title: "News at six", StartMS: 1_000, EndMS: 2_000},
		Next:      &playback.LinearProgramme{ID: "programme-2", ItemID: "item-2", Title: "Weather", StartMS: 2_000, EndMS: 3_000},
		Desired: playback.LinearDesired{State: "playing", Seek: &playback.LinearSeek{
			ID: seekID, Generation: "1", Kind: "position", PositionUS: &position,
		}},
		Media: playback.LinearMedia{
			ReservationRequiredAtMS: &deadline,
			StreamURL:               "/v1/media/linear/channel/1/token/index.m3u8",
			BufferGeneration:        "1", PresentationGeneration: "1",
			OriginMS: 1_000_000, WindowRevision: "9", WindowStartUS: "1000000", WindowEndUS: "60000000", LiveEdgeUS: "58000000",
			Gaps:  []playback.LinearInterval{{StartUS: "10000000", EndUS: "12000000"}},
			State: "active", LogoURL: "/v1/media/linear/channel/1/token/logo", Engine: playback.EngineMPV,
			Clocks: []playback.LinearClock{{StartUS: "1000000", EndUS: "60000000", OffsetUS: "250000"}},
		},
		Status: "active", Stage: "delivery",
		ConfirmedPositionUS: &confirmed, AcknowledgedSeekID: seekID,
		LogoItemID: "logo",
	})
	f.v1.Channels = engine

	var started playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "channel-tag-parity-0001"},
		map[string]any{"channelId": "library:channel", "state": "playing"}, 201, &started)
	if started.ID == "" {
		t.Fatalf("channel start %+v", started)
	}
	if _, err := f.db.Exec(`UPDATE playback_channel_sessions SET buffer_ordinal=1,status='active' WHERE session_id=?`, started.ID); err != nil {
		t.Fatal(err)
	}
	device := memberDeviceID(t, f, member)
	seedChannelObserved(t, f, started.ID, device, 10, now, &playback.ObservedPlayback{
		Video:                &playback.ObservedVideo{Width: 1920, Height: 1080, FrameRateHz: 23.976, BitDepth: 10, DynamicRange: "hdr10"},
		Screen:               &playback.ObservedScreen{DynamicRange: "hdr10", RefreshRateHz: 59.94},
		Audio:                &playback.ObservedAudioOutput{Handling: "passthrough", Codec: "truehd", ObjectAudio: "atmos", Channels: 8, Layout: "7.1", SampleRate: 48_000, BitDepth: 24},
		Subtitles:            &playback.ObservedSubtitles{Rendering: "drawnByDevice"},
		DeliveredBitrateKbps: 6_400,
	})
	var grant playbackv1.ObserverGrant
	f.callAs(member, "POST", "/v1/playback/sessions/"+started.ID+"/observer", nil,
		map[string]any{"generation": started.Presentation.Generation}, 200, &grant)
	held := ""
	observe := func(seq int64) playbackv1.ObservationAck {
		t.Helper()
		var ack playbackv1.ObservationAck
		body := map[string]any{
			"epoch": grant.Epoch, "seq": seq, "generation": 1, "state": "playing",
			"positionMs": seq * 1000, "rate": 1, "monotonicMs": seq * 1000,
			"playClockMs": seq * 1000, "seekSerial": 0,
		}
		if held != "" {
			body["viewTag"] = held
		}
		f.callAs(member, "PUT", "/v1/playback/sessions/"+started.ID+"/observation", nil, body, 200, &ack)
		if ack.ViewTag != "" {
			held = ack.ViewTag
		}
		return ack
	}
	compare := func(acked *playbackv1.SessionView, label string) {
		t.Helper()
		if acked == nil {
			t.Fatalf("%s: producer sent no view", label)
		}
		var readBack playbackv1.SessionView
		f.callAs(member, "GET", "/v1/playback/sessions/"+started.ID, nil, nil, 200, &readBack)
		sent, err := json.Marshal(*acked)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(readBack)
		if err != nil {
			t.Fatal(err)
		}
		if string(sent) != string(got) {
			t.Fatalf("%s: producer view differs from Get:\n producer %s\n get      %s", label, sent, got)
		}
	}
	compare(observe(1).Session, "complete channel with deadline and observed output")

	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET ended_ms=?,end_reason='terminated',message='Test ended',revision=revision+1 WHERE id=?`, now.UnixMilli(), started.ID); err != nil {
		t.Fatal(err)
	}
	ended := observe(2)
	if ended.Session == nil || ended.Session.State != "ended" || ended.Session.End == nil || ended.Session.End.Reason != "terminated" || ended.Session.End.Message != "Test ended" {
		t.Fatalf("ended session view %+v", ended.Session)
	}
	compare(ended.Session, "ended channel")
}
