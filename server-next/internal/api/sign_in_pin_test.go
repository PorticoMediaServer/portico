package api

import (
	"strings"
	"testing"
)

func TestRememberedOwnerPINCannotSwitchSignIn(t *testing.T) {
	for _, startOver := range []bool{false, true} {
		t.Run(map[bool]string{false: "switch", true: "start over"}[startOver], func(t *testing.T) {
			s, owner, _ := porticoServer(t)
			profiles := get[Page[Profile]](t, s, owner, "/v1/profiles").Items
			path := "/v1/profiles/" + profiles[0].ID
			if w := call(t, s, "PUT", path+"/pin", owner, ProfilePIN{PIN: "1234"}, nil); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var typed, kept Session
			call(t, s, "POST", path+"/select", owner, ProfileSelect{PIN: "1234", RememberPIN: true}, &typed)
			if typed.PINToken == "" {
				t.Fatal("PIN was not remembered")
			}
			if w := call(t, s, "POST", path+"/select", typed.AccessToken, ProfileSelect{PINToken: typed.PINToken}, &kept); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for _, action := range []string{"/v1/admin/sign-in/preview", "/v1/admin/sign-in/finish", "/v1/admin/sign-in/move-codes", "/v1/admin/members/usr_absent/move-code"} {
				if code := status(t, s, "POST", action, kept.AccessToken, map[string]any{}); code != 403 {
					t.Errorf("remembered PIN reached %s: HTTP %d", action, code)
				}
			}
			name := s.serverName()
			req := SignInSwitchRequest{To: "local", StartOver: startOver, ServerName: name, Username: "replacement", Password: testPassword, Device: device(1)}
			w := call(t, s, "POST", "/v1/admin/sign-in/switch", kept.AccessToken, req, nil)
			if w.Code != 403 || !strings.Contains(w.Body.String(), "profile_pin_required") {
				t.Fatalf("remembered PIN changed sign-in: HTTP %d", w.Code)
			}
			if got := get[Page[Member]](t, s, kept.AccessToken, "/v1/admin/members"); len(got.Items) != 3 {
				t.Fatalf("refused switch removed accounts: %d", len(got.Items))
			}
			var fresh Session
			if code := call(t, s, "POST", path+"/select", kept.AccessToken, ProfileSelect{PIN: "1234"}, &fresh).Code; code != 200 {
				t.Fatal(code)
			}
			if code := status(t, s, "POST", "/v1/admin/sign-in/switch", fresh.AccessToken, req); code != 200 {
				t.Errorf("typed PIN refused: HTTP %d", code)
			}
		})
	}
}
