//go:build !darwin && !linux

package decoder

import "os/exec"

// No sandbox backend exists here: bridge jobs run with the baseline, and the
// owner's diagnostics say so (mediaexec).
func (d decoderJob) command() (*exec.Cmd, error) { return mediaCommand(d.mediaJob()) }
