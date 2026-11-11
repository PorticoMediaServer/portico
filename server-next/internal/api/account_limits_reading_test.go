package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A book's address lives for hours and carries its own permission. An account held to the
// home network reads nothing with it from away, and one held to viewing hours reads nothing
// once they end; an account held to nothing reads from anywhere.
func TestABooksAddressIsHeldToHomeAndToViewingHours(t *testing.T) {
	b := newBookShelf(t)
	s, owner := b.s, b.owner.AccessToken
	base, later := time.Now().UTC(), time.Duration(0)
	s.now = func() time.Time { return base.Add(later) }
	max, _ := addMember(t, s, owner, "max", "member"), addMember(t, s, owner, "eve", "member")
	// Home only, and hours that are open for the next hour.
	limits := noLimits()
	limits.HomeOnly = true
	start := base.Hour()*60 + base.Minute()
	limits.ViewingHours = &auth.ViewingHours{Timezone: "UTC", Windows: []auth.ViewingWindow{{Days: []int{int(base.Weekday())}, StartMinute: start, EndMinute: (start + 60) % 1440}}}
	hold(t, s, owner, max.ID, limits)

	nineteen := b.find(db.KindBook, "Nineteen")
	open := func(username string, n int) string {
		t.Helper()
		var in SignInResponse
		if w := sent(t, s, atHome, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: testPassword, Device: device(n)}, &in); w.Code != 200 {
			t.Fatalf("%s signing in at home answered %d: %s", username, w.Code, w.Body)
		}
		var on Session
		if w := sent(t, s, atHome, "POST", "/v1/profiles/"+in.Profiles[0].ID+"/select", in.Session.AccessToken, ProfileSelect{}, &on); w.Code != 200 {
			t.Fatalf("%s choosing a profile answered %d", username, w.Code)
		}
		return get[BookAnswer](t, s, on.AccessToken, "/v1/items/"+nineteen+"/book").MediaURL + "/parts/OEBPS/text/chapter%201.xhtml"
	}
	held, free := open("max", 5), open("eve", 6)
	read := func(peer, address string) int { return sent(t, s, peer, "GET", address, "", nil, nil).Code }
	if got := read(atHome, held); got != 200 {
		t.Fatalf("at home, inside its hours, the held account's book answered %d", got)
	}
	if got := read(away, held); got != 403 {
		t.Errorf("an account held to home read its book from away: %d, want 403", got)
	}
	if got := read(away, free); got != 200 {
		t.Errorf("an account held to nothing reading from away answered %d", got)
	}
	later = 90 * time.Minute
	if got := read(atHome, held); got != 403 {
		t.Errorf("an account outside its viewing hours read its book: %d, want 403", got)
	}
	if got := read(atHome, free); got != 200 {
		t.Errorf("an account with no hours reading later answered %d", got)
	}
}
