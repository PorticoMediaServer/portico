package main

import (
	"bytes"
	"log/slog"
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

// The release packages name the data folder PORTICO_DATA_DIR. A server that read only its
// own name for it would start on an empty folder somewhere else.
func TestThePackagedNameOfASettingWins(t *testing.T) {
	var said bytes.Buffer
	log := slog.New(slog.NewTextHandler(&said, nil))
	if got := packaged(log, "PORTICO_DATA_DIR", "PORTICO_DATA", "data"); got != "data" {
		t.Fatalf("with neither set: %q", got)
	}
	t.Setenv("PORTICO_DATA", "/old")
	if got := packaged(log, "PORTICO_DATA_DIR", "PORTICO_DATA", "data"); got != "/old" || said.Len() != 0 {
		t.Fatalf("with this server's name only: %q %s", got, said.String())
	}
	t.Setenv("PORTICO_DATA_DIR", "/var/lib/portico")
	if got := packaged(log, "PORTICO_DATA_DIR", "PORTICO_DATA", "data"); got != "/var/lib/portico" || !strings.Contains(said.String(), "PORTICO_DATA") {
		t.Fatalf("with both set: %q %s", got, said.String())
	}
}
