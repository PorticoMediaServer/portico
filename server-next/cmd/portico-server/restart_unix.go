//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// restartSelf replaces this process with a new run of the same program: the same process id,
// so a service manager or a container sees no change. Children are ended first: a child
// that dies with the server (converters, probe workers) is told so only when the process
// exits, which replacing it is not.
func restartSelf() error {
	endChildren()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// After the program file was replaced (an update copied over it), Linux names the running
	// one "<path> (deleted)": the new file at that path is the one to start.
	self = strings.TrimSuffix(self, " (deleted)")
	return syscall.Exec(self, os.Args, os.Environ())
}

// endChildren kills each child of this process and the process group it leads, then gives
// them a moment to go.
func endChildren() {
	children := childPIDs()
	for _, pid := range children {
		syscall.Kill(-pid, syscall.SIGKILL) // its group: what a confined tool started
		syscall.Kill(pid, syscall.SIGKILL)
	}
	for deadline := time.Now().Add(2 * time.Second); len(children) > 0 && time.Now().Before(deadline); {
		var status syscall.WaitStatus
		if pid, _ := syscall.Wait4(-1, &status, syscall.WNOHANG, nil); pid <= 0 {
			time.Sleep(20 * time.Millisecond)
			if len(childPIDs()) == 0 {
				return
			}
		}
	}
}

// childPIDs lists this process's children: from /proc on Linux, from ps elsewhere.
func childPIDs() []int {
	var out []int
	if lists, _ := filepath.Glob("/proc/self/task/*/children"); len(lists) > 0 {
		for _, name := range lists {
			raw, _ := os.ReadFile(name)
			for _, f := range strings.Fields(string(raw)) {
				if pid, err := strconv.Atoi(f); err == nil {
					out = append(out, pid)
				}
			}
		}
		return out
	}
	raw, _ := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	me := os.Getpid()
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			pid, _ := strconv.Atoi(f[0])
			if ppid, _ := strconv.Atoi(f[1]); ppid == me && pid > 0 {
				out = append(out, pid)
			}
		}
	}
	return out
}
