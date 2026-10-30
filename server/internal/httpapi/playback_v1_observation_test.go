package httpapi

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// The observation protocol over HTTP (P2-20/21): a client that declares an older one cannot start
// playback; a current one claims an epoch, reports, is told to claim again under a wrong epoch,
// and its Stop writes the latest reported position.
func TestPlaybackObservationsOverHTTP(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "observe-start-key-0001", "Portico-Playback": "1"}, startBody(f.items[0], nil))
	if w.Code != 400 || v1Code(w) != "client_update_required" {
		t.Fatalf("start with an older protocol: %d %s", w.Code, w.Body.String())
	}
	var sessions int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_v1_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("%d sessions after the refused start (%v)", sessions, err)
	}
	protocol := map[string]string{"Portico-Playback": "2"}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "observe-start-key-0002", "Portico-Playback": "2"}, startBody(f.items[0], map[string]any{"startFrom": "beginning"}), 201, &s)
	if s.Control == nil || s.Control.Revision != "1" || s.Control.State != "playing" {
		t.Fatalf("control %+v", s.Control)
	}
	var grant playbackv1.ObserverGrant
	f.call("POST", "/v1/playback/sessions/"+s.ID+"/observer", protocol, map[string]any{"generation": s.Presentation.Generation}, 200, &grant)
	if len(grant.Epoch) != 22 || grant.EveryMs != 1000 {
		t.Fatalf("grant %+v", grant)
	}
	sample := func(epoch string, seq int, position int) map[string]any {
		return map[string]any{"epoch": epoch, "seq": seq, "generation": s.Presentation.Generation, "state": "playing", "positionMs": position, "rate": 1, "monotonicMs": seq * 1000, "playClockMs": seq * 1000, "seekSerial": 0}
	}
	var ack playbackv1.ObservationAck
	w = f.call("PUT", "/v1/playback/sessions/"+s.ID+"/observation", protocol, sample(grant.Epoch, 1, 4000), 200, &ack)
	if ack.AcceptedSeq != 1 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("ack %+v, headers %v", ack, w.Header())
	}
	w = f.raw("PUT", "/v1/playback/sessions/"+s.ID+"/observation", f.owner.AccessToken, protocol, sample(strings.Repeat("x", 22), 2, 5000))
	if w.Code != 409 || v1Code(w) != "observation_epoch_changed" {
		t.Fatalf("wrong epoch: %d %s", w.Code, w.Body.String())
	}
	// No batch has run yet: the Stop itself writes the reported position.
	f.call("DELETE", "/v1/playback/sessions/"+s.ID, protocol, nil, 204, nil)
	var position int64
	if err := f.db.QueryRow(`SELECT position_ms FROM playback_v1_sessions WHERE id=?`, s.ID).Scan(&position); err != nil || position != 4000 {
		t.Fatalf("stopped at %d (%v), want the reported 4000", position, err)
	}
}
