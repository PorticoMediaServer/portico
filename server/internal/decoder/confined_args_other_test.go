//go:build !linux

package decoder

import (
	"os/exec"
	"testing"
)

// confinedArguments is the command line itself where confinement wraps the codec
// in place (sandbox-exec on macOS) or does not wrap it at all.
func confinedArguments(t *testing.T, cmd *exec.Cmd) []string {
	t.Helper()
	return cmd.Args
}
