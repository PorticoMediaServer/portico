//go:build windows

package storage

import (
	"context"
	"io"
	"os"
)

// DescriptorPassing reports that a helper can hand the server an open file.
// Windows has no SCM_RIGHTS equivalent wired up here, so direct play keeps the
// fully isolated reader and converted playback is not yet available.
const DescriptorPassing = false

func (c *Client) OpenPlaybackDescriptor(context.Context, string, int64, int64, ...*PlaybackBinding) (*os.File, error) {
	return nil, ErrPlaybackSource
}
func playbackDescriptorHelper(request, io.Writer) error { return ErrPlaybackSource }

func (c *Client) ValidatePlaybackDescriptor(context.Context, *os.File, int64, int64) error {
	return ErrPlaybackSource
}
func playbackValidateDescriptorHelper(request, io.Writer) error { return ErrPlaybackSource }

func (c *Client) OpenDiscPlaybackDescriptor(context.Context, InventoryRequest, string, int64, int64, bool) (*os.File, error) {
	return nil, ErrPlaybackSource
}
