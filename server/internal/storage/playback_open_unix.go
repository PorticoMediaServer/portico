//go:build !windows

package storage

import (
	"os"
	"syscall"
)

func openPlaybackNoFollow(path string) (*os.File, error) {
	// NONBLOCK prevents a raced FIFO from occupying a storage helper indefinitely.
	// The caller checks the opened object's regular-file type before reading it.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
