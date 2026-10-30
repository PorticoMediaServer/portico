package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"
)

func TestSignInFailureCodesAndRetryWindow(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
		retry  string
	}{
		{identity.ErrPasswordChangeRequired, 403, "password_change_required", ""},
		{&identity.AccountLockError{RetryAfter: 347}, 429, "account_locked", "347"},
		{identity.ErrUnauthorized, 401, "unauthorized", ""},
	} {
		w := httptest.NewRecorder()
		failure(w, test.err)
		var body struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != test.status || body.Error.Code != test.code || w.Header().Get("Retry-After") != test.retry {
			t.Fatalf("%v: status=%d error=%+v retry=%q", test.err, w.Code, body.Error, w.Header().Get("Retry-After"))
		}
		if test.status == 401 && body.Error.Message != "Authentication is required." {
			t.Fatalf("unauthenticated message: %q", body.Error.Message)
		}
	}
}

func TestRetiredProfileStepUpRouteIsAbsent(t *testing.T) {
	d, session := logoutHTTPFixture(t)
	w := logoutHTTPRequest(New(d), "POST", "/v1/direct/profiles/"+session.Viewer.ProfileID+"/step-up", session.AccessToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("retired profile proof route is still served: %d %s", w.Code, w.Body.String())
	}
}

func TestSetupRangePlaybackStopAndOrigin(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	cat := catalog.New(db)
	host, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	player := playback.New(db)
	scanner := ingestion.New(db, cat, assets.Probe{})
	handler := New(Dependencies{DB: db, Identity: id, Catalog: cat, Ingestion: scanner, Playback: player, PlaybackV1: &playbackv1.Service{}, Hosted: host, Origins: []string{"http://127.0.0.1:19412"}})
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	secret, e := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if e != nil {
		t.Fatal(e)
	}
	w := request("POST", "/v1/setup", "", map[string]string{"authMode": "local", "setupToken": string(secret), "username": "owner", "password": "long-test-password", "name": "Test"})
	if w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	var auth identity.Envelope
	if e = json.Unmarshal(w.Body.Bytes(), &auth); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(storagepaths.Path(root, "setup-token")); !os.IsNotExist(e) {
		t.Fatal("setup token retained")
	}
	lib, e := cat.Create("Movies", "movie", root)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "Film.mp4")
	if e = os.WriteFile(path, []byte("0123456789"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job',?,'running',?);INSERT INTO scan_queue(job_id,path,kind) VALUES('job',?,'file')`, lib.ID, time.Now().Format(time.RFC3339), path)
	if e != nil {
		t.Fatal(e)
	}
	if e = seedHTTPMovie(context.Background(), db, lib.ID, path, assets.Facts{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 60}); e != nil {
		t.Fatal(e)
	}
	settleCompactCatalogue(t, db)
	items, _, e := cat.List(catalog.Viewer{Profile: auth.Viewer.ProfileID, Libraries: []string{lib.ID}}, lib.ID, "", 10)
	if e != nil || len(items) != 1 {
		t.Fatal(e)
	}
	start, _ := json.Marshal(map[string]string{"itemId": items[0].ID})
	r := httptest.NewRequest("POST", "/v1/playback/sessions", bytes.NewReader(start))
	r.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "router-start-0000001")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("playback %d %s", w.Code, w.Body.String())
	}
	var session playbackv1.SessionView
	_ = json.Unmarshal(w.Body.Bytes(), &session)
	if session.Presentation.Mode != "direct" {
		t.Fatalf("playback %s", w.Body.String())
	}
	streamURL := session.Presentation.URL
	r = httptest.NewRequest("GET", streamURL, nil)
	r.Header.Set("Range", "bytes=2-5")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" {
		t.Fatalf("range %d %q", w.Code, w.Body.String())
	}
	w = request("DELETE", "/v1/playback/sessions/"+session.ID, auth.AccessToken, nil)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	w = request("GET", streamURL, "", nil)
	// NEW-35: a stopped grant is a hidden 404 presentation_ended, never a 401.
	if w.Code != 404 || !strings.Contains(w.Body.String(), "presentation_ended") {
		t.Fatal("stopped grant remained valid", w.Code)
	}
	r = httptest.NewRequest("GET", "/v1/system", nil)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("origin accepted")
	}
	// A server set up for direct sign-in isn't attached to Portico Accounts, whatever the build
	// can reach; the web sign-in page decides by this (the demo showed the account hand-off).
	w = request("GET", "/v1/system", "", nil)
	var system struct {
		HostedAttached *bool `json:"hostedAttached"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &system); e != nil || system.HostedAttached == nil || *system.HostedAttached {
		t.Fatalf("direct server reports Portico Accounts: %s", w.Body.String())
	}
	w = request("GET", "/v1/items", auth.AccessToken, nil)
	if strings.Contains(w.Body.String(), root) {
		t.Fatal("filesystem path exposed")
	}
}
