// Package musicanalysis reads each song of a music library with Portico's sound model, for
// Mixes and radio by sound (area 7 rule 58). It is heavy background work: it runs only in the
// owner's hours for it (area 12 rule 41), its decoding and its model run as background
// processes that give way to playback, and it writes in small Background batches behind
// people's own writes. A server without the model installed does none of it.
package musicanalysis

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/sonic"
)

// JobKind is the job that analyses one music library.
const JobKind = "music_analysis"

// Tools gives the supervisor and the ffmpeg in force; "" for ffmpeg is a server that cannot
// decode.
type Tools func(ctx context.Context) (*processpool.Supervisor, string, error)

// Service analyses music libraries.
type Service struct {
	DB    *db.DB
	Log   *slog.Logger
	Tools Tools
	// Hours are the owner's hours for heavy work; Now is the server's clock.
	Hours func() settings.HeavyWorkSettings
	Now   func() time.Time
	// Locate finds the installed model: the helper, the model folder and the libraries the
	// helper loads. The default looks beside ffmpeg, where a release installs it, unless
	// PORTICO_SONIC_HELPER, PORTICO_SONIC_MODEL_DIRECTORY and PORTICO_SONIC_LIBRARIES say.
	Locate func(ffmpeg string) sonic.RuntimeOptions

	mu      sync.Mutex
	runtime *sonic.Runtime
	tried   bool
}

// Locate is the default place of the installed model.
func Locate(ffmpeg string) sonic.RuntimeOptions {
	helper := os.Getenv("PORTICO_SONIC_HELPER")
	if helper == "" && ffmpeg != "" {
		helper = filepath.Join(filepath.Dir(ffmpeg), "portico-sonic")
	}
	model := os.Getenv("PORTICO_SONIC_MODEL_DIRECTORY")
	if model == "" && helper != "" {
		model = filepath.Join(filepath.Dir(helper), "musicnn-mtt")
	}
	return sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Libraries: filepath.SplitList(os.Getenv("PORTICO_SONIC_LIBRARIES"))}
}

