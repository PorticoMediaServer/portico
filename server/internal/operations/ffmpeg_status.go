package operations

import (
	"context"
	"time"

	"fmt"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

type FFmpegStatus struct {
	Path            string   `json:"path"`
	State           string   `json:"state"`
	InUse           bool     `json:"inUse"`
	ReasonCode      string   `json:"reasonCode,omitempty"`
	MissingFeatures []string `json:"missingFeatures"`
	CheckedAt       string   `json:"checkedAt"`
}

// FFmpegValidator checks the saved build with the same qualification and
// sandbox as startup. It does not install it or disturb active conversions.
func FFmpegValidator(active string) func(context.Context, string) FFmpegStatus {
	return ffmpegValidator(active, decoder.ProbeToolchain)
}

type ffmpegRecheckKey struct{}

// WithFFmpegRecheck requests an explicit qualification instead of the cached
// status. Authority is still decided by Settings before this is consulted.
func WithFFmpegRecheck(ctx context.Context) context.Context {
	return context.WithValue(ctx, ffmpegRecheckKey{}, true)
}

func ffmpegValidator(active string, probe func(context.Context, string) (decoder.ToolchainFacts, error)) func(context.Context, string) FFmpegStatus {
	lock := make(chan struct{}, 1)
	var previous FFmpegStatus
	var identity string
	var checked bool
	return func(ctx context.Context, path string) FFmpegStatus {
		if path == "" {
			path = active
		}
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return FFmpegStatus{Path: path, State: "unchecked", InUse: path == active, ReasonCode: "ffmpeg.validation.timedOut", MissingFeatures: []string{}, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
		}
		defer func() { <-lock }()
		resolved, err := exec.LookPath(path)
		var fileIdentity string
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(resolved)
			if err == nil {
				fileIdentity = fmt.Sprintf("%s:%d:%d", resolved, info.Size(), info.ModTime().UnixNano())
			}
		}
		force, _ := ctx.Value(ffmpegRecheckKey{}).(bool)
		if checked && !force && path == previous.Path && fileIdentity == identity {
			return previous
		}
		out := FFmpegStatus{Path: path, State: "refused", InUse: path == active, MissingFeatures: []string{}, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			out.ReasonCode = "ffmpeg.validation.unreadable"
		} else {
			bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			facts, e := probe(bounded, path)
			if e != nil {
				out.ReasonCode = "ffmpeg.validation.cannotRun"
				if bounded.Err() != nil {
					out.State = "unchecked"
					out.ReasonCode = "ffmpeg.validation.timedOut"
				}
			} else {
				out.MissingFeatures = facts.MissingRequired()
				if len(out.MissingFeatures) > 0 {
					out.ReasonCode = "ffmpeg.validation.missingFeatures"
				} else {
					out.State = "accepted"
				}
			}
		}
		previous, identity, checked = out, fileIdentity, true
		return out
	}
}
