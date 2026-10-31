package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestPINReentryRouteIsExactlyWhatAGatedSignInMayAsk(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/v1/me", true},
		{"GET", "/v1/direct", true},
		{"GET", "/v1/system", true},
		{"DELETE", "/v1/sessions/current", true},
		{"POST", "/v1/direct/profiles/9b1c2d3e/select", true},

		{"POST", "/v1/me", false},
		{"HEAD", "/v1/me", false},
		{"PUT", "/v1/direct", false},
		{"GET", "/v1/me/devices", false},
		{"GET", "/v1/direct/members", false},
		{"GET", "/v1/direct/sessions", false},
		{"GET", "/v1/systems", false},
		{"GET", "/v1/system/health", false},
		{"GET", "/v1/sessions/current", false},
		{"DELETE", "/v1/sessions", false},
		{"DELETE", "/v1/sessions/other", false},
		{"GET", "/v1/direct/profiles/9b1c2d3e/select", false},
		{"POST", "/v1/direct/profiles/9b1c2d3e", false},
		{"POST", "/v1/direct/profiles/9b1c2d3e/pin-reset", false},
		{"POST", "/v1/direct/profiles/9b1c2d3e/select/more", false},
		{"POST", "/v1/direct/profiles/9b1c2d3e/x/select", false},
		{"DELETE", "/v1/direct/trust", false},
		{"GET", "/v1/libraries", false},
		{"GET", "/v1/items/abc", false},
		{"GET", "/v1/playback/abc", false},
		{"POST", "/v1/playback", false},
		{"GET", "/v1/admin/access/members", false},
		{"GET", "/", false},
		{"GET", "", false},
	} {
		if got := pinReentryRoute(c.method, c.path); got != c.want {
			t.Errorf("pinReentryRoute(%s %q)=%v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// httpErrorCode reads the stable error code of a refusal, whatever envelope the
// API wraps it in.
func httpErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		return ""
	}
	if body.Code != "" {
		return body.Code
	}
	return body.Error.Code
}

type pinGateHTTP struct {
	d       Dependencies
	h       http.Handler
	owner   identity.Envelope
	kids    identity.DirectProfile
	pin     string
	install string
}

func newPINGateHTTP(t *testing.T) *pinGateHTTP {
	t.Helper()
	d, owner := completedLocalFoundationFixture(t)
	ctx := t.Context()
	snapshot, err := d.Identity.CreateDirectProfile(ctx, owner.AccessToken, "Kids", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var kids identity.DirectProfile
	for _, p := range snapshot.Profiles {
		if !p.Primary {
			kids = p
		}
	}
	f := &pinGateHTTP{d: d, h: New(d), owner: owner, kids: kids, pin: "4821", install: "pin-gate-installation"}
	if _, err = d.Identity.EditDirectProfile(ctx, owner.AccessToken, kids.ID, identity.DirectProfileEdit{ExpectedRevision: kids.Revision, PIN: &f.pin}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *pinGateHTTP) rememberedKids(t *testing.T) identity.DirectSelectionResult {
	t.Helper()
	out, err := f.d.Identity.SelectDirectProfile(t.Context(), f.owner.AccessToken, f.kids.ID, identity.DirectSelection{PIN: f.pin, Remember: true, InstallationID: f.install})
	if err != nil || out.Remembered == nil {
		t.Fatalf("remembering Kids: %+v %v", out.Remembered, err)
	}
	return out
}

func (f *pinGateHTTP) request(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return pinRecoveryRequest(t, f.h, method, path, token, body)
}

func (f *pinGateHTTP) selectPath() string { return "/v1/direct/profiles/" + f.kids.ID + "/select" }

func TestAGatedSignInAnswersProfilePINReentryExceptWhereItMayEnterThePIN(t *testing.T) {
	f := newPINGateHTTP(t)
	kids := f.rememberedKids(t)
	token := kids.Session.AccessToken

	// Working before the gate, and the answer is cached for a moment.
	for i := 0; i < 2; i++ {
		if w := f.request(t, "GET", "/v1/libraries", token, nil); w.Code != 200 {
			t.Fatalf("an ordinary request before the gate: %d %s", w.Code, w.Body.String())
		}
	}

	// "Forget remembered devices" from the main profile gates the sign-in.
	if err := f.d.Identity.ForgetRememberedDevices(t.Context(), f.owner.AccessToken, f.kids.ID, ""); err != nil {
		t.Fatal(err)
	}

	// Ordinary viewing, and management, are refused as 403 profile_pin_reentry.
	// The sign-in is not revoked, so it is never a 401. The first request has the
	// principal cache warm from before the gate.
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/libraries", nil},
		{"GET", "/v1/libraries", nil},
		{"POST", "/v1/direct/profiles", map[string]string{"name": "Mine", "art": "blue"}},
	} {
		w := f.request(t, probe.method, probe.path, token, probe.body)
		if w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
			t.Fatalf("%s %s on a gated sign-in: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}

	// It may still say who it is, read its account, and enter the PIN.
	for _, path := range []string{"/v1/me", "/v1/direct"} {
		if w := f.request(t, "GET", path, token, nil); w.Code != 200 {
			t.Fatalf("GET %s on a gated sign-in: %d %s", path, w.Code, w.Body.String())
		}
	}
	// Having been resolved on an allowed route does not make an ordinary one pass.
	if w := f.request(t, "GET", "/v1/libraries", token, nil); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
		t.Fatalf("an ordinary request after an allowed one: %d %s", w.Code, w.Body.String())
	}
	// Starting a new playback, or controlling the account's others, waits for the PIN. (A
	// playback that already started is exempt: TestRunningPlaybackRoute… and the
	// identity gate test cover it; this fixture has no playback service.)
	for _, probe := range []struct{ method, path string }{{"POST", "/v1/playback/sessions"}, {"GET", "/v1/playback/controls"}} {
		if w := f.request(t, probe.method, probe.path, token, map[string]any{}); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
			t.Fatalf("%s %s on a gated sign-in: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}

	// A wrong PIN is refused as a wrong PIN and charged; the gate stays.
	w := f.request(t, "POST", f.selectPath(), token, map[string]any{"pin": "0000", "installationId": f.install, "remember": true})
	if w.Code != 403 || httpErrorCode(t, w) != "profile_pin_invalid" {
		t.Fatalf("a wrong PIN from a gated sign-in: %d %s", w.Code, w.Body.String())
	}
	var charged int
	if err := f.d.DB.QueryRow(`SELECT COALESCE(sum(failed_attempts),0) FROM direct_profile_pin_attempts WHERE profile_id=?`, f.kids.ID).Scan(&charged); err != nil || charged != 1 {
		t.Fatalf("the wrong PIN was charged %d times: %v", charged, err)
	}
	if w = f.request(t, "GET", "/v1/libraries", token, nil); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
		t.Fatalf("a wrong PIN lifted the gate: %d %s", w.Code, w.Body.String())
	}
	// The retry delay is real; let the same sign-in try again after it elapses.
	if _, err := f.d.DB.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, f.kids.ID); err != nil {
		t.Fatal(err)
	}

	// The right PIN for the same profile mints a working, ungated successor.
	w = f.request(t, "POST", f.selectPath(), token, map[string]any{"pin": f.pin, "installationId": f.install, "remember": true})
	if w.Code != 200 {
		t.Fatalf("entering the PIN again: %d %s", w.Code, w.Body.String())
	}
	var chosen struct {
		Session    identity.Envelope          `json:"session"`
		Remembered *identity.DirectRemembered `json:"remembered"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &chosen); err != nil || chosen.Session.AccessToken == "" || chosen.Remembered == nil {
		t.Fatalf("the successor: %+v %v", chosen, err)
	}
	if chosen.Session.SessionFamilyID == kids.Session.SessionFamilyID || chosen.Session.Viewer.ProfileID != f.kids.ID {
		t.Fatalf("the successor is not a new sign-in on Kids: %+v", chosen.Session.Viewer)
	}
	if w = f.request(t, "GET", "/v1/libraries", chosen.Session.AccessToken, nil); w.Code != 200 {
		t.Fatalf("the successor's ordinary request: %d %s", w.Code, w.Body.String())
	}
	// A gated sign-in can still sign out.
	if w = f.request(t, "DELETE", "/v1/sessions/current", token, nil); w.Code != 204 && w.Code != 200 {
		t.Fatalf("a gated sign-in signing out: %d %s", w.Code, w.Body.String())
	}
	if w = f.request(t, "GET", "/v1/me", token, nil); w.Code != 401 {
		t.Fatalf("a signed-out gated sign-in: %d %s", w.Code, w.Body.String())
	}
}

func TestChangingAProfilePINGatesItsOtherSignInsOverHTTP(t *testing.T) {
	f := newPINGateHTTP(t)
	kids := f.rememberedKids(t)
	if w := f.request(t, "GET", "/v1/libraries", kids.Session.AccessToken, nil); w.Code != 200 {
		t.Fatalf("before the change: %d %s", w.Code, w.Body.String())
	}
	current, err := f.d.Identity.DirectMe(t.Context(), f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	for _, p := range current.Profiles {
		if p.ID == f.kids.ID {
			revision = p.Revision
		}
	}
	w := f.request(t, "PATCH", "/v1/direct/profiles/"+f.kids.ID, f.owner.AccessToken, map[string]any{"expectedRevision": revision, "pin": "7153"})
	if w.Code != 200 {
		t.Fatalf("changing the PIN: %d %s", w.Code, w.Body.String())
	}
	if w = f.request(t, "GET", "/v1/libraries", kids.Session.AccessToken, nil); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
		t.Fatalf("the sign-in made before the PIN changed: %d %s", w.Code, w.Body.String())
	}
	if w = f.request(t, "GET", "/v1/me", kids.Session.AccessToken, nil); w.Code != 200 {
		t.Fatalf("who is it, after the PIN changed: %d %s", w.Code, w.Body.String())
	}
	// The changer is not asked.
	if w = f.request(t, "GET", "/v1/libraries", f.owner.AccessToken, nil); w.Code != 200 {
		t.Fatalf("the sign-in that changed the PIN: %d %s", w.Code, w.Body.String())
	}
}

func TestARememberedMainProfileAnswersProfilePINManageOnManagementRoutes(t *testing.T) {
	d, owner := completedLocalFoundationFixture(t)
	ctx := t.Context()
	const pin = "4821"
	const install = "pin-manage-installation"
	snapshot, err := d.Identity.DirectMe(ctx, owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	main := snapshot.Profiles[0]
	mainPIN := pin
	if _, err = d.Identity.EditDirectProfile(ctx, owner.AccessToken, main.ID, identity.DirectProfileEdit{ExpectedRevision: main.Revision, PIN: &mainPIN}); err != nil {
		t.Fatal(err)
	}
	login, err := d.Identity.DirectLogin(ctx, "owner", "Local-password1!")
	if err != nil {
		t.Fatal(err)
	}
	remembered, err := d.Identity.SelectDirectProfile(ctx, login.AccountToken, main.ID, identity.DirectSelection{PIN: pin, Remember: true, InstallationID: install})
	if err != nil || remembered.Remembered == nil {
		t.Fatalf("remembering the main profile: %+v %v", remembered.Remembered, err)
	}
	h := New(d)
	token := remembered.Session.AccessToken
	manage := []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/direct/profiles", map[string]string{"name": "Mine", "art": "blue"}},
		{"GET", "/v1/admin/access/members", nil},
	}
	// The admin route is typed and answers through its own error mapper; it is
	// checked on its own in TestTypedRoutesAnswerTheirPINCodes.
	manage = manage[:1]
	for _, probe := range manage {
		if w := pinRecoveryRequest(t, h, probe.method, probe.path, token, probe.body); w.Code == 403 && strings.Contains(w.Body.String(), "profile_pin_manage") {
			t.Fatalf("%s %s was locked right after the PIN was entered: %s", probe.method, probe.path, w.Body.String())
		}
	}

	// An hour later (manage_until has passed): management asks for the PIN again,
	// viewing does not.
	if _, err = d.DB.Exec(`UPDATE authorization_session_families SET manage_until='2000-01-01T00:00:00Z' WHERE id=?`, remembered.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	for _, probe := range manage {
		w := pinRecoveryRequest(t, h, probe.method, probe.path, token, probe.body)
		if w.Code != 403 || httpErrorCode(t, w) != "profile_pin_manage" {
			t.Fatalf("%s %s after the hour: %d %s", probe.method, probe.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/v1/me", "/v1/libraries"} {
		if w := pinRecoveryRequest(t, h, "GET", path, token, nil); w.Code != 200 {
			t.Fatalf("GET %s while management is locked: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := pinRecoveryRequest(t, h, "GET", "/v1/direct", token, nil)
	var me identity.DirectSnapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &me) != nil || !me.ManagePINRequired || !me.CanManage || !me.RememberProfiles {
		t.Fatalf("the locked snapshot: %d %s", w.Code, w.Body.String())
	}

	// The same-profile select with the PIN and remember unlocks it again.
	w = pinRecoveryRequest(t, h, "POST", "/v1/direct/profiles/"+main.ID+"/select", token, map[string]any{"pin": pin, "installationId": install, "remember": true})
	var again struct {
		Session identity.Envelope `json:"session"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &again) != nil || again.Session.AccessToken == "" {
		t.Fatalf("entering the PIN to manage: %d %s", w.Code, w.Body.String())
	}
	for _, probe := range manage {
		if w = pinRecoveryRequest(t, h, probe.method, probe.path, again.Session.AccessToken, probe.body); w.Code == 403 && strings.Contains(w.Body.String(), "profile_pin_manage") {
			t.Fatalf("%s %s stayed locked after the PIN was entered again: %s", probe.method, probe.path, w.Body.String())
		}
	}
	w = pinRecoveryRequest(t, h, "GET", "/v1/direct", again.Session.AccessToken, nil)
	var unlocked identity.DirectSnapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &unlocked) != nil || unlocked.ManagePINRequired || !unlocked.CanManage {
		t.Fatalf("the unlocked snapshot: %d %s", w.Code, w.Body.String())
	}
}

// Most routes are typed (apikit) and map errors through v1Error or their own
// mapper, not through failure(). A gated sign-in and a locked management route
// must answer the same two codes there, never 500.
func TestTypedRoutesAnswerTheirPINCodes(t *testing.T) {
	f := newPINGateHTTP(t)
	f.d.Access = AccessArea{Access: access.New(f.d.DB)}
	f.h = New(f.d)
	kids := f.rememberedKids(t)
	token := kids.Session.AccessToken
	typed := []string{"/v1/home", "/v1/search?q=a", "/v1/receivers"}
	for _, path := range typed {
		if w := f.request(t, "GET", path, token, nil); w.Code != 200 {
			t.Fatalf("GET %s before the gate: %d %s", path, w.Code, w.Body.String())
		}
	}
	if err := f.d.Identity.ForgetRememberedDevices(t.Context(), f.owner.AccessToken, f.kids.ID, ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range typed {
		if w := f.request(t, "GET", path, token, nil); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
			t.Fatalf("GET %s on a gated sign-in: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := f.request(t, "POST", "/v1/direct/profiles/"+f.kids.ID+"/pin-reset", token, map[string]string{"pin": ""}); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_reentry" {
		t.Fatalf("a PIN reset from a gated sign-in: %d %s", w.Code, w.Body.String())
	}

	// A remembered main profile with a PIN, past its management hour.
	const pin = "4821"
	snapshot, err := f.d.Identity.DirectMe(t.Context(), f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	var main identity.DirectProfile
	for _, p := range snapshot.Profiles {
		if p.Primary {
			main = p
		}
	}
	mainPIN := pin
	if _, err = f.d.Identity.EditDirectProfile(t.Context(), f.owner.AccessToken, main.ID, identity.DirectProfileEdit{ExpectedRevision: main.Revision, PIN: &mainPIN}); err != nil {
		t.Fatal(err)
	}
	login, err := f.d.Identity.DirectLogin(t.Context(), "owner", "Local-password1!")
	if err != nil {
		t.Fatal(err)
	}
	remembered, err := f.d.Identity.SelectDirectProfile(t.Context(), login.AccountToken, main.ID, identity.DirectSelection{PIN: pin, Remember: true, InstallationID: f.install})
	if err != nil || remembered.Remembered == nil {
		t.Fatalf("remembering the main profile: %+v %v", remembered.Remembered, err)
	}
	if w := f.request(t, "GET", "/v1/admin/access/members", remembered.Session.AccessToken, nil); w.Code != 200 {
		t.Fatalf("an admin route right after the PIN was entered: %d %s", w.Code, w.Body.String())
	}
	if _, err = f.d.DB.Exec(`UPDATE authorization_session_families SET manage_until='2000-01-01T00:00:00Z' WHERE id=?`, remembered.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if w := f.request(t, "GET", "/v1/admin/access/members", remembered.Session.AccessToken, nil); w.Code != 403 || httpErrorCode(t, w) != "profile_pin_manage" {
		t.Fatalf("an admin route after the management hour: %d %s", w.Code, w.Body.String())
	}
}

func TestRunningPlaybackRouteIsOnlyAPlaybackThatAlreadyStarted(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/v1/playback/sessions/abc", true},
		{"PATCH", "/v1/playback/sessions/abc", true},
		{"PUT", "/v1/playback/sessions/abc/observation", true},
		{"DELETE", "/v1/playback/sessions/abc", true},
		{"GET", "/v1/media/grant", true},
		{"HEAD", "/v1/media/grant/audio", true},
		{"GET", "/v1/items/abc/playback/subtitles", true},
		{"GET", "/v1/subtitle-fonts/cjk", true},
		{"POST", "/v1/playback/sessions", false},
		{"POST", "/v1/playback/sessions/", false},
		{"GET", "/v1/playback/controls", false},
		{"POST", "/v1/playback/controls/abc:stop", false},
		{"POST", "/v1/media/grant", false},
		{"GET", "/v1/media/", false},
		{"GET", "/v1/items/abc/image", false},
		{"GET", "/v1/items/abc", false},
		{"POST", "/v1/subtitle-fonts/cjk", false},
	} {
		if got := runningPlaybackRoute(c.method, c.path); got != c.want {
			t.Errorf("runningPlaybackRoute(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// Every area of management powers (People, other profiles' limits, libraries,
// server settings, sign-in, remote access, API keys, the console) refuses a
// remembered main profile with a PIN once its management hour has passed, with
// profile_pin_manage, and works again after a fresh PIN check.
func TestEveryManagementAreaAsksARememberedMainProfileForItsPIN(t *testing.T) {
	d, owner := completedLocalFoundationFixture(t)
	d.Access = AccessArea{Access: access.New(d.DB)}
	ctx := t.Context()
	const pin, install = "4821", "pin-manage-areas"
	created, err := d.Identity.CreateDirectProfile(ctx, owner.AccessToken, "Kids", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var main, kids identity.DirectProfile
	for _, p := range created.Profiles {
		if p.Primary {
			main = p
		} else {
			kids = p
		}
	}
	mainPIN := pin
	if _, err = d.Identity.EditDirectProfile(ctx, owner.AccessToken, main.ID, identity.DirectProfileEdit{ExpectedRevision: main.Revision, PIN: &mainPIN}); err != nil {
		t.Fatal(err)
	}
	login, err := d.Identity.DirectLogin(ctx, "owner", "Local-password1!")
	if err != nil {
		t.Fatal(err)
	}
	remembered, err := d.Identity.SelectDirectProfile(ctx, login.AccountToken, main.ID, identity.DirectSelection{PIN: pin, Remember: true, InstallationID: install})
	if err != nil || remembered.Remembered == nil {
		t.Fatalf("remembering the main profile: %v", err)
	}
	h := New(d)
	areas := []struct{ area, method, path string }{
		{"people", "GET", "/v1/admin/access/members"},
		{"people", "GET", "/v1/admin/access/members/" + created.Account.ID + "/profiles"},
		{"other profiles' limits", "PATCH", "/v1/direct/profiles/" + kids.ID},
		{"libraries", "GET", "/v1/admin/libraries"},
		{"libraries", "GET", "/v1/admin/library-settings"},
		{"server settings", "GET", "/v1/admin/maintenance/settings"},
		{"server settings", "GET", "/v1/admin/notifications/settings"},
		{"server settings", "GET", "/v1/admin/dvr/settings"},
		{"server settings", "GET", "/v1/admin/backups/settings"},
		{"sign-in", "GET", "/v1/admin/sign-in/policy"},
		{"remote access", "GET", "/v1/networking/remote"},
		{"remote access", "GET", "/v1/networking/certificate"},
		{"api keys", "GET", "/v1/admin/api-keys"},
		{"console", "GET", "/v1/admin/console/alerts"},
	}
	renames := 0
	body := func(method string) any {
		if method != "PATCH" {
			return nil
		}
		var revision int64
		if err := d.DB.QueryRow(`SELECT revision FROM direct_profiles WHERE id=?`, kids.ID).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		renames++
		return map[string]any{"expectedRevision": revision, "name": "Kids " + strconv.Itoa(renames)}
	}
	locked := func(w *httptest.ResponseRecorder) bool {
		return w.Code == 403 && strings.Contains(w.Body.String(), `"profile_pin_manage"`)
	}
	reachable := map[string]int{}
	for _, a := range areas {
		w := pinRecoveryRequest(t, h, a.method, a.path, remembered.Session.AccessToken, body(a.method))
		if locked(w) || w.Code == 404 || w.Code == 401 {
			t.Fatalf("%s %s %s inside the management hour: %d %s", a.area, a.method, a.path, w.Code, w.Body.String())
		}
		reachable[a.path] = w.Code
	}
	if _, err = d.DB.Exec(`UPDATE authorization_session_families SET manage_until='2000-01-01T00:00:00Z' WHERE id=?`, remembered.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	for _, a := range areas {
		if w := pinRecoveryRequest(t, h, a.method, a.path, remembered.Session.AccessToken, body(a.method)); !locked(w) {
			t.Fatalf("%s %s %s after the hour: %d %s", a.area, a.method, a.path, w.Code, w.Body.String())
		}
	}
	// A fresh PIN check (choosing the same profile with its PIN) unlocks every area.
	again, err := d.Identity.SelectDirectProfile(ctx, remembered.Session.AccessToken, main.ID, identity.DirectSelection{PIN: pin, Remember: true, InstallationID: install})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range areas {
		if w := pinRecoveryRequest(t, h, a.method, a.path, again.Session.AccessToken, body(a.method)); locked(w) || w.Code == 401 {
			t.Fatalf("%s %s %s after the PIN was entered again: %d %s", a.area, a.method, a.path, w.Code, w.Body.String())
		}
	}
}
