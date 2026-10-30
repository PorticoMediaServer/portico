package httpapi

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"testing"
)

func TestAdministratorControlsAnotherProfilesSessionWithoutMediaURLs(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, item := f.member()
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "member-control-start"}, startBody(item, nil))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session playbackv1.SessionView
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	var controls playbackv1.SessionControls
	f.call("GET", "/v1/admin/sessions/"+session.ID+"/controls", nil, nil, 200, &controls)
	// The length of what is playing comes with the controls, so a position slider has an end.
	if controls.DurationMs != 600000 {
		t.Fatal("controls without the title's length", controls)
	}
	var list playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].DurationMs != 600000 {
		t.Fatal("session list without the title's length", list)
	}
	f.call("POST", "/v1/admin/sessions/"+session.ID+"/controls", nil, playbackv1.ControlCommand{Action: "pause", ExpectedRevision: controls.Revision}, 200, &controls)
	if controls.State != "paused" {
		t.Fatal(controls)
	}
	position := int64(9000)
	f.call("POST", "/v1/admin/sessions/"+session.ID+"/controls", nil, playbackv1.ControlCommand{Action: "seek", PositionMs: &position, ExpectedRevision: controls.Revision}, 200, &controls)
	if controls.PositionMs != 9000 {
		t.Fatal(controls)
	}
	f.call("POST", "/v1/admin/sessions/"+session.ID+"/controls", nil, playbackv1.ControlCommand{Action: "resume", ExpectedRevision: controls.Revision}, 200, &controls)
	if controls.State != "playing" {
		t.Fatal(controls)
	}
	w = f.raw("POST", "/v1/admin/sessions/"+session.ID+"/controls", member, nil, map[string]any{"action": "pause", "expectedRevision": controls.Revision})
	if w.Code != 403 {
		t.Fatal("member controlled admin transport", w.Code, w.Body.String())
	}
}

func TestRemoteControlDocumentHidesATitleWhenProfileAccessChanges(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, item := f.member()
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "remote-visible-start"}, startBody(item, nil))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session playbackv1.SessionView
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES('member-profile',0,0,'[]',1,1,1,1,2) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=0,allow_unrated=0,revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	w = f.raw("GET", "/v1/playback/controls", member, nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var document playbackv1.RemoteControlsDocument
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Items) != 1 || document.Items[0].SessionID != session.ID || document.Items[0].Item != nil || len(document.Items[0].Actions) != 1 || document.Items[0].Actions[0] != "stop" || document.Items[0].Next != nil || document.Items[0].DurationMs != 0 || document.Items[0].PositionMs != 0 {
		t.Fatal(document)
	}
	// PP11/SE3: a hidden title still occupies this account's slot. The viewer
	// can release that slot without regaining media or transport access.
	f.callAs(member, "POST", "/v1/playback/controls/"+session.ID+":stop", nil, map[string]any{}, 204, nil)
}

func TestAdministratorPauseIsKeptWhileTheSessionIsPreparing(t *testing.T) {
	f := newV1Fixture(t, 1)
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "preparing-control-start"}, startBody(f.items[0], nil), 201, &session)
	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET state='preparing',revision=revision+1 WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	var controls playbackv1.SessionControls
	f.call("GET", "/v1/admin/sessions/"+session.ID+"/controls", nil, nil, 200, &controls)
	// The length of what is playing comes with the controls, so a position slider has an end.
	if controls.DurationMs != 600000 {
		t.Fatal("controls without the title's length", controls)
	}
	var list playbackv1.AdminSessionPage
	f.call("GET", "/v1/admin/sessions", nil, nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].DurationMs != 600000 {
		t.Fatal("session list without the title's length", list)
	}
	f.call("POST", "/v1/admin/sessions/"+session.ID+"/controls", nil, playbackv1.ControlCommand{Action: "pause", ExpectedRevision: controls.Revision}, 200, &controls)
	f.v1.ReadyCheck = func(context.Context, playback.Session) (bool, error) { return true, nil }
	f.call("GET", "/v1/playback/sessions/"+session.ID, nil, nil, 200, &session)
	if session.State != "paused" {
		t.Fatal("prepared playback forgot the pause", session.State)
	}
}
