//go:build darwin

package decoder

import "os/exec"

// macOS decoders reach the loopback gateway directly under their sandbox-exec
// profile (network only to that port), so every bridge job is a plain
// mediaexec job; mediaexec gives picture-producing jobs the font pack.
func (d decoderJob) command() (*exec.Cmd, error) { return mediaCommand(d.mediaJob()) }
