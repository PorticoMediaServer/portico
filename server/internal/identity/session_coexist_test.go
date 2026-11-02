package identity

import (
	"context"
	"errors"
	"testing"
)

// Same-account sessions on different devices (two browsers, two origins, a phone and a TV)
// coexist and renew independently, repeatedly and in turn; only an explicit sign-out or a
// revoke ends one. A later sign-in elsewhere never ends them.
func TestSameAccountSessionsCoexistAndRenewRepeatedly(t *testing.T) {
	ctx := context.Background()
	s, _, _ := directFixture(t)
	signIn := func(installation string) Envelope {
		t.Helper()
		deviceCtx, err := WithIssuingDevice(ctx, registration(installation), "203.0.113.7:51314")
		if err != nil {
			t.Fatal(err)
		}
		login, err := s.DirectLogin(deviceCtx, "owner", "Testing1!")
		if err != nil || login.Session == nil || login.Session.RefreshToken == "" {
			t.Fatalf("sign-in: %+v %v", login, err)
		}
		if login.Session.InstallationID != installation {
			t.Fatalf("the session is bound to %q, not the installation that signed in", login.Session.InstallationID)
		}
		return *login.Session
	}
	alive := func(name string, session Envelope) {
		t.Helper()
		if _, err := s.Authenticate(session.AccessToken); err != nil {
			t.Fatalf("%s was signed out: %v", name, err)
		}
	}
	left, right := Token(), Token()
	a, b := signIn(left), signIn(right)
	for round := 0; round < 8; round++ {
		next, err := s.RefreshSession(ctx, a.RefreshToken, left, Token())
		if err != nil {
			t.Fatalf("round %d: the first device's renewal: %v", round, err)
		}
		a = next
		if next, err = s.RefreshSession(ctx, b.RefreshToken, right, Token()); err != nil {
			t.Fatalf("round %d: the second device's renewal: %v", round, err)
		}
		b = next
		alive("the first device", a)
		alive("the second device", b)
	}
	// A third device signing in to the same account ends neither.
	c := signIn(Token())
	alive("the first device", a)
	alive("the second device", b)
	alive("the third device", c)
	if _, err := s.RefreshSession(ctx, a.RefreshToken, left, Token()); err != nil {
		t.Fatalf("the first device could not renew after another sign-in: %v", err)
	}
}

// A sign-in that names no installation is bound to one the server makes and returns
// (Session.InstallationID). A renewal claiming any other installation is refused, and that
// refusal ends nothing: the session still renews with its own binding. This is what a test
// harness meets when it swaps the browser's sign-in for a header-less one.
func TestRenewalFromAnotherInstallationIsRefusedWithoutEndingTheSession(t *testing.T) {
	ctx := context.Background()
	s, _, _ := directFixture(t)
	login, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil || login.Session == nil {
		t.Fatal(err)
	}
	session := *login.Session
	if _, err = s.RefreshSession(ctx, session.RefreshToken, Token(), Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a renewal claiming another installation: %v", err)
	}
	if _, err = s.Authenticate(session.AccessToken); err != nil {
		t.Fatalf("the refused renewal ended the session: %v", err)
	}
	if _, err = s.RefreshSession(ctx, session.RefreshToken, session.InstallationID, Token()); err != nil {
		t.Fatalf("the session no longer renews with its own binding: %v", err)
	}
}
