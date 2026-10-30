// Package childprocess makes a child process die with the server that started
// it.
//
// Setpgid was set in exactly two packages, and the media pipeline set none:
// ffmpeg for conversion, audio rendering, VOD encoding and artwork extraction
// all ran in the server's own process group. Killing one of those kills the
// process, not the tree it started, and under the sandbox wrappers — sandbox-exec
// on macOS, a helper on Linux — killing the wrapper does not kill the wrapped
// ffmpeg at all.
//
// Windows had no equivalent of any kind: it does not propagate termination to
// children, so the helper's rclone survived the helper, holding a mount, and
// accumulated across restarts.
package childprocess

import "os/exec"

// Configure prepares a command so it can be killed as a tree. Call it before
// Start, in place of setting SysProcAttr by hand.
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	configure(cmd)
	// CommandContext installs Cancel; ordinary exec.Command rejects a non-nil
	// Cancel because it has no context to monitor.
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { Kill(cmd); return nil }
	}
}

// Kill ends the command and everything it started. On Unix that is the process
// group, which is what makes the sandbox wrappers safe; on Windows the job
// object the whole server runs in does it at process exit, and this kills the
// immediate child.
func Kill(cmd *exec.Cmd) { kill(cmd) }

// AdoptTree makes every child of this process die with it. It is called once, at
// startup, and is a no-op where the OS already behaves that way.
func AdoptTree() error { return adoptTree() }
