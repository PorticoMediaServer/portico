package httpapi

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// NEW-31 regression: an owner streams read never fails because a subsystem
// has nothing to report. The B8 console/streams history endpoint was removed
// (active streams come from the v1 admin sessions list); that list answers
// 200 with an empty list when nothing is playing and lists a live v1 VOD
// session once one starts. The removed path stays gone rather than failing
// as a 5xx.
func TestOwnerStreamsReadEmptyThenLive(t *testing.T) {
	f := newV1Fixture(t, 1)
	var empty playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &empty)
	if empty.Page.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("no playback listed %+v", empty.Page)
	}
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "streams-live-00000001"}, startBody(f.items[0], nil), 201, &s)
	if s.Kind != "vod" || s.State != "playing" {
		t.Fatalf("not a live VOD session: %+v", s)
	}
	var page playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &page)
	if page.Page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("one live session listed %+v", page.Page)
	}
	if got := page.Items[0]; got.ID != s.ID || got.Kind != "vod" {
		t.Fatalf("live session %+v", got)
	}
	if w := f.raw("GET", "/v1/admin/console/streams?status=active", f.owner.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("removed console streams path answers %d, want 404 (gone, never a 5xx): %s", w.Code, w.Body.String())
	}
}
