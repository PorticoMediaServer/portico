package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func stopRepairKey(n int) string { return fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), n) }
func stopRepairSession(t *testing.T, f *v1Fixture, token, item string) playbackv1.SessionView {
	t.Helper()
	w := f.raw("POST", "/v1/playback/sessions", token, map[string]string{"Idempotency-Key": "stop-repair-start"}, startBody(item, nil))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session playbackv1.SessionView
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}
func stopRepairRestrict(t *testing.T, f *v1Fixture) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES('member-profile',0,0,'[]',1,1,1,1,2) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=0,allow_unrated=0,revision=revision+1`); err != nil {
		t.Fatal(err)
	}
}

func TestAccountStopReceiptHTTPGenerationQuotaAndDigest(t *testing.T) {
	f := newV1Fixture(t, 1)
	session := stopRepairSession(t, f, f.owner.AccessToken, f.items[0])
	w := f.raw("GET", "/v1/playback/controls", f.owner.AccessToken, nil, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var controls playbackv1.RemoteControlsDocument
	if err := json.Unmarshal(w.Body.Bytes(), &controls); err != nil {
		t.Fatal(err)
	}
	if len(controls.Items) != 1 || controls.Items[0].Generation != session.Presentation.Generation {
		t.Fatalf("selected occurrence missing: %+v", controls)
	}
	path := "/v1/playback/controls/" + session.ID + ":stop"
	key := stopRepairKey(91)
	headers := map[string]string{"Idempotency-Key": key}
	// Protocol calls cannot capture whichever occurrence happens to exist later.
	f.call("POST", path, headers, map[string]any{}, 400, nil)
	body := map[string]any{"generation": controls.Items[0].Generation}
	if _, err := f.db.Exec(`UPDATE user_mutation_global SET stop_bytes=33554432-ordinary_bytes WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	f.call("POST", path, headers, body, 503, nil)
	if _, err := f.db.Exec(`UPDATE user_mutation_global SET stop_bytes=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE playback_v1_sessions SET generation=generation+1 WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	f.call("POST", path, headers, body, 412, nil)
	var ended int64
	if err := f.db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&ended); err != nil || ended != 0 {
		t.Fatal("stale Stop ended successor", ended, err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatal("refused Stop receipt", n, err)
	}
	// An explicitly selected new action binds its body in the receipt digest.
	headers["Idempotency-Key"] = stopRepairKey(92)
	body["generation"] = session.Presentation.Generation + 1
	f.call("POST", path, headers, body, 204, nil)
	body["generation"] = session.Presentation.Generation
	w = f.raw("POST", path, f.owner.AccessToken, headers, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "idempotency_conflict") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestOrdinaryStopReceiptHTTPCurrentContentAuthority(t *testing.T) {
	f := newV1Fixture(t, 1)
	token, item := f.member()
	session := stopRepairSession(t, f, token, item)
	key := stopRepairKey(93)
	headers := map[string]string{"Idempotency-Key": key}
	path := "/v1/playback/sessions/" + session.ID
	body := map[string]any{"generation": session.Presentation.Generation, "positionMs": 12000}
	f.callAs(token, "DELETE", path, headers, body, 204, nil)
	stopRepairRestrict(t, f)
	f.callAs(token, "GET", path, nil, nil, 404, nil)
	f.callAs(token, "GET", "/v1/user-mutations/"+key, nil, nil, 404, nil)
	f.callAs(token, "DELETE", path, headers, body, 404, nil)
	// Session pruning cannot turn a hidden original title into an allowed replay.
	if _, err := f.db.Exec(`DELETE FROM playback_v1_sessions WHERE id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	f.callAs(token, "DELETE", path, headers, body, 404, nil)
	if _, err := f.db.Exec(`DELETE FROM profile_restrictions WHERE profile_id='member-profile'`); err != nil {
		t.Fatal(err)
	}
	f.callAs(token, "GET", "/v1/user-mutations/"+key, nil, nil, 200, nil)
	f.callAs(token, "DELETE", path, headers, body, 204, nil)
}

func TestAccountStopReceiptHTTPHiddenTitleException(t *testing.T) {
	f := newV1Fixture(t, 1)
	token, item := f.member()
	session := stopRepairSession(t, f, token, item)
	stopRepairRestrict(t, f)
	key := stopRepairKey(94)
	headers := map[string]string{"Idempotency-Key": key}
	path := "/v1/playback/controls/" + session.ID + ":stop"
	body := map[string]any{"generation": session.Presentation.Generation}
	f.callAs(token, "POST", path, headers, body, 204, nil)
	f.callAs(token, "POST", path, headers, body, 204, nil)
	f.callAs(token, "GET", "/v1/user-mutations/"+key, nil, nil, 200, nil)
	// Older receipts lack immutable evidence and cannot borrow a successor's item.
	if _, err := f.db.Exec(`UPDATE user_mutation_receipts SET stop_item_id='' WHERE key=?`, key); err != nil {
		t.Fatal(err)
	}
	f.callAs(token, "GET", "/v1/user-mutations/"+key, nil, nil, 404, nil)
	f.callAs(token, "POST", path, headers, body, 404, nil)
}
