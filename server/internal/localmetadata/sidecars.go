package localmetadata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Sidecar image discovery for every agent (Spec — Page Content §0.5): the
// local files each library kind honours live next to the media, so they are
// read here, at analysis, never inside a catalog write transaction. The keys
// travel in Facts and the catalog commit writes them as provider='local'
// artwork candidates (migration 0263); local art wins over online art unless
// the owner picks otherwise in the editor.
//
// Layout rules, per kind:
//   - movie: stem-specific or generic backdrop and logo aliases beside the file.
//   - tv/anime: poster/folder/show, fanart/backdrop/art/background and
//     logo/clearlogo (jpg/jpeg/png/tbn/webp) in the file's directory, then its parent (never above the
//     library root); season posters seasonNN.jpg, seasonNN-poster.jpg and
//     season-specials-poster.jpg for the episode's own season number. When the
//     file's directory is a season folder ("Season 01", "Specials") the show's
//     images come from the parent only, and the season folder's own
//     poster.jpg/folder.jpg is that season's poster.
//   - music: album art is beside the tracks (above a CD1-style directory);
//     artist art is in the album's parent. Album backgrounds fall back to the
//     artist background. Every lookup stays below the library root.
//
// Only positive hits are cached by path (a show's hundred episodes share one
// read). A miss is not remembered here; instead the scan says which names it
// saw when it listed the directory (WithListing), and while the directory is
// unchanged a name it did not see is not asked for.
func (s *Service) ReadSidecars(ctx context.Context, library, kind, root, path string, season int) map[string]string {
	out := map[string]string{}
	if s.readSmall == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dir := filepath.Dir(path)
	dirs := []string{dir}
	if parent := filepath.Dir(dir); parent != dir && underRoot(root, parent) && (kind == "tv" || kind == "anime" || kind == "music" || kind == "audiobook") {
		dirs = append(dirs, parent)
	}
	first := func(dirs []string, names ...string) string {
		for _, dir := range dirs {
			for _, name := range names {
				if key, ok := s.sidecarKey(ctx, library, filepath.Join(dir, name)); ok {
					return key
				}
			}
		}
		return ""
	}
	switch kind {
	case "movie":
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if key := first(dirs, videoArtworkNames(stem+"-fanart", "fanart", "backdrop", "art", "background")...); key != "" {
			out["item/backdrop"] = key
		}
		if key := first(dirs, videoArtworkNames(stem+"-clearlogo", "logo", "clearlogo")...); key != "" {
			out["item/logo"] = key
		}
	case "tv", "anime":
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if key := first(dirs[:1], stem+"-thumb.jpg", stem+"-thumb.png"); key != "" {
			out["item/still"] = key
		}

		// A season folder's own images are that season's, as Kodi and Jellyfin
		// lay them out; the show's are in the folder above it.
		show, inSeason := dirs, seasonFolderName.MatchString(filepath.Base(dir))
		if inSeason {
			show = dirs[1:]
		}
		if key := first(show, videoArtworkNames("poster", "folder", "show")...); key != "" {
			out["show/poster"] = key
		}
		if key := first(show, videoArtworkNames("fanart", "backdrop", "art", "background")...); key != "" {
			out["show/backdrop"] = key
		}
		if key := first(show, videoArtworkNames("logo", "clearlogo")...); key != "" {
			out["show/logo"] = key
		}
		if season >= 0 && season <= 9999 {
			names := videoArtworkNames("season"+strconv.Itoa(season), "season"+twoDigits(season), "season"+twoDigits(season)+"-poster", "Season"+strconv.Itoa(season), "Season"+twoDigits(season), "Season"+twoDigits(season)+"-poster")
			if season == 0 {
				names = append(names, videoArtworkNames("season-specials-poster")...)
			}
			key := first(dirs, names...)
			if key == "" && inSeason {
				key = first(dirs[:1], videoArtworkNames("poster", "folder")...)
			}
			if key != "" {
				out["season/"+strconv.Itoa(season)+"/poster"] = key
			}
		}
	case "music":
		return musicSidecars(root, path, first)
	case "audiobook":
		if key := first(dirs, "artist.jpg", "artist.png"); key != "" {
			out["artist/portrait"] = key
		}
		if key := first(dirs, "fanart.jpg", "fanart.png", "backdrop.jpg", "backdrop.png"); key != "" {
			out["artist/backdrop"] = key
		}
		// The album's own parent-folder cover (CD1/-style layouts); the
		// file's own folder is already covered by PrepareKind.
		if len(dirs) > 1 {
			if key := first(dirs[1:], "cover.jpg", "cover.png", "folder.jpg", "folder.png"); key != "" {
				out["album/cover"] = key
			}
		}
	}
	return out
}

