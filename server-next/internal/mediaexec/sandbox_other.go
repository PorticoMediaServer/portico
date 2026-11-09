//go:build !darwin && !linux

package mediaexec

import (
	"fmt"
	"runtime"
)

func probeSandbox(string) (string, bool, error) {
	return "", false, fmt.Errorf("Portico has no decoder sandbox for %s yet", runtime.GOOS)
}

func sandboxArgv(Job, Posture) ([]string, error) { return nil, ErrUnsupported }
