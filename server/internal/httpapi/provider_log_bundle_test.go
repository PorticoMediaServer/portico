package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

func TestDiagnosticsBundleScrubsEveryHistoricalLogEntry(t *testing.T) {
	directory := t.TempDir()
	const public = "https://provider.invalid/live/news/sports/master.m3u8"
	for name, message := range map[string]string{
		"messages.log":   "active https://provider.invalid/live/short-user/short-pass/7.ts " + public,
		"messages.1.log": "rotated https://provider.invalid/live/rotated-user/rotated-pass/8.ts https://api.acoustid.org/v2/lookup?client=private-client-key&duration=42 " + public,
	} {
		line := time.Now().UTC().Format(time.RFC3339Nano) + "\tinfo\tserver\t" + message + "\n"
		if err := os.WriteFile(filepath.Join(directory, name), []byte(line), 0600); err != nil {
			t.Fatal(err)
		}
	}
	recorder := servicelog.New(servicelog.Options{Directory: directory})
	t.Cleanup(func() { recorder.Close() })
	f := administrationFixtureNewWith(t, func(d *Dependencies) { d.Access.Logs = recorder })
	w := f.call("GET", "/v1/admin/diagnostics/bundle", f.owner, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("owner export: %d %s", w.Code, w.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"messages.json": false, "logs/messages.log": false, "logs/messages.1.log": false}
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"short-user", "short-pass", "rotated-user", "rotated-pass", "private-client-key"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Errorf("archive entry %s exposes %s: %s", entry.Name, secret, body)
			}
		}
		if _, log := want[entry.Name]; log {
			want[entry.Name] = true
			if !strings.Contains(string(body), "[REDACTED]") || !strings.Contains(string(body), public) {
				t.Errorf("archive entry %s lost redaction or ordinary diagnostics: %s", entry.Name, body)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("archive did not contain %s", name)
		}
	}
}
