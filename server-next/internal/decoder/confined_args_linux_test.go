//go:build linux

package decoder

import (
	"os/exec"
	"testing"
)

// confinedArguments is the codec invocation a confined command will run. On
// Linux it travels inside the helper's encoded specification, so tests that
// inspect what a probe would run must decode it rather than read cmd.Args.
func confinedArguments(t *testing.T, cmd *exec.Cmd) []string {
	t.Helper()
	if len(cmd.Args) < 3 || cmd.Args[1] != "--portico-decoder-linux" {
		// No bubblewrap here: the probe runs the trusted binary directly.
		return cmd.Args
	}
	spec, err := decodeLinux(cmd.Args[2])
	if err != nil {
		t.Fatal("confined command carries an invalid specification", err)
	}
	return append([]string{spec.Executable}, spec.Arguments...)
}
