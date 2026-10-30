package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Only the producer's buffer changes. A cold runtime owns a real session with
// no buffer generation; the wrapper must not mint a replacement or a generation.
type pendingStartChannelEngine struct {
	memberChannelEngine
	starts     int
	generation string
}

func (e *pendingStartChannelEngine) StartTx(context.Context, *sql.Tx, identity.Principal, playback.ChannelStart) (playback.LinearSelection, error) {
	e.starts++
	return playback.LinearSelection{}, nil
}
func (e *pendingStartChannelEngine) ReadTx(_ context.Context, _ *sql.Tx, _ identity.Principal, id string) (playback.ChannelState, error) {
	out := playback.ChannelState{Name: "News", Media: playback.LinearMedia{State: "preparing"}}
	if e.generation != "" {
		out.Media.State = "active"
		out.Media.BufferGeneration = e.generation
		out.Media.StreamURL = "/v2/media/linear/" + id + "/" + e.generation + "/fixture/index.m3u8"
	}
	return out, nil
}

func TestColdChannelStartKeepsRealSessionAndProducerGeneration(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := memberWithSettledCatalogue(t, f)
	channelFixture(t, f)
	engine := &pendingStartChannelEngine{}
	f.v1.Channels = engine
	setMemberLimits(t, f, `{"maxStreams":1}`)
	body := map[string]any{"channelId": "library:channel", "state": "playing"}
	headers := map[string]string{"Idempotency-Key": "cold-channel-start-000001"}
	read := func() playbackv1.SessionView {
		t.Helper()
		w := f.raw("POST", "/v1/playback/sessions", member, headers, body)
		if w.Code != 201 {
			t.Fatalf("real channel start: %d %s", w.Code, w.Body.String())
		}
		if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(201), w.Body.Bytes()); len(issues) != 0 {
			t.Fatal(issues)
		}
		var v playbackv1.SessionView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	cold := read()
	if cold.ID == "" || cold.Revision == "" || cold.Lease.ExpiresAt == "" || cold.Kind != "channel" || cold.Presentation.Generation != 0 || cold.Presentation.URL != "" || cold.Presentation.Linear == nil || cold.Presentation.Linear.State != "preparing" || cold.RetryAfterMs != 0 {
		t.Fatal(cold)
	}
	replay := read()
	if replay.ID != cold.ID || replay.Revision != cold.Revision || replay.Presentation.Generation != 0 || engine.starts != 1 {
		t.Fatal("cold replay replaced the resource", cold, replay, engine.starts)
	}
	for _, kind := range []string{"channel", "live"} {
		v := cold
		v.Kind = kind
		out, err := startSessionResponse(v, false)
		if err != nil {
			t.Fatal(kind, err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(201), raw); len(issues) != 0 {
			t.Fatal(kind, issues)
		}
	}
	// The channel's normal 201/GET refresh is separate from Root67 start retries.
	wait := cold
	wait.RetryAfterMs = 500
	if _, err := startSessionResponse(wait, true); err == nil {
		t.Fatal("cold channel became a server-directed start retry")
	}
	waitJSON, err := json.Marshal(wait)
	if err != nil {
		t.Fatal(err)
	}
	if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(202), waitJSON); len(issues) == 0 {
		t.Fatal("cold channel changed the202 variants")
	}
	engine.generation = "7"
	ready := read()
	if ready.ID != cold.ID || ready.Revision != cold.Revision || ready.Presentation.Generation != 7 || ready.Presentation.URL == "" || ready.Presentation.Linear.State != "active" || engine.starts != 1 {
		t.Fatal("real buffer was not projected on the original resource", ready, engine.starts)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM playback_v1_sessions WHERE id=?`, cold.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if code, _ := startChannel(t, f, member, "cold-second-original-0001"); code != 409 {
		t.Fatal("cold session lost its stream slot", code)
	}
	assertMalformedColdStarts(t, cold)
	// Replaying the genuinely ended channel follows channelPresentation's
	// early return: engine/linear remain absent facts, never fabricated native.
	if stopped := f.raw("DELETE", "/v1/playback/sessions/"+cold.ID, member, nil, nil); stopped.Code != 204 {
		t.Fatalf("channel stop: %d %s", stopped.Code, stopped.Body.String())
	}
	ended := read()
	if ended.State != "ended" || ended.End == nil || ended.Presentation.Generation != 0 || ended.Presentation.Engine != "" || ended.Presentation.URL != "" {
		t.Fatal("ended channel replay changed facts", ended)
	}
	invalid := ended
	invalid.Kind = "vod"
	invalidJSON, _ := json.Marshal(invalid)
	if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(201), invalidJSON); len(issues) == 0 {
		t.Fatal("empty channel engine authorized a native resource")
	}
	coldJSON, _ := json.Marshal(cold)
	var value map[string]any
	json.Unmarshal(coldJSON, &value)
	value["presentation"].(map[string]any)["linear"].(map[string]any)["unexpected"] = true
	unknownJSON, _ := json.Marshal(value)
	if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(201), unknownJSON); len(issues) == 0 {
		t.Fatal("unknown linear field escaped closure")
	}

}

func assertMalformedColdStarts(t *testing.T, original playbackv1.SessionView) {
	t.Helper()
	for _, test := range []struct {
		name   string
		change func(*playbackv1.SessionView)
	}{
		{"vod", func(v *playbackv1.SessionView) { v.Kind = "vod" }},
		{"audio", func(v *playbackv1.SessionView) { v.Kind = "audio" }},
		{"unknown kind", func(v *playbackv1.SessionView) { v.Kind = "unknown" }},
		{"missing linear", func(v *playbackv1.SessionView) { v.Presentation.Linear = nil }},
		{"active without buffer", func(v *playbackv1.SessionView) { v.Presentation.Linear.State = "active" }},
		{"URL without generation", func(v *playbackv1.SessionView) { v.Presentation.URL = "/stream.m3u8" }},
		{"negative generation", func(v *playbackv1.SessionView) { v.Presentation.Generation = -1 }},
		{"direct mode", func(v *playbackv1.SessionView) { v.Presentation.Mode = "direct" }},
		{"missing id", func(v *playbackv1.SessionView) { v.ID = "" }},
		{"missing revision", func(v *playbackv1.SessionView) { v.Revision = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := original
			linear := *original.Presentation.Linear
			v.Presentation.Linear = &linear
			test.change(&v)
			if _, err := startSessionResponse(v, false); err == nil {
				t.Fatal("malformed pending presentation accepted")
			}
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if issues := (apispec.Document{}).ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(201), raw); len(issues) == 0 {
				t.Fatal("schema accepted malformed pending presentation")
			}
		})
	}
}
