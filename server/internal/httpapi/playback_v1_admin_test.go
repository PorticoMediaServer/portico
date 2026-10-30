package httpapi

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Spec §14, Now Playing: the owner sees each active session with its viewer,
// device, title and delivery, never its media URL; a member can't list them;
// terminating ends the session for the viewer with the message, and it drops
// off the list. Paging is a keyset (a page costs its page).
func TestPlaybackV1NowPlayingListsAndTerminates(t *testing.T) {
	f := newV1Fixture(t, 3)
	ids := []string{}
	for i := 0; i < 3; i++ {
		var s playbackv1.SessionView
		f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "now-playing-00000" + string(rune('1'+i))}, startBody(f.items[i], map[string]any{"state": "paused"}), 201, &s)
		ids = append(ids, s.ID)
	}
	settleCompactCatalogue(t, f.db)
	var page playbackv1.AdminSessionPage
	w := f.call("GET", "/v1/admin/sessions?limit=2", nil, nil, 200, &page)
	if strings.Contains(w.Body.String(), "/v1/media/") || strings.Contains(w.Body.String(), "\"url\"") {
		t.Fatalf("the list carries a media URL: %s", w.Body.String())
	}
	if page.Page.Total != 3 || len(page.Items) != 2 || page.Page.NextCursor == "" {
		t.Fatalf("first page %+v", page.Page)
	}
	a := page.Items[0]
	if a.User.Name != "owner" || a.Item == nil || a.Item.Title == "" || a.Kind != "vod" || a.State != "paused" || a.Location == "" || a.StartedAt == "" {
		t.Fatalf("session %+v", a)
	}
	var rest playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions?limit=2&cursor="+page.Page.NextCursor, nil, nil, 200, &rest)
	seen := map[string]bool{}
	for _, s := range append(page.Items, rest.Items...) {
		seen[s.ID] = true
	}
	if len(rest.Items) != 1 || rest.Page.NextCursor != "" || len(seen) != 3 {
		t.Fatalf("second page %+v, seen %v", rest, seen)
	}
	member, _ := f.member()
	settleCompactCatalogue(t, f.db)
	if w := f.raw("GET", "/v1/admin/sessions", member, nil, nil); w.Code != 403 && w.Code != 401 && w.Code != 404 {
		t.Fatalf("a member listed sessions: %d", w.Code)
	}
	f.call("POST", "/v1/admin/sessions/"+ids[1]+":terminate", map[string]string{"Idempotency-Key": "terminate-00000001"}, map[string]any{"message": "The server is restarting in five minutes."}, 204, nil)
	var ended playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+ids[1], nil, nil, 200, &ended)
	if ended.State != "ended" || ended.End == nil || ended.End.Reason != "terminated" || ended.End.Message != "The server is restarting in five minutes." {
		t.Fatalf("a re-read of the terminated session: %+v %+v", ended, ended.End)
	}
	var after playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &after)
	if after.Page.Total != 2 {
		t.Fatalf("after terminate: %+v", after.Page)
	}
	for _, s := range after.Items {
		if s.ID == ids[1] {
			t.Fatal("the terminated session is still listed")
		}
	}
	var reason, message string
	if err := f.db.QueryRow(`SELECT end_reason,message FROM playback_v1_sessions WHERE id=?`, ids[1]).Scan(&reason, &message); err != nil || reason != "terminated" || message != "The server is restarting in five minutes." {
		t.Fatalf("ended as %q %q: %v", reason, message, err)
	}
	// Again: idempotent. Unknown: not found.
	f.call("POST", "/v1/admin/sessions/"+ids[1]+":terminate", map[string]string{"Idempotency-Key": "terminate-00000002"}, nil, 204, nil)
	if w := f.raw("POST", "/v1/admin/sessions/nope:terminate", f.owner.AccessToken, map[string]string{"Idempotency-Key": "terminate-00000003"}, nil); w.Code != 404 {
		t.Fatalf("unknown session: %d", w.Code)
	}
}

func TestPlaybackV1AccountPlaybackListHidesRestrictedTitlesAndStopsOwnSlots(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, _ := f.member()
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "account-list-000001"}, startBody(f.items[0], nil), 201, &session)
	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET account_id='member',profile_id='other-profile' WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	var list playbackv1.RemoteControlsDocument
	w := f.callAs(member, "GET", "/v1/playback/controls", nil, nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].Item != nil || list.Items[0].Next != nil || len(list.Items[0].Actions) != 1 || list.Items[0].Actions[0] != "stop" || strings.Contains(w.Body.String(), f.items[0]) {
		t.Fatalf("account list privacy: %s", w.Body.String())
	}
	f.callAs(member, "GET", "/v1/playback/sessions/"+session.ID, nil, nil, 404, nil)
	f.call("POST", "/v1/playback/controls/"+session.ID+":stop", nil, map[string]any{}, 404, nil)
	f.callAs(member, "POST", "/v1/playback/controls/"+session.ID+":stop", nil, map[string]any{}, 204, nil)
	var ended int64
	var reason string
	if err := f.db.QueryRow(`SELECT ended_ms,end_reason FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&ended, &reason); err != nil || ended == 0 || reason != "stopped" {
		t.Fatalf("stop: %d %s %v", ended, reason, err)
	}
}
