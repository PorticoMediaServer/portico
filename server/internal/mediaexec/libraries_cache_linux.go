//go:build linux

package mediaexec

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
)

// This memo saves repeated ELF parsing, not custody checks. Every hit stats the
// exact aliases/directories and checks the consumed ELF metadata bytes. Changes
// force the original bounded resolver; it still decides every mounted path.
type libraryNode struct {
	entry    os.FileInfo
	target   os.FileInfo
	resolved string
	metadata *libraryMetadata
}

type libraryWatch map[string]libraryNode

func (w libraryWatch) record(path string) {
	if _, exists := w[path]; exists {
		return
	}
	entry, _ := os.Lstat(path)
	target, _ := os.Stat(path)
	resolved, _ := filepath.EvalSymlinks(path)
	w[path] = libraryNode{entry: entry, target: target, resolved: resolved}
}

func sameLibraryInfo(previous, now os.FileInfo, err error) bool {
	if previous == nil {
		return os.IsNotExist(err)
	}
	if err != nil || !os.SameFile(previous, now) || previous.Mode() != now.Mode() || previous.Size() != now.Size() || !previous.ModTime().Equal(now.ModTime()) {
		return false
	}
	before, okBefore := previous.Sys().(*syscall.Stat_t)
	after, okAfter := now.Sys().(*syscall.Stat_t)
	// ctime catches in-place changes followed by restoration of mtime/size.
	return okBefore && okAfter && before.Ctim == after.Ctim
}

func (w libraryWatch) unchanged() bool {
	for path, previous := range w {
		entry, entryErr := os.Lstat(path)
		target, targetErr := os.Stat(path)
		resolved, _ := filepath.EvalSymlinks(path)
		if !sameLibraryInfo(previous.entry, entry, entryErr) ||
			!sameLibraryInfo(previous.target, target, targetErr) || resolved != previous.resolved {
			return false
		}
		if previous.metadata != nil && !previous.metadata.unchanged(path, previous.target) {
			return false
		}
	}
	return true
}

type libraryClosure struct {
	paths []string
	watch libraryWatch
}

var libraryClosures = struct {
	sync.Mutex
	entries map[string]*libraryClosure
}{entries: map[string]*libraryClosure{}}

func cachedLibraryClosure(key string) []string {
	libraryClosures.Lock()
	entry := libraryClosures.entries[key]
	libraryClosures.Unlock()
	if entry == nil || !entry.watch.unchanged() {
		return nil
	}
	return slices.Clone(entry.paths)
}

func rememberLibraryClosure(key string, paths []string, watch libraryWatch) {
	libraryClosures.Lock()
	defer libraryClosures.Unlock()
	// Each closure already has maxLibraryFiles as a hard bound. Keep at most
	// 32 tool/explicit-library combinations, independent of request volume.
	if len(libraryClosures.entries) >= 32 {
		for oldest := range libraryClosures.entries {
			delete(libraryClosures.entries, oldest)
			break
		}
	}
	libraryClosures.entries[key] = &libraryClosure{slices.Clone(paths), watch}
}
