package mediaengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
// the folder the same way as the scan's disc reader.

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

// discPlayable says this server's media tools came with the disc reader.
func discPlayable() bool {
	_, err := exec.LookPath(mediatools.Resolve("portico-disc"))
	return err == nil
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
	source, err := disc.OpenLocalInput(d.input, d.directory)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	found, err := disc.Tool{}.ProbeInput(ctx, source)
	if err != nil {
		return nil, err
	}
	title, ok := found.MainTitle()
	if !ok {
		return nil, disc.ErrProtocol
	}
	input, err := found.FFmpegInput(source.Path(), title, 0, nil)
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
	cmd, err := mediaexec.CommandContext(ctx, source.Job(mediaexec.Job{Executable: ffprobe, Args: args, MaxFileBytes: mediaexec.NoOutputFiles}))
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
	d.probe, d.title, d.facts = found, title, facts
	if found.Kind == "bluray" {
		// Where a copied picture can be cut. A disc reader without the list (an older bundle)
		// leaves the picture converted, as before.
		if points, err := (disc.Tool{}).EntryPointsInput(ctx, source, title, streaming.HLSSegmentSeconds*1000); err == nil {
			for _, ms := range points.EntryPointsMs {
				d.keyframes = append(d.keyframes, float64(ms)/1000)
			}
		}
	}
	return d, nil
}

// open hands a converter the main feature and the title's chapters.
func (d *measuredDisc) open() (*streaming.PlaybackInput, error) {
	source, err := disc.OpenLocalInput(d.input, d.directory)
	if err != nil {
		return nil, streaming.ErrSourceUnavailable
	}
	// The title from its start, for reading a subtitle track out of it.
	start, err := d.probe.FFmpegInput(source.Path(), d.title, 0, nil)
	if err != nil {
		source.Close()
		return nil, err
	}
	job := source.Job(mediaexec.Job{})
	input := &streaming.PlaybackInput{File: source.File, Inherit: len(job.Files) != 0, MountFD: job.InputFD, MountPath: job.InputMount,
		Keyframes: d.keyframes, Options: start.Options, Argument: start.URL}
	if input.Inherit {
		var chapters *os.File
		chapters, err = d.chapters()
		input.Extra = []*os.File{chapters}
	} else {
		input.ChapterMetadata, err = d.title.ChapterMetadata()
	}
	if err != nil {
		input.Close()
		return nil, err
	}
	input.InputAt = func(ctx context.Context, ms int64) ([]string, string, int64, error) {
		var seek *disc.Seek
		if ms > 0 && d.probe.Kind == "bluray" {
			// A Blu-ray title is found at a time by the disc reader, which knows where its clips are.
			found, err := disc.Tool{}.SeekInput(ctx, source, *d.title.Playlist, ms)
			if err != nil {
				return nil, "", 0, err
			}
			seek = &found
		}
		at, err := d.probe.FFmpegInput(source.Path(), d.title, ms, seek)
		return at.Options, at.URL, at.TimestampOffsetMs, err
	}
	return input, nil
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
