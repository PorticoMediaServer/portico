package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// passwordAccount creates a direct account that signs in with a password.
func passwordAccount(t *testing.T, f *v1Fixture, username, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,?,?,1)`, username, username, hash, username+"-profile"); err != nil {
		t.Fatal(err)
	}
}

func setAccountLimits(t *testing.T, f *v1Fixture, account, body string) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO access_limits VALUES(?,1,0,?) ON CONFLICT(account_id) DO UPDATE SET body=excluded.body,revision=access_limits.revision+1`, account, body); err != nil {
		t.Fatal(err)
	}
}

func liveFamilies(t *testing.T, f *v1Fixture, account string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE account_id=? AND revoked=0`, account).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func directSignIn(t *testing.T, f *v1Fixture, username, password string) (int, string, identity.DirectSignIn) {
	t.Helper()
	w := f.raw("POST", "/v1/direct/sign-in", "", nil, map[string]string{"username": username, "password": password})
	var out identity.DirectSignIn
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, v1Code(w), out
}

func tokenWorks(t *testing.T, f *v1Fixture, token string, want int) {
	t.Helper()
	if w := f.raw("GET", "/v1/me", token, nil, nil); w.Code != want {
		t.Fatalf("GET /v1/me: %d, want %d: %s", w.Code, want, w.Body.String())
	}
}

// A sign-in outside the member's hours is refused before a session exists: the
// account holds no more live session families afterwards than before. Inside
// the hours the same credentials sign in.
func TestMemberLimitsScheduleAtDirectSignIn(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	passwordAccount(t, f, "schedmember", "Testing1!")
	setAccountLimits(t, f, "schedmember", `{"schedule":`+mondayWindow+`}`)
	pinLimitsTime(f, mondayInside())
	code, _, out := directSignIn(t, f, "schedmember", "Testing1!")
	if code != 200 || out.Session == nil {
		t.Fatalf("sign-in inside the hours: %d %+v", code, out)
	}
	tokenWorks(t, f, out.Session.AccessToken, 200)
	if n := liveFamilies(t, f, "schedmember"); n != 1 {
		t.Fatalf("live families after one sign-in: %d", n)
	}
	pinLimitsTime(f, mondayOutside())
	code, v1code, _ := directSignIn(t, f, "schedmember", "Testing1!")
	if code != 403 || v1code != "outside_allowed_hours" {
		t.Fatalf("sign-in outside the hours: %d %s", code, v1code)
	}
	if n := liveFamilies(t, f, "schedmember"); n != 1 {
		t.Fatalf("the refused sign-in left a live session: %d families", n)
	}
}

// A refresh outside the member's hours is refused without ending anything: the
// device keeps its sign-in, the same refresh credential works again once the
// hours return, and nothing was rotated in between.
func TestMemberLimitsScheduleAtRefresh(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	passwordAccount(t, f, "schedrefresh", "Testing1!")
	setAccountLimits(t, f, "schedrefresh", `{"schedule":`+mondayWindow+`}`)
	pinLimitsTime(f, mondayInside())
	_, _, signed := directSignIn(t, f, "schedrefresh", "Testing1!")
	if signed.Session == nil {
		t.Fatal("no session to refresh")
	}
	installationOf := func(env identity.Envelope) string {
		var installation string
		if err := f.db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, env.DeviceID).Scan(&installation); err != nil {
			t.Fatal(err)
		}
		return installation
	}
	refresh := func(env identity.Envelope, key int) (int, string, identity.Envelope) {
		w := f.raw("POST", "/v1/auth/refresh", "", nil, map[string]string{"refreshToken": env.RefreshToken, "installationId": installationOf(env), "requestId": fmt.Sprintf("refresh-test-%04d", key)})
		var out identity.Envelope
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, v1Code(w), out
	}
	pinLimitsTime(f, mondayOutside())
	for attempt := 1; attempt <= 2; attempt++ {
		code, v1code, _ := refresh(*signed.Session, attempt)
		if code != 403 || v1code != "outside_allowed_hours" {
			t.Fatalf("refresh %d outside the hours: %d %s", attempt, code, v1code)
		}
	}
	if n := liveFamilies(t, f, "schedrefresh"); n != 1 {
		t.Fatalf("a refusal outside the hours ended the sign-in: %d live families", n)
	}
	var generation int
	if err := f.db.QueryRow(`SELECT current_generation FROM authorization_session_families WHERE id=?`, signed.Session.SessionFamilyID).Scan(&generation); err != nil || generation != 1 {
		t.Fatalf("a refused renewal rotated the session: generation %d %v", generation, err)
	}
	pinLimitsTime(f, mondayInside())
	code, _, rotated := refresh(*signed.Session, 3)
	if code != 200 || rotated.AccessToken == "" || rotated.RefreshToken == signed.Session.RefreshToken {
		t.Fatalf("the same credential inside the hours: %d %+v", code, rotated)
	}
	tokenWorks(t, f, rotated.AccessToken, 200)
}

// Choosing a profile outside the hours is refused too, and leaves the account
// session it was asked from in place.
func TestMemberLimitsScheduleAtProfileChoice(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	passwordAccount(t, f, "schedchoice", "Testing1!")
	setAccountLimits(t, f, "schedchoice", `{"schedule":`+mondayWindow+`}`)
	pinLimitsTime(f, mondayInside())
	_, _, signed := directSignIn(t, f, "schedchoice", "Testing1!")
	if signed.Session == nil {
		t.Fatal("no session")
	}
	pinLimitsTime(f, mondayOutside())
	w := f.raw("POST", "/v1/direct/profiles/"+signed.Account.PrimaryProfileID+"/select", signed.Session.AccessToken, nil, map[string]any{})
	if w.Code != 403 || v1Code(w) != "outside_allowed_hours" {
		t.Fatalf("profile choice outside the hours: %d %s", w.Code, w.Body.String())
	}
	if n := liveFamilies(t, f, "schedchoice"); n != 1 {
		t.Fatalf("live families after a refused choice: %d", n)
	}
	pinLimitsTime(f, mondayInside())
	if w = f.raw("POST", "/v1/direct/profiles/"+signed.Account.PrimaryProfileID+"/select", signed.Session.AccessToken, nil, map[string]any{}); w.Code != 200 {
		t.Fatalf("profile choice inside the hours: %d %s", w.Code, w.Body.String())
	}
}

// Self-registration signs the new account in through the same schedule
// check. A new account can hold no limits envelope yet, so the schedule can
// only allow here; the assertion is that the session is issued live and the
// path runs the check (a stored envelope would be enforced like any other).
func TestMemberLimitsScheduleAtRegister(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	if err := f.id.SetSelfRegistration(identity.SelfRegistrationOpen); err != nil {
		t.Fatal(err)
	}
	pinLimitsTime(f, mondayInside())
	var out identity.DirectSignIn
	f.callAs("", "POST", "/v1/auth/register", nil, map[string]string{"username": "schedreg2", "password": "Testing1!"}, 201, &out)
	if out.Session == nil {
		t.Fatalf("register inside the hours issued no session: %+v", out)
	}
	tokenWorks(t, f, out.Session.AccessToken, 200)
	if n := liveFamilies(t, f, out.Session.Viewer.AccountID); n != 1 {
		t.Fatalf("live families after registration: %d", n)
	}
}

// memberTOTPStep is the six-digit code for a test TOTP secret at a step,
// mirroring the identity package (base32 without padding, SHA-1, 30 s steps).
func memberTOTPStep(t *testing.T, secret string, step int64) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		t.Fatal(err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// The second factor completes the sign-in, so the schedule is checked at
// completion, not at the password step: the password step still issues a
// challenge outside the hours, but the challenge cannot become a session.
func TestMemberLimitsScheduleAtTwoFactorChallenge(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	ctx := context.Background()
	enrol := func(username string) string {
		t.Helper()
		passwordAccount(t, f, username, "Testing1!")
		setAccountLimits(t, f, username, `{"schedule":`+mondayWindow+`}`)
		login, err := f.id.DirectLoginFrom(ctx, username, "Testing1!", false)
		if err != nil {
			t.Fatal(err)
		}
		enrolment, err := f.id.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.id.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", memberTOTPStep(t, enrolment.Secret, time.Now().Unix()/30)); err != nil {
			t.Fatal(err)
		}
		return enrolment.Secret
	}
	denySecret := enrol("twofadeny")
	allowSecret := enrol("twofaallow")
	complete := func(username, secret string) (int, string, identity.DirectSignIn) {
		t.Helper()
		_, _, challenged := directSignIn(t, f, username, "Testing1!")
		if challenged.Challenge == nil || challenged.Session != nil {
			t.Fatalf("password step issued no challenge: %+v", challenged)
		}
		// The enrolment consumed this step's code; the completion uses the
		// next one, inside the acceptance window.
		code := memberTOTPStep(t, secret, time.Now().Unix()/30+1)
		w := f.raw("POST", "/v1/auth/two-factor/challenge", "", nil, map[string]string{"token": challenged.Challenge.Token, "code": code})
		var out identity.DirectSignIn
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, v1Code(w), out
	}
	pinLimitsTime(f, mondayOutside())
	before := liveFamilies(t, f, "twofadeny")
	code, v1code, _ := complete("twofadeny", denySecret)
	if code != 403 || v1code != "outside_allowed_hours" {
		t.Fatalf("challenge completion outside the hours: %d %s", code, v1code)
	}
	if n := liveFamilies(t, f, "twofadeny"); n != before {
		t.Fatalf("the refused completion left a live session: %d families, was %d", n, before)
	}
	pinLimitsTime(f, mondayInside())
	code, _, out := complete("twofaallow", allowSecret)
	if code != 200 || out.Session == nil {
		t.Fatalf("challenge completion inside the hours: %d %+v", code, out)
	}
	tokenWorks(t, f, out.Session.AccessToken, 200)
}
