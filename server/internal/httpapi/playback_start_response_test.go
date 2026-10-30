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
	response := playbackStartResponse{RetryAfterMs: 500}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"retryAfterMs":500}` {
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
	if err != nil || string(before) != string(after) {
		t.Fatalf("real session JSON changed: %v\n%s\n%s", err, before, after)
	}
	validator := apispec.Document{}
	for _, status := range []int{201, 202} {
		v := session
		if status == 202 {
			v.RetryAfterMs = 500
		}
		raw, _ := json.Marshal(v)
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
		check(201, pending, false)
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
	copyValue := func() map[string]any {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	nested := func(v map[string]any, key string) map[string]any { return v[key].(map[string]any) }
	for name, mutate := range map[string]func(map[string]any){
		"root closure":         func(v map[string]any) { v["unexpected"] = true },
		"lease closure":        func(v map[string]any) { nested(v, "lease")["unexpected"] = true },
		"presentation closure": func(v map[string]any) { nested(v, "presentation")["unexpected"] = true },
		"decision closure":     func(v map[string]any) { nested(nested(v, "presentation"), "decision")["unexpected"] = true },
		"nested stream closure": func(v map[string]any) {
			nested(nested(nested(v, "presentation"), "decision"), "video")["unexpected"] = true
		},
		"required":            func(v map[string]any) { delete(v, "lease") },
		"null required":       func(v map[string]any) { v["presentation"] = nil },
		"wrong type":          func(v map[string]any) { v["id"] = 2 },
		"kind":                func(v map[string]any) { v["kind"] = "other" },
		"role":                func(v map[string]any) { v["role"] = "other" },
		"projected state":     func(v map[string]any) { v["state"] = "preparing" },
		"engine":              func(v map[string]any) { nested(v, "presentation")["engine"] = "other" },
		"empty native engine": func(v map[string]any) { nested(v, "presentation")["engine"] = "" },
		"mode":                func(v map[string]any) { nested(v, "presentation")["mode"] = "other" },
		"negative position":   func(v map[string]any) { nested(v, "presentation")["startPositionMs"] = -1 },
		"zero generation":     func(v map[string]any) { nested(v, "presentation")["generation"] = 0 },
		"lease date":          func(v map[string]any) { nested(v, "lease")["expiresAt"] = "not-a-time" },
		"lease offset":        func(v map[string]any) { nested(v, "lease")["expiresAt"] = "2026-10-27T01:02:03+00:00" },
		"lease precision":     func(v map[string]any) { nested(v, "lease")["expiresAt"] = "2026-10-27T01:02:03.1234567890Z" },
		"negative interval":   func(v map[string]any) { nested(v, "lease")["reportEveryMs"] = -1 },
		"zero interval":       func(v map[string]any) { nested(v, "lease")["reportEveryMs"] = 0 },
		"end contamination":   func(v map[string]any) { v["end"] = map[string]any{"reason": "stopped"} },
		"retry missing":       func(v map[string]any) { delete(v, "retryAfterMs") },
		"retry null":          func(v map[string]any) { v["retryAfterMs"] = nil },
		"retry bounds":        func(v map[string]any) { v["retryAfterMs"] = 10001 },
		"empty decision":      func(v map[string]any) { nested(v, "presentation")["decision"] = map[string]any{} },
	} {
		t.Run(name, func(t *testing.T) { value := copyValue(); mutate(value); check(202, value, false) })
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
