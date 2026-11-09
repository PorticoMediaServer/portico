// Package physicalrun is the values-only composition port. It imports no
// concrete storage/dbwork implementation or SQLite function registration.
package physicalrun

import (
	"context"
	"io"
	"os/exec"
)

type Runner interface {
	RunOwnedLinear(context.Context, string, *exec.Cmd, func(io.Reader) error, func(), func()) error
}
