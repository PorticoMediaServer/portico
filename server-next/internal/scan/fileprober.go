package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// FileProber is the Prober a server runs: ffprobe, confined to the one file, reading its
// headers. The program that uses it must answer the sandbox's helper request in its main
// (mediaexec.RunHelper), as the server binary does.
type FileProber struct{}

// pooled runs the tool on a long-lived confined worker that is handed the open file and
// never its path, so a scan does not build a sandbox for every file. Where there is no such
// worker (not Linux, or no sandbox) it reports false and the tool is run on the path.
func pooled(ctx context.Context, binary string, args []string, path string, mode mediafacts.ScanMode, out io.Writer) (bool, error) {
	if mode != mediafacts.ScanFile || runtime.GOOS != "linux" {
		return false, nil
	}
	args = slices.Clone(args)
	at := slices.Index(args, path)
	if at < 0 {
		return false, nil
	}
	args[at] = "/proc/self/fd/3"
	job := mediaexec.Job{Executable: binary, Args: args, Background: true, MaxCPUSeconds: 300}
	if !mediaexec.ProbePoolAccepts(job) {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return true, err
	}
	defer file.Close()
	result, handled, err := mediaexec.RunPooledProbe(ctx, mediaexec.PooledProbe{Job: job, Input: file, OutputLimit: 4 << 20, LowPriority: true})
	if !handled || err != nil {
		return handled, err
	}
	if _, err := out.Write(result.Stdout); err != nil {
		return true, err
	}
	if result.Err != nil {
		return true, fmt.Errorf("%w: %v", errToolRefused, result.Err)
	}
	return true, nil
}

// errToolRefused says the tool ran on a pooled worker and could not make sense of the file.
var errToolRefused = errors.New("the media tool could not read the file")

func (FileProber) Probe(ctx context.Context, path string) (Facts, error) {
	// The confined tool is given one real file, so a link is resolved first. The path came
	// from the scan's own walk of a library folder, never from a request.
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Facts{}, err
	}
	found, err := mediafacts.Probe{ScanCommand: pooled}.Inspect(ctx, real)
	return factsOf(ctx, found, err)
}

// RemoteProber is the ReaderProber a server runs: ffprobe on the decoder's isolated bridge,
// able to reach only the one loopback address that serves the file by range (package
// inputbridge), never told where the file is.
type RemoteProber struct {
	// Processes supervises the probes; nil uses one shared by every RemoteProber.
	Processes *processpool.Supervisor
}

// A scan reads four files at once by itself. A limit of four here as well would refuse, not
// queue, the probes of a second remote library scanned at the same time.
var remoteProbes = &processpool.Supervisor{}

func (p RemoteProber) ProbeReader(ctx context.Context, file io.ReaderAt, size int64) (Facts, error) {
	processes := p.Processes
	if processes == nil {
		processes = remoteProbes
	}
	found, err := inputbridge.Inspect(ctx, processes, "", file, size)
	return factsOf(ctx, found, err)
}

// factsOf turns what ffprobe found into a file's facts, or says why there are none.
func factsOf(ctx context.Context, found mediafacts.Facts, err error) (Facts, error) {
	var exit *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return Facts{}, ctx.Err()
	case errors.Is(err, mediafacts.ErrNoTimedStream):
		return Facts{}, ErrNotMedia
	case errors.As(err, &exit), errors.Is(err, errToolRefused):
		// The tool ran and could not make sense of the file.
		return Facts{}, ErrDamaged
	default:
		// The tool could not be run or took too long: try the file again next scan.
		return Facts{}, err
	}
	facts := Facts{Container: found.Container, DurationMS: int64(math.Round(found.Duration * 1000)), Bitrate: found.BitRate}
	for _, s := range found.Streams {
		d := s.Detail
		stream := db.Stream{Codec: s.Codec, Profile: d.Profile, Language: s.Language, Title: s.Title, Default: s.Default, Forced: s.Forced,
			HearingImpaired: s.HearingImpaired, Bitrate: d.BitRate, Channels: s.Channels, ChannelLayout: s.ChannelLayout, SampleRate: d.SampleRate}
		switch s.Type {
		case "video":
			stream.Kind = db.StreamVideo
			stream.Width, stream.Height, stream.FrameRate, stream.BitDepth = d.Width, d.Height, d.FrameRate, d.BitDepth
			if d.DynamicRange != mediafacts.RangeSDR {
				stream.HDR = d.DynamicRange
			}
		case "audio":
			stream.Kind = db.StreamAudio
		case "subtitle":
			stream.Kind = db.StreamSubtitle
		default:
			continue
		}
		facts.Streams = append(facts.Streams, stream)
	}
	for _, c := range found.Chapters {
		facts.Chapters = append(facts.Chapters, db.Marker{StartMS: int64(math.Round(c.Start * 1000)), EndMS: int64(math.Round(c.End * 1000)), Title: c.Title})
	}
	return facts, nil
}
