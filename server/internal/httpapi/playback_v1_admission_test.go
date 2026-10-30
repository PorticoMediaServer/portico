package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// member adds a member account whose only library is a second one ("Kids"),
// with one film, and returns the member's token and that film.
func (f *v1Fixture) member() (string, string) {
	f.t.Helper()
	folder := filepath.Join(f.root, "kids")
	if err := os.MkdirAll(folder, 0700); err != nil {
		f.t.Fatal(err)
	}
	kids, err := f.cat.Create("Kids", "movie", folder)
	if err != nil {
		f.t.Fatal(err)
	}
	kidsHandle := f.catalogTest.Handle(kids.ID)
	path := filepath.Join(folder, "Kids Film.mp4")
	if err = os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		f.t.Fatal(err)
	}
	film := createPlaybackMovie(f.t, f.catalogTest, kidsHandle, path, "Kids Film", 2000, 600, "mp4", "h264", "aac")
	f.names["member-film"] = film
	f.catalogTest.Drain()
	if _, err = f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',X'00','member-profile',1)`); err != nil {
		f.t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='member'`, `["`+kids.ID+`"]`); err != nil {
		f.t.Fatal(err)
	}
	env, err := f.id.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		f.t.Fatal(err)
	}
	return env.AccessToken, film.Public
}

// Review P1 / SEC-02: a title in a library that isn't shared with the member is
// 404 on every path, exactly like a title that doesn't exist, never 401.
func TestPlaybackV1MemberCannotReachUnsharedLibrary(t *testing.T) {
	f := newV1Fixture(t, 1)
	member, film := f.member()
	for _, c := range []struct {
		method, path string
		headers      map[string]string
		body         any
	}{
		{"GET", "/v1/items/" + f.items[0] + "/playback-options", nil, nil},
		{"POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "member-key-00000001"}, startBody(f.items[0], nil)},
		{"GET", "/v1/items/does-not-exist/playback-options", nil, nil},
	} {
		w := f.raw(c.method, c.path, member, c.headers, c.body)
		if w.Code != 404 || v1Code(w) != "not_found" {
			t.Errorf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	f.callAs(member, "GET", "/v1/items/"+film+"/playback-options", nil, nil, 200, nil)
	var s playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "member-key-00000002"}, startBody(film, nil), 201, &s)
	// The owner's session is invisible to the member (another profile).
	var mine playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "owner-key-000000001"}, startBody(f.items[0], nil), 201, &mine)
	if w := f.raw("GET", "/v1/playback/sessions/"+mine.ID, member, nil, nil); w.Code != 404 {
		t.Fatalf("member read the owner's session: %d", w.Code)
	}
}

// Review P2: the planner plans from the device's capability document, which
// belongs to the device: it survives the token-keyed profile rows going away
// (rotation, pruning).
func TestPlaybackV1PlannerUsesTheDeviceDocument(t *testing.T) {
	f := newV1Fixture(t, 1)
	options := "/v1/items/" + f.items[0] + "/playback-options"
	var before playbackv1.Options
	f.call("GET", options, nil, nil, 200, &before)
	if before.Plan == nil || before.Plan.Mode != "direct" {
		t.Fatalf("baseline plan %+v", before.Plan)
	}
	mkvOnly := webCapabilities()
	nativeEngine(mkvOnly)["transports"] = []map[string]any{{"transport": "direct", "containers": []string{"mkv"}, "video": []string{"h264"}, "audio": []string{"aac", "mp3"}}}
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, mkvOnly, 204, nil)
	var after playbackv1.Options
	f.call("GET", options, nil, nil, 200, &after)
	if after.Plan != nil && after.Plan.Mode == "direct" {
		t.Fatalf("an MP4 still plays direct on a device that declared only MKV: %+v", after.Plan)
	}
	// Another sign-in of the same device plans from the same document.
	var device string
	_ = f.db.QueryRow(`SELECT d.device_id FROM authorization_family_tokens t JOIN identity_device_families d ON d.family_id=t.family_id WHERE t.token_hash=?`, identity.Digest(f.owner.AccessToken)).Scan(&device)
	if device == "" {
		t.Fatal("no device for the owner token")
	}
	// One document is stored: the one the device published, as planning reads it.
	var planner string
	_ = f.db.QueryRow(`SELECT document FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&planner)
	if stored, err := playback.ParseCapabilityDocument([]byte(planner)); err != nil || len(stored.Engines.Native.Transports) != 1 {
		t.Fatalf("the published document is not what is stored: %v %s", err, planner)
	}
	// A refused write (stale If-Match) changes nothing (review P3).
	f.call("PUT", "/v1/me/devices/current/capabilities", map[string]string{"If-Match": `"9"`}, webCapabilities(), 412, nil)
	var still string
	_ = f.db.QueryRow(`SELECT document FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&still)
	if still != planner {
		t.Fatal("a 412 changed the stored document")
	}
}

// Review P6: golden fixtures for the v1 routes, decoded by packages/contracts'
// generated decoders (PORTICO_CONTRACT_UPDATE=1 records them).
func TestPlaybackV1ContractFixtures(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	capsRead := f.call("GET", "/v1/me/devices/current/capabilities", nil, nil, 200, nil)
	options := f.call("GET", "/v1/items/"+f.items[0]+"/playback-options", nil, nil, 200, nil)
	start := f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "contract-key-000001"}, startBody(f.items[0], map[string]any{"startFrom": "beginning"}), 201, nil)
	var s playbackv1.SessionView
	_ = json.Unmarshal(start.Body.Bytes(), &s)
	read := f.call("GET", "/v1/playback/sessions/"+s.ID, nil, nil, 200, nil)
	change := f.call("PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": s.Revision}, map[string]any{"state": "paused"}, 200, nil)
	if os.Getenv("PORTICO_CONTRACT_UPDATE") != "1" {
		return
	}
	dir := "../../testdata/contracts/"
	record := func(file, schema, method, path string, request []byte, w *httptest.ResponseRecorder) {
		if err := contract.Record(dir+file, schema, method, path, request, w, "device"); err != nil {
			t.Fatal(err)
		}
	}
	record("get-device-capabilities.json", "GetDeviceCapabilitiesResponse", "GET", "/v1/me/devices/current/capabilities", nil, capsRead)
	record("get-playback-options.json", "GetPlaybackOptionsResponse", "GET", "/v1/items/{itemId}/playback-options", nil, options)
	record("start-playback-session.json", "StartPlaybackSessionResponse", "POST", "/v1/playback/sessions", []byte(`{"itemId":"<item>","startFrom":"beginning"}`), start)
	record("get-playback-session.json", "GetPlaybackSessionResponse", "GET", "/v1/playback/sessions/{id}", nil, read)
	record("change-playback-session.json", "ChangePlaybackSessionResponse", "PATCH", "/v1/playback/sessions/{id}", []byte(`{"state":"paused"}`), change)
}

func TestPlaybackV1AccountCapFailureCarriesObservedLimitAndPlayingCount(t *testing.T) {
	f := newV1Fixture(t, 2)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET per_account_cap=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cap-observed-00001"}, startBody(f.items[0], nil), 201, nil)
	w := f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cap-observed-00002"}, startBody(f.items[1], nil), 409, nil)
	var doc struct {
		Error struct {
			Code, Reason string
			Current      playbackv1.StreamLimitDetail
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Error.Code != "stream_limit_reached" || doc.Error.Reason != "owner_account_cap" || doc.Error.Current.Limit != 1 || doc.Error.Current.Playing != 1 || doc.Error.Current.Scope != "account" {
		t.Fatalf("observed failure: %s", w.Body.String())
	}
}
