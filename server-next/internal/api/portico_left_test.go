package api

import (
	"net/http"
	"strings"
	"testing"
)

// A person who left is refused until the owner lets them back in; then they sign in to the
// account they had, and Portico Services lists them on the server again.
func TestTheOwnerCanLetAPersonWhoLeftBackIn(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	code, _, before := p.signIn(accountSam, p.invite(), 1)
	if code != 200 {
		t.Fatal("sam could not join")
	}
	p.run(documentGap)
	p.fake.MemberLeft(p.s.id, accountSam.ID)
	if code, _ := p.notice(p.fake.Note(p.s.id)); code != 200 {
		t.Fatalf("the note answered %d", code)
	}
	if code, refused, _ := p.signIn(accountSam, "", 1); code != 403 || refused != "left_server" {
		t.Fatalf("a person who left answered %d %s, want 403 left_server", code, refused)
	}

	if got := memberState(t, p.s, p.owner.AccessToken, "sam"); got != "left" {
		t.Fatalf("People shows sam as %q, want left", got)
	}
	back := "/v1/admin/portico/left/" + before.Account.ID + "/let-back-in"
	if code := status(t, p.s, "POST", back, before.Session.AccessToken, nil); code == 204 {
		t.Fatal("a person who left let themselves back in")
	}
	for range 2 { // a repeat changes nothing
		if code := status(t, p.s, "POST", back, p.owner.AccessToken, nil); code != 204 {
			t.Fatalf("letting sam back in answered %d", code)
		}
	}
	if got := memberState(t, p.s, p.owner.AccessToken, "sam"); got != "active" {
		t.Fatalf("after being let back in People shows sam as %q", got)
	}
	if code := status(t, p.s, "POST", "/v1/admin/portico/left/nobody/let-back-in", p.owner.AccessToken, nil); code != 404 {
		t.Errorf("letting back in an account that is not here answered %d, want 404", code)
	}

	code, _, after := p.signIn(accountSam, "", 1)
	if code != 200 || after.Account.ID != before.Account.ID {
		t.Fatalf("after being let back in, sam's sign-in answered %d as %q, want their own account %q", code, after.Account.ID, before.Account.ID)
	}
	p.run(documentGap)
	if _, members := p.fake.Linked(p.s.id); members[accountSam.ID] != "member" || !p.works(after) {
		t.Errorf("after the next document Portico Services holds %v and sam's sign-in works: %v", members, p.works(after))
	}
}

// A request Portico Services refuses as signed before the server's present link (a database
// put back from an older backup) is not a matter of the server's name.
func TestARequestSignedBeforeTheLinkTellsTheOwnerToLinkAgain(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	p.servicesAnswer(func(r *http.Request) *http.Response {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/document") {
			return canned(401, map[string]string{"Date": p.clock.Now().UTC().Format(http.TimeFormat)},
				`{"error":{"code":"signed_before_link","message":"refused","retry":false,"requestId":"req_1"}}`)
		}
		return nil
	})
	if code, _, _ := p.signIn(accountSam, p.invite(), 1); code != 200 {
		t.Fatal("sam could not join")
	}
	p.run(documentGap)
	var state PorticoLinkState
	call(t, p.s, "GET", "/v1/admin/portico", p.owner.AccessToken, nil, &state)
	if state.Problem != msgPorticoRelink {
		t.Errorf("a request signed before the link is shown to the owner as %q", state.Problem)
	}
}
