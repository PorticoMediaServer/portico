package main

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

// A packaged server that is given no data folder keeps its data where the package's kind
// says, never in whatever folder it was started in.
func TestDefaultDataFolder(t *testing.T) {
	for _, c := range []struct {
		goos   string
		env    map[string]string
		docker bool
		want   string
	}{
		{"linux", nil, false, "/home/a/.local/share/portico"},
		{"linux", map[string]string{"PORTICO_SERVICE": "1"}, false, "/var/lib/portico"},
		{"linux", map[string]string{"PORTICO_CONTAINER": "1"}, false, "/config"},
		{"linux", nil, true, "/config"},
		{"darwin", nil, false, "/home/a/Library/Application Support/Portico"},
		{"windows", map[string]string{"LOCALAPPDATA": `C:\Users\a\AppData\Local`}, false, `C:\Users\a\AppData\Local\Portico`},
		{"windows", map[string]string{"PORTICO_SERVICE": "1", "LOCALAPPDATA": `C:\x`}, false, `C:\ProgramData\Portico`},
	} {
		got, err := dataFolder(c.goos, func(name string) string { return c.env[name] }, "/home/a", c.docker)
		if err != nil || got != c.want {
			t.Errorf("%s %v: %q %v, want %q", c.goos, c.env, got, err, c.want)
		}
	}
	if _, err := dataFolder("linux", func(string) string { return "" }, "", false); err == nil {
		t.Error("no home folder and no data folder given was accepted")
	}
}

func TestAnEarlierServersDataFolderIsRefused(t *testing.T) {
	data := t.TempDir()
	if err := oldServerData(data); err != nil {
		t.Fatalf("an empty folder: %v", err)
	}
	os.Mkdir(filepath.Join(data, "Database"), 0o700)
	os.WriteFile(filepath.Join(data, "Database", "server.sqlite"), nil, 0o600)
	if err := oldServerData(data); err == nil {
		t.Fatal("a folder holding the earlier server's database was accepted")
	}
	os.WriteFile(filepath.Join(data, "main.db"), nil, 0o600)
	if err := oldServerData(data); err != nil {
		t.Fatalf("a folder this server already uses: %v", err)
	}
}

// The container's health check: ready is 0, anything else is not.
func TestHealthCheckAsksTheRunningServer(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/ready" {
			t.Errorf("asked %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	// The packaged address names every interface; the check reaches the server on this machine.
	if err := healthCheck("0.0.0.0:" + port); err != nil {
		t.Fatalf("a ready server: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthCheck(":" + port); err == nil {
		t.Fatal("a server that is not ready passed")
	}
	srv.Close()
	if err := healthCheck("127.0.0.1:" + port); err == nil {
		t.Fatal("no server passed")
	}
}
