package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

// The owner and administrators read Messages and can record more detail; a member cannot. The
// owner's support export holds the messages with file paths replaced, the settings without
// secrets and the activity record, and no password.
func TestMessagesAndSupportExport(t *testing.T) {
	s := newTestServer(t)
	l, err := serverlog.Open(filepath.Join(t.TempDir(), "logs"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s.UseLog(l)
	s.log = slog.New(l.Handler())
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "ada", "admin")
	addMember(t, s, owner.AccessToken, "max", "member")
	held := addMember(t, s, owner.AccessToken, "kit", "admin")
	none, no := []string{}, false
	if code := status(t, s, "PATCH", "/v1/admin/members/"+held.ID, owner.AccessToken, MemberEdit{AllLibraries: &no, Libraries: &none}); code != 200 {
		t.Fatalf("holding an administrator to some libraries answered %d", code)
	}
	limited := signInAs(t, s, "kit", testPassword, 3).AccessToken
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, s, "max", testPassword, 2).AccessToken
	s.log.With("log_kind", "scan").Warn("a library folder cannot be read", "path", "/srv/media/films/Heat (1995)/heat.mkv", "password", "hunter2")
	s.log.Error("a file could not be opened", "error", "open /media/Films/The Thing (1982)/The Thing (1982).mkv: no such file or directory")
	if code := status(t, s, "PUT", "/v1/admin/settings/backups", owner.AccessToken, json.RawMessage(`{"folder":"/mnt/nas/The Archive"}`)); code != 200 {
		t.Fatalf("setting the backup folder answered %d", code)
	}

	var page LogPage
	if w := call(t, s, "GET", "/v1/admin/logs?level=warn", admin, nil, &page); w.Code != 200 || len(page.Items) != 2 ||
		!strings.Contains(page.Items[1].Attrs["path"], "heat.mkv") || page.Items[1].Attrs["password"] != "[hidden]" {
		t.Fatalf("an administrator's Messages answered %d: %s", w.Code, w.Body)
	}
	var scans LogPage
	if w := call(t, s, "GET", "/v1/admin/logs?kind=scan", admin, nil, &scans); w.Code != 200 || len(scans.Items) != 1 || scans.Items[0].Kind != "scan" {
		t.Fatalf("the scan filter answered %d: %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/admin/logs?kind=unknown", "/v1/admin/logs/events?kind=unknown"} {
		if code := status(t, s, "GET", path, admin, nil); code != 400 {
			t.Errorf("an unknown kind at %s answered %d", path, code)
		}
	}
	// Messages name the files of every library: not for a member, nor for an administrator
	// held to some libraries.
	for _, token := range []string{member, limited} {
		for _, c := range []struct{ method, path string }{{"GET", "/v1/admin/logs"}, {"PUT", "/v1/admin/logs/detail"}, {"GET", "/v1/admin/logs/events"}} {
			var body any
			if c.method == "PUT" {
				body = LogDetail{Minutes: 30}
			}
			if code := status(t, s, c.method, c.path, token, body); code != 403 {
				t.Errorf("a member or limited administrator: %s %s answered %d", c.method, c.path, code)
			}
		}
	}
	var detail LogDetailState
	if call(t, s, "PUT", "/v1/admin/logs/detail", admin, LogDetail{Minutes: 30}, &detail); detail.DetailUntil == nil {
		t.Errorf("Record more detail did not start")
	}
	call(t, s, "PUT", "/v1/admin/logs/detail", admin, LogDetail{}, &detail)

	if code := status(t, s, "POST", "/v1/admin/support-export", admin, SupportExportRequest{}); code != 403 {
		t.Errorf("an administrator made a support export: %d", code)
	}
	w := call(t, s, "POST", "/v1/admin/support-export", owner.AccessToken, SupportExportRequest{Days: 1}, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("the support export answered %d: %s", w.Code, w.Body)
	}
	z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		raw, _ := io.ReadAll(r)
		files[f.Name] = string(raw)
	}
	messages := files["messages.jsonl"]
	if !strings.Contains(messages, "a library folder cannot be read") || strings.Contains(messages, "heat.mkv") || strings.Contains(messages, "hunter2") ||
		strings.Contains(messages, "Thing") || !strings.Contains(messages, "no such file") {
		t.Errorf("the exported messages: %s", messages)
	}
	if strings.Contains(files["settings.json"], "Archive") {
		t.Errorf("the export's settings name the backup folder: %s", files["settings.json"])
	}
	if !strings.Contains(files["settings.json"], `"general"`) || !strings.Contains(files["activity.jsonl"], "member.") {
		t.Errorf("the export's settings and activity: %s / %s", files["settings.json"], files["activity.jsonl"])
	}
}
