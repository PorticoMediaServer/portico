package httpapi

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func rateCaller(device string) v1Caller {
	return v1Caller{Principal: identity.Principal{Viewer: identity.Viewer{ServerID: "server", Authority: "local", AccountID: "account", ProfileID: "profile"}}, DeviceID: device}
}

func TestObservationRateBurstRefillAndDeviceIsolation(t *testing.T) {
	l := newObservationLimiter()
	now := time.Now()
	caller := rateCaller("one")
	for i := 0; i < observationBurst; i++ {
		if !l.allow(caller, now) {
			t.Fatal("manual burst was refused before its limit")
		}
	}
	for i := 0; i < 100; i++ {
		if l.allow(caller, now) {
			t.Fatal("device exceeded its burst")
		}
	}
	if !l.allow(rateCaller("two"), now) {
		t.Fatal("one device's flood refused another device")
	}
	caller.Hash = "rotated-token"
	caller.ProfileID = "another-profile"
	if l.allow(caller, now) {
		t.Fatal("token or profile rotation minted a new bucket")
	}
	caller.AccountID = "different-account"
	if !l.allow(caller, now) {
		t.Fatal("device ID collision crossed account boundaries")
	}
	caller = rateCaller("one")
	now = now.Add(time.Second)
	for i := 0; i < observationsPerSecond; i++ {
		if !l.allow(caller, now) {
			t.Fatal("tokens did not refill at the declared cadence")
		}
	}
	if l.allow(caller, now) {
		t.Fatal("refill exceeded the declared cadence")
	}
	if !l.allow(caller, now.Add(observationRateTTL)) {
		t.Fatal("idle bucket did not expire and recover")
	}
}

func TestObservationRateLedgerIsBounded(t *testing.T) {
	l := newObservationLimiter()
	now := time.Now()
	for i := 0; i < observationRateEntries+100; i++ {
		if !l.allow(rateCaller(fmt.Sprintf("device-%d", i)), now) {
			t.Fatal("full accounting table refused a new legitimate device")
		}
	}
	if len(l.entries) != observationRateEntries || l.order.Len() != observationRateEntries {
		t.Fatal("rate ledger exceeded its memory bound")
	}
}

func TestObservationHTTPRateRefusalDoesNotWriteAndOtherDeviceContinues(t *testing.T) {
	f := newV1Fixture(t, 2)
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "observation-rate-session-0001"}, startBody(f.items[0], nil), 201, &session)
	h := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
		return f.raw(method, path, f.owner.AccessToken, nil, body)
	})
	path := "/v1/playback/sessions/" + session.ID + "/observation"
	h.claim(session.ID, session.Presentation.Generation)
	denied := 0
	lastAccepted := int64(0)
	for seq := int64(1); seq <= 100; seq++ {
		accepted := f.v1.ObservationStats().Accepted
		w := h.observe(session.ID, session.Presentation.Generation, map[string]any{"state": "playing", "positionMs": seq * 100})
		switch w.Code {
		case 200:
			lastAccepted = seq
		case 429:
			denied++
			if v1Code(w) != "rate_limited" || w.Header().Get("Retry-After") != "1" {
				t.Fatalf("rate refusal contract: %d %s %s", w.Code, w.Header(), w.Body.String())
			}
		default:
			t.Fatalf("observation returned %d %s", w.Code, w.Body.String())
		}
		if w.Code == 429 {
			if f.v1.ObservationStats().Accepted != accepted {
				t.Fatal("refused observation reached the collector")
			}
			var recorded int64
			if err := f.db.QueryRow(`SELECT last_seq FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded > lastAccepted {
				t.Fatal("rejected observation wrote evidence")
			}
		}
	}
	if denied == 0 {
		t.Fatal("rapid fresh reports were never throttled")
	}
	other := f.device("independent-observation-device")
	var otherSession playbackv1.SessionView
	f.callAs(other.AccessToken, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "observation-rate-session-0002"}, startBody(f.items[1], nil), 201, &otherSession)
	otherObserver := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
		return f.raw(method, path, other.AccessToken, nil, body)
	})
	otherObserver.report(otherSession.ID, otherSession.Presentation.Generation, map[string]any{"state": "playing", "positionMs": 100})
	// Stop drains the latest accepted sample. Refused samples must never replace it.
	f.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204, nil)
	var recorded, position int64
	if err := f.db.QueryRow(`SELECT last_seq,position_ms FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&recorded, &position); err != nil || recorded == 0 || position != lastAccepted*100 {
		t.Fatalf("drained seq=%d position=%d want latest accepted sample=%d (%v)", recorded, position, lastAccepted, err)
	}
	if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, identity.Digest(f.owner.AccessToken)); err != nil {
		t.Fatal(err)
	}
	if w := f.raw("PUT", path, f.owner.AccessToken, nil, map[string]any{"seq": 101, "generation": session.Presentation.Generation, "state": "playing", "positionMs": 10100, "rate": 1}); w.Code != 401 {
		t.Fatalf("rate bucket bypassed or preceded authentication: %d", w.Code)
	}
}
