package scan

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Prober reads the inside of one media file: what a file is, how long it is and its tracks.
// It reads headers only and decodes nothing.
type Prober interface {
	Probe(ctx context.Context, path string) (Facts, error)
}

// Facts is what a file was found to hold.
type Facts struct {
	Container  string
	DurationMS int64
	Bitrate    int64
	Streams    []db.Stream
	Chapters   []db.Marker // StartMS, EndMS and Title; the rest is filled in when stored
}

// What a Prober says about a file it cannot give facts for. Any other error means the file
// could not be looked at just now: it keeps the title its name gave it and is tried again.
var (
	ErrDamaged     = errors.New("the file is damaged")
	ErrUnsupported = errors.New("the file is of a kind that is not supported")
	ErrNotMedia    = errors.New("the file is not a media file")
)

// probe reads every media file of the library that has not been read, a few at a time.
func (r *run) probe() error {
	var after int64
	for {
		files, err := r.s.DB.UnprobedFiles(r.ctx, r.lib.ID, after, r.batch)
		if err != nil || len(files) == 0 {
			return err
		}
		// A few files are read at once: each read is a confined tool at low priority, and
		// one at a time would make a large library's first scan take several times as long.
		results := make([]*db.Probe, len(files))
		var wg sync.WaitGroup
		slots := make(chan struct{}, max(1, min(4, runtime.NumCPU()/2)))
		for i, file := range files {
			after = file.ID
			local := r.source(file.Root).LocalPath(file.Rel)
			if local == "" {
				continue
			}
			slots <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-slots; wg.Done() }()
				results[i] = r.probeFile(file, local)
			}()
		}
		wg.Wait()
		if err := r.ctx.Err(); err != nil {
			return err
		}
		probes := make([]db.Probe, 0, len(files))
		for _, probe := range results {
			if probe == nil {
				continue
			}
			if probe.Problem == db.ProblemNone {
				r.stats.Probed++
			}
			probes = append(probes, *probe)
		}
		if err := r.s.DB.StoreProbes(r.ctx, probes); err != nil {
			return err
		}
		if r.s.Progress != nil {
			r.s.Progress(r.stats)
		}
	}
}

// probeFile reads one file. It returns nothing for a file that could not be looked at just
// now, which is tried again on the next scan.
func (r *run) probeFile(file db.UnprobedFile, local string) *db.Probe {
	if r.ctx.Err() != nil {
		return nil
	}
	probe := &db.Probe{FileID: file.ID, Size: file.Size, ModifiedNS: file.ModifiedNS}
	facts, err := r.s.Prober.Probe(r.ctx, local)
	switch {
	case err == nil:
		probe.Container, probe.DurationMS, probe.Bitrate = facts.Container, facts.DurationMS, facts.Bitrate
		probe.Streams, probe.Chapters = facts.Streams, facts.Chapters
	case errors.Is(err, ErrDamaged):
		probe.Problem = db.ProblemDamaged
	case errors.Is(err, ErrUnsupported):
		probe.Problem = db.ProblemUnsupported
	case errors.Is(err, ErrNotMedia):
		probe.Problem = db.ProblemNotMedia
	default:
		r.s.log().Warn("file not read", "file", local, "error", err)
		return nil
	}
	return probe
}
