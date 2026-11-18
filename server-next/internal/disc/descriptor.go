package disc

import (
	"context"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
)

const imageDescriptorPath = "/dev/fd/3"

// LocalInput is a library disc opened read-only. On Linux the child gets only
// the descriptor. Other platforms reopen the admitted path.
type LocalInput struct {
	*os.File
	directory bool
}

// OpenLocalInput opens the disc identified by the scan or playback's library lookup.
func OpenLocalInput(path string, directory bool) (LocalInput, error) {
	if runtime.GOOS != "linux" {
		var err error
		path, err = libraryfile.ResolveReadPath(path, directory)
		if err != nil {
			return LocalInput{}, err
		}
	}
	file, err := os.Open(path)
	return LocalInput{File: file, directory: directory}, err
}

func (in LocalInput) Path() string {
	switch {
	case runtime.GOOS != "linux":
		return in.Name()
	case in.directory:
		return "/disc"
	default:
		return imageDescriptorPath
	}
}

func (in LocalInput) Job(job mediaexec.Job) mediaexec.Job {
	if runtime.GOOS != "linux" {
		job.ReadPaths = append(job.ReadPaths, in.Path())
	} else {
		job.Files = append(job.Files, in.File)
		if in.directory {
			job.InputFD, job.InputMount = 3, in.Path()
		}
	}
	return job
}

func (t Tool) runInput(ctx context.Context, job mediaexec.Job) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	job.Executable = t.Binary
	if job.Executable == "" {
		job.Executable = mediatools.Resolve("portico-disc")
	}
	job.MaxFileBytes = mediaexec.NoOutputFiles
	cmd, err := mediaexec.CommandContext(ctx, job)
	if err != nil {
		return nil, err
	}
	out := &outputBuffer{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err = cmd.Run(); err != nil {
		return nil, err
	}
	return out.data, nil
}

func (t Tool) probeInput(ctx context.Context, job mediaexec.Job) (Probe, error) {
	raw, err := t.runInput(ctx, job)
	if err != nil {
		return Probe{}, err
	}
	return DecodeProbe(raw)
}

func (t Tool) ProbeInput(ctx context.Context, in LocalInput) (Probe, error) {
	return t.probeInput(ctx, in.Job(mediaexec.Job{Args: []string{"probe", in.Path()}}))
}

func (t Tool) SeekInput(ctx context.Context, in LocalInput, playlist int, ms int64) (Seek, error) {
	if playlist < 0 || playlist > 99999 || ms < 0 {
		return Seek{}, ErrProtocol
	}
	args := []string{"seek", in.Path(), strconv.Itoa(playlist), strconv.FormatInt(ms, 10)}
	raw, err := t.runInput(ctx, in.Job(mediaexec.Job{Args: args}))
	if err != nil {
		return Seek{}, err
	}
	return DecodeSeek(raw, playlist, ms)
}

// EntryPointsInput lists a Blu-ray title's entry points, one for each stepMs of it.
func (t Tool) EntryPointsInput(ctx context.Context, in LocalInput, title Title, stepMs int64) (EntryPoints, error) {
	if title.Playlist == nil {
		return EntryPoints{}, ErrProtocol
	}
	args := []string{"entry-points", in.Path(), strconv.Itoa(*title.Playlist), strconv.FormatInt(stepMs, 10)}
	raw, err := t.runInput(ctx, in.Job(mediaexec.Job{Args: args}))
	if err != nil {
		return EntryPoints{}, err
	}
	return DecodeEntryPoints(raw, title, stepMs)
}
