package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

type avatarUnreadBody struct{ t *testing.T }

func (b avatarUnreadBody) Read([]byte) (int, error) {
	b.t.Error("avatar body read before permission check")
	return 0, io.EOF
}
func (avatarUnreadBody) Close() error { return nil }

func TestAvatarUploadAuthenticatesBeforeReadingBody(t *testing.T) {
	d, _ := logoutHTTPFixture(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("Testing-password1!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.DB.Exec(`UPDATE accounts SET password_hash=? WHERE id='account'`, hash); err != nil {
		t.Fatal(err)
	}
	login, err := d.Identity.DirectLoginFrom(context.Background(), "owner", "Testing-password1!", true)
	if err != nil || login.Session == nil {
		t.Fatalf("direct account login: %v", err)
	}
	owner := *login.Session
	snap, err := d.Identity.CreateDirectProfile(context.Background(), owner.AccessToken, "Child", "mint")
	if err != nil {
		t.Fatal(err)
	}
	var profile string
	for _, p := range snap.Profiles {
		if p.Name == "Child" {
			profile = p.ID
		}
	}
	selection, err := d.Identity.SelectDirectProfile(context.Background(), owner.AccessToken, profile, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	child := selection.Session
	h := New(d)
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {strings.Repeat("x", 43), 401}, {child.AccessToken, 403}} {
		r := httptest.NewRequest("POST", "/v1/direct/profiles/"+profile+"/avatar", avatarUnreadBody{t})
		r.Header.Set("Content-Type", "image/png")
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("permission check: want %d got %d %s", tc.want, w.Code, w.Body.String())
		}
	}
	// An approved manager still uploads a real picture and all renditions.
	img := image.NewRGBA(image.Rect(0, 0, 80, 120))
	img.Set(1, 1, color.RGBA{R: 180, A: 255})
	var raw bytes.Buffer
	if err = png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	request := func(data []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/direct/profiles/"+profile+"/avatar", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request(raw.Bytes())
	if w.Code != 201 {
		t.Fatalf("legitimate upload failed: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err = d.DB.QueryRow(`SELECT count(*) FROM profile_avatar_objects`).Scan(&count); err != nil || count != len(identity.AvatarRenditions) {
		t.Fatalf("renditions missing: %d %v", count, err)
	}
	// A valid manager who stops sending the body cannot occupy an upload slot
	// indefinitely. Exercise the real net/http read deadline, not a recorder.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = fmt.Fprintf(conn, "POST /v1/direct/profiles/%s/avatar HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nAccept-Encoding: gzip\r\nContent-Type: image/png\r\nContent-Length: 100\r\n\r\n", profile, address.Host, owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("stalled authenticated upload did not finish at its deadline: %v", err)
	}
	response.Body.Close()
	conn.Close()
	if response.StatusCode == 201 || time.Since(started) >= time.Second {
		t.Fatalf("stalled body accepted or held upload capacity: status %d elapsed %v", response.StatusCode, time.Since(started))
	}
	// Waiting for another image job honors request cancellation, without replacing
	// the accepted avatar with a partial publication.
	release, err := imagework.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.Identity.UploadProfileAvatar(ctx, owner.AccessToken, profile, raw.Bytes()); err == nil {
		t.Fatal("cancelled image upload accepted")
	}
	release()
	w = request(raw.Bytes())
	if w.Code != 201 {
		t.Fatalf("upload did not recover: %d %s", w.Code, w.Body.String())
	}
}
