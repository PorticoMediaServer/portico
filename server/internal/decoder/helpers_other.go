//go:build !linux

package decoder

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func configureInheritedFiles(*exec.Cmd)         {}
func RunHelper(args []string) (bool, error)     { return mediaexec.RunHelper(args) }
func CheckConfinement(ctx context.Context) bool { return ProbeConfinement(ctx) == nil }

// ProbeConfinement says why decoders can't be sandboxed here, or nil.
func ProbeConfinement(context.Context) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%w: no decoder sandbox is supported on %s", ErrConfinementUnavailable, runtime.GOOS)
	}
	if _, e := exec.LookPath("/usr/bin/sandbox-exec"); e != nil {
		return fmt.Errorf("%w: /usr/bin/sandbox-exec is missing", ErrConfinementUnavailable)
	}
	return nil
}

// bridgeSandbox is the Linux helper sandbox; other platforms don't have it.
func bridgeSandbox() bool { return false }
