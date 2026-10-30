package httpapi

import (
	"encoding/json"
	"testing"
)

// Who can record is the owner's: an administrator managing a member can switch
// that member's Recordings off but never on, and only the owner uses the Who
// can record endpoint. The owners page and the profile switch agree.
func TestRecordingPermissionIsOwnerOnlyAcrossBothSwitches(t *testing.T) {
	f := administrationFixtureNew(t)
	restrictions := `{"expectedRevision":1,"allowUnrated":true,"allowDownloads":true,"allowLiveTv":true,"allowDvr":true,"allowWatchTogether":true,"blockedLabels":[]}`
	path := "/v1/admin/access/members/member/profiles/member-profile/restrictions"
	if w := f.call("PUT", path, f.admin, restrictions); w.Code != 403 {
		t.Fatalf("administrator switched a member's Recordings on: %d %s", w.Code, w.Body.String())
	}
	grant := `{"owner":{"authority":"local","accountId":"member","profileId":"member-profile"},"enabled":true,"revision":1}`
	for _, token := range []string{f.admin, f.member} {
		if w := f.call("PUT", "/v1/admin/dvr/recording-permissions", token, grant); w.Code != 403 && w.Code != 401 {
			t.Fatalf("Who can record accepted a non-owner: %d %s", w.Code, w.Body.String())
		}
	}
	w := f.call("PUT", "/v1/admin/dvr/recording-permissions", f.owner, grant)
	if w.Code != 200 {
		t.Fatalf("owner grant: %d %s", w.Code, w.Body.String())
	}
	w = f.call("GET", "/v1/admin/dvr/recording-owners", f.owner, "")
	var page struct {
		Result struct {
			Items []recordingOwnerChoice `json:"items"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err, w.Body.String())
	}
	states := map[string]recordingOwnerChoice{}
	for _, item := range page.Result.Items {
		states[item.Owner.ProfileID] = item
	}
	if !states["member-profile"].CanRecord || states["member-profile"].Revision != 2 || !states["owner-profile"].CanRecord || states["admin-profile"].CanRecord {
		t.Fatalf("owners page: %+v", states)
	}
	// The administrator may now switch it off again.
	off := `{"expectedRevision":2,"allowUnrated":true,"allowDownloads":true,"allowLiveTv":true,"allowDvr":false,"allowWatchTogether":true,"blockedLabels":[]}`
	if w = f.call("PUT", path, f.admin, off); w.Code != 200 {
		t.Fatalf("administrator switching Recordings off: %d %s", w.Code, w.Body.String())
	}
}
