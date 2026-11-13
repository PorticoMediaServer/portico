package api

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"testing"
)

func TestTheOwnerCreatesARuleForAPermittedProfile(t *testing.T) {
	s, owner, members := seededWith(t, 30, 1)
	fake := fakeProvider(t, s.now())
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/playlist.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	var target DvrPermission
	for _, row := range get[Page[DvrPermission]](t, s, owner, "/v1/admin/dvr/permissions").Items {
		if !row.OwnerAccount {
			target = row
		}
	}
	programme := programmeID(t, s, owner, "Morning News")
	ask := DvrRuleSave{ProfileID: target.ProfileID, Programme: programme}
	if got := status(t, s, "POST", "/v1/dvr/rules", owner, ask); got != 403 {
		t.Fatalf("a profile without permission: %d", got)
	}
	status(t, s, "PATCH", "/v1/admin/dvr/permissions", owner, DvrPermissionChange{AccountID: target.AccountID, Enabled: true, Revision: target.AccountRevision})
	var preview DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules?preview=true", owner, ask, &preview); w.Code != 200 || preview.Preview == nil || !preview.Preview.NewOnlyAvailable || preview.ProfileID != target.ProfileID {
		t.Fatalf("target preview: %d %+v", w.Code, preview)
	}
	if len(get[Page[DvrRule]](t, s, owner, "/v1/dvr/rules?scope=everyone").Items) != 0 {
		t.Fatal("a preview saved a rule")
	}
	ask.Programme, ask.Paused = programmeID(t, s, owner, "Night Film"), new(true)
	var rule DvrRule
	if w := call(t, s, "POST", "/v1/dvr/rules", owner, ask, &rule); w.Code != 201 || rule.ProfileID != target.ProfileID || rule.ProfileName != target.ProfileName {
		t.Fatalf("target rule: %d %+v", w.Code, rule)
	}
	own := get[Page[DvrRule]](t, s, members[0], "/v1/dvr/rules")
	if len(own.Items) != 1 || own.Items[0].ID != rule.ID {
		t.Fatalf("target rules: %+v", own)
	}
	if got := status(t, s, "POST", "/v1/dvr/rules", members[0], ask); got != 403 {
		t.Fatalf("a member used the owner's target field: %d", got)
	}
	permissionPath := "/v1/admin/dvr/permissions"
	readTarget := func() DvrPermission {
		for _, row := range get[Page[DvrPermission]](t, s, owner, permissionPath).Items {
			if row.ProfileID == target.ProfileID {
				return row
			}
		}
		t.Fatal("target disappeared")
		return DvrPermission{}
	}
	setAllowed := func(enabled bool) {
		row := readTarget()
		if code := status(t, s, "PATCH", permissionPath, owner, DvrPermissionChange{AccountID: row.AccountID, ProfileID: row.ProfileID, Enabled: enabled, Revision: row.Revision}); code != 200 {
			t.Fatalf("changing recording permission: %d", code)
		}
	}
	setAllowed(false)
	if w := call(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID+"?preview=true", owner, DvrRuleSave{}, &preview); w.Code != 200 || preview.Preview == nil || preview.Preview.Matches != 0 {
		t.Fatalf("a revoked profile's preview: %d %+v", w.Code, preview)
	}
	status(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{Paused: new(false)})
	if rows := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=upcoming&scope=everyone"); len(rows.Items) != 0 {
		t.Fatalf("a rule booked without permission: %+v", rows.Items)
	}
	setAllowed(true)
	status(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{Paused: new(false)})
	if rows := get[Page[DvrRecording]](t, s, owner, "/v1/dvr/recordings?state=upcoming&scope=everyone"); len(rows.Items) != 1 || rows.Items[0].BookedBy != target.ProfileName {
		t.Fatalf("a permitted rule did not book for its profile: %+v", rows.Items)
	}
	if got := status(t, s, "PATCH", "/v1/dvr/rules/"+rule.ID, owner, DvrRuleSave{Paused: new(true)}); got != 200 {
		t.Fatalf("the owner paused another profile's rule: %d", got)
	}
	if got := status(t, s, "DELETE", "/v1/dvr/rules/"+rule.ID, owner, nil); got != 204 {
		t.Fatalf("the owner removed another profile's rule: %d", got)
	}
}
