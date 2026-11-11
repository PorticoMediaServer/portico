package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func (s *Server) registerPlaybackRoutes(ctx context.Context) {
	s.playback = playback.New(s.db, playback.NoEngine{}, playback.NoEngine{}, s.log, func() time.Time { return s.now() })
	go s.playback.Run(ctx)
	s.playbackRoutes()
	s.queues = playqueue.New(s.db, s.playback, func() time.Time { return s.now() })
	s.queueRoutes()
	s.subtitleRoutes()
}
