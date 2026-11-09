package api

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// signInAs signs a person in on device n and puts the session on their main profile.
func signInAs(t *testing.T, s *Server, username, password string, n int) Session {
	t.Helper()
	var in SignInResponse
	if w := call(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: username, Password: password, Device: device(n)}, &in); w.Code != 200 {
		t.Fatalf("%s could not sign in: %d %s", username, w.Code, w.Body)
	}
	var chosen Session
	if w := call(t, s, "POST", "/v1/profiles/"+in.Profiles[0].ID+"/select", in.Session.AccessToken, ProfileSelect{}, &chosen); w.Code != 200 {
		t.Fatalf("%s could not choose a profile: %d %s", username, w.Code, w.Body)
	}
	chosen.RefreshToken = in.Session.RefreshToken
	return chosen
}

func addMember(t *testing.T, s *Server, token, username, role string) Member {
	t.Helper()
	var m Member
	if w := call(t, s, "POST", "/v1/admin/members", token, MemberCreate{Username: username, Password: testPassword, Role: role}, &m); w.Code != 200 {
		t.Fatalf("creating %s answered %d: %s", username, w.Code, w.Body)
	}
	return m
}

// The owner manages admins and members, an admin manages members, and nobody else manages
// anyone. Disabling ends the person's sessions at once; a demotion takes hold at once.
func TestWhoMayManageWhom(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	ada := addMember(t, s, owner.AccessToken, "ada", "admin")
	max := addMember(t, s, owner.AccessToken, "max", "member")
	if code := status(t, s, "POST", "/v1/admin/members", owner.AccessToken, MemberCreate{Username: "MAX", Password: testPassword}); code != 409 {
		t.Errorf("a taken username answered %d, want 409", code)
	}
	admin := signInAs(t, s, "ada", testPassword, 1)
	member := signInAs(t, s, "max", testPassword, 2)

	var list Page[Member]
	if w := call(t, s, "GET", "/v1/admin/members", admin.AccessToken, nil, &list); w.Code != 200 || len(list.Items) != 3 || list.Items[0].Role != "owner" {
		t.Fatalf("an admin's member list answered %d %+v", w.Code, list)
	}
	ownerID := list.Items[0].ID
	yes, no, adminRole := true, false, "admin"
	for name, c := range map[string]struct {
		token, method, path string
		body                any
		want                int
	}{
		"a member lists people":         {member.AccessToken, "GET", "/v1/admin/members", nil, 403},
		"a member disables an admin":    {member.AccessToken, "PATCH", "/v1/admin/members/" + ada.ID, MemberEdit{Disabled: &yes}, 403},
		"an admin disables the owner":   {admin.AccessToken, "PATCH", "/v1/admin/members/" + ownerID, MemberEdit{Disabled: &yes}, 403},
		"an admin removes the owner":    {admin.AccessToken, "DELETE", "/v1/admin/members/" + ownerID, nil, 403},
		"an admin edits themself":       {admin.AccessToken, "PATCH", "/v1/admin/members/" + ada.ID, MemberEdit{Disabled: &no}, 403},
		"an admin makes an admin":       {admin.AccessToken, "PATCH", "/v1/admin/members/" + max.ID, MemberEdit{Role: &adminRole}, 403},
		"an admin creates an admin":     {admin.AccessToken, "POST", "/v1/admin/members", MemberCreate{Username: "eve", Password: testPassword, Role: "admin"}, 403},
		"an admin resets the owner":     {admin.AccessToken, "POST", "/v1/admin/members/" + ownerID + "/password-reset", PasswordResetCreate{}, 403},
		"the owner disables themself":   {owner.AccessToken, "PATCH", "/v1/admin/members/" + ownerID, MemberEdit{Disabled: &yes}, 403},
		"a member reads another's list": {member.AccessToken, "GET", "/v1/profiles?member=" + ada.ID, nil, 403},
		"an admin edits owner profiles": {admin.AccessToken, "POST", "/v1/profiles?member=" + ownerID, ProfileCreate{Name: "Mine"}, 403},
		"an unknown member":             {owner.AccessToken, "PATCH", "/v1/admin/members/usr_nobody", MemberEdit{Disabled: &yes}, 404},
		"an unknown library":            {owner.AccessToken, "PATCH", "/v1/admin/members/" + max.ID, MemberEdit{Libraries: &[]string{"lib_nothing"}}, 400},
	} {
		if code := status(t, s, c.method, c.path, c.token, c.body); code != c.want {
			t.Errorf("%s: answered %d, want %d", name, code, c.want)
		}
	}

	// An admin manages a member's profiles through the ordinary profile routes.
	var kid Profile
	if w := call(t, s, "POST", "/v1/profiles?member="+max.ID, admin.AccessToken, ProfileCreate{Name: "Kid"}, &kid); w.Code != 200 {
		t.Fatalf("an admin creating a member's profile answered %d: %s", w.Code, w.Body)
	}
	var profiles Page[Profile]
	call(t, s, "GET", "/v1/profiles", member.AccessToken, nil, &profiles)
	if len(profiles.Items) != 2 {
		t.Errorf("the member sees %d profiles, want 2", len(profiles.Items))
	}

	// Disabling ends the member's sessions in the same commit, and they cannot sign in.
	if code := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, admin.AccessToken, MemberEdit{Disabled: &yes}); code != 200 {
		t.Fatalf("disabling answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", member.AccessToken, nil); code != 401 {
		t.Errorf("a disabled member's token answered %d, want 401", code)
	}
	if code, _ := renew(t, s, member.RefreshToken, 2, "r"); code != 401 {
		t.Errorf("a disabled member's renewal answered %d, want 401", code)
	}
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(2)}); code != 401 {
		t.Errorf("a disabled member's sign-in answered %d, want 401", code)
	}
	status(t, s, "PATCH", "/v1/admin/members/"+max.ID, admin.AccessToken, MemberEdit{Disabled: &no})
	member = signInAs(t, s, "max", testPassword, 2)

	// A demoted admin's token stops at once; the renewed one is a member's.
	memberRole := "member"
	if code := status(t, s, "PATCH", "/v1/admin/members/"+ada.ID, owner.AccessToken, MemberEdit{Role: &memberRole}); code != 200 {
		t.Fatalf("demoting answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/admin/members", admin.AccessToken, nil); code != 401 {
		t.Errorf("a demoted admin's old token answered %d, want 401 so the app renews", code)
	}
	code, renewed := renew(t, s, admin.RefreshToken, 1, "r")
	if code != 200 || renewed.Viewer.Role != "member" || status(t, s, "GET", "/v1/admin/members", renewed.AccessToken, nil) != 403 {
		t.Errorf("after demotion the renewed session is %d %+v", code, renewed.Viewer)
	}

	// Removing a member ends their sessions and their account; doing it twice is fine.
	for range 2 {
		if code := status(t, s, "DELETE", "/v1/admin/members/"+max.ID, owner.AccessToken, nil); code != 204 {
			t.Fatalf("removing answered %d", code)
		}
	}
	if code := status(t, s, "GET", "/v1/me", member.AccessToken, nil); code != 401 {
		t.Errorf("a removed member's token answered %d, want 401", code)
	}
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(2)}); code != 401 {
		t.Errorf("a removed member's sign-in answered %d, want 401", code)
	}
}

