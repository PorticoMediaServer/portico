package api

import (
	"strings"
	"testing"
)

// On a server that uses Portico Accounts, a person's own account document names the Portico
// Account they sign in with, for Settings > Sign-in; the members list never names anyone's;
// and a local account names none.
func TestTheAccountDocumentNamesItsOwnPorticoAccount(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	code, _, in := p.signIn(accountJustin, "", 1)
	if code != 200 {
		t.Fatalf("the owner's sign-in answered %d", code)
	}
	if me := get[Me](t, p.s, in.Session.AccessToken, "/v1/me"); me.Account.HostedAccountID != accountJustin.ID {
		t.Fatalf("the owner's own account document names %q, want %q", me.Account.HostedAccountID, accountJustin.ID)
	}
	invitation := p.invite()
	if code, refused, _ := p.signIn(accountSam, invitation, 2); code != 200 {
		t.Fatalf("joining with an invitation answered %d %s", code, refused)
	}
	w := call(t, p.s, "GET", "/v1/admin/members", p.owner.AccessToken, nil, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "hostedAccountId") || strings.Contains(w.Body.String(), accountSam.ID) {
		t.Fatalf("the members list answered %d and names a Portico Account: %s", w.Code, w.Body)
	}

	local := newTestServer(t)
	owner := onMainProfile(t, local)
	if me := get[Me](t, local, owner.AccessToken, "/v1/me"); me.Account.HostedAccountID != "" {
		t.Fatalf("a local account names Portico Account %q", me.Account.HostedAccountID)
	}
}
