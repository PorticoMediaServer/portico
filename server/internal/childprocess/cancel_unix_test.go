//go:build !windows

package childprocess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestContextCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	marker := filepath.Join(t.TempDir(), "child")
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 60 & echo $! > "$1"; wait`, "sh", marker)
	Configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer Kill(cmd)
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("child did not start")
	}
	cancel()
	_ = cmd.Wait()
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		// A killed descendant may remain a zombie until init reaps it.
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant survived context cancellation")
}
