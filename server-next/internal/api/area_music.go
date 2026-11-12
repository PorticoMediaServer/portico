package api

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// startMusic starts sound analysis of music libraries, for Mixes and radio by sound. New
// calls it once, after the job runner exists and before the jobs left from the last run are
// taken up. On a server without the sound model installed, nothing is ever started.
func (s *Server) startMusic(ctx context.Context) error {
	music := &musicanalysis.Service{DB: s.db, Log: s.log, Tools: s.analysisTools, Now: s.now,
		Hours: func() settings.HeavyWorkSettings { return settings.General.Get(s.settings).HeavyWork }}
	s.jobs.Register(music.Kind())
	s.goBackground(func() { s.analyseMusic(ctx, music) })
	return nil
}

// analysisTools is the media engine's supervisor and ffmpeg, once the engine is set.
func (s *Server) analysisTools(ctx context.Context) (*processpool.Supervisor, string, error) {
	tools, ok := s.playback.Streams().(interface {
		AnalysisTools(context.Context) (*processpool.Supervisor, string, error)
	})
	if !ok {
		return nil, "", nil
	}
	return tools.AnalysisTools(ctx)
}

// analyseMusic starts each music library's analysis a minute after the server starts and
// then every hour, when the model is installed and the library has songs it has not read: new
// songs are read without waiting for anyone, and a library with nothing to read lists no task.
func (s *Server) analyseMusic(ctx context.Context, music *musicanalysis.Service) {
	wait := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = time.Hour
		runtime, _, _ := music.Runtime(ctx)
		if runtime == nil {
			continue
		}
		libraries, err := s.db.MusicLibraries(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Error("music libraries to analyse could not be read", "error", err)
		}
		for _, lib := range libraries {
			left, err := s.db.TracksToAnalyse(ctx, lib.ID, runtime.Identity().Version, 0, 1)
			if err == nil && len(left) > 0 {
				_, err = s.jobs.Start(ctx, musicanalysis.JobKind, jobs.Request{Target: jobs.Target{Library: lib.PublicID}})
			}
			if err != nil && ctx.Err() == nil && !errors.Is(err, db.ErrNotFound) {
				s.log.Error("a music library's analysis could not be started", "library", lib.PublicID, "error", err)
			}
		}
	}
}
