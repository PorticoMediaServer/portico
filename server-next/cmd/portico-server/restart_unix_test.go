//go:build !windows

package main

import (
	"os/exec"
	"syscall"
	"testing"
)

// Before the server replaces itself, a child in its own process group (as every media tool
// is started) is ended: replacing the process does not tell a child that dies with it.
func TestRestartEndsChildren(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep command:", err)
	}
	endChildren()
	if err := syscall.Kill(cmd.Process.Pid, 0); err == nil {
		cmd.Process.Kill()
		t.Fatal("a child was still running after endChildren")
	}
}
