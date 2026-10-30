package ingestion

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// Naming evidence only, never an I/O grant. An incomplete, changed, remote or
// oversized directory cannot establish an album-wide packed-number pattern.
func (s *Service) scanMusicTracksListed(ctx context.Context, job string, source catalog.LibrarySource) localmetadata.MusicTrackListing {
	return scanMusicTracksListedFrom(source, s.scanDirectoryListed(ctx, job, source))
}

// scanMusicTracksListedFrom projects album-cohort naming evidence from an
// analysis's shared recheck.
func scanMusicTracksListedFrom(source catalog.LibrarySource, current directoryRecheck) localmetadata.MusicTrackListing {
	return func(directory string) ([]string, bool) {
		relative, err := filepath.Rel(source.ResolvedPath, directory)
		if err != nil || relative != "." && !filepath.IsLocal(relative) {
			return nil, false
		}
		dir, known := current(relative)
		if !known || dir.musicAmbiguous || len(dir.names) > naming.MaxPackedTrackCohort {
			return nil, false
		}
		names := []string{}
		for name := range dir.names {
			if assets.SupportedForKind(name, "music") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return names, true
	}
}
