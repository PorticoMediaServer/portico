package administration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublishedFeedBytesDriveUpdateNotice(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed to exercise the release feed writer")
	}
	script, err := filepath.Abs("../../../scripts/write-public-update-manifest.mjs")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err = os.Mkdir(filepath.Join(directory, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, script, "1.2.3")
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "dist", "portico-update-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	service := New(nil)
	service.Fetch = func(context.Context, string) ([]byte, error) { return raw, nil }
	report := service.Updates(context.Background(), UpdateBuild{Version: "1.0.0"}, "stable", "https://example.test/feed")
	if !report.UpdateAvailable || report.Latest == nil || report.Latest.Version != "1.2.3" || report.AutoInstall {
		t.Fatal(report)
	}
}
