package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func memberProfilesOf(t *testing.T, f administrationFixture, account, token string) identity.MemberProfiles {
	t.Helper()
	w := f.call("GET", "/v1/admin/access/members/"+account+"/profiles", token, "")
	if w.Code != 200 {
		t.Fatalf("read %s: %d %s", account, w.Code, w.Body)
	}
	var out identity.MemberProfiles
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	return out
}

// People's Manage reads and edits another person's profiles only where the
// caller's tier manages theirs: the owner manages administrators and members,
// an administrator members only, and nobody reaches the owner's this way.
func TestMemberProfilesFollowTheTierThatManagesThePerson(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`INSERT INTO direct_profiles(id,account_id,name,position,art) VALUES('member-kids','member','Kids',1,'mint')`); e != nil {
		t.Fatal(e)
	}
	page := memberProfilesOf(t, f, "member", f.admin)
	if page.AccountID != "member" || len(page.Items) != 2 || !page.Items[0].Primary || page.Items[1].ID != "member-kids" || page.Items[1].Restrictions.Revision < 1 {
		t.Fatalf("profiles: %+v", page)
	}
	for _, c := range []struct {
		name, account, token string
		want                 int
	}{
		{"an administrator reading a peer", "admin", f.admin, 403},
		{"an administrator reading the owner", "owner", f.admin, 403},
		{"the owner reading their own through People", "owner", f.owner, 403},
		{"a member", "member", f.member, tl6RefusedStatus(f.member)},
		{"nobody", "member", "", 401},
		{"an unknown account", "nobody", f.owner, 404},
	} {
		if w := f.call("GET", "/v1/admin/access/members/"+c.account+"/profiles", c.token, ""); w.Code != c.want {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	if w := f.call("GET", "/v1/admin/access/members/admin/profiles", f.owner, ""); w.Code != 200 {
		t.Fatalf("the owner reading an administrator: %d %s", w.Code, w.Body)
	}
	// The same refusals hold for every edit.
	kids := page.Items[1]
	rename := fmt.Sprintf(`{"expectedRevision":%d,"name":"Little ones"}`, kids.Revision)
	if w := f.call("PATCH", "/v1/admin/access/members/admin/profiles/admin-profile", f.admin, `{"expectedRevision":1,"name":"Peer"}`); w.Code != 403 {
		t.Fatalf("an administrator renamed a peer's profile: %d %s", w.Code, w.Body)
	}
	// A profile id from another account is not reachable through this person.
	if w := f.call("PATCH", "/v1/admin/access/members/member/profiles/admin-profile", f.owner, `{"expectedRevision":1,"name":"Moved"}`); w.Code != 404 {
		t.Fatalf("a profile was edited through the wrong account: %d %s", w.Code, w.Body)
	}
	var name string
	if e := f.deps.DB.QueryRow(`SELECT name FROM direct_profiles WHERE id='admin-profile'`).Scan(&name); e != nil || name == "Moved" || name == "Peer" {
		t.Fatalf("a refused edit changed the profile: %q %v", name, e)
	}
	w := f.call("PATCH", "/v1/admin/access/members/member/profiles/member-kids", f.admin, rename)
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	var renamed identity.MemberProfile
	if e := json.Unmarshal(w.Body.Bytes(), &renamed); e != nil || renamed.Name != "Little ones" || renamed.Revision != kids.Revision+1 {
		t.Fatalf("renamed: %v %+v", e, renamed)
	}
	// A replay of the same revision is a conflict, never a second write.
	if w = f.call("PATCH", "/v1/admin/access/members/member/profiles/member-kids", f.admin, rename); w.Code != 409 || !strings.Contains(w.Body.String(), "revision_mismatch") {
		t.Fatalf("stale rename: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{"expectedRevision":2}`, `{"expectedRevision":2,"name":"  "}`, `{"expectedRevision":2,"art":"plaid"}`, `{"expectedRevision":0,"name":"Zero"}`} {
		if w = f.call("PATCH", "/v1/admin/access/members/member/profiles/member-kids", f.admin, bad); w.Code != 400 {
			t.Fatalf("invalid edit %s: %d %s", bad, w.Code, w.Body)
		}
	}
}

func TestMemberProfileRestrictionsApplyLiveAndCarryRevisions(t *testing.T) {
	f := administrationFixtureNew(t)
	kids := memberProfilesOf(t, f, "member", f.owner).Items[0]
	body := fmt.Sprintf(`{"expectedRevision":%d,"ratingSystem":"mpaa","maximumAgeRating":"PG","allowUnrated":false,"blockedLabels":["Horror"],"allowDownloads":false,"allowLiveTv":true,"allowDvr":false,"allowWatchTogether":true}`, kids.Restrictions.Revision)
	if w := f.call("PUT", "/v1/admin/access/members/admin/profiles/admin-profile/restrictions", f.admin, body); w.Code != 403 {
		t.Fatalf("an administrator restricted a peer: %d %s", w.Code, w.Body)
	}
	w := f.call("PUT", "/v1/admin/access/members/member/profiles/"+kids.ID+"/restrictions", f.admin, body)
	if w.Code != 200 {
		t.Fatalf("restrict: %d %s", w.Code, w.Body)
	}
	var out identity.MemberProfile
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	r := out.Restrictions
	if r.MaximumAgeRating != "PG" || r.AllowUnrated || r.AllowDownloads || len(r.BlockedLabels) != 1 || r.Revision != kids.Restrictions.Revision+1 {
		t.Fatalf("restrictions: %+v", r)
	}
	// The person's own sessions read the same row: nobody is signed out, and
	// the restriction is what their next request sees.
	if _, e := f.deps.Identity.Authenticate(f.member); e != nil {
		t.Fatalf("a restriction signed the person out: %v", e)
	}
	content, e := f.deps.Identity.ViewerRestrictions(t.Context(), identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: kids.ID}})
	if e != nil || content.MaximumAge == nil || *content.MaximumAge != r.MaximumAge || !content.BlockUnrated || content.Revision != r.Revision {
		t.Fatalf("the restriction did not apply live: %v %+v", e, content)
	}
	if w = f.call("PUT", "/v1/admin/access/members/member/profiles/"+kids.ID+"/restrictions", f.admin, body); w.Code != 409 {
		t.Fatalf("a stale restriction write: %d %s", w.Code, w.Body)
	}
	if w = f.call("PUT", "/v1/admin/access/members/member/profiles/"+kids.ID+"/restrictions", f.admin, strings.Replace(body, `"PG"`, `"NOT-A-RATING"`, 1)); w.Code != 400 {
		t.Fatalf("an unknown rating: %d %s", w.Code, w.Body)
	}
}

