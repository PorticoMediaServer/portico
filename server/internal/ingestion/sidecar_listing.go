package ingestion

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// A scan lists every directory before it analyses a file in it, and analysis
// then asks storage for each sidecar name a file might have beside it: about
// thirty names for an episode, each one a helper process, nearly all of them
// for files that are not there. What the listing already showed answers most of
// those questions for the price of one look at the directory.
//
// The memory is per scan and per directory, and it stands only while the
// directory is as it was listed: before it is used for a file, the directory's
// identity and revision are read again (once per analysed file), and a
// directory that has changed since is forgotten, so a sidecar added after the
// listing is read like any other. Only a listing this process saw from its
// first page to its last is kept at all. Anything else — a directory an
// incremental scan skipped, a listing resumed after a restart, a remote source,
// a kind of file not tracked here, more directories than the bound, a
// directory that cannot be re-read just now — is simply not known, and storage
// is asked for each name as before.
const (
	maxListedDirectories = 100000
	maxListedNames       = 400000
)

// listedDirectory is one directory as a scan saw it.
type listedDirectory struct {
	names              map[string]struct{}
	artwork            map[string]string
	identity, revision string
	musicAmbiguous     bool
}

type scanListing struct {
	open           map[string]map[string]struct{}
	done           map[string]listedDirectory
	names          int
	artworkOpen    map[string]map[string]string
	artworkNames   int
	musicAmbiguous map[string]bool
}

// sidecarLike reports whether a name has one of the extensions local metadata
// looks for beside a media file. Only those names can be reported absent;
// audio names are also kept as bounded album-cohort naming evidence.
func sidecarLike(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".tbn", ".webp", ".nfo", ".opf", ".json":
		return true
	}
	return false
}

// listedName is how names are compared: a file system that ignores letter case
// or Unicode form would open the file either way, so neither may make a
// present file look absent.
func listedName(name string) string { return strings.ToLower(norm.NFC.String(name)) }

func (s *Service) noteDirectoryPage(job, relative, cursor string, page storage.InventoryPage) {
	s.listingMu.Lock()
	defer s.listingMu.Unlock()
	l := s.listings[job]
	if l == nil {
		// A new scan is a good moment to forget the ones that have ended.
		for other := range s.listings {
			var status string
			if s.db == nil || s.db.QueryRow(`SELECT status FROM jobs WHERE id=?`, other).Scan(&status) != nil || status != "queued" && status != "running" && status != "paused" {
				delete(s.listings, other)
			}
		}
		if s.listings == nil {
			s.listings = map[string]*scanListing{}
		}
		l = &scanListing{open: map[string]map[string]struct{}{}, done: map[string]listedDirectory{}}
		s.listings[job] = l
	}
	if cursor == "" {
		if l.musicAmbiguous == nil {
			l.musicAmbiguous = map[string]bool{}
		}
		delete(l.musicAmbiguous, relative)
		if l.artworkOpen == nil {
			l.artworkOpen = map[string]map[string]string{}
		}
		l.artworkNames -= len(l.artworkOpen[relative]) + len(l.done[relative].artwork)
		l.artworkOpen[relative] = map[string]string{}
		l.names -= len(l.open[relative]) + len(l.done[relative].names)
		delete(l.open, relative)
		delete(l.done, relative)
		if len(l.open)+len(l.done) >= maxListedDirectories {
			delete(l.artworkOpen, relative)
			return
		}
		l.open[relative] = map[string]struct{}{}
	}
	names, started := l.open[relative]
	if !started {
		return
	}
	for _, v := range page.Entries {
		if v.Directory || (!sidecarLike(v.Name) && !assets.SupportedForKind(v.Name, "music")) {
			continue
		}
		if l.names >= maxListedNames {
			l.names -= len(names)
			delete(l.open, relative)
			l.artworkNames -= len(l.artworkOpen[relative])
			delete(l.artworkOpen, relative)
			return
		}
		if imageSidecarLike(v.Name) {
			if images := l.artworkOpen[relative]; images != nil {
				if _, seen := images[v.Name]; !seen {
					if l.artworkNames >= maxListedNames {
						l.artworkNames -= len(images)
						l.artworkOpen[relative] = nil
					} else {
						images[v.Name] = storage.InventoryRevision(v)
						l.artworkNames++
					}
				}
			}
		}
		if _, seen := names[listedName(v.Name)]; seen && assets.SupportedForKind(v.Name, "music") {
			l.musicAmbiguous[relative] = true // case/Unicode collisions cannot establish a unique album cohort
		}
		if _, seen := names[listedName(v.Name)]; !seen {
			names[listedName(v.Name)] = struct{}{}
			l.names++
		}
	}
	if page.Complete {
		delete(l.open, relative)
		if page.DirectoryIdentity != "" && page.DirectoryRevision != "" {
			l.done[relative] = listedDirectory{names: names, artwork: l.artworkOpen[relative], identity: page.DirectoryIdentity, revision: page.DirectoryRevision, musicAmbiguous: l.musicAmbiguous[relative]}
		} else {
			l.names -= len(names)
			l.artworkNames -= len(l.artworkOpen[relative])
		}
		delete(l.artworkOpen, relative)
		delete(l.musicAmbiguous, relative)
	}
}

// forgetListing drops what a scan remembered, when the scan has ended.
func (s *Service) forgetListing(job string) {
	s.listingMu.Lock()
	delete(s.listings, job)
	delete(s.ignoreScopes, job)
	s.listingMu.Unlock()
}

