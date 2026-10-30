package httpapi

import (
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// NEW-33: a prepared (private) next track's direct audio serves its opening
// bytes before commit, through the same grant checks as a committed one; the
// prefetch budget is spent only by bytes actually sent, so a failed request
// can be retried; after commit the committed grant serves without a budget and
// the finished one is a hidden 404 (a grant is a capability: never 401).
func TestPlaybackV1PreparedAudioServesItsOpeningBytes(t *testing.T) {
	f, player := renderingFixture(t, 3)
	// As in production, the remote-source relay is configured, so every original
	// file serve first asks it (local files pass through): the path NEW-33 broke.
	player.ConfigureRemote(playback.NewRemote(f.db, nil, assets.Probe{}))
	member, _ := f.member()
	q := playingQueue(t, f, "prepared-audio-queue-01")
	p := prepareNext(t, f, q, "prepared-audio-prep-001")
	plan := p.Presentation.AudioRender
	if plan == nil || plan.Mode != "direct" || plan.PrefetchBytes != 10 {
		t.Fatalf("prepared plan %+v", plan)
	}
	// Another profile can't prepare on this queue.
	if w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", member, map[string]string{"If-Match": q.Queue.Revision, "Idempotency-Key": "prepared-audio-member-1"}, map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation}); w.Code != 404 && w.Code != 403 {
		t.Fatalf("another profile prepared: %d %s", w.Code, w.Body.String())
	}
	// A request that fails after the budget check spends nothing: hide the file,
	// ask, put it back, and the whole budget is still there.
	var path string
	if err := f.db.QueryRow(`SELECT a.path FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE l.entity_id=?`, f.catalogTest.ID(p.ItemID)).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatal(err)
	}
	if w := f.raw("GET", plan.URL, "", map[string]string{"Range": "bytes=0-9"}, nil); w.Code/100 == 2 || w.Code == 401 {
		t.Fatalf("a missing source served: %d %s", w.Code, w.Body.String())
	}
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatal(err)
	}
	w := f.raw("GET", plan.URL, "", map[string]string{"Range": "bytes=0-9"}, nil)
	if w.Code != 206 || w.Body.String() != "0123456789" {
		t.Fatalf("a prepared track's opening bytes: %d %q", w.Code, w.Body.String())
	}
	if w = f.raw("GET", plan.URL, "", map[string]string{"Range": "bytes=0-0"}, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "prepared_limit") {
		t.Fatalf("past the prefetch budget: %d %s", w.Code, w.Body.String())
	}
	var c playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &c)
	if w = f.raw("GET", c.Session.Presentation.AudioRender.URL, "", map[string]string{"Range": "bytes=2-5"}, nil); w.Code != 206 || w.Body.String() != "2345" {
		t.Fatalf("the committed track: %d %q", w.Code, w.Body.String())
	}
	// The finished track's grant is gone: a hidden 404, not a sign-in prompt.
	if w = f.raw("GET", q.Session.Presentation.AudioRender.URL, "", map[string]string{"Range": "bytes=0-0"}, nil); w.Code != 404 || !strings.Contains(w.Body.String(), "presentation_ended") {
		t.Fatalf("the finished track's grant: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("GET", "/v1/media/not-a-grant-at-all-000000000000/audio", "", nil, nil); w.Code != 404 {
		t.Fatalf("an unknown grant: %d %s", w.Code, w.Body.String())
	}
}

// NEW-35: an ended presentation's media grant (the original file and its
// HLS files alike) answers a hidden 404 presentation_ended, never a 401 that
// would send the client to sign in again.
func TestPlaybackV1EndedGrantIsPresentationEnded(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "ended-grant-key-000001"}, startBody(f.items[0], map[string]any{"startFrom": "beginning"}), 201, &s)
	if w := f.raw("GET", s.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil); w.Code != 206 && w.Code != 200 {
		t.Fatalf("a live grant: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.db.Exec(`UPDATE playback_sessions SET state='ended'`); err != nil {
		t.Fatal(err)
	}
	w := f.raw("GET", s.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "presentation_ended") {
		t.Fatalf("an ended grant: %d %s", w.Code, w.Body.String())
	}
}
