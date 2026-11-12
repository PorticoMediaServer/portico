package api

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// metadataArea is what metadata (area 04) keeps on the server. Add this area's fields here and its
// start-up to startMetadata, in this file, so that building the area never edits server.go.
type metadataArea struct {
	// metadata looks titles up at their library's provider. See metadata.go.
	metadata *metadata.Service
	// keys is the owner's provider keys, opened. See metadata_settings.go.
	keys providerKeys
}

// startMetadata builds this area's service and registers its routes. New calls it once, after
// the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startMetadata(ctx context.Context) error {
	s.metadata = &metadata.Service{DB: s.db, Providers: map[string]metadata.Provider{}, Settings: s.settings, Pictures: s.artwork,
		SubtitleDir: filepath.Join(s.dataDir, "subtitles")}
	if err := s.loadProviderKeys(ctx); err != nil {
		return err
	}
	s.metadataRoutes()
	s.metadataSettingsRoutes()
	s.editorRoutes()
	s.collectionRoutes()
	s.artworkRoutes()
	s.metadataJobs()
	s.picturePass()
	// What a scan found is looked up when it ends.
	s.scans.AfterScan = func(ctx context.Context, library string) { s.lookUp(ctx, library) }
	go s.lookupsAgain(ctx)
	return nil
}

// lookUp starts a library's lookup and refresh, unless the library has no provider to ask
// (local metadata only, or no key for its provider): then there is nothing to do, and no task
// is listed for it.
func (s *Server) lookUp(ctx context.Context, library string) {
	asks, err := s.metadata.Asks(ctx, library)
	if err == nil && asks {
		_, err = s.jobs.Start(ctx, jobAfterScan, jobs.Request{Target: jobs.Target{Library: library}})
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, db.ErrNotFound) { // a library removed since is not an error
		s.log.Error("looking a library's titles up could not be started", "library", library, "error", err)
	}
}

// lookupsAgain starts each library's lookup and refresh every hour, as a scan does when it
// ends, so a title the provider failed on is asked again once its wait is over and matched
// titles are refreshed when due, in a library nobody rescans. Asking again while a library's
// run is waiting or going joins it, and a run with nothing to do reads two small indexes.
func (s *Server) lookupsAgain(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		libraries, err := s.db.AllLibraries(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Error("libraries to look up again could not be read", "error", err)
		}
		for _, lib := range libraries {
			s.lookUp(ctx, lib.PublicID)
		}
	}
}

// UseProvider gives the server an online metadata source in place of the one of its name: a
// test's own.
func (s *Server) UseProvider(p metadata.Provider) { s.metadata.Use(p) }
