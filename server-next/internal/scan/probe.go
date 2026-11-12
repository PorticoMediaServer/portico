package scan

import (
	"context"
	"errors"
	"io"
	"path"
	"runtime"
	"slices"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Prober reads the inside of one media file: what a file is, how long it is and its tracks.
// It reads headers only and decodes nothing.
type Prober interface {
	Probe(ctx context.Context, path string) (Facts, error)
}

// ReaderProber does the same for a file that is not on a disk here, given a way to read
// ranges of it and its size. It asks for the parts that hold the file's facts, never the
// whole file.
type ReaderProber interface {
	ProbeReader(ctx context.Context, file io.ReaderAt, size int64) (Facts, error)
}

// Facts is what a file was found to hold.
type Facts struct {
	Packets    *db.Packets
	Container  string
	DurationMS int64
	Bitrate    int64
	// Title is the title written inside the file, if it has one that reads like a title. It
	// is used only where nothing better names the title: see db.Probe.
	Title    string
	Streams  []db.Stream
	Chapters []db.Marker // StartMS, EndMS and Title; the rest is filled in when stored
}

// What a Prober says about a file it cannot give facts for. Any other error means the file
// could not be looked at just now: it keeps the title its name gave it and is tried again.
var (
	ErrDamaged     = errors.New("the file is damaged")
	ErrUnsupported = errors.New("the file is of a kind that is not supported")
	ErrEncrypted   = errors.New("the disc is copy-protected")
	// ErrNoDiscReader: the file stands for a film disc and this server's media tools have no
	// disc reader. The disc is listed with that reason and tried again by every scan, so it
	// is read as soon as the server has one.
	ErrNoDiscReader = errors.New("this server's media tools can't read discs")
	ErrNotMedia     = errors.New("the file is not a media file")
)

// probe reads every media file of the library that has not been read, a few at a time. Where
// the library's scan depth and a remote source's preset differ, the lower wins: a library on
// File list only, or a source on Just list the files, has nothing read.
func (r *run) probe() error {
	// How many files there are to read is counted once, here: it includes what an earlier
	// scan left unread, which the walk of this one did not see as new.
	unread, err := r.s.DB.UnprobedCount(r.ctx, r.lib.ID)
	if err != nil {
		return err
	}
	r.stats.Unread = unread
	if r.s.Progress != nil {
		r.s.Progress(r.stats)
	}
	var after int64
	for {
		files, err := r.s.DB.UnprobedFiles(r.ctx, r.lib.ID, after, r.batch)
		if err != nil || len(files) == 0 {
			if err == nil {
				r.stats.Unread = 0
			}
			return err
		}
		// Up to four files are read at once: each read is a confined tool at low priority, so
		// it gives way to playback, and one at a time would make a large library's first
		// scan take several times as long.
		// The progress names the oldest file still being read: the one a scan that sits on
		// one file is sitting on.
		results := make([]*db.Probe, len(files))
		var wg sync.WaitGroup
		var reading sync.Mutex
		var underWay []string // in the order they were taken up
		taken := func(rel string, starts bool) {
			reading.Lock()
			defer reading.Unlock()
			if starts {
				underWay = append(underWay, rel)
			} else if at := slices.Index(underWay, rel); at >= 0 {
				underWay = slices.Delete(underWay, at, at+1)
			}
			if len(underWay) > 0 && r.stats.Current != underWay[0] {
				r.stats.Current = underWay[0]
				if r.s.Progress != nil {
					r.s.Progress(r.stats)
				}
			}
		}
		slots := make(chan struct{}, min(4, runtime.NumCPU()))
		for i, file := range files {
			after = file.ID
			src := r.source(file.PathID)
			local := src.LocalPath(file.Rel)
			if remote, ok := src.(Remote); local == "" && (r.s.ReaderProber == nil || ok && !remote.readsFiles()) || local != "" && r.s.Prober == nil {
				continue
			}
			slots <- struct{}{}
			taken(file.Rel, true)
			wg.Add(1)
			go func() {
				defer func() { taken(file.Rel, false); <-slots; wg.Done() }()
				results[i] = r.probeFile(file, src, local)
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
		r.stats.Unread = max(r.stats.Unread-len(files), 0)
		if r.s.Progress != nil {
			r.s.Progress(r.stats)
		}
	}
}

// probeFile reads one file. It returns nothing for a file that could not be looked at just
// now, which is tried again on the next scan.
func (r *run) probeFile(file db.UnprobedFile, src Source, local string) *db.Probe {
	if r.ctx.Err() != nil {
		return nil
	}
	probe := &db.Probe{FileID: file.ID, Size: file.Size, ModifiedNS: file.ModifiedNS}
	var facts Facts
	var err error
	if local != "" {
		facts, err = r.s.Prober.Probe(r.ctx, local)
	} else {
		// Not on a disk here: the file is opened through its source, which reads only the
		// ranges the prober asks for, each one a request within the source's limits.
		local = path.Join(file.Root, file.Rel)
		var opened File
		if opened, err = src.Open(r.ctx, file.Rel); err == nil {
			facts, err = r.s.ReaderProber.ProbeReader(r.ctx, opened, opened.Size())
			opened.Close()
		}
	}
	switch {
	case err == nil:
		probe.Container, probe.DurationMS, probe.Bitrate = facts.Container, facts.DurationMS, facts.Bitrate
		probe.Streams, probe.Chapters, probe.Title = facts.Streams, facts.Chapters, facts.Title
		probe.Packets = facts.Packets
	case errors.Is(err, ErrDamaged):
		probe.Problem = db.ProblemDamaged
	case errors.Is(err, ErrUnsupported):
		probe.Problem = db.ProblemUnsupported
	case errors.Is(err, ErrEncrypted):
		probe.Problem = db.ProblemEncryptedDisc
	case errors.Is(err, ErrNoDiscReader):
		probe.Problem = db.ProblemNoDiscReader
	case errors.Is(err, ErrNotMedia):
		probe.Problem = db.ProblemNotMedia
	default:
		r.s.log().Warn("file not read", "file", local, "error", err)
		return nil
	}
	return probe
}
