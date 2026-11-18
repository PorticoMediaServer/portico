package api

import (
	"strings"
	"testing"
)

// The Top Shelf feed is the account's titles on a home screen, so it is held to the account's
// home-only rule as a sign-in is: away from home it says why and shows nothing, and at home it
// works again with the same token.
func TestTopShelfIsHeldToTheAccountsHomeRule(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	tv := signInAs(t, s, "max", testPassword, 2)
	var grant TopShelfToken
	if w := call(t, s, "POST", "/v1/topshelf/token", tv.AccessToken, TopShelfTokenRequest{InstallationID: device(2).InstallationID}, &grant); w.Code != 200 {
		t.Fatalf("the token answered %d: %s", w.Code, w.Body)
	}
	limits := noLimits()
	limits.HomeOnly = true
	hold(t, s, owner, max.ID, limits)
	if w := sent(t, s, away, "GET", "/v1/topshelf", grant.Token, nil, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Errorf("a home-only account's shelf away from home answered %d %s, want 403 away_from_home", w.Code, w.Body)
	}
	if w := sent(t, s, atHome, "GET", "/v1/topshelf", grant.Token, nil, nil); w.Code != 200 {
		t.Errorf("a home-only account's shelf at home answered %d %s, want 200", w.Code, w.Body)
	}
}
