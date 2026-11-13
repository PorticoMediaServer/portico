package api

import "testing"

func TestRecordingPermissionsUseAccountCeilingsAndProfileChoices(t *testing.T) {
	s, owner, members := seededWith(t, 20, 1)
	path := "/v1/admin/dvr/permissions"
	page := get[Page[DvrPermission]](t, s, owner, path)
	var own, member DvrPermission
	for _, row := range page.Items {
		if row.OwnerAccount {
			own = row
		} else {
			member = row
		}
	}
	if !own.CanRecord || !own.AccountAllowed || !own.ProfileAllowed || member.CanRecord || member.AccountAllowed || !member.ProfileAllowed {
		t.Fatalf("defaults: %+v", page.Items)
	}
	for _, method := range []string{"GET", "PATCH"} {
		if got := status(t, s, method, path, members[0], DvrPermissionChange{}); got != 403 {
			t.Fatalf("a member %s: %d", method, got)
		}
	}
	if got := status(t, s, "PATCH", path, owner, DvrPermissionChange{AccountID: own.AccountID, Enabled: false, Revision: own.AccountRevision}); got != 403 {
		t.Fatalf("the read-only owner ceiling: %d", got)
	}
	change := DvrPermissionChange{AccountID: member.AccountID, Enabled: true, Revision: member.AccountRevision}
	if got := status(t, s, "PATCH", path, owner, change); got != 200 {
		t.Fatalf("allowing the account: %d", got)
	}
	if got := status(t, s, "PATCH", path, owner, change); got != 412 {
		t.Fatalf("a stale permission save: %d", got)
	}
	if !get[DvrOverview](t, s, members[0], "/v1/dvr").CanRecord {
		t.Fatal("account permission did not reach recording authorization")
	}
	page = get[Page[DvrPermission]](t, s, owner, path)
	for _, row := range page.Items {
		if row.ProfileID == member.ProfileID {
			member = row
		}
	}
	change = DvrPermissionChange{AccountID: member.AccountID, ProfileID: member.ProfileID, Enabled: false, Revision: member.Revision}
	if got := status(t, s, "PATCH", path, owner, change); got != 200 {
		t.Fatalf("narrowing a profile: %d", got)
	}
	if get[DvrOverview](t, s, members[0], "/v1/dvr").CanRecord {
		t.Fatal("a profile lifted its account ceiling")
	}
	// The owner account is on by definition, but its profile can narrow that choice too.
	change = DvrPermissionChange{AccountID: own.AccountID, ProfileID: own.ProfileID, Enabled: false, Revision: own.Revision}
	if got := status(t, s, "PATCH", path, owner, change); got != 200 {
		t.Fatalf("narrowing an owner profile: %d", got)
	}
	if get[DvrOverview](t, s, owner, "/v1/dvr").CanRecord {
		t.Fatal("the owner profile's choice was ignored")
	}
}