func TestMemberProfilePINResetClearsThePINAndKeepsTheProfilesSessions(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, e := f.deps.DB.Exec(`UPDATE direct_profiles SET pin_hash=x'00' WHERE id='member-profile'`); e != nil {
		t.Fatal(e)
	}
	profile := memberProfilesOf(t, f, "member", f.owner).Items[0]
	if !profile.PINRequired {
		t.Fatal("setup: PIN not set")
	}
	reset := fmt.Sprintf(`{"expectedRevision":%d}`, profile.Revision)
	if w := f.call("POST", "/v1/admin/access/members/admin/profiles/admin-profile/pin-reset", f.admin, `{"expectedRevision":1}`); w.Code != 403 {
		t.Fatalf("an administrator reset a peer's PIN: %d %s", w.Code, w.Body)
	}
	w := f.call("POST", "/v1/admin/access/members/member/profiles/member-profile/pin-reset", f.admin, reset)
	if w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	var out identity.MemberProfile
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil || out.PINRequired || out.PINRevision != profile.PINRevision+1 || out.Revision != profile.Revision+1 {
		t.Fatalf("cleared: %v %+v", e, out)
	}
	// With no PIN left there is nothing to enter again: the profile's sessions
	// carry on, neither signed out nor gated.
	if _, e := f.deps.Identity.Authenticate(f.member); e != nil {
		t.Fatalf("clearing the PIN ended or gated a session on the profile: %v", e)
	}
	if w = f.call("POST", "/v1/admin/access/members/member/profiles/member-profile/pin-reset", f.admin, reset); w.Code != 409 {
		t.Fatalf("a replayed reset: %d %s", w.Code, w.Body)
	}
	// The caller's own sessions are untouched.
	if w = f.call("GET", "/v1/admin/access/members", f.admin, ""); w.Code != 200 {
		t.Fatalf("the administrator was signed out: %d", w.Code)
	}
}
