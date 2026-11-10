package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// The sandbox around ffmpeg runs this binary again as its helper. The real
// binary must answer that itself: a helper request it cannot use is refused by
// the helper, never passed to the server's own flags or allowed to start a server.
func TestServerBinaryAnswersTheSandboxHelper(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "portico-server")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(binary, mediaexec.ShimFlag, "not-a-job", "--", "/bin/true").CombinedOutput()
	if err == nil || !strings.Contains(string(out), mediaexec.ErrInvalidJob.Error()) || strings.Contains(string(out), "flag provided") {
		t.Fatalf("the helper request was not answered by the helper: %v\n%s", err, out)
	}
}
