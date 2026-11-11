package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// liveTVArea is what Live TV and DVR (area 11) keeps on the server. Add this area's fields here and its start-up
// to startLiveTV, in this file, so that building the area never edits server.go.
type liveTVArea struct {
	// channels builds Library Channel schedules and says what is on. See library_channels.go.
	channels *librarychannels.Service
}

// startLiveTV builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startLiveTV(ctx context.Context) error {
	s.channels = librarychannels.New(s.db, s.log, func() time.Time { return s.now() })
	go s.channels.Run(ctx)
	s.libraryChannelRoutes()
	s.liveRoutes()
	return nil
}
