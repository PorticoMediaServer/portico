package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sendPicture(s *Server, token, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	return serve(s, r)
}

// A profile's picture is uploaded from the account's main profile, made small, and shown only
// to people signed in to that account and to the owner and admins: never by an address that
// works without signing in.
func TestProfilePictures(t *testing.T) {
	s := newTestServer(t)
	in := setup(t, s)
	owner, profile := onMain(t, s, in), "/v1/profiles/"+in.Profiles[0].ID
	photo := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	for i := range photo.Pix {
		photo.Pix[i] = uint8(i * 7)
	}
	photo.Set(0, 0, color.RGBA{200, 10, 10, 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, photo); err != nil {
		t.Fatal(err)
	}

	w := sendPicture(s, owner.AccessToken, profile+"/avatar", raw.Bytes())
	var saved Profile
	if w.Code != 200 || jsonInto(w, &saved) != nil || !strings.HasPrefix(saved.Avatar, profile+"/avatar?v=") {
		t.Fatalf("uploading a picture answered %d %s", w.Code, w.Body)
	}
	for size, edge := range map[string]int{"small": 160, "large": 512} {
		got := serve(s, newRequest("GET", saved.Avatar+"&size="+size, owner.AccessToken, nil))
		config, format, err := image.DecodeConfig(got.Body)
		if got.Code != 200 || err != nil || (format != "jpeg" && format != "png") || config.Width != edge {
			t.Errorf("the %s picture: %d, %s %d wide, %v", size, got.Code, format, config.Width, err)
		}
		if cache := got.Header().Get("Cache-Control"); !strings.HasPrefix(cache, "private") {
			t.Errorf("the %s picture may be kept by shared caches: %q", size, cache)
		}
	}
	if listed := get[Page[Profile]](t, s, owner.AccessToken, "/v1/profiles").Items; listed[0].Avatar != saved.Avatar {
		t.Errorf("the profile list gives the picture as %q, want %q", listed[0].Avatar, saved.Avatar)
	}

	// Nobody outside the account sees it, with or without the address; the owner and admins
	// see a member's by naming the member.
	max := addMember(t, s, owner.AccessToken, "max", "member")
	member := signInAs(t, s, "max", testPassword, 2)
	maxProfile := "/v1/profiles/" + get[Me](t, s, member.AccessToken, "/v1/me").Profile.ID
	if w := sendPicture(s, member.AccessToken, maxProfile+"/avatar", raw.Bytes()); w.Code != 200 {
		t.Fatalf("a member uploading their own picture answered %d", w.Code)
	}
	for name, c := range map[string]struct {
		token, path string
		want        int
	}{
		"not signed in":                    {"", saved.Avatar, http.StatusUnauthorized},
		"another account's person":         {member.AccessToken, saved.Avatar, 404},
		"the owner, not naming the member": {owner.AccessToken, maxProfile + "/avatar", 404},
		"the owner, naming the member":     {owner.AccessToken, maxProfile + "/avatar?member=" + max.ID, 200},
		"a member naming the owner":        {member.AccessToken, profile + "/avatar?member=" + in.Account.ID, 403},
		"a size that is not one":           {owner.AccessToken, saved.Avatar + "&size=huge", 400},
	} {
		if got := serve(s, newRequest("GET", c.path, c.token, nil)); got.Code != c.want {
			t.Errorf("%s: answered %d, want %d", name, got.Code, c.want)
		}
	}

	// Only the main profile changes pictures; what is not a picture, or is too large, is refused.
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner.AccessToken, ProfileCreate{Name: "Kid"}, &kid)
	var onKid Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 3).Session.AccessToken, ProfileSelect{}, &onKid)
	if w := sendPicture(s, onKid.AccessToken, "/v1/profiles/"+kid.ID+"/avatar", raw.Bytes()); w.Code != 403 {
		t.Errorf("a profile other than the main one changing a picture answered %d, want 403", w.Code)
	}
	if got := serve(s, newRequest("GET", saved.Avatar, onKid.AccessToken, nil)); got.Code != 200 {
		t.Errorf("another profile of the same account reading the picture answered %d", got.Code)
	}
	if w := sendPicture(s, owner.AccessToken, profile+"/avatar", []byte("not a picture")); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("something that is not a picture answered %d, want 415", w.Code)
	}
	if w := sendPicture(s, owner.AccessToken, profile+"/avatar", make([]byte, maxProfilePicture+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a picture over 4 MB answered %d, want 413", w.Code)
	}

	// A new picture has a new address, and removing it leaves none.
	photo.Set(5, 5, color.RGBA{1, 2, 3, 255})
	raw.Reset()
	png.Encode(&raw, photo)
	var again Profile
	if w := sendPicture(s, owner.AccessToken, profile+"/avatar", raw.Bytes()); w.Code != 200 || jsonInto(w, &again) != nil || again.Avatar == saved.Avatar {
		t.Errorf("a second picture answered %d with the address %q", w.Code, again.Avatar)
	}
	var none Profile
	if w := call(t, s, "DELETE", profile+"/avatar", owner.AccessToken, nil, &none); w.Code != 200 || none.Avatar != "" {
		t.Errorf("removing the picture answered %d %+v", w.Code, none)
	}
	if got := serve(s, newRequest("GET", profile+"/avatar", owner.AccessToken, nil)); got.Code != 404 {
		t.Errorf("a removed picture answered %d, want 404", got.Code)
	}
}

// Where people sign in with a Portico Account, a profile's picture belongs to that account:
// this server refuses to take or remove one, says why, and stores nothing.
func TestProfilePicturesOnAPorticoAccountsServer(t *testing.T) {
	s := newTestServer(t)
	in := setup(t, s)
	owner, avatar := onMain(t, s, in), "/v1/profiles/"+in.Profiles[0].ID+"/avatar"
	if err := s.db.SaveSetting(t.Context(), signInKindKey, signInPorticoAccounts, s.now()); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 64, 64)))
	var refused errorBody
	w := sendPicture(s, owner.AccessToken, avatar, raw.Bytes())
	if w.Code != 409 || jsonInto(w, &refused) != nil || refused.Error.Code != "managed_by_portico_account" || !strings.Contains(refused.Error.Message, "Portico Account") {
		t.Fatalf("uploading a picture answered %d %+v", w.Code, refused.Error)
	}
	if code := status(t, s, "DELETE", avatar, owner.AccessToken, nil); code != 409 {
		t.Errorf("removing a picture answered %d, want 409", code)
	}
	if got := get[Profile](t, s, owner.AccessToken, "/v1/profiles/"+in.Profiles[0].ID); got.Avatar != "" {
		t.Errorf("the profile reads back with a local picture: %q", got.Avatar)
	}
	if kept, err := s.db.ProfilePictureName(t.Context(), mustUser(t, s, owner.AccessToken), in.Profiles[0].ID); err == nil {
		t.Errorf("a picture was stored on this server: %q", kept)
	}
}

func mustUser(t *testing.T, s *Server, token string) int64 {
	t.Helper()
	claims, err := s.tokens.CheckAccess(token, s.now())
	if err != nil {
		t.Fatal(err)
	}
	return claims.UserID
}
