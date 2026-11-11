package api

import "testing"

// The owner or an admin sees a member's devices and signs one out: that device's sign-in
// stops at once and the member's other device carries on. Nobody reaches the devices of
// someone they do not manage. The member list says when each account was last active and
// names its profiles.
func TestMemberDevicesAndListFields(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	ada := addMember(t, s, owner.AccessToken, "ada", "admin")
	max := addMember(t, s, owner.AccessToken, "max", "member")
	eve := addMember(t, s, owner.AccessToken, "eve", "member")
	admin := signInAs(t, s, "ada", testPassword, 1)
	phone := signInAs(t, s, "max", testPassword, 2)
	tv := signInAs(t, s, "max", testPassword, 3)
	other := signInAs(t, s, "eve", testPassword, 4)
	if code := status(t, s, "POST", "/v1/profiles", phone.AccessToken, ProfileCreate{Name: "Kid"}); code != 200 {
		t.Fatalf("adding a profile answered %d", code)
	}

	var list Page[Member]
	if w := call(t, s, "GET", "/v1/admin/members", owner.AccessToken, nil, &list); w.Code != 200 {
		t.Fatalf("the member list answered %d %s", w.Code, w.Body)
	}
	var ownerID string
	for _, m := range list.Items {
		switch m.Username {
		case "max":
			if m.LastActiveAt == nil || len(m.Profiles) != 2 || m.Profiles[0] != "max" || m.Profiles[1] != "Kid" {
				t.Errorf("max is listed with lastActiveAt %v and profiles %v", m.LastActiveAt, m.Profiles)
			}
		case "ada", "eve":
		default:
			ownerID = m.ID
		}
	}
	var never Member
	sam := addMember(t, s, owner.AccessToken, "sam", "member")
	if call(t, s, "GET", "/v1/admin/members/"+sam.ID, owner.AccessToken, nil, &never); never.LastActiveAt != nil || len(never.Profiles) != 1 {
		t.Errorf("an account that never signed in shows lastActiveAt %v and profiles %v", never.LastActiveAt, never.Profiles)
	}

	devices := func(token, member string) Page[Device] {
		t.Helper()
		var out Page[Device]
		if w := call(t, s, "GET", "/v1/admin/members/"+member+"/devices", token, nil, &out); w.Code != 200 {
			t.Fatalf("a member's devices answered %d %s", w.Code, w.Body)
		}
		return out
	}
	seen := devices(admin.AccessToken, max.ID)
	if len(seen.Items) != 2 || !seen.Items[0].SignedIn || seen.Items[0].Current {
		t.Fatalf("max's devices are %+v, want two signed in and none marked as the asker's", seen.Items)
	}
	for name, c := range map[string]struct {
		token, method, path string
		want                int
	}{
		"a member lists a member's devices":    {phone.AccessToken, "GET", "/v1/admin/members/" + eve.ID + "/devices", 403},
		"a member signs a device out":          {phone.AccessToken, "DELETE", "/v1/admin/members/" + eve.ID + "/devices/" + seen.Items[0].ID, 403},
		"an admin lists the owner's devices":   {admin.AccessToken, "GET", "/v1/admin/members/" + ownerID + "/devices", 403},
		"an admin signs the owner's out":       {admin.AccessToken, "DELETE", "/v1/admin/members/" + ownerID + "/devices/" + seen.Items[0].ID, 403},
		"an admin lists their own this way":    {admin.AccessToken, "GET", "/v1/admin/members/" + ada.ID + "/devices", 403},
		"devices of nobody":                    {owner.AccessToken, "GET", "/v1/admin/members/usr_nobody/devices", 404},
		"someone else's device under a member": {owner.AccessToken, "DELETE", "/v1/admin/members/" + max.ID + "/devices/" + devices(owner.AccessToken, eve.ID).Items[0].ID, 204},
	} {
		if code := status(t, s, c.method, c.path, c.token, nil); code != c.want {
			t.Errorf("%s: answered %d, want %d", name, code, c.want)
		}
	}
	if code := status(t, s, "GET", "/v1/me", other.AccessToken, nil); code != 200 {
		t.Errorf("eve's device, named under max, was signed out: %d", code)
	}

	if code := status(t, s, "DELETE", "/v1/admin/members/"+max.ID+"/devices/"+seen.Items[0].ID, admin.AccessToken, nil); code != 204 {
		t.Fatalf("signing a member's device out answered %d", code)
	}
	// Whichever of the two devices it was, that one is out and the other carries on.
	onTV, onPhone := status(t, s, "GET", "/v1/me", tv.AccessToken, nil), status(t, s, "GET", "/v1/me", phone.AccessToken, nil)
	if !(onTV == 401 && onPhone == 200) && !(onTV == 200 && onPhone == 401) {
		t.Errorf("after one device was signed out the two answer %d and %d, want one 401 and one 200", onTV, onPhone)
	}
	if left := devices(owner.AccessToken, max.ID); len(left.Items) != 1 || left.Items[0].ID == seen.Items[0].ID {
		t.Errorf("after the sign-out max's devices are %+v, want the other one alone", left.Items)
	}
}
