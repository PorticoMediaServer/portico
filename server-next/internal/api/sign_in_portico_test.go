package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// On a server that signs people in with Portico Accounts the sign-in settings say that
// required two-step does not apply, with the one line a screen shows in its place. A server
// with local sign-in says no such thing.
func TestRequiredTwoStepIsNotOfferedOnAPorticoAccountsServer(t *testing.T) {
	local := newTestServer(t)
	if doc := get[SettingsDocument](t, local, onMainProfile(t, local).AccessToken, "/v1/admin/settings/sign-in"); len(doc.NotApplicable) != 0 {
		t.Errorf("a server with local sign-in says these settings do not apply: %v", doc.NotApplicable)
	}
	s, owner, _ := porticoServer(t)
	doc := get[SettingsDocument](t, s, owner, "/v1/admin/settings/sign-in")
	if doc.NotApplicable["requireTwoStep"] != twoStepOnPortico || len(doc.NotApplicable) != 1 {
		t.Errorf("a Portico Accounts server says %v do not apply, want required two-step with its line", doc.NotApplicable)
	}
	var all Page[SettingsDocument]
	call(t, s, "GET", "/v1/admin/settings", owner, nil, &all)
	for _, d := range all.Items {
		if (d.Name == "sign-in") != (len(d.NotApplicable) == 1) {
			t.Errorf("in the list of documents, %s says %v do not apply", d.Name, d.NotApplicable)
		}
	}
}

// On such a server a save that turns required two-step on is refused with that same line;
// the other sign-in settings still save, and so does sending it as it is, off.
func TestRequiredTwoStepCannotBeTurnedOnOnAPorticoAccountsServer(t *testing.T) {
	s, owner, _ := porticoServer(t)
	for _, on := range []string{"admins", "everyone"} {
		w := call(t, s, "PUT", "/v1/admin/settings/sign-in", owner, json.RawMessage(`{"requireTwoStep":"`+on+`"}`), nil)
		if w.Code != 422 || !strings.Contains(w.Body.String(), twoStepOnPortico) {
			t.Errorf("turning required two-step on for %s answered %d %s", on, w.Code, w.Body)
		}
	}
	if got := settings.SignIn.Get(s.settings).RequireTwoStep; got != "nobody" {
		t.Errorf("after the refused saves the setting is %q", got)
	}
	saveSignIn(t, s, owner, `{"staySignedInDays":30,"requireTwoStep":"nobody"}`, 200)
}

// Switching a server to Portico Accounts while two-step is required of everyone turns the
// requirement off, and the activity record says so.
func TestSwitchingToPorticoAccountsTurnsRequiredTwoStepOff(t *testing.T) {
	s := newTestServer(t)
	fake := &fakeHosted{t: t}
	s.hosted = fake
	owner := onMain(t, s, setup(t, s)).AccessToken
	saveSignIn(t, s, owner, `{"requireTwoStep":"everyone"}`, 200)
	fake.decision = hosted.Decision{Purpose: hosted.PurposeSwitch, Account: hosted.Account{ID: "pa_1", Username: "justin"}}
	var switched SignInResponse
	body := SignInSwitchRequest{To: "portico", CurrentPassword: testPassword, Approval: "approved-for-" + s.id, Device: device(1)}
	if w := call(t, s, "POST", "/v1/admin/sign-in/switch", owner, body, &switched); w.Code != 200 {
		t.Fatalf("switching to Portico Accounts answered %d: %s", w.Code, w.Body)
	}
	if got := settings.SignIn.Get(s.settings).RequireTwoStep; got != "nobody" {
		t.Errorf("after the switch required two-step is %q, want nobody", got)
	}
	said := 0
	for _, entry := range get[Page[ActivityEntry]](t, s, onMain(t, s, switched).AccessToken, "/v1/admin/audit").Items {
		if entry.Action == "settings.two_step_not_required" && strings.Contains(entry.Detail, "Portico Accounts") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("the activity record says %d times that required two-step was turned off, want once", said)
	}
}
