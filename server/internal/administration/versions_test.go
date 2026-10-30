package administration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSemanticVersionOrdering(t *testing.T) {
	ordered := []string{"1.2.0-1", "1.2.0-alpha", "1.2.0-alpha.1", "1.2.0-beta.1", "1.2.0-beta.2", "1.2.0-beta.10", "1.2.0-rc.1", "1.2.0", "1.2.1", "2.0.0", "99999999999999999999999999999.0.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			cmp := compareVersions(a, b)
			if i < j && cmp >= 0 || i > j && cmp <= 0 || i == j && cmp != 0 {
				t.Fatalf("%s vs %s: %d", a, b, cmp)
			}
		}
	}
	if compareVersions("1.2.0+build.2", "1.2.0+build.1") != 0 {
		t.Fatal("metadata changed precedence")
	}
	for _, v := range []string{"", "1", "v1.2.0", "1.02.0", "1.2.0-", "1.2.0+", "1.2.0-beta.01", "1.2.0-a..b", "1.2.0+bad+again"} {
		if _, ok := parseVersion(v); ok {
			t.Fatalf("accepted invalid version %q", v)
		}
	}
}
func TestUpdatesSelectsNewestValidPrerelease(t *testing.T) {
	s, _, _ := newService(t)
	s.Fetch = func(context.Context, string) ([]byte, error) {
		return json.Marshal(updateFeed{Channels: map[string][]UpdateBuild{"beta": {{Version: "1.2.0-beta.2"}, {Version: "garbage"}, {Version: "1.2.0-beta.10"}, {Version: "1.2.0-beta.1"}}}})
	}
	got := s.Updates(context.Background(), UpdateBuild{Version: "1.2.0-beta.1"}, "beta", "https://example.invalid")
	if !got.UpdateAvailable || got.Latest.Version != "1.2.0-beta.10" {
		t.Fatalf("%+v", got)
	}
}

// The release pipeline's own writer and this server's reader must agree: the
// manifest a release publishes is the feed an owner points the server at.
func TestUpdatesReadTheManifestTheReleasePipelinePublishes(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "write-public-update-manifest.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(script); err != nil {
		t.Skip("the release scripts are not part of this source tree")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the release manifest writer cannot run")
	}
	work := t.TempDir()
	if err = os.Mkdir(filepath.Join(work, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, script, "1.4.0")
	command.Dir = work
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("the manifest writer failed: %v\n%s", err, output)
	}
	published, err := os.ReadFile(filepath.Join(work, "dist", "portico-update-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, _, _ := newService(t)
	s.Fetch = func(context.Context, string) ([]byte, error) { return published, nil }
	got := s.Updates(context.Background(), UpdateBuild{Version: "1.3.9"}, "stable", "https://example.invalid/portico-update-manifest.json")
	if got.State != "available" || !got.UpdateAvailable || got.Latest == nil || got.Latest.Version != "1.4.0" {
		t.Fatalf("the published manifest was not read as a newer release: %+v", got)
	}
	if got.NotesURL != "https://github.com/PorticoMediaServer/portico-server/releases/tag/v1.4.0" {
		t.Fatalf("the release page was not offered: %q", got.NotesURL)
	}
	if got.AutoInstall {
		t.Fatal("a published manifest turned on automatic installation")
	}
	same := s.Updates(context.Background(), UpdateBuild{Version: "1.4.0"}, "stable", "https://example.invalid/portico-update-manifest.json")
	if same.UpdateAvailable || same.State != "current" {
		t.Fatalf("the running release was reported as out of date: %+v", same)
	}
}
