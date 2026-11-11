package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner's Dashboard gathers what is happening now. Each alert is raised by its real cause
// and goes by itself when the cause ends: a failed scheduled backup, a library folder that
// cannot be read, a remote source that cannot be reached, a new version. Streams are counted
// as Now playing lists them, problem reports nobody answered are counted, and the newest
// backup is the one just made. An administrator and a member cannot read it.
func TestOwnerDashboard(t *testing.T) {
	th := newTheatre(t)
	s, owner, ctx := th.s, th.owner.AccessToken, context.Background()
	addMember(t, s, owner, "ada", "admin")
	addMember(t, s, owner, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, s, "max", testPassword, 2).AccessToken
	for _, token := range []string{admin, member} {
		if code := status(t, s, "GET", "/v1/admin/dashboard", token, nil); code != 403 {
			t.Errorf("an administrator or member read the Dashboard: %d", code)
		}
	}
	// This machine's own free space must not raise the storage alert here.
	if code := status(t, s, "PUT", "/v1/admin/settings/storage", owner, json.RawMessage(`{"warnPercent":1,"majorPercent":1}`)); code != 200 {
		t.Fatalf("setting the storage warnings answered %d", code)
	}
	read := func() Dashboard {
		t.Helper()
		var d Dashboard
		if w := call(t, s, "GET", "/v1/admin/dashboard", owner, nil, &d); w.Code != 200 {
			t.Fatalf("the Dashboard answered %d: %s", w.Code, w.Body)
		}
		return d
	}
	codes := func(d Dashboard) []string {
		var out []string
		for _, a := range d.Alerts {
			out = append(out, a.Code)
		}
		slices.Sort(out)
		return out
	}
	if d := read(); len(d.Alerts) != 0 || d.Scans == nil || d.Tasks.Items == nil || d.NowPlaying.Streams != 0 {
		t.Fatalf("a quiet server's Dashboard: %+v", d)
	}

	// Two streams.
	th.play(owner, th.film)
	th.play(member, th.song)
	var playing Page[NowPlaying]
	call(t, s, "GET", "/v1/admin/sessions", owner, nil, &playing)
	away := 0
	for _, p := range playing.Items {
		away += btoi(p.Away)
	}
	// A library whose folder is gone by its next scan.
	folder := filepath.Join(t.TempDir(), "Films")
	os.MkdirAll(folder, 0o755)
	var films AdminLibrary
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, LibraryCreate{Name: "Films", Kind: "movies", Folders: []string{folder}}, &films); w.Code != 200 {
		t.Fatalf("adding a library answered %d: %s", w.Code, w.Body)
	}
	// Adding a library starts its first scan, and Scan now joins a scan already waiting or running.
	// So the first scan is waited for before the folder goes, and the next one finds it gone.
	scanNow(t, s, owner, films.ID)
	os.Remove(folder)
	scanNow(t, s, owner, films.ID)
	// A remote source found unreachable.
	nas, err := s.db.CreateRemoteSource(ctx, "src_dashboard", db.NewRemoteSource{UserID: 1, Kind: "webdav", Name: "NAS", Config: "{}", Secret: []byte("sealed"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetRemoteSourceState(ctx, nas.ID, "unreachable", "dial tcp: connection refused")
	// A newer version, from the owner's own update address.
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":"v1.1.0","name":"Portico 1.1"}`))
	}))
	defer releases.Close()
	s.updates.Version = "1.0.0"
	if code := status(t, s, "PUT", "/v1/admin/settings/general", owner, json.RawMessage(`{"updateCheck":"address","updateAddress":"`+releases.URL+`"}`)); code != 200 {
		t.Fatalf("setting the update address answered %d", code)
	}
	call(t, s, "POST", "/v1/admin/updates/check", owner, nil, nil)
	// A scheduled backup into a folder that is not there.
	backupNow := func() {
		t.Helper()
		if _, err := s.Backups().Start(true); err != nil {
			t.Fatal(err)
		}
		for s.Backups().Running() != nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if code := status(t, s, "PUT", "/v1/admin/settings/backups", owner, json.RawMessage(`{"folder":"`+filepath.Join(t.TempDir(), "unmounted", "backups")+`"}`)); code != 200 {
		t.Fatalf("setting the backup folder answered %d", code)
	}
	backupNow()
	// A problem report nobody has answered.
	if code := status(t, s, "POST", "/v1/reports", member, ReportCreate{Kind: "problem", Message: "The song stutters"}); code != 200 {
		t.Fatalf("reporting a problem answered %d", code)
	}

	d := read()
	if got := codes(d); !slices.Equal(got, []string{"backup_failed", "library_unreachable", "remote_unreachable", "update_available"}) {
		t.Fatalf("the alerts are %v: %+v", got, d.Alerts)
	}
	for _, a := range d.Alerts {
		switch a.Code {
		case "library_unreachable":
			if a.Action != (AlertAction{Kind: "library-source", Target: films.ID}) || a.Params["library"] != "Films" || !a.Major {
				t.Errorf("the library's alert: %+v", a)
			}
		case "remote_unreachable":
			if a.Action != (AlertAction{Kind: "remote-source", Target: nas.PublicID}) || a.Params["source"] != "NAS" {
				t.Errorf("the source's alert: %+v", a)
			}
		case "update_available":
			if a.Params["latest"] != "v1.1.0" || a.Action.Kind != "update" || a.Major {
				t.Errorf("the update's alert: %+v", a)
			}
		case "backup_failed":
			if a.Params["reason"] == "" || a.Action.Kind != "backups" {
				t.Errorf("the backup's alert: %+v", a)
			}
		}
	}
	if d.NowPlaying.Streams != 2 || d.NowPlaying.Away != away || d.OpenReports != 1 {
		t.Errorf("the totals: %+v, %d open reports; Now playing lists %d streams, %d away", d.NowPlaying, d.OpenReports, len(playing.Items), away)
	}

	// Each cause ends, and its alert goes by itself.
	os.MkdirAll(folder, 0o755)
	scanNow(t, s, owner, films.ID)
	s.db.SetRemoteSourceState(ctx, nas.ID, "ok", "")
	status(t, s, "PUT", "/v1/admin/settings/backups", owner, json.RawMessage(`{"folder":""}`))
	th.clock = th.clock.Add(time.Second) // a new backup's id
	backupNow()
	s.updates.Version = "1.1.0"
	call(t, s, "POST", "/v1/admin/updates/check", owner, nil, nil)
	d = read()
	if len(d.Alerts) != 0 {
		t.Errorf("alerts after their causes ended: %+v", d.Alerts)
	}
	list, _ := s.Backups().List()
	if len(list) != 1 || !d.Backups.Read || d.Backups.Last == nil || d.Backups.Last.ID != list[0].ID || !d.Backups.Last.Scheduled {
		t.Errorf("the newest backup reads %+v; the folder holds %+v", d.Backups, list)
	}
}
