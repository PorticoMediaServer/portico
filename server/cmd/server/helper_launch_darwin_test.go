package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func helperProcessStart() (int64, string) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return 0, err.Error()
	}
	return time.Unix(p.Proc.P_starttime.Sec, int64(p.Proc.P_starttime.Usec)*1000).UnixNano(), ""
}

// Timeout-only, read-only tools. No helper execution, signing, xattr mutation,
// assessment bypass or signals to tracked helpers. Tool subprocesses have their
// own deadlines. Process names exclude argv/environment. Each command has a
// 250ms deadline inside one 2s diagnostic budget; missing data stays labelled.
func logHelperLaunchDiagnostics(t *testing.T, binary string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	run := func(label, tool string, args ...string) string {
		call, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		defer stop()
		cmd := exec.CommandContext(call, tool, args...)
		cmd.WaitDelay = 50 * time.Millisecond
		var output boundedLaunchOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		at := time.Now()
		err := cmd.Run()
		t.Logf("empty-source launch %s at=%s elapsed=%s error=%v context=%v bytes=%d/4096 truncated=%t\n%s",
			label, at.Format(time.RFC3339Nano), time.Since(at), err, call.Err(), len(output.data), output.truncated, output.data)
		return string(output.data)
	}
	pids := run("direct-child-pids", "/usr/bin/pgrep", "-P", strconv.Itoa(os.Getpid()))
	var selected []string
	for _, raw := range strings.Fields(pids) {
		if pid, err := strconv.Atoi(raw); err == nil && pid > 0 && len(selected) < 8 {
			selected = append(selected, strconv.Itoa(pid))
		}
	}
	if len(selected) > 0 {
		run("child-process-birth-and-image", "/bin/ps", "-p", strings.Join(selected, ","), "-o", "pid=,ppid=,lstart=,state=,comm=")
	}
	for _, attribute := range []string{"com.apple.quarantine", "com.apple.provenance"} {
		run(fmt.Sprintf("xattr-%s-hex", attribute), "/usr/bin/xattr", "-px", attribute, binary)
	}
	run("codesign-display", "/usr/bin/codesign", "--display", "--verbose=2", binary)
	run("codesign-verify", "/usr/bin/codesign", "--verify", "--strict", "--verbose=2", binary)
}
