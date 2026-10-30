package httpapi

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestPlaybackStartMinimalWaitHasNoSessionAndIs202Only(t *testing.T) {
	response, err := startPreparedResponse(playbackv1.PreparedStart{Preparing: true, RetryAfterMs: 500})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"variant":"wait","retryAfterMs":500}` {
		t.Fatalf("private preparation fabricated session fields: %s", raw)
	}
	validator := apispec.Document{}
	if issues := validator.ValidateJSON(response.JSONSchemaForStatus(202), raw); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := validator.ValidateJSON(response.JSONSchemaForStatus(201), raw); len(issues) == 0 {
		t.Fatal("minimal wait accepted as a completed session")
	}
	for _, raw := range []string{
		`{}`, `{"retryAfterMs":0}`, `{"retryAfterMs":-1}`, `{"retryAfterMs":10001}`,
		`{"retryAfterMs":1.5}`, `{"retryAfterMs":"500"}`, `{"retryAfterMs":null}`,
		`{"retryAfterMs":500,"id":""}`, `{"retryAfterMs":500,"presentation":{}}`,
	} {
		if issues := validator.ValidateJSON(response.JSONSchemaForStatus(202), []byte(raw)); len(issues) == 0 {
			t.Fatalf("invalid pre-session response accepted: %s", raw)
		}
	}
}

func TestPlaybackStartProjectionRequiresGenuinePreparationOrRealSession(t *testing.T) {
	for _, wait := range []int{1, 500, 10000} {
		out, err := startPreparedResponse(playbackv1.PreparedStart{Preparing: true, RetryAfterMs: wait})
		if err != nil || out.SessionView != nil || out.RetryAfterMs != wait {
			t.Fatalf("pre-session wait gained a resource: %+v, %v", out, err)
		}
	}
	for _, invalid := range []playbackv1.PreparedStart{
		{}, {RetryAfterMs: 500}, {Preparing: true},
		{Preparing: true, RetryAfterMs: -1}, {Preparing: true, RetryAfterMs: 10001},
		{Session: &playbackv1.SessionView{}, Preparing: true, RetryAfterMs: 500},
	} {
		if _, err := startPreparedResponse(invalid); err == nil {
			t.Fatalf("invalid producer result became a start response: %+v", invalid)
		}
	}
}

func TestPlaybackStartWrapperKeepsActualSessionJSON(t *testing.T) {
	f := newV1Fixture(t, 1)
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "start-shape-00000001"}, startBody(f.items[0], nil), 201, &session)
	out, err := startSessionResponse(session, false)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := startPreparedResponse(playbackv1.PreparedStart{Session: &session})
	if err != nil || projected.SessionView == nil || projected.ID != session.ID || projected.RetryAfterMs != 0 {
		t.Fatalf("ordinary route result changed: %+v, %v", projected, err)
	}
	preparing := session
	preparing.RetryAfterMs = 500
	if _, err = startPreparedResponse(playbackv1.PreparedStart{Session: &preparing, Preparing: true, RetryAfterMs: 501}); err == nil {
		t.Fatal("a wait unrelated to the real session was accepted")
	}
	before, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(out)
	if err != nil || string(tagged(before)) != string(after) {
		t.Fatalf("real session JSON changed: %v\n%s\n%s", err, before, after)
	}
	validator := apispec.Document{}
	for _, status := range []int{201, 202} {
		v := session
		if status == 202 {
			v.RetryAfterMs = 500
		}
		raw, _ := json.Marshal(v)
		raw = tagged(raw)
		projected, err := startPreparedResponse(playbackv1.PreparedStart{Session: &v, Preparing: status == 202, RetryAfterMs: v.RetryAfterMs})
		if err != nil {
			t.Fatalf("real session projection status %d: %v", status, err)
		}
		projectedJSON, err := json.Marshal(projected)
		if err != nil || string(projectedJSON) != string(raw) {
			t.Fatalf("real session projection status %d changed: %v\n%s\n%s", status, err, raw, projectedJSON)
		}
		if issues := validator.ValidateJSON(out.JSONSchemaForStatus(status), raw); len(issues) != 0 {
			t.Fatalf("real session status %d: %v", status, issues)
		}
	}
	if _, err := startSessionResponse(playbackv1.SessionView{}, true); err == nil {
		t.Fatal("empty SessionView became a preparation response")
	}
	registry, err := FoundationRegistry(f.deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range registry.Definitions() {
		if definition.ID == "start_playback_session" {
			if definition.Response != reflect.TypeFor[playbackStartResponse]() || !reflect.DeepEqual(definition.SuccessStatuses, []int{202}) {
				t.Fatal("start route lost its authoritative response declaration")
			}
			if _, err := apikit.Schema(definition.Response); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing start route")
}

func TestPlaybackStartCanonicalSchemaValidatesCompleteActualResource(t *testing.T) {
	f := newV1Fixture(t, 1)
	var real playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "start-domain-positive-0001"}, startBody(f.items[0], nil), 201, &real)
	pendingFixture := newV1Fixture(t, 1)
	pendingFixture.v1.ReadyCheck = func(context.Context, playback.Session) (bool, error) { return false, nil }
	pendingFixture.v1.ReadyWait = time.Millisecond
	var actualPending playbackv1.SessionView
	pendingFixture.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "start-domain-real-202-0001"}, startBody(pendingFixture.items[0], nil), 202, &actualPending)
	if actualPending.ID == "" || actualPending.RetryAfterMs < 1 || actualPending.State != "playing" {
		t.Fatal("fixture did not produce a real projected 202", actualPending)
	}
	validator := apispec.Document{}
	check := func(status int, value any, want bool) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, isMap := value.(map[string]any); !isMap {
			raw = tagged(raw)
		}
		issues := validator.ValidateJSON((playbackStartResponse{}).JSONSchemaForStatus(status), raw)
		if (len(issues) == 0) != want {
			t.Fatalf("status %d valid=%v: %v\n%s", status, want, issues, raw)
		}
	}
	check(201, real, true)
	check(202, actualPending, true)
	for _, state := range []string{"playing", "paused"} {
		pending := real
		pending.State = state
		pending.RetryAfterMs = 500
		check(202, pending, true)
		// A completed start never carries a wait; the server refuses to send one.
		if _, err := startSessionResponse(pending, false); err == nil {
			t.Fatal("a 201 session carried a preparation wait")
		}
	}
	ended := real
	ended.State = "ended"
	ended.End = &playbackv1.SessionEnd{Reason: "stopped"}
	check(201, ended, true)
	ended.RetryAfterMs = 500
	check(202, ended, false)

	pending := real
	pending.RetryAfterMs = 500
	raw, _ := json.Marshal(pending)
	raw = tagged(raw)
	copyValue := func() map[string]any {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	nested := func(v map[string]any, key string) map[string]any { return v[key].(map[string]any) }
	for name, mutate := range map[string]func(map[string]any){
		"untagged":          func(v map[string]any) { delete(v, "variant") },
		"unknown tag":       func(v map[string]any) { v["variant"] = "other" },
		"required":          func(v map[string]any) { delete(v, "lease") },
		"null required":     func(v map[string]any) { v["presentation"] = nil },
		"wrong type":        func(v map[string]any) { v["id"] = 2 },
		"kind":              func(v map[string]any) { v["kind"] = "other" },
		"role":              func(v map[string]any) { v["role"] = "other" },
		"projected state":   func(v map[string]any) { v["state"] = "preparing" },
		"engine":            func(v map[string]any) { nested(v, "presentation")["engine"] = "other" },
		"mode":              func(v map[string]any) { nested(v, "presentation")["mode"] = "other" },
		"negative position": func(v map[string]any) { nested(v, "presentation")["startPositionMs"] = -1 },
		"lease date":        func(v map[string]any) { nested(v, "lease")["expiresAt"] = "not-a-time" },
		"lease offset":      func(v map[string]any) { nested(v, "lease")["expiresAt"] = "2026-10-27T01:02:03+00:00" },
		"lease precision":   func(v map[string]any) { nested(v, "lease")["expiresAt"] = "2026-10-27T01:02:03.1234567890Z" },
		"negative interval": func(v map[string]any) { nested(v, "lease")["reportEveryMs"] = -1 },
		"zero interval":     func(v map[string]any) { nested(v, "lease")["reportEveryMs"] = 0 },
		"retry missing":     func(v map[string]any) { delete(v, "retryAfterMs") },
		"retry null":        func(v map[string]any) { v["retryAfterMs"] = nil },
		"retry bounds":      func(v map[string]any) { v["retryAfterMs"] = 10001 },
	} {
		t.Run(name, func(t *testing.T) { value := copyValue(); mutate(value); check(202, value, false) })
	}
	// R-09b: responses are read open. A newer server's added members, at any
	// depth, are ignored rather than refused; the session invariants (a native
	// engine, a generation, no end while preparing) are the server's own and
	// startSessionResponse refuses to send anything else.
	for name, mutate := range map[string]func(map[string]any){
		"root addition":         func(v map[string]any) { v["unexpected"] = true },
		"lease addition":        func(v map[string]any) { nested(v, "lease")["unexpected"] = true },
		"presentation addition": func(v map[string]any) { nested(v, "presentation")["unexpected"] = true },
		"decision addition":     func(v map[string]any) { nested(nested(v, "presentation"), "decision")["unexpected"] = true },
	} {
		t.Run(name, func(t *testing.T) { value := copyValue(); mutate(value); check(202, value, true) })
	}
	for _, date := range []string{"2026-10-27T01:02:03Z", "2026-10-27T01:02:03.1Z", "2026-10-27T01:02:03.123456789Z"} {
		value := copyValue()
		nested(value, "lease")["expiresAt"] = date
		check(202, value, true)
	}
	// The producer omits its nil optional pointer (SessionView.ObservedPlayback
	// is `json:"observedPlayback,omitempty"`), so the actual wire body carries no
	// such member. The map fixture models that omission. An explicit null is not
	// the optional member's value: the schema does not declare it nullable, so
	// it is refused rather than read as absence.
	value := copyValue()
	if _, sent := value["observedPlayback"]; sent {
		t.Fatalf("the actual producer sent the absent optional member: %s", raw)
	}
	check(202, value, true)
	nulled := copyValue()
	nulled["observedPlayback"] = nil
	check(202, nulled, false)
}

// tagged is a session body as the start route sends it: the session tag first.
func tagged(raw []byte) []byte {
	return append([]byte(`{"variant":"session",`), raw[1:]...)
}
