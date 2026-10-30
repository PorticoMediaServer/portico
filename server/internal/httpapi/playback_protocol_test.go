package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Every client declares the observation protocol (Portico-Playback: 2). This
// package's tests send requests directly, so a request that names no version
// is taken as a current client here; TestPlaybackObservationsOverHTTP sends an
// older version to test the refusal.
func init() { assumedPlaybackProtocol = playbackv1.PlaybackProtocol }

// httpObserver reports for sessions the way a playing device does: it claims
// each session once, then sends observations. report also waits until the
// server's batch worker has written the sample (the first sample, and every
// pause, seek, end, error or skip, is written within about a second), so a
// test can assert its durable effect.
type httpObserver struct {
	t  testing.TB
	db *sql.DB
	// do sends one request as the playing device.
	do     func(method, path string, body any) *httptest.ResponseRecorder
	epochs map[string]string
	seq    map[string]int64
}

func newHTTPObserver(t testing.TB, db *sql.DB, do func(method, path string, body any) *httptest.ResponseRecorder) *httpObserver {
	return &httpObserver{t: t, db: db, do: do, epochs: map[string]string{}, seq: map[string]int64{}}
}

// claim makes this device the session's observer (once per session).
func (h *httpObserver) claim(id string, generation int) string {
	h.t.Helper()
	if epoch := h.epochs[id]; epoch != "" {
		return epoch
	}
	w := h.do("POST", "/v1/playback/sessions/"+id+"/observer", map[string]any{"generation": generation})
	if w.Code != 200 {
		h.t.Fatalf("claim %s: %d %s", id, w.Code, w.Body.String())
	}
	var grant playbackv1.ObserverGrant
	if err := json.Unmarshal(w.Body.Bytes(), &grant); err != nil {
		h.t.Fatal(err)
	}
	h.epochs[id] = grant.Epoch
	return grant.Epoch
}

// observe sends one sample. The sample names at least state and positionMs;
// epoch, seq and generation are filled in, and the clocks default to a device
// that has played since the claim.
func (h *httpObserver) observe(id string, generation int, sample map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	body := map[string]any{"rate": 1}
	for k, v := range sample {
		body[k] = v
	}
	h.seq[id]++
	seq := h.seq[id]
	body["epoch"], body["seq"], body["generation"] = h.claim(id, generation), seq, generation
	if _, ok := body["monotonicMs"]; !ok {
		body["monotonicMs"] = seq * 1000
	}
	if _, ok := body["playClockMs"]; !ok {
		body["playClockMs"] = 0
	}
	return h.do("PUT", "/v1/playback/sessions/"+id+"/observation", body)
}

// report sends one sample and waits until it is written (the session's
// written sequence moves, or it ends).
func (h *httpObserver) report(id string, generation int, sample map[string]any) {
	h.t.Helper()
	before := h.written(id)
	w := h.observe(id, generation, sample)
	if w.Code != 200 {
		h.t.Fatalf("observation %s: %d %s", id, w.Code, w.Body.String())
	}
	h.waitWritten(id, before)
}

func (h *httpObserver) waitWritten(id, before string) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.written(id) == before {
		if time.Now().After(deadline) {
			h.t.Fatalf("observation for %s was not written", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *httpObserver) written(id string) string {
	h.t.Helper()
	var seq, ended int64
	if err := h.db.QueryRow(`SELECT last_seq,ended_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&seq, &ended); err != nil {
		h.t.Fatal(err)
	}
	return fmt.Sprint(seq, "/", ended)
}
