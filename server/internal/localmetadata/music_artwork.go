package localmetadata

import (
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

var musicImageExtensions = []string{"jpg", "jpeg", "png", "tbn", "webp"}

func musicImageNames(stems ...string) []string {
	names := make([]string, 0, len(stems)*len(musicImageExtensions))
	for _, stem := range stems {
		for _, extension := range musicImageExtensions {
			names = append(names, stem+"."+extension)
		}
	}
	return names
}

func musicCoverNames() []string {
	// Preserve the existing front/Cover.JPG aliases as well as §45's names.
	return append(musicImageNames("album", "cover", "default", "folder", "poster", "front"), "Cover.JPG", "Folder.jpg")
}

func musicSidecars(root, path string, first func([]string, ...string) string) map[string]string {
	out := map[string]string{}
	album := filepath.Dir(path)
	if _, disc := naming.DiscFolder(filepath.Base(album)); disc {
		if parent := filepath.Dir(album); parent != album && underRoot(root, parent) {
			album = parent
		}
	}
	artistDirs := []string{}
	if artist := filepath.Dir(album); artist != album && underRoot(root, artist) {
		artistDirs = append(artistDirs, artist)
	}
	poster := first(artistDirs, musicImageNames("artist", "artist-cover", "artist-default", "artist-folder", "artist-poster", "cover", "default", "folder", "poster")...)
	if poster == "" {
		// Keep explicitly artist-named images beside a track compatible. An
		// album's generic cover/folder/poster never becomes its artist portrait.
		poster = first([]string{album}, musicImageNames("artist", "artist-cover", "artist-default", "artist-folder", "artist-poster")...)
	}
	if poster != "" {
		out["artist/portrait"] = poster
	}
	artistBackdrop := first(artistDirs, musicImageNames("art", "artist-art", "artist-backdrop", "artist-background", "artist-fanart", "backdrop", "background", "fanart")...)
	if artistBackdrop == "" {
		artistBackdrop = first([]string{album}, musicImageNames("artist-art", "artist-backdrop", "artist-background", "artist-fanart")...)
	}
	if artistBackdrop != "" {
		out["artist/backdrop"] = artistBackdrop
	}
	if cover := first([]string{album}, musicCoverNames()...); cover != "" {
		out["album/cover"] = cover
	}
	backdrop := first([]string{album}, musicImageNames("art", "backdrop", "background", "fanart")...)
	if backdrop == "" {
		backdrop = artistBackdrop
	}
	if backdrop != "" {
		out["album/backdrop"] = backdrop
	}
	return out
}
