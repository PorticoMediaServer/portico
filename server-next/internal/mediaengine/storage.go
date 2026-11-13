package mediaengine

import (
	"context"
	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
	"slices"
)

// ConversionFolders includes an older folder while a stream still uses it.
func (e *Engine) ConversionFolders() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []string{}
	for _, p := range append([]*place{e.current}, e.retired...) {
		if !slices.Contains(out, p.folder) {
			out = append(out, p.folder)
		}
		if folder := p.streams.MemoryFolder(); folder != "" && !slices.Contains(out, folder) {
			out = append(out, folder)
		}
	}
	return out
}

// CachedConversions uses each place's existing stream/producer/read ownership checks.
func (e *Engine) CachedConversions(ctx context.Context, empty bool) (int64, error) {
	e.mu.Lock()
	places := append([]*place{e.current}, e.retired...)
	e.mu.Unlock()
	var bytes int64
	for _, p := range places {
		n, err := p.streams.CachedConversions(ctx, empty)
		bytes += n
		if err != nil {
			return bytes, err
		}
	}
	return bytes, nil
}

// A place exists only after open wrote in it successfully. Memory setup also writes its
// private ownership marker; failed setup never installs a memory folder.
func (e *Engine) ConversionFolderReady(folder string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range append([]*place{e.current}, e.retired...) {
		if p.folder == folder || p.streams.MemoryFolder() == folder {
			return true
		}
	}
	return false
}

// MemoryConversionsAvailable reports a RAM filesystem, not permission to ignore its cap.
// Actual output still has to pass ConfigureMemory and each admission's measured limits.
func (e *Engine) MemoryConversionsAvailable() bool {
	e.mu.Lock()
	folder := e.current.folder
	e.mu.Unlock()
	mounts, err := storagepaths.CurrentMounts()
	return err == nil && storagepaths.MemoryTranscodeDirectory(folder, mounts) != ""
}
