package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// §17.2: while the presentation is prepared the start answers 202 with
// retryAfterMs and Retry-After; the identical replay gets 201 once it is ready.
func TestPlaybackV1StartPreparation(t *testing.T) {
	f := newV1Fixture(t, 1)
	rounds := 2
	f.v1.ReadyCheck = func(context.Context, playback.Session) (bool, error) {
		rounds--
		return rounds < 0, nil
	}
	key := map[string]string{"Idempotency-Key": "prepare-key-00000001"}
	var first playbackv1.SessionView
	w := f.call("POST", "/v1/playback/sessions", key, startBody(f.items[0], map[string]any{"state": "paused"}), 202, &first)
	if first.RetryAfterMs <= 0 || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("202 %+v %v", first, w.Header())
	}
	f.call("POST", "/v1/playback/sessions", key, startBody(f.items[0], map[string]any{"state": "paused"}), 202, nil)
	var ready playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", key, startBody(f.items[0], map[string]any{"state": "paused"}), 201, &ready)
	if ready.ID != first.ID || ready.State != "paused" || ready.RetryAfterMs != 0 || ready.Revision != "2" {
		t.Fatalf("ready %+v", ready)
	}
	// Reports while preparing change nothing (there is nothing playing yet).
	f.call("POST", "/v1/playback/sessions", key, startBody(f.items[0], map[string]any{"state": "paused"}), 201, nil)
}

// SEC-02 / invariant 5: a title the profile may not see is 404 on every path:
// options, start, change, observer claim and observation renewal. The session's grant stops working.
func TestPlaybackV1RestrictedTitleIsNotFoundEverywhere(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 1)
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "restrict-key-0000001"}, startBody(f.items[0], nil), 201, &s)
	h := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
		return f.raw(method, path, f.owner.AccessToken, nil, body)
	})
	epoch := h.claim(s.ID, s.Presentation.Generation)
	// The profile now refuses unrated titles (every fixture film is unrated).
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES(?,12,0,2) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=0,revision=revision+1`, f.owner.Viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	f.handler = New(Dependencies{DB: f.db, Identity: f.id, Catalog: f.cat, Playback: playback.New(f.db), PlaybackV1: f.v1})
	for _, c := range []struct {
		method, path string
		headers      map[string]string
		body         any
	}{
		{"GET", "/v1/items/" + f.items[0] + "/playback-options", nil, nil},
		{"POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "restrict-key-0000002"}, startBody(f.items[0], nil)},
		{"PATCH", "/v1/playback/sessions/" + s.ID, map[string]string{"If-Match": s.Revision}, map[string]any{"quality": map[string]any{"mode": "limit", "maxHeight": 2000}}},
		{"POST", "/v1/playback/sessions/" + s.ID + "/observer", nil, map[string]any{"generation": 1}},
	} {
		if w := f.raw(c.method, c.path, f.owner.AccessToken, c.headers, c.body); w.Code != 404 {
			t.Errorf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	// An observation reads nothing on its hit path, so the device that already
	// owns the session has its next sample taken; the write refuses it, records
	// nothing and retires the owner, and from then on the title is not found.
	sample := func(seq int) *httptest.ResponseRecorder {
		return f.raw("PUT", "/v1/playback/sessions/"+s.ID+"/observation", f.owner.AccessToken, nil, map[string]any{"epoch": epoch, "seq": seq, "generation": 1, "state": "playing", "positionMs": 1000, "rate": 1, "monotonicMs": seq * 1000})
	}
	if w := sample(1); w.Code != 200 {
		t.Fatalf("the owner's first sample after the restriction: %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for seq := 2; ; seq++ {
		w := sample(seq)
		if w.Code == 404 {
			break
		}
		if w.Code != 200 || time.Now().After(deadline) {
			t.Fatalf("observation after the restriction: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(250 * time.Millisecond) // a device's own pace, inside the rate limit
	}
	var position, lastSeq int64
	if err := f.db.QueryRow(`SELECT position_ms,last_seq FROM playback_v1_sessions WHERE id=?`, s.ID).Scan(&position, &lastSeq); err != nil || position != 0 || lastSeq != 0 {
		t.Errorf("the refused sample was written: position %d seq %d (%v)", position, lastSeq, err)
	}
	if w := f.raw("GET", s.Presentation.URL, "", nil, nil); w.Code < 400 {
		t.Errorf("restricted title's grant still plays: %d", w.Code)
	}
}