// seasonFolderName is the catalogue's own rule for a season folder (see
// catalog.ParseEpisode): "Season 1", "season_02", "Specials".
var seasonFolderName = regexp.MustCompile(`(?i)^(?:season[ ._-]*[0-9]{1,4}|specials)$`)

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// underRoot reports whether dir is strictly under the library root (both
// absolute and cleaned): sidecar discovery never reads at or above the root,
// so a stray file at the root can never tattoo every entity beneath it.
func underRoot(root, dir string) bool {
	root, dir = filepath.Clean(root), filepath.Clean(dir)
	return dir != root && strings.HasPrefix(dir, root+string(filepath.Separator))
}

type listingKey struct{}

// WithListing tells sidecar lookups under ctx what the scan already saw. listed
// reports whether a file is at path and whether the scan knows: it must answer
// known only for a directory it listed completely and that has not changed
// since. A lookup for a file known to be absent is skipped; every other lookup
// asks storage as it always did, so a listing can only save a read that would
// have failed.
func WithListing(ctx context.Context, listed func(path string) (present, known bool)) context.Context {
	return context.WithValue(ctx, listingKey{}, listed)
}

// unlisted reports that the scan listed path's directory and saw no such file.
func unlisted(ctx context.Context, path string) bool {
	listed, ok := ctx.Value(listingKey{}).(func(string) (bool, bool))
	if !ok || listed == nil {
		return false
	}
	present, known := listed(path)
	return known && !present
}

// sidecarKey reads one sidecar file into the local artwork cache, returning
// its key. Positive hits are cached by path within a bounded map.
func (s *Service) sidecarKey(ctx context.Context, library, full string) (string, bool) {
	if unlisted(ctx, full) {
		s.mu.Lock()
		delete(s.sidecars, full)
		s.mu.Unlock()
		return "", false
	}
	s.mu.Lock()
	if key, ok := s.sidecars[full]; ok {
		s.mu.Unlock()
		return key, true
	}
	s.mu.Unlock()
	if unlisted(ctx, full) {
		return "", false
	}
	raw, err := s.readSmall(ctx, library, full, 4<<20)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			artworkReadFailed(ctx)
		}
		return "", false
	}
	key, err := s.store(ctx, raw)
	if err != nil {
		artworkReadFailed(ctx)
		return "", false
	}
	s.mu.Lock()
	if s.sidecars == nil {
		s.sidecars = map[string]string{}
	}
	if len(s.sidecars) > 1024 {
		s.sidecars = map[string]string{}
	}
	s.sidecars[full] = key
	s.mu.Unlock()
	return key, true
}

// InvalidateSidecars forgets positive cache entries when inventory observes
// changed or removed sidecars. Content-addressed stored images stay intact.
func (s *Service) InvalidateSidecars(folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path := range s.sidecars {
		relative, err := filepath.Rel(folder, path)
		if err == nil && filepath.IsLocal(relative) {
			delete(s.sidecars, path)
		}
	}
}

type artworkReadCheckKey struct{}

func WithArtworkReadCheck(ctx context.Context, failed func()) context.Context {
	return context.WithValue(ctx, artworkReadCheckKey{}, failed)
}
func artworkReadFailed(ctx context.Context) {
	if failed, ok := ctx.Value(artworkReadCheckKey{}).(func()); ok {
		failed()
	}
}
