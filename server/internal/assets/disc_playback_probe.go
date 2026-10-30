package assets

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
)

// InspectDiscPlayback measures the selected title from a caller-retained input.
// This is not the scan pathname adapter: it requires inherited custody and a
// mandatory original authority checker, and never opens an origin or updates
// catalogue facts. The caller retains the input through accepting publication.
func (p Probe) InspectDiscPlayback(ctx context.Context, input *PlaybackInput, structure disc.Probe, title disc.Title) (Facts, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || input == nil || !input.Inherit || input.Validate == nil || input.InputAt == nil || input.Lifetime == nil || input.Lifetime.Done() == nil {
		return Facts{}, disc.ErrProtocol
	}
	raw, err := json.Marshal(structure)
	if err != nil {
		return Facts{}, disc.ErrProtocol
	}
	structure, err = disc.DecodeProbe(raw)
	if err != nil {
		return Facts{}, err
	}
	// The validated reader enforces exact title membership before any child is
	// launched. Do not accept a valid-looking title number from another probe.
	if _, err = structure.FFmpegInput(disc.DescriptorPath(false), title, 0, nil); err != nil {
		return Facts{}, err
	}
	run, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	run, release := input.BorrowContext(run)
	defer release()
	if err = input.Validate(run); err != nil {
		return Facts{}, err
	}
	options, argument, _, err := input.InputAt(run, 0)
	if err != nil {
		return Facts{}, err
	}
	if input.File == nil {
		return Facts{}, disc.ErrProtocol
	}
	if _, err = input.File.Seek(0, io.SeekStart); err != nil && input.Argument != "pipe:3" {
		return Facts{}, err
	}
	args := append([]string{"-probesize", "8388608", "-analyzeduration", "5000000"}, options...)
	args = append(args, "-v", "error", "-show_format", "-show_streams", "-show_chapters", "-of", "json", argument)
	binary := p.Binary
	if binary == "" {
		binary = mediatools.Resolve("ffprobe")
	}
	cmd, err := mediaexec.CommandContext(run, mediaexec.Job{Executable: binary, Args: args, Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, MaxFileBytes: mediaexec.NoOutputFiles})
	if err != nil {
		return Facts{}, err
	}
	out := &boundedBuffer{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = out, io.Discard, time.Second
	if err = input.Validate(run); err != nil {
		return Facts{}, err
	}
	if err = cmd.Run(); err != nil {
		return Facts{}, err
	}
	if err = input.Validate(run); err != nil {
		return Facts{}, err
	}
	return decodeDiscMedia(out.data, title)
}