// An invitation makes exactly one account, however many people try it at once, with the
// role it carries. A revoked or used one is refused like a code nobody made.
func TestInvitations(t *testing.T) {
	s := newTestServer(t)
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	s.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	// The racers that lose count as wrong codes from one address; step past the wait they earn.
	wait := func() { clock.Add((2 * time.Minute).Milliseconds()) }
	owner := onMainProfile(t, s)
	admin := func() Session {
		addMember(t, s, owner.AccessToken, "ada", "admin")
		return signInAs(t, s, "ada", testPassword, 1)
	}()
	invite := func(token string, body InvitationCreate) (int, InvitationCreated) {
		var out InvitationCreated
		w := call(t, s, "POST", "/v1/admin/invitations", token, body, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, out
	}
	if code, _ := invite(admin.AccessToken, InvitationCreate{Role: "admin"}); code != 403 {
		t.Errorf("an admin inviting an admin answered %d, want 403", code)
	}
	if code, _ := invite(owner.AccessToken, InvitationCreate{Libraries: []string{"lib_nothing"}}); code != 400 {
		t.Errorf("an invitation to an unknown library answered %d, want 400", code)
	}
	some := false
	_, made := invite(admin.AccessToken, InvitationCreate{AllLibraries: &some, Note: "Max from work"})
	if made.Code == "" || made.Invitation.State != "open" || made.Invitation.CreatedBy != "ada" {
		t.Fatalf("unexpected invitation: %+v", made)
	}
	var preview InvitationPreview
	if w := call(t, s, "GET", "/v1/auth/invitations/"+made.Code, "", nil, &preview); w.Code != 200 || preview.Role != "member" || preview.ServerName != "Portico" {
		t.Fatalf("preview answered %d %+v", w.Code, preview)
	}

	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			body := InvitationAccept{Username: fmt.Sprintf("guest%d", i), Password: testPassword, Device: device(10 + i)}
			var in SignInResponse
			if w := call(t, s, "POST", "/v1/auth/invitations/"+made.Code+"/accept", "", body, nil); w.Code == 200 {
				jsonInto(w, &in)
				if in.Account.Role == "member" && status(t, s, "GET", "/v1/me", in.Session.AccessToken, nil) == 200 {
					accepted.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("one invitation made %d accounts; want exactly 1", accepted.Load())
	}
	wait()
	var members Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner.AccessToken, nil, &members)
	if len(members.Items) != 3 || members.Items[2].AllLibraries {
		t.Errorf("after one acceptance there are %d accounts (the new one sees all libraries: %v)", len(members.Items), members.Items[len(members.Items)-1].AllLibraries)
	}

	// A username that is taken leaves the invitation open for another try.
	_, second := invite(owner.AccessToken, InvitationCreate{Role: "admin"})
	if code := status(t, s, "POST", "/v1/auth/invitations/"+second.Code+"/accept", "", InvitationAccept{Username: "ada", Password: testPassword, Device: device(30)}); code != 409 {
		t.Errorf("accepting with a taken username answered %d, want 409", code)
	}
	if code := status(t, s, "GET", "/v1/auth/invitations/"+second.Code, "", nil); code != 200 {
		t.Errorf("after a taken username the invitation previews as %d, want 200", code)
	}
	if code := status(t, s, "DELETE", "/v1/admin/invitations/"+second.Invitation.ID, owner.AccessToken, nil); code != 204 {
		t.Fatalf("revoke answered %d", code)
	}
	for name, code := range map[string]string{"revoked": second.Code, "used": made.Code, "never made": "AAAA-AAAA-AAAA-AAAA"} {
		wait()
		body := InvitationAccept{Username: "late", Password: testPassword, Device: device(31)}
		if got := status(t, s, "POST", "/v1/auth/invitations/"+code+"/accept", "", body); got != 404 {
			t.Errorf("accepting a %s invitation answered %d, want 404", name, got)
		}
	}
}

// A reset code from an admin lets a member choose a new password once. That signs them out
// everywhere. A wrong code and an unknown username are answered the same.
func TestPasswordResetByCode(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	max := addMember(t, s, owner.AccessToken, "max", "member")
	member := signInAs(t, s, "max", testPassword, 2)

	var reset PasswordResetCode
	if w := call(t, s, "POST", "/v1/admin/members/"+max.ID+"/password-reset", owner.AccessToken, PasswordResetCreate{}, &reset); w.Code != 200 {
		t.Fatalf("issuing a reset code answered %d: %s", w.Code, w.Body)
	}
	if status(t, s, "GET", "/v1/me", member.AccessToken, nil) != 200 {
		t.Fatal("issuing a reset code signed the member out before it was used")
	}
	redeem := func(username, code string) (int, string) {
		w := call(t, s, "POST", "/v1/auth/password-reset/confirm", "", PasswordResetRequest{Username: username, Code: code, NewPassword: "a brand new one"}, nil)
		var body errorBody
		jsonInto(w, &body)
		return w.Code, body.Error.Code + ": " + body.Error.Message
	}
	wrongCode, wrong := redeem("max", "AAAA-AAAA-AAAA")
	unknownCode, unknown := redeem("nobody-here", reset.Code)
	if wrongCode != 401 || unknownCode != 401 || wrong != unknown {
		t.Fatalf("a wrong code (%d %s) and an unknown username (%d %s) were answered differently", wrongCode, wrong, unknownCode, unknown)
	}
	if code, _ := redeem("Max", reset.Code); code != 204 {
		t.Fatalf("the right code answered %d, want 204", code)
	}
	if code, _ := redeem("max", reset.Code); code != 401 {
		t.Errorf("a used reset code answered %d, want 401", code)
	}
	if code := status(t, s, "GET", "/v1/me", member.AccessToken, nil); code != 401 {
		t.Errorf("the member's old session answered %d after the reset, want 401", code)
	}
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "max", Password: testPassword, Device: device(2)}); code != 401 {
		t.Errorf("the old password answered %d after the reset, want 401", code)
	}
	signInAs(t, s, "max", "a brand new one", 2)
}