// Runtime is the installed model, opened the first time it is asked for once ffmpeg is
// known; nil when the server has none. A model whose run fails is asked for again at the
// next start.
func (s *Service) Runtime(ctx context.Context) (*sonic.Runtime, *processpool.Supervisor, string) {
	supervisor, ffmpeg, err := s.Tools(ctx)
	if err != nil || ffmpeg == "" || supervisor == nil {
		return nil, supervisor, ffmpeg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tried {
		s.tried = true
		locate := s.Locate
		if locate == nil {
			locate = Locate
		}
		options := locate(ffmpeg)
		options.Supervisor = supervisor
		if r, err := sonic.OpenRuntime(options); err == nil {
			s.runtime = r
		} else if !errors.Is(err, sonic.ErrUnavailable) || options.Helper != "" && fileExists(options.Helper) {
			s.Log.Warn("music analysis is not available: the sound model installed with the server could not be opened", "error", err)
		}
	}
	if s.runtime == nil || !s.runtime.CanAttempt() {
		return nil, supervisor, ffmpeg
	}
	return s.runtime, supervisor, ffmpeg
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Kind is the job kind, registered with the runner by the area that owns music.
func (s *Service) Kind() jobs.Kind {
	return jobs.Kind{Name: JobKind, Access: jobs.Internal, Lane: "heavy-music", Run: s.run}
}

// page is how many songs are read from the database at a time, and saved together.
const page = 25

func (s *Service) run(ctx context.Context, work jobs.Work) error {
	lib, err := s.library(ctx, work.Target.Library)
	if err != nil {
		return err
	}
	runtime, supervisor, ffmpeg := s.Runtime(ctx)
	if runtime == nil {
		return jobs.DoneWith{Note: "Music analysis can't run on this server."}
	}
	model := runtime.Identity().Version
	var done int64
	after, unreachable := int64(0), 0
	for {
		tracks, err := s.DB.TracksToAnalyse(ctx, lib.ID, model, after, page)
		if err != nil || len(tracks) == 0 {
			return err
		}
		var batch []db.TrackAnalysis
		for _, t := range tracks {
			if err := s.waitForHours(ctx); err != nil {
				return errors.Join(err, s.DB.SaveTrackAnalyses(context.WithoutCancel(ctx), batch, s.Now()))
			}
			a, err := s.analyse(ctx, runtime, supervisor, ffmpeg, t)
			if errors.Is(err, errNotNow) {
				// No row: the song is listed again next time. Many in a row is the drive or
				// the share, not the songs: stop, and the hourly start tries again.
				after = t.ItemID
				if unreachable++; unreachable >= unreachableInARow {
					return errors.Join(jobs.DoneWith{Note: "This library's files can't be reached right now. They are tried again later."},
						s.DB.SaveTrackAnalyses(context.WithoutCancel(ctx), batch, s.Now()))
				}
				continue
			}
			unreachable = 0
			if err != nil {
				// Cancelled or stopping, or the model itself failed: keep what is done.
				return errors.Join(err, s.DB.SaveTrackAnalyses(context.WithoutCancel(ctx), batch, s.Now()))
			}
			a.Model = model
			batch = append(batch, a)
			after = t.ItemID
			done++
			work.Progress(done, 0)
		}
		if err := s.DB.SaveTrackAnalyses(ctx, batch, s.Now()); err != nil {
			return err
		}
	}
}

func (s *Service) library(ctx context.Context, publicID string) (db.Library, error) {
	libraries, err := s.DB.MusicLibraries(ctx)
	if err != nil {
		return db.Library{}, err
	}
	for _, l := range libraries {
		if l.PublicID == publicID {
			return l, nil
		}
	}
	return db.Library{}, db.ErrNotFound
}

// waitForHours returns at once inside the owner's hours for heavy work, and otherwise when
// they begin. It looks at the hours again every minute, so a change to them counts at once.
func (s *Service) waitForHours(ctx context.Context) error {
	for {
		open, opens := s.Hours().Open(s.Now())
		if open {
			return nil
		}
		wait := min(max(opens.Sub(s.Now()), time.Second), time.Minute)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// PausedUntil says when heavy work may next run, if it may not run now: for "Paused until
// 1 am".
func PausedUntil(hours settings.HeavyWorkSettings, now time.Time) (time.Time, bool) {
	open, opens := hours.Open(now)
	return opens, !open
}

// songUnreadable is kept for a song the decoder or the model could not read; it is not tried
// again until its file changes.
const songUnreadable = "This song's sound could not be read."

// errNotNow is a song whose file cannot be opened now. That is usually the drive or the share
// (unplugged, asleep, not mounted yet), not the song, so nothing is kept for it.
var errNotNow = errors.New("the song's file cannot be opened now")

// unreachableInARow songs whose files will not open end a run.
const unreachableInARow = 20

// analyse reads one song. errNotNow is a file that cannot be opened now; any other error is
// the job's to stop on (cancelled, or the model failed); a song the decoder or the model
// cannot read is a TrackAnalysis with Failed set.
func (s *Service) analyse(ctx context.Context, runtime *sonic.Runtime, supervisor *processpool.Supervisor, ffmpeg string, t db.TrackToAnalyse) (db.TrackAnalysis, error) {
	out := db.TrackAnalysis{Track: t}
	file, err := os.Open(t.Path)
	if err != nil {
		return out, errNotNow
	}
	defer file.Close()
	durationUS := t.DurationMS * 1000
	startUS, regionUS := t.StartMS*1000, durationUS-t.StartMS*1000
	if t.EndMS > t.StartMS {
		regionUS = (t.EndMS - t.StartMS) * 1000
	}
	if durationUS <= 0 || regionUS <= 0 || startUS >= durationUS {
		out.Failed = songUnreadable
		return out, nil
	}
	spec := decoder.AnalysisSpec{Kind: "sonic_pcm", MaxDurationUS: durationUS, StartUS: startUS, RegionUS: min(regionUS, durationUS-startUS)}
	// The allowance is a bound on samples read, never a stated length: it covers the
	// source's rounding and the resampler's flush.
	maxSamples := int64(math.Ceil(float64(spec.RegionUS)*sonic.SampleRate/1e6)) + sonic.SampleRate/10
	decode := mediaanalysis.FileDecode(supervisor, ffmpeg, nil, file, t.Size)
	key := "music-analysis:" + strconv.FormatInt(t.ItemID, 10)
	analysis, err := runtime.Run(ctx, key, file, func(yield func([]float32) error) error {
		return decode(ctx, key+":pcm", spec, func(input io.Reader) error {
			_, err := sonic.LogMel(input, maxSamples, yield)
			return err
		})
	})
	switch {
	case ctx.Err() != nil:
		return out, ctx.Err()
	case errors.Is(err, sonic.ErrUnavailable):
		return out, err
	case err != nil:
		out.Failed = songUnreadable
		return out, nil
	}
	if out.Embedding, err = analysis.Embedding.Bytes(); err != nil {
		out.Embedding, out.Failed = nil, songUnreadable
		return out, nil
	}
	out.Tags = make([]byte, sonic.TagCount*4)
	for i, v := range analysis.Tags {
		binary.LittleEndian.PutUint32(out.Tags[i*4:], math.Float32bits(v))
	}
	return out, nil
}
