//go:build linux

package mediaexec

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type runtimeGeneration struct {
	identity string
	watch    libraryWatch
	// checked is when the watch was last confirmed current.
	checked time.Time
}

// runtimeGenerationRecheck is how long a confirmed generation is trusted
// before its files are stat'ed again. Confirming one stats every executable,
// library and alias in the closure (about 600 system calls for a distribution
// FFmpeg), and playback-options asked on every call: 7% of a busy server's
// CPU. A replaced binary or library is noticed within this window.
var runtimeGenerationRecheck = 5 * time.Second

var runtimeGenerations = struct {
	sync.Mutex
	entries map[string]runtimeGeneration
}{entries: map[string]runtimeGeneration{}}

func runtimeGenerationKey(executable string, extra []string) string {
	return strings.Join(append(append([]string{executable}, extra...), environment()...), "\x00")
}

// Only metadata is inspected here. ELF parsing, metadata-byte validation and
// full-content hashing belong to startup/background observation, never a hit.
func (w libraryWatch) generationCurrent(ctx context.Context) bool {
	for path, previous := range w {
		if ctx.Err() != nil {
			return false
		}
		entry, entryErr := os.Lstat(path)
		target, targetErr := os.Stat(path)
		resolved, _ := filepath.EvalSymlinks(path)
		if !sameLibraryInfo(previous.entry, entry, entryErr) ||
			!sameLibraryInfo(previous.target, target, targetErr) || resolved != previous.resolved {
			return false
		}
	}
	return ctx.Err() == nil
}

func runtimeGenerationWatch(executable string, extra, paths []string) libraryWatch {
	key := strings.Join(append([]string{executable, executable}, extra...), "\x00")
	libraryClosures.Lock()
	closure := libraryClosures.entries[key]
	libraryClosures.Unlock()
	if closure == nil || !slices.Equal(closure.paths, paths) {
		return nil
	}
	watch := make(libraryWatch, len(closure.watch))
	for path, node := range closure.watch {
		node.metadata = nil
		watch[path] = node
	}
	return watch
}

func rememberRuntimeGeneration(ctx context.Context, key, identity string, watch libraryWatch) {
	if len(watch) == 0 || !watch.generationCurrent(ctx) {
		return
	}
	runtimeGenerations.Lock()
	defer runtimeGenerations.Unlock()
	if len(runtimeGenerations.entries) >= 32 {
		for previous := range runtimeGenerations.entries {
			delete(runtimeGenerations.entries, previous)
			break
		}
	}
	runtimeGenerations.entries[key] = runtimeGeneration{identity: identity, watch: watch, checked: time.Now()}
}

// CachedRuntimeIdentity returns a completed native observation only while its
// executable, dependency aliases and loader search directories retain the same
// generation. A miss performs no content reads or ELF resolution. The caller
// can use software immediately and arrange a background observation.
// An owner changing code in place without changing any observed metadata must
// restart Portico. This is a generation cache, not fresh byte attestation.
func CachedRuntimeIdentity(ctx context.Context, executable string) (string, bool) {
	if ctx == nil || ctx.Err() != nil {
		return "", false
	}
	key := runtimeGenerationKey(executable, configuredLibraries())
	runtimeGenerations.Lock()
	entry, found := runtimeGenerations.entries[key]
	runtimeGenerations.Unlock()
	if !found {
		return "", false
	}
	if time.Since(entry.checked) >= runtimeGenerationRecheck {
		if !entry.watch.generationCurrent(ctx) {
			return "", false
		}
		runtimeGenerations.Lock()
		if current, ok := runtimeGenerations.entries[key]; ok && current.identity == entry.identity {
			current.checked = time.Now()
			runtimeGenerations.entries[key] = current
		}
		runtimeGenerations.Unlock()
	}
	if ctx.Err() != nil || key != runtimeGenerationKey(executable, configuredLibraries()) {
		return "", false
	}
	return entry.identity, true
}
