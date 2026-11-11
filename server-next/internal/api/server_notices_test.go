package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// When an alert's cause starts the owner gets one notice, not one at every look; administrators
// get it too only once the owner chooses so; a cause that ends and comes back is told again; a
// restart tells nothing already told, a newer version included before the first check after the
// start; and a later version is told although the last was not installed.
func TestServerNoticesAreSentOnceWhenACauseStarts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	// This machine's own free space must not raise the storage alert here.
	if code := status(t, s, "PUT", "/v1/admin/settings/storage", owner, json.RawMessage(`{"warnPercent":1,"majorPercent":1}`)); code != 200 {
		t.Fatalf("setting the storage warnings answered %d", code)
	}
	tell := func() {
		t.Helper()
		if err := s.tellNewAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(token string) []Notification {
		t.Helper()
		var n Notifications
		if w := call(t, s, "GET", "/v1/notifications?view=all", token, nil, &n); w.Code != 200 {
			t.Fatalf("the inbox answered %d: %s", w.Code, w.Body)
		}
		return n.Items
	}
	kinds := func(token string) []string {
		var out []string
		for _, n := range inbox(token) {
			out = append(out, n.Kind)
		}
		slices.Sort(out)
		return out
	}
	tag := "v1.1.0"
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	defer releases.Close()
	checkForUpdates := func() {
		t.Helper()
		s.updates.Version = "1.0.0"
		if code := status(t, s, "POST", "/v1/admin/updates/check", owner, nil); code != 200 {
			t.Fatalf("checking for updates answered %d", code)
		}
	}

	// A remote source found unreachable and a newer version, each looked at twice.
	nas, err := s.db.CreateRemoteSource(ctx, "src_notices", db.NewRemoteSource{UserID: 1, Kind: "webdav", Name: "NAS", Config: "{}", Secret: []byte("sealed"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetRemoteSourceState(ctx, nas.ID, "unreachable", "dial tcp: connection refused")
	if code := status(t, s, "PUT", "/v1/admin/settings/general", owner, json.RawMessage(`{"updateCheck":"address","updateAddress":"`+releases.URL+`"}`)); code != 200 {
		t.Fatalf("setting the update address answered %d", code)
	}
	checkForUpdates()
	tell()
	tell()
	if got := kinds(owner); !slices.Equal(got, []string{"remote_unreachable", "update_available"}) {
		t.Fatalf("the owner's notices after two looks: %v", got)
	}
	for _, n := range inbox(owner) {
		if n.Kind == "remote_unreachable" && (n.Ref != nas.PublicID || jsonField(t, n.Params, "source") != `"NAS"`) {
			t.Errorf("the source's notice: %+v", n)
		}
	}
	if got := kinds(admin); len(got) != 0 {
		t.Errorf("an administrator was told before the owner chose so: %v", got)
	}

	// The owner chooses administrators too; the source recovers, then refuses its sign-in.
	if code := status(t, s, "PUT", "/v1/admin/settings/notices", owner, json.RawMessage(`{"serverNoticesTo":"owner-and-admins","keepDays":180}`)); code != 200 {
		t.Fatalf("choosing administrators answered %d", code)
	}
	s.db.SetRemoteSourceState(ctx, nas.ID, "ok", "")
	tell()
	s.db.SetRemoteSourceState(ctx, nas.ID, "sign_in", "401 Unauthorized")
	tell()
	if got := kinds(owner); !slices.Equal(got, []string{"remote_unreachable", "remote_unreachable", "update_available"}) {
		t.Errorf("the owner's notices after the source came back and failed again: %v", got)
	}
	if got := kinds(admin); !slices.Equal(got, []string{"remote_unreachable"}) {
		t.Errorf("the administrator's notices: %v", got)
	}

	// A restart: the source still refuses, and no check for a new version has run yet.
	closeDB(s.db)
	s = openTestServer(t, path)
	tell()
	checkForUpdates() // the same version as before the restart
	tell()
	if got := kinds(owner); len(got) != 3 {
		t.Errorf("a restart told the owner again: %v", got)
	}
	tag = "v1.2.0"
	checkForUpdates()
	tell()
	if got := kinds(owner); len(got) != 4 || inbox(owner)[0].Kind != "update_available" || jsonField(t, inbox(owner)[0].Params, "latest") != `"v1.2.0"` {
		t.Errorf("a later version was not told: %v", got)
	}
}

// jsonField is one field of a JSON object, as JSON.
func jsonField(t *testing.T, raw JSON, name string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	return string(fields[name])
}
