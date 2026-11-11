package mediaexec

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The owner can start the server with LIBVA_DRIVERS_PATH and LD_LIBRARY_PATH naming folders
// for a graphics driver kept outside the system's own. On Linux those folders are shown,
// read-only, to hardware jobs inside their sandbox. Two kinds of folder are never shown.

// ownerFolders is the folders of such a list that a hardware job may read.
func ownerFolders(list string) []string {
	shown, _ := sortOwnerFolders(list)
	return shown
}

// sortOwnerFolders splits a path list into the folders shown to hardware jobs and the ones
// skipped, each with why. A relative or unclean entry, or the whole file system, is dropped
// without a word: the loader would not use it as written either.
func sortOwnerFolders(list string) (shown []string, skipped [][2]string) {
	configured.RLock()
	data := configured.dataFolder
	configured.RUnlock()
	temporary := resolvedFolder(os.TempDir())
	for _, dir := range filepath.SplitList(list) {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
			continue
		}
		real := resolvedFolder(dir)
		switch {
		case holds(real, data):
			// It would hand the database and the keys to every hardware job. Skipping costs
			// nothing: the loader does not search subfolders.
			skipped = append(skipped, [2]string{dir, "it is or contains the server's data folder"})
		case holds(real, temporary):
			// Mounted after the job's own private temporary folder, it would put the
			// machine's in its place.
			skipped = append(skipped, [2]string{dir, "it is or contains the temporary folder"})
		default:
			shown = append(shown, dir)
		}
	}
	return shown, skipped
}

// holds reports whether folder is inner or one of the folders above it.
func holds(folder, inner string) bool {
	return inner != "" && (folder == inner || strings.HasPrefix(inner, strings.TrimSuffix(folder, "/")+"/"))
}

// resolvedFolder is a folder as an absolute path with links followed, or as given where it
// cannot be resolved.
func resolvedFolder(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

// reportOwnerFolders says once, at start, which of the owner's folders hardware jobs will be
// shown and which are skipped.
func reportOwnerFolders() {
	if runtime.GOOS != "linux" {
		return
	}
	for _, name := range []string{"LIBVA_DRIVERS_PATH", "LD_LIBRARY_PATH"} {
		shown, skipped := sortOwnerFolders(os.Getenv(name))
		for _, skip := range skipped {
			log.Printf("warn: [playback] %s names %s, which is not shown to hardware conversions: %s. Name the driver's own folder.", name, skip[0], skip[1])
		}
		if len(shown) > 0 {
			log.Printf("info: [playback] hardware conversions can read %s (from %s).", strings.Join(shown, ", "), name)
		}
	}
}
