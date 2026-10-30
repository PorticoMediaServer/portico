package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func memberPictureBytes(t *testing.T, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 80; x++ {
			a := uint8(255)
			if alpha && x < 40 {
				a = 80
			}
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 70, B: 30, A: a})
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestMemberProfilePictureInvalidPathsHaveTypedClientErrors(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, tc := range []struct {
		name, account, profile string
		want                   int
		code                   string
	}{
		{"long account", strings.Repeat("A", 4000), "member-profile", 400, "invalid_request"},
		{"nul account", "a%00b", "member-profile", 400, "invalid_request"},
		{"invalid utf8 account", "%ff", "member-profile", 400, "invalid_request"},
		{"long profile", "member", strings.Repeat("A", 4000), 400, "invalid_request"},
		{"nul profile", "member", "a%00b", 400, "invalid_request"},
		{"invalid utf8 profile", "member", "%ff", 400, "invalid_request"},
		{"missing account", "missing", "member-profile", 404, "not_found"},
		{"missing profile", "member", "missing", 404, "avatar_not_found"},
		{"missing picture", "member", "member-profile", 404, "avatar_not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.call("GET", "/v1/admin/access/members/"+tc.account+"/profiles/"+tc.profile+"/avatar", f.owner, "")
			var body struct {
				Error struct {
					Code string
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.want || body.Error.Code != tc.code {
				t.Fatalf("got %d %s; want %d %s", w.Code, w.Body, tc.want, tc.code)
			}
		})
	}
}

func TestMemberProfilePicturesTierAccountAndRevision(t *testing.T) {
	f := administrationFixtureNew(t)
	base := "/v1/admin/access/members/member/profiles/member-profile/avatar"
	raw := memberPictureBytes(t, false)
	upload := func(token, path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		token, path string
		want        int
	}{
		{"", base + "?expectedRevision=1", 401}, {f.member, base + "?expectedRevision=1", 403},
		{f.admin, strings.ReplaceAll(base, "/member/", "/admin/") + "?expectedRevision=1", 403},
		{f.owner, strings.ReplaceAll(base, "/member/", "/owner/") + "?expectedRevision=1", 403},
		{f.owner, strings.Replace(base, "/member-profile/", "/admin-profile/", 1) + "?expectedRevision=1", 404},
		{f.owner, base + "?expectedRevision=999", 409},
	} {
		r := httptest.NewRequest("POST", tc.path, avatarUnreadBody{t})
		r.Header.Set("Authorization", "Bearer "+tc.token)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("admission %s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	w := upload(f.admin, base+"?expectedRevision=1", raw)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var p identity.MemberProfile
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.Avatar == nil || p.Revision != 2 || !strings.HasPrefix(p.Avatar.URL, base+"?v=") {
		t.Fatalf("profile: %+v", p)
	}
	url := p.Avatar.URL
	if w = f.call("GET", url, f.owner, ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	if _, e := jpeg.Decode(bytes.NewReader(w.Body.Bytes())); e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{f.member, ""} {
		if w = f.call("GET", url, token, ""); w.Code == 200 {
			t.Fatalf("avatar escaped owner authority: %d", w.Code)
		}
	}
	if w = f.call("GET", "/v1/profiles/member-profile/avatar?v="+strconv.FormatInt(p.Avatar.Version, 10), f.owner, ""); w.Code == 200 {
		t.Fatal("self-picture read widened to another account")
	}
	if w = f.call("GET", "/v1/profiles/member-profile/avatar?v="+strconv.FormatInt(p.Avatar.Version, 10), f.member, ""); w.Code != 200 {
		t.Fatalf("person cannot read own picture: %d %s", w.Code, w.Body)
	}
	if w = upload(f.owner, base+"?expectedRevision=1", raw); w.Code != 409 {
		t.Fatalf("stale upload: %d", w.Code)
	}
	if w = f.call("DELETE", base, f.owner, `{"expectedRevision":1}`); w.Code != 409 {
		t.Fatalf("stale remove: %d", w.Code)
	}
	if w = f.call("DELETE", base, f.owner, `{"expectedRevision":2}`); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if w = f.call("GET", url, f.owner, ""); w.Code != 404 {
		t.Fatalf("removed picture: %d", w.Code)
	}
	w = upload(f.owner, base+"?expectedRevision=3", memberPictureBytes(t, true))
	if w.Code != 200 {
		t.Fatalf("reupload: %d %s", w.Code, w.Body)
	}
	var next identity.MemberProfile
	if e := json.Unmarshal(w.Body.Bytes(), &next); e != nil {
		t.Fatal(e)
	}
	if next.Avatar == nil || next.Avatar.Version <= p.Avatar.Version {
		t.Fatalf("reused immutable version: %+v", next)
	}
	if w = f.call("GET", url, f.owner, ""); w.Code != 404 {
		t.Fatalf("old URL resurrected: %d", w.Code)
	}
	if w = f.call("GET", next.Avatar.URL, f.owner, ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("transparent rendition: %d", w.Code)
	}
	if _, e := png.Decode(bytes.NewReader(w.Body.Bytes())); e != nil {
		t.Fatal(e)
	}
	if _, e := f.deps.DB.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='admin'`); e != nil {
		t.Fatal(e)
	}
	if w = f.call("GET", next.Avatar.URL, f.admin, ""); w.Code == 200 {
		t.Fatal("revoked administrator read picture")
	}
}

func TestMemberProfilePictureRejectsInvalidImagesAndChangedPublication(t *testing.T) {
	f := administrationFixtureNew(t)
	base := "/v1/admin/access/members/member/profiles/member-profile/avatar?expectedRevision=1"
	for _, raw := range [][]byte{[]byte("RIFFxxxxWEBP"), []byte("not a picture"), make([]byte, identity.AvatarUploadBytes+1)} {
		r := httptest.NewRequest("POST", base, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+f.owner)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 415 {
			t.Fatalf("invalid image: %d %s", w.Code, w.Body)
		}
	}
	prepared, e := identity.PrepareProfileAvatar(t.Context(), memberPictureBytes(t, false))
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a profile edit after preflight/decode and before publication.
	if _, e = f.deps.DB.Exec(`UPDATE direct_profiles SET revision=revision+1 WHERE id='member-profile'`); e != nil {
		t.Fatal(e)
	}
	// Real HTTP identity checks are also covered above; the accepting store must fence independently.
	_, e = f.deps.Access.Access.SetMemberProfilePicture(t.Context(), nil, f.deps.Identity, "member", "member-profile", 1, &prepared)
	if e != identity.ErrProfileChanged {
		t.Fatalf("late revision: %v", e)
	}
	var count int
	if e = f.deps.DB.QueryRow(`SELECT count(*) FROM profile_avatars`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("failed publication wrote images: %d %v", count, e)
	}
}

// The body is read only after preflight, so this simulates a tier change during upload.
type pictureReadHook struct {
	body *bytes.Reader
	hook func()
}

func (r *pictureReadHook) Read(p []byte) (int, error) {
	if r.hook != nil {
		r.hook()
		r.hook = nil
	}
	return r.body.Read(p)
}
func (*pictureReadHook) Close() error { return nil }

func TestMemberProfilePictureRechecksAuthorityAfterBody(t *testing.T) {
	f := administrationFixtureNew(t)
	body := &pictureReadHook{body: bytes.NewReader(memberPictureBytes(t, false)), hook: func() {
		if _, e := f.deps.DB.Exec(`UPDATE direct_memberships SET role='member' WHERE account_id='admin'`); e != nil {
			t.Fatal(e)
		}
	}}
	r := httptest.NewRequest("POST", "/v1/admin/access/members/member/profiles/member-profile/avatar?expectedRevision=1", body)
	r.Header.Set("Authorization", "Bearer "+f.admin)
	r.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 403 && w.Code != 401 {
		t.Fatalf("late authority: %d %s", w.Code, w.Body)
	}
	var count int
	if e := f.deps.DB.QueryRow(`SELECT count(*) FROM profile_avatars`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("unauthorized picture published: %d %v", count, e)
	}
}
