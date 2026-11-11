package api

import (
	"context"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
)

// downloadsArea is what downloads and offline (area 9) keeps on the server. Add this area's fields here and its start-up
// to startDownloads, in this file, so that building the area never edits server.go.
type downloadsArea struct {
	// prepare makes the smaller copies people download. See package prepare.
	prepare *prepare.Service
}

// startDownloads builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startDownloads(ctx context.Context) error {
	// The copies made for downloads: made in the background, and swept when nobody wants them.
	var err error
	if s.prepare, err = prepare.New(prepare.Options{DB: s.db, Folder: filepath.Join(s.dataDir, "prepared"), Settings: s.settings,
		Remote: s.remote.Open, Log: s.log, Now: func() time.Time { return s.now() }}); err != nil {
		return err
	}
	go s.prepare.Run(ctx)
	s.jobs.Tidy("unused prepared copies", 6*time.Hour, s.prepare.Tidy)
	return nil
}