// directoryRecheck is one analysis's view of the directories this scan listed.
// The projections below share it, so the directory is read once per analysed
// file however many of them ask about it, and a directory found changed is
// forgotten for all of them.
type directoryRecheck func(string) (listedDirectory, bool)

// scanListed answers, for the analysis of one file, whether this scan saw a
// sidecar at a path. known is false unless the scan listed the whole directory,
// the name is of a kind it remembers, and the directory is still exactly as it
// was listed. That last check reads the directory once per analysis, however
// many projections ask about it; a directory found changed is forgotten.
func (s *Service) scanListed(ctx context.Context, job string, source catalog.LibrarySource) func(path string) (present, known bool) {
	return scanListedFrom(source, s.scanDirectoryListed(ctx, job, source))
}

// analysisListings builds one directory recheck for the analysis of one file
// and every listing projection the analysis uses. Sharing the recheck is what
// keeps a folder to one observation per analysed file; the projections stay
// separate because each answers a different question from the same listing.
func (s *Service) analysisListings(ctx context.Context, job string, source catalog.LibrarySource) (func(string) (bool, bool), func(string) (localmetadata.ArtworkDirectoryListing, bool), localmetadata.MusicTrackListing) {
	current := s.scanDirectoryListed(ctx, job, source)
	return scanListedFrom(source, current), scanArtworkListedFrom(source, current), scanMusicTracksListedFrom(source, current)
}

// scanListedFrom projects sidecar answers from an analysis's shared recheck.
func scanListedFrom(source catalog.LibrarySource, current directoryRecheck) func(path string) (present, known bool) {
	return func(path string) (bool, bool) {
		if !sidecarLike(path) {
			return false, false
		}
		relative, err := filepath.Rel(source.ResolvedPath, filepath.Dir(path))
		if err != nil || relative != "." && !filepath.IsLocal(relative) {
			return false, false
		}
		listed, known := current(relative)
		if !known {
			return false, false
		}
		_, present := listed.names[listedName(filepath.Base(path))]
		return present, true
	}
}

// scanDirectoryListed shares the existing per-analysis directory recheck with
// image enumeration. A name lookup never manufactures a listing observation.
func (s *Service) scanDirectoryListed(ctx context.Context, job string, source catalog.LibrarySource) directoryRecheck {
	var mu sync.Mutex
	unchanged := map[string]bool{}
	return func(relative string) (listedDirectory, bool) {
		s.listingMu.Lock()
		var listed listedDirectory
		known := false
		if l := s.listings[job]; l != nil {
			listed, known = l.done[relative]
		}
		s.listingMu.Unlock()
		if !known {
			return listedDirectory{}, false
		}
		mu.Lock()
		defer mu.Unlock()
		same, checked := unchanged[relative]
		if !checked {
			same = s.directoryAsListed(ctx, source, relative, listed)
			unchanged[relative] = same
			if !same {
				s.listingMu.Lock()
				if l := s.listings[job]; l != nil && l.done[relative].revision == listed.revision {
					l.names -= len(l.done[relative].names)
					l.artworkNames -= len(l.done[relative].artwork)
					delete(l.done, relative)
				}
				s.listingMu.Unlock()
			}
		}
		if !same {
			return listedDirectory{}, false
		}
		return listed, true
	}
}

// directoryAsListed reads a directory's identity and revision the way the
// scan's own verification does and compares them with the listing's. A
// directory that cannot be read just now is treated as changed.
func (s *Service) directoryAsListed(ctx context.Context, source catalog.LibrarySource, relative string, listed listedDirectory) bool {
	req := storage.InventoryRequest{Root: source.ResolvedPath, RootIdentity: source.RootIdentity, RelativePath: relative, FollowSymlinks: source.FollowSymlinks}
	var v storage.Snapshot
	var err error
	if s.storage != nil {
		v, err = s.storage.InventoryDirectory(ctx, source.ID, req)
	} else {
		v, err = storage.LocalInventoryDirectory(req)
	}
	return err == nil && v.ObjectIdentity == listed.identity && storage.InventoryRevision(v) == listed.revision
}

// scanArtworkListed exposes actual case-preserving image names only from a
// complete, still-current directory observation. It is discovery data, never
// an I/O grant; every image still passes the admitted metadata reader.
func (s *Service) scanArtworkListed(ctx context.Context, job string, source catalog.LibrarySource) func(string) (localmetadata.ArtworkDirectoryListing, bool) {
	return scanArtworkListedFrom(source, s.scanDirectoryListed(ctx, job, source))
}

// scanArtworkListedFrom projects artwork names from an analysis's shared
// recheck.
func scanArtworkListedFrom(source catalog.LibrarySource, current directoryRecheck) func(string) (localmetadata.ArtworkDirectoryListing, bool) {
	return func(directory string) (localmetadata.ArtworkDirectoryListing, bool) {
		relative, err := filepath.Rel(source.ResolvedPath, directory)
		if err != nil || relative != "." && !filepath.IsLocal(relative) {
			return localmetadata.ArtworkDirectoryListing{}, false
		}
		dir, known := current(relative)
		if !known || dir.artwork == nil {
			return localmetadata.ArtworkDirectoryListing{}, false
		}
		entries := make([]localmetadata.ArtworkListingEntry, 0, len(dir.artwork))
		for name, revision := range dir.artwork {
			entries = append(entries, localmetadata.ArtworkListingEntry{Name: name, Revision: revision})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		return localmetadata.ArtworkDirectoryListing{SourceID: source.ID, RootIdentity: source.RootIdentity, FollowSymlinks: source.FollowSymlinks, Entries: entries}, true
	}
}

func imageSidecarLike(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".tbn", ".webp":
		return true
	}
	return false
}
