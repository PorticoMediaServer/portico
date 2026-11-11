package api

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// metadataArea is what metadata (area 04) keeps on the server. Add this area's fields here and its
// start-up to startMetadata, in this file, so that building the area never edits server.go.
type metadataArea struct {
	// metadata looks titles up at their library's provider. See metadata.go.
	metadata *metadata.Service
}

// startMetadata builds this area's service and registers its routes. New calls it once, after
// the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startMetadata(ctx context.Context) error {
	s.metadata = &metadata.Service{DB: s.db, Providers: map[string]metadata.Provider{}}
	s.metadataRoutes()
	s.editorRoutes()
	s.metadataJobs()
	// What a scan found is looked up when it ends.
	s.scans.AfterScan = func(ctx context.Context, library string) {
		if _, err := s.jobs.Start(ctx, jobAfterScan, jobs.Request{Target: jobs.Target{Library: library}}); err != nil && ctx.Err() == nil {
			s.log.Error("looking up what a scan found could not be started", "library", library, "error", err)
		}
	}
	go s.lookupsAgain(ctx)
	return nil
}

// lookupsAgain starts the lookup, hourly, for each library holding a title whose wait after the
// provider failed on it is over, so it does not wait for the library's next scan.
func (s *Server) lookupsAgain(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		libraries, err := s.db.LookupsDue(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			s.log.Error("titles to look up again could not be read", "error", err)
		}
		for _, library := range libraries {
			if _, err := s.jobs.Start(ctx, jobAfterScan, jobs.Request{Target: jobs.Target{Library: library}}); err != nil && ctx.Err() == nil {
				s.log.Error("looking titles up again could not be started", "library", library, "error", err)
			}
		}
	}
}

// UseProvider gives the server an online metadata source. Without one, titles are looked up
// nowhere and keep what their files say.
func (s *Server) UseProvider(p metadata.Provider) { s.metadata.Providers[p.Name()] = p }
