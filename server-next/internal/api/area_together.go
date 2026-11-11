package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/livefeed"
	"github.com/PorticoMediaServer/portico/server-next/internal/receivers"
)

// togetherArea is what playing together and other devices (area 8) keeps on the server. Add this area's fields here and its start-up
// to startTogether, in this file, so that building the area never edits server.go.
type togetherArea struct {
	// live carries the changes to what lives only in memory onto the change feed.
	live *livefeed.Publisher
	// receivers are the devices open to playback from other devices. See receivers.go.
	receivers *receivers.Service
}

// startTogether builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startTogether(ctx context.Context) error {
	clock := func() time.Time { return s.now() }
	s.live = livefeed.New(clock)
	s.receivers = receivers.New(s.live, clock)
	// The account's other devices hear when one of its sessions starts, ends, pauses or plays.
	s.playback.OnChange(func(userID int64, id string) {
		s.live.ToAccount(userID, livefeed.TopicPlayback, livefeed.OpUpdated, id)
	})
	go s.sweepMemory(ctx)
	s.receiverRoutes()
	return nil
}
