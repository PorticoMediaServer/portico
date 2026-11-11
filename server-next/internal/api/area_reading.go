package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

// readingArea is what reading (area 10) keeps on the server. Add this area's fields here and its start-up
// to startReading, in this file, so that building the area never edits server.go.
type readingArea struct {
	// reading opens books. See reading.go.
	reading *reading.Service
}

// startReading builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startReading(ctx context.Context) error {
	// Book addresses are signed with a key the server keeps, so they work across a restart.
	bookKey, err := s.db.SettingOrCreate(ctx, "reading.address_key", auth.NewKey)
	if err != nil {
		return err
	}
	// A book on remote storage is read through its source, as playback reads a film there.
	s.reading = reading.New(s.db, bookKey, func() time.Time { return s.now() }, s.remote.Open)
	s.readingRoutes()
	// A deleted annotation is kept as a marker for 70 days, so an older edit cannot bring it
	// back; once a day the markers past that go, in books nobody has written to since.
	s.jobs.Tidy("deleted annotations", 24*time.Hour, func(ctx context.Context) error {
		_, err := s.db.PruneDeletedAnnotations(ctx, s.now())
		return err
	})
	return nil
}
