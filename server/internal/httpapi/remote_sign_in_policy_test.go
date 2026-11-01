package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/connectivity"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The owner's "who may sign in from outside this network" setting, tried on
// every way a session can be obtained, from a home address and from a public
// one, under each of its three values.

const (
	homePeer   = "192.168.1.20:5000"
	remotePeer = "203.0.113.9:5000"
)

// The network homePeer is posed in is named explicitly. Automatic home
// networks follow the host's attached interfaces, so the LAN has to be the
// owner's saved network, not whichever range the test runner happens to have.
const fixtureHomeNetwork = "192.168.1.0/24"

func remotePolicyFixture(t *testing.T) *v1Fixture {
	t.Helper()
	f := memberLimitsFixture(t, 0)
	// Save the owner's LAN policy before the router's first request and rebuild
	// the router, so that request reads the saved document through a cold
	// policy cache instead of the runner's interfaces.
	v1HomeNetworks(t, f, fixtureHomeNetwork)
	return f
}

func setRemoteSignIn(t *testing.T, f *v1Fixture, policy string) {
	t.Helper()
	var current connectivity.Policy
	w := f.rawFrom(homePeer, "GET", "/v1/admin/connectivity/policy", f.owner.AccessToken, nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil || w.Code != 200 {
		t.Fatalf("read policy: %d %s", w.Code, w.Body.String())
	}
	body := map[string]any{"expectedRevision": current.Revision, "operationId": "remote-sign-in-" + policy + "-" + strconv.FormatInt(current.Revision, 10), "remoteSignInPolicy": policy}
	if w = f.rawFrom(homePeer, "PATCH", "/v1/admin/connectivity/policy", f.owner.AccessToken, nil, body); w.Code != 200 {
		t.Fatalf("set policy %s: %d %s", policy, w.Code, w.Body.String())
	}
}

func signInFrom(f *v1Fixture, peer, username, password string) (*httptest.ResponseRecorder, identity.DirectSignIn) {
	w := f.rawFrom(peer, "POST", "/v1/direct/sign-in", "", nil, map[string]string{"username": username, "password": password})
	var out identity.DirectSignIn
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func familyCount(t *testing.T, f *v1Fixture) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM authorization_session_families`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantRemoteRefusal(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 403 || v1Code(w) != "remote_sign_in_not_allowed" {
		t.Fatalf("%s: got %d %s, want 403 remote_sign_in_not_allowed: %s", what, w.Code, v1Code(w), w.Body.String())
	}
}

func TestRemoteSignInPolicyOnPasswordSignIn(t *testing.T) {
	f := remotePolicyFixture(t)
	passwordAccount(t, f, "remotemember", "Testing1!")
	for _, c := range []struct {
		policy                           string
		ownerRemote, memberRemote, local bool
	}{
		{"allow", true, true, true},
		{"owner-only", true, false, true},
	} {
		setRemoteSignIn(t, f, c.policy)
		check := func(who, peer, user, password string, admitted bool) {
			t.Helper()
			before := familyCount(t, f)
			w, out := signInFrom(f, peer, user, password)
			if admitted {
				if w.Code != 200 || out.Session == nil {
					t.Fatalf("%s under %q: %d %s", who, c.policy, w.Code, w.Body.String())
				}
				return
			}
			wantRemoteRefusal(t, who+" under "+c.policy, w)
			if after := familyCount(t, f); after != before {
				t.Fatalf("%s under %q: a refused sign-in created a session (%d, was %d)", who, c.policy, after, before)
			}
		}
		check("owner from outside", remotePeer, "owner", "long-test-password", c.ownerRemote)
		check("member from outside", remotePeer, "remotemember", "Testing1!", c.memberRemote)
		check("member at home", homePeer, "remotemember", "Testing1!", c.local)
		check("owner at home", homePeer, "owner", "long-test-password", c.local)
	}
}

// A device that signed in at home keeps working when it leaves: choosing a
// profile and renewing the session continue a sign-in that was already
// admitted, and the setting promises to leave those alone.
func TestRemoteSignInPolicyLeavesEstablishedSessionsAlone(t *testing.T) {
	f := remotePolicyFixture(t)
	passwordAccount(t, f, "traveller", "Testing1!")
	headers := map[string]string{"X-Portico-Installation-Id": "installation-traveller-000000000000000000000001", "X-Portico-Device-Name": "Phone", "X-Portico-Device-Platform": "ios", "X-Portico-App": "portico"}
	w := f.rawFrom(homePeer, "POST", "/v1/direct/sign-in", "", headers, map[string]string{"username": "traveller", "password": "Testing1!"})
	var signed identity.DirectSignIn
	if err := json.Unmarshal(w.Body.Bytes(), &signed); err != nil || w.Code != 200 || signed.Session == nil {
		t.Fatalf("sign-in at home: %d %s", w.Code, w.Body.String())
	}
	// Remote access Off closes the server to outside altogether (the gate);
	// owner-only is the sign-in policy that refuses this member's new sign-ins.
	setRemoteSignIn(t, f, "owner-only")
	// The session works from outside.
	if w = f.rawFrom(remotePeer, "GET", "/v1/me", signed.Session.AccessToken, nil, nil); w.Code != 200 {
		t.Fatalf("an established session was refused from outside: %d %s", w.Code, w.Body.String())
	}
	// Choosing its profile from outside is not a new sign-in.
	w = f.rawFrom(remotePeer, "POST", "/v1/direct/profiles/"+signed.Account.PrimaryProfileID+"/select", signed.Session.AccessToken, headers, map[string]any{})
	var selected identity.DirectSelectionResult
	if err := json.Unmarshal(w.Body.Bytes(), &selected); err != nil || w.Code != 200 || selected.Session.AccessToken == "" {
		t.Fatalf("profile choice from outside: %d %s", w.Code, w.Body.String())
	}
	// Nor is renewing it.
	w = f.rawFrom(remotePeer, "POST", "/v1/auth/refresh", "", headers, map[string]string{"refreshToken": selected.Session.RefreshToken, "installationId": headers["X-Portico-Installation-Id"], "requestId": "refresh-traveller-0001"})
	var renewed identity.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &renewed); err != nil || w.Code != 200 || renewed.AccessToken == "" {
		t.Fatalf("renewal from outside: %d %s", w.Code, w.Body.String())
	}
	// A new sign-in from outside is still refused.
	w, _ = signInFrom(f, remotePeer, "traveller", "Testing1!")
	wantRemoteRefusal(t, "new sign-in from outside", w)
}

func TestRemoteSignInPolicyOnSecondFactorCompletion(t *testing.T) {
	f := remotePolicyFixture(t)
	passwordAccount(t, f, "factormember", "Testing1!")
	login, err := f.id.DirectLoginFrom(t.Context(), "factormember", "Testing1!", false)
	if err != nil {
		t.Fatal(err)
	}
	enrolment, err := f.id.EnrolTwoFactor(t.Context(), login.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.VerifyTwoFactor(t.Context(), login.AccountToken, "Testing1!", memberTOTPStep(t, enrolment.Secret, time.Now().Unix()/30)); err != nil {
		t.Fatal(err)
	}
	// The password step happens at home, where a challenge is issued; the code
	// is then presented from outside.
	setRemoteSignIn(t, f, "owner-only")
	w, challenged := signInFrom(f, homePeer, "factormember", "Testing1!")
	if w.Code != 200 || challenged.Challenge == nil {
		t.Fatalf("password step: %d %s", w.Code, w.Body.String())
	}
	before := familyCount(t, f)
	code := memberTOTPStep(t, enrolment.Secret, time.Now().Unix()/30+1)
	w = f.rawFrom(remotePeer, "POST", "/v1/auth/two-factor/challenge", "", nil, map[string]string{"token": challenged.Challenge.Token, "code": code})
	wantRemoteRefusal(t, "code presented from outside under owner-only", w)
	if after := familyCount(t, f); after != before {
		t.Fatalf("a refused completion created a session (%d, was %d)", after, before)
	}
}

func TestRemoteSignInPolicyOnSelfRegistration(t *testing.T) {
	f := remotePolicyFixture(t)
	if err := f.id.SetSelfRegistration(identity.SelfRegistrationOpen); err != nil {
		t.Fatal(err)
	}
	accounts := func() int {
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, policy := range []string{"owner-only"} {
		setRemoteSignIn(t, f, policy)
		before := accounts()
		w := f.rawFrom(remotePeer, "POST", "/v1/auth/register", "", nil, map[string]string{"username": fmt.Sprintf("outsider%d", i), "password": "Testing1!"})
		wantRemoteRefusal(t, "registration from outside under "+policy, w)
		if after := accounts(); after != before {
			t.Fatalf("a refused registration under %q left an account behind", policy)
		}
		if w = f.rawFrom(homePeer, "POST", "/v1/auth/register", "", nil, map[string]string{"username": fmt.Sprintf("insider%d", i), "password": "Testing1!"}); w.Code != 201 {
			t.Fatalf("registration at home under %q: %d %s", policy, w.Code, w.Body.String())
		}
	}
}

// A television asking for a code: the television's own address is the one
// judged, when it asks and again when it collects, because the television is
// the device being signed in. Who approves, and from where, does not matter.
func TestRemoteSignInPolicyOnTelevisionCodes(t *testing.T) {
	f := remotePolicyFixture(t)
	member, _ := f.member()
	start := func(peer string, n int) (*httptest.ResponseRecorder, identity.QuickAuthorization) {
		w := f.rawFrom(peer, "POST", "/v1/quick-connect", "", nil, map[string]string{"requestId": fmt.Sprintf("00000000-0000-4000-8000-%012d", n), "deviceName": "Living room", "platform": "tvos", "appVersion": "1.0"})
		var out identity.QuickAuthorization
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	approve := func(token string, code identity.QuickAuthorization) {
		t.Helper()
		w := f.rawFrom(homePeer, "POST", "/v1/quick-connect/review", token, nil, map[string]string{"userCode": code.UserCode})
		var preview identity.QuickPreview
		if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 {
			t.Fatalf("review: %d %s", w.Code, w.Body.String())
		}
		if w = f.rawFrom(homePeer, "POST", "/v1/quick-connect/decision", token, nil, map[string]string{"requestId": preview.RequestID, "userCode": code.UserCode, "decision": "approve"}); w.Code != 204 {
			t.Fatalf("approve: %d %s", w.Code, w.Body.String())
		}
		if _, err := f.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0`); err != nil {
			t.Fatal(err)
		}
	}
	collect := func(peer string, code identity.QuickAuthorization) *httptest.ResponseRecorder {
		return f.rawFrom(peer, "POST", "/v1/quick-connect/token", "", nil, map[string]string{"deviceCode": code.DeviceCode})
	}

	// Owner only: a television outside may ask for a code. A member's approval
	// does not sign it in; the owner's does.
	setRemoteSignIn(t, f, "owner-only")
	w, code := start(remotePeer, 1)
	if w.Code != 201 {
		t.Fatalf("code for a television outside under owner-only: %d %s", w.Code, w.Body.String())
	}
	approve(member, code)
	before := familyCount(t, f)
	wantRemoteRefusal(t, "television outside collecting a member's approval", collect(remotePeer, code))
	if after := familyCount(t, f); after != before {
		t.Fatalf("a refused collection created a session (%d, was %d)", after, before)
	}
	_, code = start(remotePeer, 2)
	approve(f.owner.AccessToken, code)
	if w = collect(remotePeer, code); w.Code != 200 {
		t.Fatalf("television outside collecting the owner's approval: %d %s", w.Code, w.Body.String())
	}
	// A television at home is signed in by a member as before.
	_, code = start(homePeer, 3)
	approve(member, code)
	if w = collect(homePeer, code); w.Code != 200 {
		t.Fatalf("television at home under owner-only: %d %s", w.Code, w.Body.String())
	}
}
