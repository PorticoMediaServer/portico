package api

import (
	"strings"
	"testing"
)

// The main profile sets a profile's limits and the server applies them at once: a rating
// limit hides titles, unrated ones too until Allow unrated titles is on. The limited profile
// can read its own limits and change nothing.
func TestProfileLimits(t *testing.T) {
	s, _, _ := seeded(t)
	in := signIn(t, s, 1)
	main, mainProfile := onMain(t, s, in), in.Profiles[0].ID
	var kid Profile
	if w := call(t, s, "POST", "/v1/profiles", main.AccessToken, ProfileCreate{Name: "Kid"}, &kid); w.Code != 200 {
		t.Fatalf("adding a profile answered %d: %s", w.Code, w.Body)
	}
	var onKid Session
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 2).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the profile answered %d: %s", w.Code, w.Body)
	}
	films := get[Libraries](t, s, main.AccessToken, "/v1/libraries").Items[0].ID
	count := func() int {
		t.Helper()
		return *get[ItemPage](t, s, onKid.AccessToken, "/v1/items?library="+films+"&kind=movie&limit=1&total=true").Total
	}
	path := "/v1/profiles/" + kid.ID + "/restrictions"
	save := func(edit ProfileRestrictionsEdit) ProfileRestrictions {
		t.Helper()
		var out ProfileRestrictions
		if w := call(t, s, "PUT", path, main.AccessToken, edit, &out); w.Code != 200 {
			t.Fatalf("saving limits answered %d: %s", w.Code, w.Body)
		}
		return out
	}

	all := count()
	age := 13
	if got := save(ProfileRestrictionsEdit{MaxRatingAge: &age, AllLibraries: true}); got.MaxRatingAge == nil || *got.MaxRatingAge != 13 || got.AllowUnrated {
		t.Fatalf("the saved limits read back as %+v", got)
	}
	strict := count()
	save(ProfileRestrictionsEdit{MaxRatingAge: &age, AllowUnrated: true, AllLibraries: true})
	unrated := count()
	if strict >= unrated || unrated >= all {
		t.Fatalf("films shown: %d under a limit, %d with unrated titles allowed, %d with no limit", strict, unrated, all)
	}
	if now := get[Profile](t, s, onKid.AccessToken, "/v1/profiles/"+kid.ID); now.AccessMarker == kid.AccessMarker {
		t.Error("the profile's access marker did not move, so its apps would keep their cached lists")
	}

	label := get[ProfileRestrictions](t, s, onKid.AccessToken, path)
	if !label.AllowUnrated || !label.AccountAllLibraries {
		t.Fatalf("the profile reads its own limits as %+v", label)
	}
	save(ProfileRestrictionsEdit{AllLibraries: false, Libraries: []string{}})
	if left := count(); left != 0 {
		t.Errorf("a profile given no libraries is still shown %d films", left)
	}

	for name, c := range map[string]struct {
		token, method, path string
		body                any
		want                int
	}{
		"the limited profile changes its limits":  {onKid.AccessToken, "PUT", path, ProfileRestrictionsEdit{AllLibraries: true}, 403},
		"it reads the main profile's limits":      {onKid.AccessToken, "GET", "/v1/profiles/" + mainProfile + "/restrictions", nil, 403},
		"neither allLibraries nor a list is sent": {main.AccessToken, "PUT", path, map[string]any{"maxRatingAge": 13}, 400},
		"a rating limit out of range":             {main.AccessToken, "PUT", path, map[string]any{"maxRatingAge": 40, "allLibraries": true}, 400},
		"a label that does not exist":             {main.AccessToken, "PUT", path, ProfileRestrictionsEdit{AllLibraries: true, BlockedLabels: []string{"tag_nope"}}, 400},
		"a library that does not exist":           {main.AccessToken, "PUT", path, ProfileRestrictionsEdit{Libraries: []string{"lib_nope"}}, 400},
		"a profile that does not exist":           {main.AccessToken, "GET", "/v1/profiles/prf_nope/restrictions", nil, 404},
	} {
		if code := status(t, s, c.method, c.path, c.token, c.body); code != c.want {
			t.Errorf("%s: answered %d, want %d", name, code, c.want)
		}
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
