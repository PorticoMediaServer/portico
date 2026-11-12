package api

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/hostedtest"
)

// On a server that uses Portico Accounts, a forgotten PIN is replaced by proving the account's
// own Portico Account afresh: a note for this server's new challenge, once; another person's
// Portico Account is refused even with a real note; nobody reaches another account's profile
// this way; and the account's other device on that profile is sent back to the chooser.
func TestAForgottenPINIsReplacedWithTheAccountsOwnPorticoAccount(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	_, _, sam := p.signIn(accountSam, p.invite(), 4) // a member, before anything below changes
	_, _, owner := p.signIn(accountJustin, "", 1)
	main := owner.Profiles[0].ID
	var onMainProfile Session
	call(t, p.s, "POST", "/v1/profiles/"+main+"/select", owner.Session.AccessToken, ProfileSelect{}, &onMainProfile)
	if code := status(t, p.s, "PUT", "/v1/profiles/"+main+"/pin", onMainProfile.AccessToken, ProfilePIN{PIN: "1234"}); code != 200 {
		t.Fatalf("setting the first PIN answered %d", code)
	}
	// Another of the owner's devices on the main profile, with the PIN.
	_, _, tv := p.signIn(accountJustin, "", 2)
	var tvOnMain Session
	call(t, p.s, "POST", "/v1/profiles/"+main+"/select", tv.Session.AccessToken, ProfileSelect{PIN: "1234"}, &tvOnMain)
	if me := get[Me](t, p.s, tvOnMain.AccessToken, "/v1/me"); me.Profile == nil {
		t.Fatal("the TV is not on the main profile")
	}

	// The PIN is forgotten on a third device, at the profile chooser.
	_, _, phone := p.signIn(accountJustin, "", 3)
	prove := func(account hostedtest.Account) ProfilePIN {
		challenge := p.challenge()
		note := p.fake.Identity(account, p.s.id, challenge)
		raw, _ := json.Marshal(note)
		return ProfilePIN{PIN: "5678", Challenge: challenge, Identity: raw}
	}
	proof := prove(accountJustin)
	if w := call(t, p.s, "PUT", "/v1/profiles/"+main+"/pin", phone.Session.AccessToken, proof, nil); w.Code != 200 {
		t.Fatalf("replacing the PIN with the Portico Account answered %d: %s", w.Code, w.Body)
	}
	var phoneOnMain Session
	if w := call(t, p.s, "POST", "/v1/profiles/"+main+"/select", phone.Session.AccessToken, ProfileSelect{PIN: "5678"}, &phoneOnMain); w.Code != 200 {
		t.Fatalf("the new PIN answered %d", w.Code)
	}
	// The TV's hold on the profile ends: its next request is refused, and its sign-in, renewed,
	// is at the profile chooser.
	if code := status(t, p.s, "GET", "/v1/me", tvOnMain.AccessToken, nil); code != 401 {
		t.Fatalf("the TV on the profile whose PIN changed answered %d", code)
	}
	// The same note again changes nothing: a note is good once.
	proof.PIN = "9999"
	if code := status(t, p.s, "PUT", "/v1/profiles/"+main+"/pin", phoneOnMain.AccessToken, proof); code < 400 {
		t.Fatalf("a spent note answered %d", code)
	}

	// Another person's real Portico Account does not prove this account.
	if code := status(t, p.s, "PUT", "/v1/profiles/"+main+"/pin", phoneOnMain.AccessToken, prove(accountSam)); code != 403 {
		t.Fatalf("someone else's Portico Account answered %d", code)
	}
	// Nor does anyone's own Portico Account reach another account's profile.
	if code := status(t, p.s, "PUT", "/v1/profiles/"+main+"/pin", sam.Session.AccessToken, prove(accountSam)); code != 404 {
		t.Fatalf("a member replacing the owner's PIN answered %d", code)
	}
}
