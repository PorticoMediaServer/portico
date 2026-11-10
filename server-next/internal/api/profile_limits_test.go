package api

import (
	"strconv"
	"strings"
	"testing"
)

// Under a rating limit a title with no rating is hidden until the main profile turns
// Allow unrated titles on, and then the profile is shown more, but not everything.
func TestAllowUnratedTitles(t *testing.T) {
	s, _, _ := seeded(t)
	main := onMain(t, s, signIn(t, s, 1))
	var kid Profile
	if w := call(t, s, "POST", "/v1/profiles", main.AccessToken, ProfileCreate{Name: "Kid"}, &kid); w.Code != 200 {
		t.Fatalf("adding a profile answered %d: %s", w.Code, w.Body)
	}
	var onKid Session
	kidSignIn := signIn(t, s, 2)
	refresh := kidSignIn.Session.RefreshToken
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", kidSignIn.Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the profile answered %d: %s", w.Code, w.Body)
	}
	films := get[Libraries](t, s, main.AccessToken, "/v1/libraries").Items[0].ID
	count := func() int {
		t.Helper()
		return *get[ItemPage](t, s, onKid.AccessToken, "/v1/items?library="+films+"&kind=movie&limit=1&total=true").Total
	}
	saves := 0
	save := func(edit Restrictions) Restrictions {
		t.Helper()
		saves++
		var out Restrictions
		if w := call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", main.AccessToken, edit, &out); w.Code != 200 {
			t.Fatalf("saving limits answered %d: %s", w.Code, w.Body)
		}
		// A change of limits makes the profile's apps take a new token.
		code, next := renew(t, s, refresh, 2, "limits-"+strconv.Itoa(saves))
		if code != 200 {
			t.Fatalf("renewing after a change of limits answered %d", code)
		}
		onKid, refresh = next, next.RefreshToken
		return out
	}
	all := count()
	age := 13
	if got := save(Restrictions{MaxRatingAge: &age, AllLibraries: true}); got.AllowUnrated {
		t.Fatalf("Allow unrated titles is on without being asked for: %+v", got)
	}
	strict := count()
	if got := save(Restrictions{MaxRatingAge: &age, AllowUnrated: true, AllLibraries: true}); !got.AllowUnrated {
		t.Fatalf("Allow unrated titles did not read back: %+v", got)
	}
	if unrated := count(); strict >= unrated || unrated >= all {
		t.Fatalf("films shown: %d under a limit, %d with unrated titles allowed, %d with no limit", strict, unrated, all)
	}
}

// Profiles are shown in the order the main profile chose, an account stops at the number of
// profiles the owner allows it, and deleting says first what would go.
func TestProfileOrderCapAndDeletionPreview(t *testing.T) {
	s := newTestServer(t)
	in := setup(t, s)
	owner, mainProfile := onMain(t, s, in), in.Profiles[0].ID
	add := func(token, name string) (Profile, int) {
		t.Helper()
		var p Profile
		w := call(t, s, "POST", "/v1/profiles", token, ProfileCreate{Name: name}, nil)
		if w.Code == 200 {
			jsonInto(w, &p)
		}
		return p, w.Code
	}
	a, _ := add(owner.AccessToken, "A")
	b, _ := add(owner.AccessToken, "B")
	var listed Page[Profile]
	if w := call(t, s, "PUT", "/v1/profiles/order", owner.AccessToken, ProfileOrder{ProfileIDs: []string{b.ID, mainProfile, a.ID}}, &listed); w.Code != 200 {
		t.Fatalf("ordering answered %d: %s", w.Code, w.Body)
	}
	if len(listed.Items) != 3 || !listed.Items[0].IsPrimary || listed.Items[1].ID != b.ID || listed.Items[2].ID != a.ID {
		t.Fatalf("the profiles are listed as %+v", listed.Items)
	}
	for name, ids := range map[string][]string{"one left out": {a.ID, b.ID}, "one twice": {a.ID, a.ID, b.ID}, "a stranger": {a.ID, b.ID, "prf_nope"}} {
		if code := status(t, s, "PUT", "/v1/profiles/order", owner.AccessToken, ProfileOrder{ProfileIDs: ids}); code != 400 {
			t.Errorf("an order with %s answered %d, want 400", name, code)
		}
	}

	ada := addMember(t, s, owner.AccessToken, "ada", "admin")
	max := addMember(t, s, owner.AccessToken, "max", "member")
	two := 2
	if code := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, signInAs(t, s, "ada", testPassword, 1).AccessToken, MemberEdit{MaxProfiles: &two}); code != 403 {
		t.Errorf("an admin setting how many profiles a member may have answered %d, want 403", code)
	}
	var edited Member
	if w := call(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner.AccessToken, MemberEdit{MaxProfiles: &two}, &edited); w.Code != 200 || edited.MaxProfiles != 2 || ada.MaxProfiles != 8 {
		t.Fatalf("lowering a member's profiles answered %d %+v", w.Code, edited)
	}
	member := signInAs(t, s, "max", testPassword, 2)
	if _, code := add(member.AccessToken, "Second"); code != 200 {
		t.Fatalf("a second profile of two answered %d", code)
	}
	if w := call(t, s, "POST", "/v1/profiles", member.AccessToken, ProfileCreate{Name: "Third"}, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "This account already has 2 profiles") {
		t.Errorf("a third profile of two answered %d: %s", w.Code, w.Body)
	}

	if got := get[DeletionPreview](t, s, owner.AccessToken, "/v1/profiles/"+a.ID+"/deletion-preview"); got.Profiles != 1 {
		t.Errorf("the profile's deletion preview is %+v", got)
	}
	if got := get[DeletionPreview](t, s, owner.AccessToken, "/v1/admin/members/"+max.ID+"/deletion-preview"); got.Profiles != 2 || got.SignIns != 1 || got.Devices != 1 {
		t.Errorf("the member's deletion preview is %+v", got)
	}
	if code := status(t, s, "GET", "/v1/admin/members/"+ada.ID+"/deletion-preview", member.AccessToken, nil); code != 403 {
		t.Errorf("a member asking what deleting an admin removes answered %d, want 403", code)
	}
	if left := get[Page[Profile]](t, s, owner.AccessToken, "/v1/profiles"); len(left.Items) != 3 {
		t.Errorf("asking what a deletion removes deleted something: %d profiles left", len(left.Items))
	}
}
