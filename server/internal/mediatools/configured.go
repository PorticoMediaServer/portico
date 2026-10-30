package mediatools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
)

type configuredPair struct{ ffmpeg, ffprobe string }

var activePair atomic.Pointer[configuredPair]

// ConfigurePair installs startup-qualified tools for all later consumers,
// including helpers that resolve a tool lazily instead of receiving one.
func ConfigurePair(ffmpeg, ffprobe string) error {
	paths := []string{ffmpeg, ffprobe}
	for i, path := range paths {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(resolved) {
			return fmt.Errorf("configured media tool is not an absolute path")
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("configured media tool is not a regular file")
		}
		paths[i] = resolved
	}
	activePair.Store(&configuredPair{paths[0], paths[1]})
	return nil
}
