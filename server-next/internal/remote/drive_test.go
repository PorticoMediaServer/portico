package remote_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// Google Drive with the owner's own OAuth client: the sign-in address carries a PKCE
// challenge and Portico's fixed redirect, the pasted address's code is exchanged once with
// the verifier, and the token reaches rclone, which lists the Drive without names that could
// point elsewhere. A service account key works the same way. No request leaves this machine.
func TestDrive(t *testing.T) {
	fakeRcloneBinary(t)
	exchanges := 0
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges++
		r.ParseForm()
		f := r.PostForm
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if f.Get("code") != "code-1" || f.Get("client_id") != "client-1" || f.Get("client_secret") != "secret-1" ||
			f.Get("redirect_uri") != remote.DriveRedirect || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3599, "token_type": "Bearer"})
	}))
	defer google.Close()
	before := remote.GoogleTokenURL
	remote.GoogleTokenURL = google.URL
	t.Cleanup(func() { remote.GoogleTokenURL = before })

	var signIns remote.DriveSignIns
	id, address := signIns.Start("client-1", "secret-1")
	u, _ := url.Parse(address)
	q := u.Query()
	challenge = q.Get("code_challenge")
	if !strings.HasPrefix(address, remote.GoogleAuthURL+"?") || q.Get("redirect_uri") != remote.DriveRedirect || q.Get("state") != id ||
		q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "drive.readonly") {
		t.Fatalf("sign-in address %s", address)
	}
	if _, _, _, err := signIns.Finish(ctx, id, remote.DriveRedirect+"?state=other&code=code-1"); err == nil {
		t.Fatal("an address from another sign-in was taken")
	}
	id, address = signIns.Start("client-1", "secret-1")
	u, _ = url.Parse(address)
	challenge = u.Query().Get("code_challenge")
	clientID, secret, token, err := signIns.Finish(ctx, id, remote.DriveRedirect+"?state="+id+"&code=code-1&scope=x")
	if err != nil || clientID != "client-1" || !strings.Contains(token, `"refresh_token":"refresh-1"`) {
		t.Fatalf("finishing: %v %q", err, token)
	}
	if _, _, _, err := signIns.Finish(ctx, id, remote.DriveRedirect+"?state="+id+"&code=code-1"); err == nil || exchanges != 1 {
		t.Fatalf("a sign-in was used twice: %v, %d exchanges", err, exchanges)
	}

	for name, s := range map[string]struct {
		c remote.Config
		s remote.Secret
	}{
		"oauth":           {remote.Config{DriveClientID: clientID, Path: "Media"}, remote.Secret{DriveClientSecret: secret, DriveToken: token}},
		"service account": {remote.Config{Path: "Media"}, remote.Secret{DriveKey: `{"type":"service_account","client_email":"a@b","private_key":"k"}`}},
	} {
		storage, err := remote.Build("drive", s.c, s.s)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := storage.List(ctx, "Films")
		if err != nil || len(listed) != 1 || listed[0].Name != "Heat.mkv" {
			t.Fatalf("%s: listing %v %+v", name, err, listed)
		}
		storage.(interface{ Close() error }).Close()
	}
	if remote.CheckServiceAccountKey(`{"type":"authorized_user"}`) == nil {
		t.Fatal("a key that is not a service account's was taken")
	}
}

var challenge string
