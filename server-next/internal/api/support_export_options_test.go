package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSupportExportHonoursComponentsWindowAndAppLogPrivacy(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	clock := newRigClock(s.now())
	s.setClock(clock.Now)
	oldLog := fmt.Sprintf(`{"time":%d,"level":"info","area":"app","message":"started","facts":{"screen":"home"}}`, clock.Now().UnixMilli())
	if code := status(t, s, "POST", "/v1/reports", owner.AccessToken, ReportCreate{Kind: "crash", Log: oldLog}); code != 200 {
		t.Fatal("old report", code)
	}
	clock.Add(2 * time.Hour)
	owner = signInAs(t, s, "justin", testPassword, 1)
	log := fmt.Sprintf(`{"time":%d,"level":"error","area":"playback","message":"error","facts":{"route":"converted","codec":"hevc",`+
		`"url":"https://user:lost-password@example.invalid","path":"/srv/private/title.mkv","token":"lost-token",`+
		`"code":"unlabelled-secret","headers":"Bearer abcdefghijklmnop"}}`, clock.Now().UnixMilli())
	if code := status(t, s, "POST", "/v1/reports", owner.AccessToken, ReportCreate{Kind: "crash", Log: log}); code != 200 {
		t.Fatal("current report", code)
	}
	export := func(req SupportExportRequest) map[string]string {
		t.Helper()
		w := call(t, s, "POST", "/v1/admin/support-export", owner.AccessToken, req, nil)
		if w.Code != 200 {
			t.Fatalf("export: %d %s", w.Code, w.Body)
		}
		z, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, f := range z.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[f.Name] = string(raw)
		}
		return files
	}
	files := export(SupportExportRequest{WindowMinutes: 60, Components: []string{"client"}})
	if len(files) != 2 || files["server.json"] == "" || files["app-logs.jsonl"] == "" {
		t.Fatalf("components switched off were included: %v", files)
	}
	app := files["app-logs.jsonl"]
	if !strings.Contains(app, "converted") || strings.Contains(app, "started") {
		t.Fatalf("one-hour window: %s", app)
	}
	for _, hidden := range []string{"private", "title.mkv", "lost-password", "abcdefghijklmnop", "lost-token", "unlabelled-secret"} {
		if strings.Contains(app, hidden) {
			t.Errorf("app log exported %q", hidden)
		}
	}
	var manifest struct {
		From, To      time.Time
		PathsIncluded bool
	}
	if err := json.Unmarshal([]byte(files["server.json"]), &manifest); err != nil || manifest.To.Sub(manifest.From) != time.Hour || manifest.PathsIncluded {
		t.Fatalf("window/paths manifest: %+v %v", manifest, err)
	}
	withPaths := export(SupportExportRequest{WindowMinutes: 60, Components: []string{"client"}, IncludePaths: true})
	if strings.Contains(withPaths["app-logs.jsonl"], "/srv/private/title.mkv") {
		t.Fatalf("paths opt-in must still hide secrets: %s", withPaths["app-logs.jsonl"])
	}
	for _, hidden := range []string{"lost-password", "abcdefghijklmnop", "lost-token", "unlabelled-secret"} {
		if strings.Contains(withPaths["app-logs.jsonl"], hidden) {
			t.Errorf("paths opt-in exported secret %q", hidden)
		}
	}
	defaults := export(SupportExportRequest{Days: 1})
	if _, ok := defaults["app-logs.jsonl"]; ok {
		t.Fatal("app logs were included without opt-in")
	}
	if code := status(t, s, "POST", "/v1/admin/support-export", owner.AccessToken, json.RawMessage(`{"components":[]}`)); code != 400 {
		t.Errorf("empty components: %d", code)
	}
	for _, req := range []SupportExportRequest{{WindowMinutes: 120}, {Components: []string{"everything"}}} {
		if code := status(t, s, "POST", "/v1/admin/support-export", owner.AccessToken, req); code != 400 {
			t.Errorf("invalid choice: %d", code)
		}
	}
}
