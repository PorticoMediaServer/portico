package mediaengine

import (
	"context"
	"slices"
)

// ConversionFolders includes an older folder while a stream still uses it.
func (e *Engine) ConversionFolders() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []string{e.current.folder}
	for _, p := range e.retired {
		if !slices.Contains(out, p.folder) {
			out = append(out, p.folder)
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
