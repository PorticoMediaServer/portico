package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// These are the receiver engine's real requests, made with the access token
// obtained by pairing, rather than a fixture pretending to be the server.
// The target may describe and complete its current occurrence; it may not
// browse or control a different same-profile device's queue.
func TestCastReceiverCurrentOccurrenceRoutes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name          string
		reportedEnded bool
	}{{"before_final_observation", false}, {"after_ended_observation", true}} {
		t.Run(tc.name, func(t *testing.T) {
			castReceiverCurrentOccurrenceRoutes(t, tc.reportedEnded)
		})
	}
}

func castReceiverCurrentOccurrenceRoutes(t *testing.T, reportedEnded bool) {
	f := socialHTTP(t)
	screen := pairCastTarget(t, f, f.token, "receiver-occurrence")
	bearer := screen.Session.AccessToken
	f.callAs(bearer, "PUT", "/v1/me/devices/current/capabilities", nil, tl6WebCapabilities(), 204, nil)
	create := func(token, key string) playbackv1.QueueReply {
		t.Helper()
		var out playbackv1.QueueReply
		f.callAs(token, "POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, map[string]any{
			"segments": []any{map[string]any{"source": queueItems(f.item, f.item)}},
		}, 201, &out)
		if len(out.Window) < 2 {
			t.Fatalf("queue fixture has no next occurrence: %+v", out)
		}
		return out
	}
	q := create(f.token, "receiver-queue-current-001")
	other := create(f.device("receiver-other-controller").AccessToken, "receiver-queue-other-0001")
	var current playbackv1.SessionView
	f.callAs(bearer, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "receiver-session-current-001"}, tl6StartBody("", map[string]any{
		"queue": map[string]any{"queueId": q.Queue.ID, "entryId": q.Window[0].EntryID},
	}), 201, &current)
	if current.Queue == nil || current.Queue.QueueID != q.Queue.ID {
		t.Fatalf("receiver did not adopt its queue: %+v", current)
	}
	if w := f.as(bearer, "GET", "/v1/items/"+current.ItemID, nil); w.Code != 200 {
		t.Errorf("current receiver item metadata: %d %s", w.Code, w.Body.String())
	} else {
		var item catalog.Item
		f.decode(w, 200, &item)
		if item.ID != current.ItemID || item.Title == "" {
			t.Fatalf("current item identity: %+v", item)
		}
	}
	if reportedEnded {
		// A scheduled report may reach the server before the engine's
		// serialized natural-completion request. The ended occurrence must
		// retain only the authority needed to complete that same queue entry.
		h := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
			return f.as(bearer, method, path, body)
		})
		h.report(current.ID, current.Presentation.Generation, map[string]any{"state": "ended", "positionMs": current.Presentation.StartPositionMs})
	}
	base := "/v1/queues/" + q.Queue.ID
	var head playbackv1.QueueView
	f.callAs(bearer, "GET", base, nil, nil, 200, &head)
	var next playbackv1.QueueReply
	completion := map[string]any{"reason": "completion", "completionSessionId": current.ID, "completionGeneration": current.Presentation.Generation}
	w := f.callAs(bearer, "POST", base+":advance", map[string]string{"If-Match": head.Revision, "Idempotency-Key": "receiver-completion-current-001"}, completion, 0, nil)
	if w.Code != 200 {
		t.Errorf("current receiver ordinary completion: %d %s", w.Code, w.Body.String())
	} else {
		f.decode(w, 200, &next)
		if next.Session == nil || next.Session.ID == current.ID || next.Session.Queue == nil || next.Session.Queue.QueueID != q.Queue.ID || next.Session.Queue.EntryID == current.Queue.EntryID {
			t.Fatalf("ordinary completion did not replace the exact occurrence: %+v", next)
		}
		current = *next.Session
	}
	for _, path := range []string{"/v1/items?libraryId=" + f.library, "/v1/queues/" + other.Queue.ID, base + "/entries", base + "/window?around=current&before=1&after=1"} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 403 {
			t.Errorf("receiver escaped its occurrence with GET %s: %d", path, w.Code)
		}
	}
	if w := f.callAs(bearer, "POST", "/v1/queues/"+other.Queue.ID+":advance", map[string]string{"If-Match": other.Queue.Revision}, completion, 0, nil); w.Code != 403 {
		t.Errorf("receiver advanced a different device queue: %d", w.Code)
	}
	f.callAs(bearer, "DELETE", "/v1/playback/sessions/"+current.ID, nil, nil, 204, nil)
	for _, path := range []string{"/v1/items/" + current.ItemID, base} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 403 {
			t.Errorf("stopped receiver retained occurrence access GET %s: %d", path, w.Code)
		}
	}
}
