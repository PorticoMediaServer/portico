package assets

import (
	"net/url"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// probeJob is an ffprobe outside the scanner's descriptor hand-off: a detail
// refresh reads one library file by path, a remote probe reads a private
// loopback bridge. Either way the probe may read that one input and nothing
// else (mediaexec).
func probeJob(binary string, args []string, input string) mediaexec.Job {
	job := mediaexec.Job{Executable: binary, Args: args}
	if u, err := url.Parse(input); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if u.Hostname() == "127.0.0.1" {
			job.Loopback = u.Host
		}
		return job
	}
	if filepath.IsAbs(input) && filepath.Clean(input) == input {
		job.ReadPaths = []string{input}
	}
	return job
}
