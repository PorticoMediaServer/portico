package api

import (
	"archive/zip"
	"bytes"
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
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	member := signInAs(t, s, "max", testPassword, 2).AccessToken
	s.log.Warn("a library folder cannot be read", "path", "/srv/media/films/Heat (1995)/heat.mkv", "password", "hunter2")

	var page LogPage
	if w := call(t, s, "GET", "/v1/admin/logs?level=warn", admin, nil, &page); w.Code != 200 || len(page.Items) != 1 ||
		!strings.Contains(page.Items[0].Attrs["path"], "heat.mkv") || page.Items[0].Attrs["password"] != "[hidden]" {
		t.Fatalf("an administrator's Messages answered %d: %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/v1/admin/logs"}, {"PUT", "/v1/admin/logs/detail"}, {"GET", "/v1/admin/logs/events"}} {
		if code := status(t, s, c.method, c.path, member, LogDetail{Minutes: 30}); code != 403 {
			t.Errorf("a member: %s %s answered %d", c.method, c.path, code)
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
	if !strings.Contains(messages, "a library folder cannot be read") || strings.Contains(messages, "heat.mkv") || strings.Contains(messages, "hunter2") {
		t.Errorf("the exported messages: %s", messages)
	}
	if !strings.Contains(files["settings.json"], `"general"`) || !strings.Contains(files["activity.jsonl"], "member.") {
		t.Errorf("the export's settings and activity: %s / %s", files["settings.json"], files["activity.jsonl"])
	}
}
