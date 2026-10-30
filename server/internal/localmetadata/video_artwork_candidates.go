package localmetadata

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ReadArtworkCandidates reads only recognized image members of actual complete
// listings. An incomplete/changed/unreadable set never replaces a prior set.
func (s *Service) ReadArtworkCandidates(ctx context.Context, library, kind, root, media string, season int) ([]assets.LocalArtworkCandidate, bool) {
	listed, ok := ctx.Value(artworkListingKey{}).(func(string) (ArtworkDirectoryListing, bool))
	if !ok || listed == nil || s.storage == nil || s.readSmall == nil || (kind != "movie" && kind != "tv" && kind != "anime") {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dirs := []string{filepath.Dir(media)}
	if (kind == "tv" || kind == "anime") && seasonFolderName.MatchString(filepath.Base(dirs[0])) && underRoot(root, filepath.Dir(dirs[0])) {
		dirs = append(dirs, filepath.Dir(dirs[0]))
	}
	result := []assets.LocalArtworkCandidate{}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			return nil, false
		}
		listing, known := listed(dir)
		if !known || listing.SourceID == "" || listing.RootIdentity == "" {
			return nil, false
		}
		entries := append([]ArtworkListingEntry(nil), listing.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, entry := range entries {
			if ctx.Err() != nil {
				return nil, false
			}
			if filepath.Base(entry.Name) != entry.Name || entry.Name == "." || entry.Name == ".." || strings.ContainsAny(entry.Name, "/\\\x00") || entry.Revision == "" {
				return nil, false
			}
			path := filepath.Join(dir, entry.Name)
			slot, rank := videoArtworkOrder(kind, root, media, path, season)
			if slot == "" {
				continue
			}
			relative, err := filepath.Rel(root, path)
			if err != nil || !filepath.IsLocal(relative) {
				return nil, false
			}
			request := storage.InventoryRequest{Root: root, RootIdentity: listing.RootIdentity, RelativePath: relative, FollowSymlinks: listing.FollowSymlinks}
			// The unwitnessed proof: current admission, then the exact listing
			// revision. This is also what a rejected observation falls back to.
			explicit := func() bool {
				if storage.CheckScanRead(ctx, path) != nil {
					return false
				}
				current, err := s.storage.InventoryStat(ctx, listing.SourceID, request)
				return err == nil && !current.Directory && current.Size >= 0 && current.Size <= 4<<20 && storage.InventoryRevision(current) == entry.Revision
			}
			admitted := func(v storage.Snapshot) bool {
				return !v.Directory && v.Size >= 0 && v.Size <= 4<<20 && storage.InventoryRevision(v) == entry.Revision
			}
			check := func() bool {
				// The ingestion guard already observed this candidate. Its exact
				// snapshot is used only when it answered for this candidate under
				// this producer; anything else falls back to the explicit proof.
				observed, ok, err := storage.ObserveScanRead(ctx, path)
				if err != nil {
					return false
				}
				if !ok {
					return explicit()
				}
				if observed.Client != s.storage || observed.SourceID != listing.SourceID || observed.Root != root ||
					observed.RootIdentity != listing.RootIdentity || observed.FollowSymlinks != listing.FollowSymlinks ||
					observed.RelativePath != relative {
					return explicit()
				}
				return admitted(observed.Snapshot)
			}
			if !check() {
				artworkReadFailed(ctx)
				return nil, false
			}
			stamp := listing.SourceID + "\x00" + listing.RootIdentity + "\x00" + entry.Revision
			s.mu.Lock()
			cached := s.artworkCandidateObservations[path]
			s.mu.Unlock()
			key := ""
			if cached.stamp == stamp {
				key = cached.key
			}
			if key == "" {
				raw, err := s.readSmall(ctx, library, path, 4<<20)
				if err != nil {
					artworkReadFailed(ctx)
					return nil, false
				}
				key, err = s.store(ctx, raw)
				if err != nil {
					artworkReadFailed(ctx)
					return nil, false
				}
			}
			if !check() {
				artworkReadFailed(ctx)
				return nil, false
			}
			s.mu.Lock()
			if s.artworkCandidateObservations == nil || len(s.artworkCandidateObservations) > 1024 {
				s.artworkCandidateObservations = map[string]observedArtworkImage{}
			}
			s.artworkCandidateObservations[path] = observedArtworkImage{stamp: stamp, key: key}
			s.mu.Unlock()
			name := entry.Name
			if _, _, err := assets.NormalizeLocalArtworkSource(name, rank); err != nil {
				// Keep admitted image bytes even when their basename cannot be
				// represented as an order fact. Unknown is never canonical.
				name, rank = "", assets.LocalArtworkUnknownRank
			}
			result = append(result, assets.LocalArtworkCandidate{Slot: slot, Key: key, SourceName: name, SourceRank: rank})
		}
	}
	if ctx.Err() != nil {
		return nil, false
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		if a.SourceRank != b.SourceRank {
			return a.SourceRank < b.SourceRank
		}
		if a.SourceName != b.SourceName {
			return a.SourceName < b.SourceName
		}
		return a.Key < b.Key
	})
	return result, true
}

