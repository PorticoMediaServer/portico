package api

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/lrclib"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/musicbrainz"
)

// metadataArea is what metadata (area 04) keeps on the server. Add this area's fields here and its
// start-up to startMetadata, in this file, so that building the area never edits server.go.
type metadataArea struct {
	// metadata looks titles up at their library's provider. See metadata.go.
	metadata *metadata.Service
	// keys is the owner's provider keys, opened. See metadata_settings.go.
	keys providerKeys
	// subtitleSearches remembers recent subtitle searches. See subtitle_search.go.
	subtitleSearches subtitleSearches
	// castPhotos is the Cast photos switch in force. See metadata_cast_photos.go.
	castPhotos castPhotos
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
	// Music libraries look albums and artists up at MusicBrainz, which needs no key and is asked
	// at its own pace (definition area 4, rules 2 and 8).
	s.metadata.Use(musicbrainz.New(musicbrainz.Origin, musicbrainz.Every))
	// LRCLIB needs no key (definition area 4, rule 64): lyrics are looked up only in a music
	// library whose owner chose "Files, then LRCLIB", or by hand.
	s.metadata.Lyrics = lrclib.New(lrclib.Origin, lrclib.Every)
	s.lyricsAdminRoutes()
	s.metadataSettingsRoutes()
	s.subtitleSearchRoutes()
	s.editorRoutes()
	s.collectionRoutes()
	s.artworkRoutes()
	s.metadataJobs()
	s.picturePass()
	s.watchCastPhotos(ctx)
	// What a scan found is looked up when it ends.
	s.scans.AfterScan = func(ctx context.Context, library string, startedAt time.Time) { s.lookUp(ctx, library, startedAt) }
	go s.lookupsAgain(ctx)
	return nil
}

// lookUp starts a library's lookup and refresh, unless the library has no provider to ask
// (local metadata only, or no key for its provider): then there is nothing to do, and no task
// is listed for it. After a scan, addedSince is when it started, so the songs it added are
// asked about for lyrics; the hourly run passes none.
func (s *Server) lookUp(ctx context.Context, library string, addedSince time.Time) {
	asks, err := s.metadata.Asks(ctx, library)
	if err == nil && asks {
		var params json.RawMessage
		if !addedSince.IsZero() {
			params, _ = json.Marshal(afterScanParams{AddedSince: addedSince.UnixMilli()})
		}
		_, err = s.jobs.Start(ctx, jobAfterScan, jobs.Request{Target: jobs.Target{Library: library}, Params: params})
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
			s.lookUp(ctx, lib.PublicID, time.Time{})
		}
	}
}

// UseProvider gives the server an online metadata source in place of the one of its name: a
// test's own.
func (s *Server) UseProvider(p metadata.Provider) { s.metadata.Use(p) }
