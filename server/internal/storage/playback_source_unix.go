//go:build !windows

package storage

import (
	"context"
	"os"
)

// OpenPlaybackSource opens the source for a converter and reports what to hand
// ffmpeg after `-i`. On Unix that is the validated descriptor the helper
// transferred, inherited by the child as file descriptor 3 — so the converter
// reads the exact file that was checked, and a rescan that replaces the path
// cannot substitute different media into a running stream.
//
// The returned bool says the file must be placed in the child's ExtraFiles.
func (c *Client) OpenPlaybackSource(ctx context.Context, path string, size, modified int64, bindings ...*PlaybackBinding) (*os.File, string, bool, error) {
	file, err := c.OpenPlaybackDescriptor(ctx, path, size, modified, bindings...)
	if err != nil {
		return nil, "", false, err
	}
	return file, "/dev/fd/3", true, nil
}