// A key does what its scopes and its account allow and no more, cannot make keys or act as
// a signed-in person, survives a password change, and stops the moment it is revoked.
func TestKeysForTools(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "max", "member")
	member := signInAs(t, s, "max", testPassword, 2)
	create := func(token, path string, body KeyCreate) (int, KeyCreated) {
		var out KeyCreated
		w := call(t, s, "POST", path, token, body, nil)
		if w.Code == 200 {
			jsonInto(w, &out)
		}
		return w.Code, out
	}
	_, reader := create(owner.AccessToken, "/v1/admin/api-keys", KeyCreate{Name: "Home Assistant", Scopes: []string{"read"}})
	_, admin := create(owner.AccessToken, "/v1/admin/api-keys", KeyCreate{Name: "Console script", Scopes: []string{"admin"}})
	_, mine := create(member.AccessToken, "/v1/me/tokens", KeyCreate{Name: "My script", Scopes: []string{"admin"}})
	_, appPassword := create(member.AccessToken, "/v1/me/app-passwords", KeyCreate{Name: "Old TV app"})
	if reader.Secret == "" || admin.Secret == "" || mine.Secret == "" || appPassword.Secret == "" {
		t.Fatal("a key was not created")
	}
	for name, c := range map[string]struct {
		token, method, path string
		body                any
		want                int
	}{
		"a read key reads who it is":            {reader.Secret, "GET", "/v1/me", nil, 200},
		"a read key lists people":               {reader.Secret, "GET", "/v1/admin/members", nil, 403},
		"an admin key lists people":             {admin.Secret, "GET", "/v1/admin/members", nil, 200},
		"an admin key makes a key":              {admin.Secret, "POST", "/v1/admin/api-keys", KeyCreate{Name: "More", Scopes: []string{"admin"}}, 403},
		"an admin key changes the password":     {admin.Secret, "POST", "/v1/auth/password", PasswordRequest{CurrentPassword: testPassword, NewPassword: "another one"}, 403},
		"an admin key lists sessions":           {admin.Secret, "GET", "/v1/me/sessions", nil, 403},
		"a member's admin token lists people":   {mine.Secret, "GET", "/v1/admin/members", nil, 403},
		"a member's token reads who it is":      {mine.Secret, "GET", "/v1/me", nil, 200},
		"an app password is not a bearer token": {appPassword.Secret, "GET", "/v1/me", nil, 401},
		"a made-up key":                         {"pk_madeup", "GET", "/v1/me", nil, 401},
		"a member makes a server key":           {member.AccessToken, "POST", "/v1/admin/api-keys", KeyCreate{Name: "No", Scopes: []string{"read"}}, 403},
		"a key with an unknown scope":           {owner.AccessToken, "POST", "/v1/admin/api-keys", KeyCreate{Name: "No", Scopes: []string{"everything"}}, 400},
	} {
		if code := status(t, s, c.method, c.path, c.token, c.body); code != c.want {
			t.Errorf("%s: answered %d, want %d", name, code, c.want)
		}
	}
	// A password change ends sessions, not keys.
	if code := status(t, s, "POST", "/v1/auth/password", member.AccessToken, PasswordRequest{CurrentPassword: testPassword, NewPassword: "another one"}); code != 204 {
		t.Fatalf("password change answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", mine.Secret, nil); code != 200 {
		t.Errorf("a personal token answered %d after a password change, want 200", code)
	}
	var list Page[Key]
	if w := call(t, s, "GET", "/v1/admin/api-keys", owner.AccessToken, nil, &list); w.Code != 200 || len(list.Items) != 2 {
		t.Fatalf("the key list answered %d with %d keys", w.Code, len(list.Items))
	}
	if code := status(t, s, "DELETE", "/v1/admin/api-keys/"+reader.Key.ID, owner.AccessToken, nil); code != 204 {
		t.Fatalf("revoke answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/me", reader.Secret, nil); code != 401 {
		t.Errorf("a revoked key answered %d, want 401", code)
	}
}
