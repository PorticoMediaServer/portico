package mediaengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/disc"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// A film disc, kept as a folder (BDMV or VIDEO_TS) or as an image, plays its main feature, the
// title the scan listed (scanning rule 40), always as the server's stream: no app can play the
// disc itself. FFmpeg reads the title with its own Blu-ray and DVD reading, handed the image or
// the folder itself and never its path, the way the scan's disc reader is.

// measuredDisc is a disc's main feature: the disc as the disc reader found it, the title it
// plays, and what ffprobe finds in that title. keyframes, for a Blu-ray, are its entry points
// as the disc reader listed them (streaming.PlaybackInput.Keyframes); a DVD has none, and its
// picture is converted.
type measuredDisc struct {
	// input is the image, or the folder when directory is set.
	input     string
	directory bool
	probe     disc.Probe
	title     disc.Title
	facts     mediafacts.Facts
	keyframes []float64
}

// discPlayable says this server can play discs: its media tools came with the disc reader, and
// it runs on Linux, the one sandbox that can hand a converter a folder.
func discPlayable() bool {
	_, err := exec.LookPath(mediatools.Resolve("portico-disc"))
	return err == nil && runtime.GOOS == "linux"
}

// measureDisc reads a disc's main feature, once until the image or the folder's index file changes.
func (e *Engine) measureDisc(ctx context.Context, index string, place db.FileOnDisk) (*measuredDisc, error) {
	key := fmt.Sprint(index, "\x00", place.Size, "\x00", place.ModifiedNS)
	e.mu.Lock()
	d := e.discs[key]
	e.mu.Unlock()
	if d != nil {
		return d, nil
	}
	d, err := e.readDisc(ctx, index)
	switch {
	case errors.Is(err, mediaexec.ErrSandboxRequired):
		// The owner turned the sandbox off, and a folder cannot be handed over without it.
		return nil, playback.ErrDisc
	case err != nil:
		return nil, playback.ErrSourceUnavailable
	}
	e.mu.Lock()
	if len(e.discs) > 200 {
		clear(e.discs)
	}
	e.discs[key] = d
	e.mu.Unlock()
	return d, nil
}

func (e *Engine) readDisc(ctx context.Context, index string) (*measuredDisc, error) {
	d := &measuredDisc{}
	d.input, d.directory = scan.DiscInput(index)
	file, err := os.Open(d.input)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	found, err := disc.Tool{}.ProbeDescriptor(ctx, file, d.directory)
	if err != nil {
		return nil, err
	}
	title, ok := found.MainTitle()
	if !ok {
		return nil, disc.ErrProtocol
	}
	input, err := found.FFmpegInput(disc.DescriptorPath(d.directory), title, 0, nil)
	if err != nil {
		return nil, err
	}
	ffprobe := e.probe.Binary
	if ffprobe == "" {
		ffprobe = mediatools.Resolve("ffprobe")
	}
	args := append(append([]string{"-v", "error"}, input.Options...), "-show_format", "-show_streams", "-of", "json", input.URL)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, err := mediaexec.CommandContext(ctx, d.job(mediaexec.Job{Executable: ffprobe, Args: args, Files: []*os.File{file}, MaxFileBytes: mediaexec.NoOutputFiles}))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &capped{w: &out, left: 1 << 20, full: cancel}, io.Discard, time.Second
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	facts, err := mediafacts.DecodeProbe(out.Bytes())
	if err != nil {
		return nil, err
	}
	// The disc reader's length is the title's, where the stream's own can be a guess.
	facts.Duration = float64(title.DurationMs) / 1000
	// The disc's own subtitles are not offered: the server reads them out of a file, and the
	// title is no file. Subtitles downloaded for the film are.
	facts.Streams = slices.DeleteFunc(facts.Streams, func(s mediafacts.Stream) bool { return s.Type == "subtitle" })
	d.probe, d.title, d.facts = found, title, facts
	if found.Kind == "bluray" {
		// Where a copied picture can be cut. A disc reader without the list (an older bundle)
		// leaves the picture converted, as before.
		if points, err := (disc.Tool{}).EntryPointsDescriptor(ctx, file, d.directory, title, streaming.HLSSegmentSeconds*1000); err == nil {
			for _, ms := range points.EntryPointsMs {
				d.keyframes = append(d.keyframes, float64(ms)/1000)
			}
		}
	}
	return d, nil
}

// job has the sandbox mount the folder, descriptor 3, for a disc kept as a folder. An image is
// read through descriptor 3 itself.
func (d *measuredDisc) job(job mediaexec.Job) mediaexec.Job {
	if d.directory {
		job.InputFD, job.InputMount = 3, disc.DescriptorPath(true)
	}
	return job
}

// open hands one converter run the main feature: the image or the folder (which the sandbox
// mounts) on descriptor 3, the title's chapters on descriptor 4, and the title's input from the
// run's start.
func (d *measuredDisc) open() (*streaming.PlaybackInput, error) {
	file, err := os.Open(d.input)
	if err != nil {
		return nil, streaming.ErrSourceUnavailable
	}
	chapters, err := d.chapters()
	if err != nil {
		file.Close()
		return nil, err
	}
	mount := d.job(mediaexec.Job{})
	return &streaming.PlaybackInput{File: file, Inherit: true, MountFD: mount.InputFD, MountPath: mount.InputMount, Extra: []*os.File{chapters}, Keyframes: d.keyframes,
		InputAt: func(ctx context.Context, ms int64) ([]string, string, int64, error) {
			var seek *disc.Seek
			if ms > 0 && d.probe.Kind == "bluray" {
				// A Blu-ray title is found at a time by the disc reader, which knows where its clips are.
				found, err := disc.Tool{}.SeekDescriptor(ctx, file, d.directory, *d.title.Playlist, ms)
				if err != nil {
					return nil, "", 0, err
				}
				seek = &found
			}
			input, err := d.probe.FFmpegInput(disc.DescriptorPath(d.directory), d.title, ms, seek)
			return input.Options, input.URL, input.TimestampOffsetMs, err
		}}, nil
}

// chapters is the title's chapters as the converter reads them, in a file of its own that is
// gone from the folder as soon as it is made.
func (d *measuredDisc) chapters() (*os.File, error) {
	text, err := d.title.ChapterMetadata()
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "portico-chapters-*")
	if err != nil {
		return nil, err
	}
	os.Remove(file.Name())
	if _, err = file.Write(text); err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
