package api

import (
	"context"
	"path/filepath"
	"time"

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
	// background lives as long as the server, for work a request starts and does not wait for.
	background context.Context
	// tuners runs each channel's shared input, and viewings the live sessions on them.
	tuners   *live.Tuners
	viewings *live.Viewings
}

// startLiveTV builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startLiveTV(ctx context.Context) error {
	s.channels = librarychannels.New(s.db, s.log, func() time.Time { return s.now() })
	go s.channels.Run(ctx)
	s.libraryChannelRoutes()
	s.liveRoutes()
	var err error
	if s.sources, err = live.New(s.db, s.log, func() time.Time { return s.now() }, s.dataDir); err != nil {
		return err
	}
	s.background = ctx
	go s.sources.Run(ctx)
	s.liveSourceRoutes()
	s.tuners = live.NewTuners(filepath.Join(s.dataDir, "transcodes", "live"), live.StreamClient(), s.log, func() time.Time { return s.now() },
		func() live.Tuning { return live.DefaultTuning })
	s.viewings = live.NewViewings(func() time.Time { return s.now() })
	go s.tuners.Run(ctx)
	go s.viewings.Run(ctx)
	s.liveStreamRoute()
	return nil
}