// videoArtworkSlot classifies only known image conventions in the file/show's
// own allowed directories. A match does not authorize reading that path.
func videoArtworkSlot(kind, root, media, candidate string, season int) string {
	slot, _ := videoArtworkOrder(kind, root, media, candidate, season)
	return slot
}

func videoArtworkOrder(kind, root, media, candidate string, season int) (string, int64) {
	unknown := assets.LocalArtworkUnknownRank
	extension := strings.ToLower(filepath.Ext(candidate))
	if artworkExtensionPriority(extension) < 0 {
		return "", unknown
	}
	dir, candidateDir := filepath.Dir(media), filepath.Dir(candidate)
	stem := strings.TrimSuffix(filepath.Base(media), filepath.Ext(media))
	name := strings.TrimSuffix(filepath.Base(candidate), filepath.Ext(candidate))
	match := func(slot string, bases ...string) (string, int64) {
		rank, ok := artworkNameRank(name, extension, bases, false, 0)
		if ok {
			return slot, rank
		}
		return "", unknown
	}
	if kind == "movie" {
		if candidateDir != dir {
			return "", unknown
		}
		for _, group := range []struct {
			slot  string
			bases []string
		}{
			{"item/poster", []string{stem + "-poster", stem, "poster", "folder", "cover", "default", "movie"}},
			{"item/backdrop", []string{stem + "-fanart", "fanart", "backdrop", "art", "background"}},
			{"item/logo", []string{stem + "-clearlogo", "logo", "clearlogo"}},
		} {
			if slot, rank := match(group.slot, group.bases...); slot != "" {
				return slot, rank
			}
		}
		return "", unknown
	}
	if kind != "tv" && kind != "anime" {
		return "", unknown
	}
	showDir := dir
	inSeason := seasonFolderName.MatchString(filepath.Base(dir))
	if inSeason {
		showDir = filepath.Dir(dir)
		if !underRoot(root, showDir) {
			showDir = ""
		}
	}
	if candidateDir == showDir {
		for _, group := range []struct {
			slot  string
			bases []string
		}{
			{"show/poster", []string{"poster", "folder", "show"}},
			{"show/backdrop", []string{"fanart", "backdrop", "art", "background"}},
			{"show/logo", []string{"logo", "clearlogo"}},
		} {
			if slot, rank := match(group.slot, group.bases...); slot != "" {
				return slot, rank
			}
		}
	}
	if season < 0 || season > 9999 || !(candidateDir == showDir || inSeason && candidateDir == dir) {
		return "", unknown
	}
	slot := "season/" + strconv.Itoa(season) + "/poster"
	contextRank := int64(0)
	if inSeason && candidateDir != dir {
		contextRank = 256
	}
	bases := []string{"season" + strconv.Itoa(season), "season" + twoDigits(season), "season" + twoDigits(season) + "-poster"}
	if rank, ok := artworkNameRank(name, extension, bases, true, contextRank); ok {
		return slot, rank
	}
	if season == 0 {
		if rank, ok := artworkNameRank(name, extension, []string{"season-specials-poster"}, false, contextRank+24); ok {
			return slot, rank
		}
	}
	if inSeason && candidateDir == dir {
		if rank, ok := artworkNameRank(name, extension, []string{"poster", "folder"}, false, 512); ok {
			return slot, rank
		}
	}
	return "", unknown
}

func numberedArtName(name, base string, letter bool) bool {
	name, base = strings.ToLower(name), strings.ToLower(base)
	if name == base {
		return true
	}
	if !strings.HasPrefix(name, base) {
		return false
	}
	tail := strings.TrimPrefix(name, base)
	if letter && len(tail) == 1 && tail[0] >= 'a' && tail[0] <= 'z' {
		return true
	}
	if !strings.HasPrefix(tail, "-") || len(tail) == 1 {
		return false
	}
	for _, r := range tail[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
