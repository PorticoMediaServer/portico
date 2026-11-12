package api

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/dvr"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// liveTVArea is what Live TV and DVR (area 11) keeps on the server. Add this area's fields here and its start-up
// to startLiveTV, in this file, so that building the area never edits server.go.
type liveTVArea struct {
	// channels builds Library Channel schedules and says what is on. See library_channels.go.
	channels *librarychannels.Service
	// sources refreshes Live TV sources. See live_sources.go.
	sources *live.Service
	// liveContext lives as long as the server, for work a request starts and does not wait for.
	liveContext context.Context
	// tuners runs each channel's shared input, and viewings the live sessions on them.
	tuners   *live.Tuners
	viewings *live.Viewings
	// liveHLS makes HLS of a channel for a device that cannot play its MPEG-TS as it is (the
	// media engine); nil plays every channel as the source sends it.
	liveHLS LiveHLS
	// recorder records Live TV; dvrOptions are its settings, defaults until the dvr settings
	// document exists. See dvr.go.
	recorder *dvr.Service
	dvrNow   atomic.Pointer[dvr.Options]
}

// startLiveTV builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startLiveTV(ctx context.Context) error {
	s.channels = librarychannels.New(s.db, s.log, func() time.Time { return s.now() })
	s.goBackground(func() { s.channels.Run(ctx) })
	s.libraryChannelRoutes()
	s.liveRoutes()
	var err error
	if s.sources, err = live.New(s.db, s.log, func() time.Time { return s.now() }, s.dataDir); err != nil {
		return err
	}
	s.liveContext = ctx
	s.goBackground(func() { s.sources.Run(ctx) })
	s.liveSourceRoutes()
	s.tuners = live.NewTuners(filepath.Join(s.dataDir, "transcodes", "live"), live.StreamClient(), s.log, func() time.Time { return s.now() },
		func() live.Tuning { return live.DefaultTuning })
	s.viewings = live.NewViewings(func() time.Time { return s.now() })
	// However a viewing ends, the HLS made for its device stops with it.
	s.viewings.Ended = func(id string) {
		if s.liveHLS != nil {
			s.liveHLS.StopLive(id)
		}
	}
	s.goBackground(func() { s.tuners.Run(ctx) })
	// A live channel watched counts against the stream limits like a title (area 11 rule 22).
	s.playback.OtherStreams = s.viewings.Others
	s.goBackground(func() { s.viewings.Run(ctx) })
	s.liveStreamRoute()
	home, err := os.UserHomeDir()
	if err != nil {
		home = s.dataDir
	}
	s.setDVROptions(dvr.Defaults(home))
	s.recorder = dvr.New(s.db, s.tuners, s.sources, s.viewings, s.log, func() time.Time { return s.now() }, s.dvrOptions)
	s.recorder.Deleted = s.stopPlaying
	s.goBackground(func() { s.recorder.Run(ctx) })
	s.dvrRoutes()
	s.liveProgrammeRoutes()
	return nil
}

// dvrOptions are the recording settings in force: the defaults until the dvr settings
// document exists. setDVROptions replaces them; the recorder reads them as it goes.
func (s *Server) dvrOptions() dvr.Options { return *s.dvrNow.Load() }

func (s *Server) setDVROptions(o dvr.Options) { s.dvrNow.Store(&o) }
