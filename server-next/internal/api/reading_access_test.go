package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A book's address is good only while the sign-in that asked for it holds and its profile may
// still see the book. A profile given a rating limit stops reading within seconds, and an
// address used after its sign-in ended, by anyone it reached, reads nothing.
func TestABookAddressEndsWithItsSignInAndAccess(t *testing.T) {
	b := newBookShelf(t)
	owner := b.owner.AccessToken
	nineteen := "/v1/items/" + b.find(db.KindBook, "Nineteen") + "/book"
	part := "/parts/OEBPS/text/chapter 1.xhtml"

	var kid Profile
	call(t, b.s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	if w := call(t, b.s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, b.s, 1).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid's profile answered %d", w.Code)
	}
	address := get[BookAnswer](t, b.s, onKid.AccessToken, nineteen).MediaURL
	if r := b.fetch(address + part); r.StatusCode != 200 {
		t.Fatalf("the kid's address answered %d", r.StatusCode)
	}
	age := 13
	if w := call(t, b.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{MaxRatingAge: &age, AllLibraries: true}, nil); w.Code != 200 {
		t.Fatalf("limiting the kid answered %d", w.Code)
	}
	b.clock = b.clock.Add(checkForTest)
	if r := b.fetch(address + part); r.StatusCode != 404 {
		t.Fatalf("an address kept working after its profile lost the book: %d", r.StatusCode)
	}

	other := onMain(t, b.s, signIn(t, b.s, 2))
	address = get[BookAnswer](t, b.s, other.AccessToken, nineteen).MediaURL
	if r := b.fetch(address + "/source?save=1"); r.StatusCode != 200 {
		t.Fatalf("saving through a live address answered %d", r.StatusCode)
	}
	if w := call(t, b.s, "DELETE", "/v1/me/sessions/"+other.SessionID, owner, nil, nil); w.Code != 204 {
		t.Fatalf("ending the other sign-in answered %d", w.Code)
	}
	for _, path := range []string{part, "/source?save=1"} {
		if r := b.fetch(address + path); r.StatusCode != 404 {
			t.Fatalf("%s through the address of an ended sign-in answered %d", path, r.StatusCode)
		}
	}
}

// checkForTest is longer than the time a profile's access to a book is remembered.
const checkForTest = 6 * time.Second
