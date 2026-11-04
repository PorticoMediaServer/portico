package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestDiagnosticsExportScrubsSavedSettingsCredentials(t *testing.T) {
	f := administrationFixtureNew(t)
	ctx := context.Background()
	document, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	const privateFeed = "https://feed-user:feed-pass@updates.example.invalid/releases.json?channel=stable&%61pi_key=short-feed-key"
	values := document.Requested
	values.UpdateFeedURL = privateFeed
	body, err := json.Marshal(operations.SettingsChange{ExpectedRevision: document.Revision, IdempotencyKey: "private-update-feed-export", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.call("PATCH", "/v1/admin/console/settings", f.owner, string(body)); w.Code != 200 {
		t.Fatalf("owner settings PATCH: %d %s", w.Code, w.Body)
	}
	// Other credential stores must not become document inputs to the bundle.
	secrets := []string{"tmdb-fixture-key", "tvdb-fixture-key", "subtitles-fixture-key", "subtitles-fixture-user", "subtitles-fixture-password", "acoustid-fixture-key"}
	if _, err := f.deps.DB.Exec(`UPDATE metadata_runtime_settings SET tmdb_read_token=?,tvdb_project_key=?,opensubtitles_api_key=?,opensubtitles_username=?,opensubtitles_password=?,acoustid_key=? WHERE singleton=1`, secrets[0], secrets[1], secrets[2], secrets[3], secrets[4], secrets[5]); err != nil {
		t.Fatal(err)
	}
	w := f.call("GET", "/v1/admin/diagnostics/bundle", f.owner, "")
	if w.Code != 200 {
		t.Fatalf("owner export: %d %s", w.Code, w.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	secrets = append(secrets, "feed-user", "feed-pass", "short-feed-key", f.owner, f.admin, f.member)
	foundSettings := false
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s exposes a stored credential %q", entry.Name, secret)
			}
		}
		if strings.HasSuffix(entry.Name, ".json") && !json.Valid(raw) {
			t.Errorf("redaction corrupted %s: %s", entry.Name, raw)
		}
		if entry.Name == "settings.json" {
			foundSettings = true
			var exported map[string]any
			if err := json.Unmarshal(raw, &exported); err != nil {
				t.Fatal(err)
			}
			if got, want := exported["updates.feedUrl"], "https://[REDACTED]@updates.example.invalid/releases.json?channel=stable&%61pi_key=[REDACTED]"; got != want {
				t.Errorf("feed diagnostic: got %v want %v", got, want)
			}
			if exported["registryRevision"] != document.RegistryRevision {
				t.Errorf("public registry revision changed: %v", exported["registryRevision"])
			}
		}
	}
	if !foundSettings {
		t.Fatal("export omitted settings.json")
	}
	current, err := f.deps.Console.Settings(ctx, operations.AllowServerScope)
	if err != nil || current.Requested.UpdateFeedURL != privateFeed {
		t.Fatalf("export changed the stored update-feed choice: %v %v", current.Requested.UpdateFeedURL, err)
	}
}
