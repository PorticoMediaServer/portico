//go:build !windows

// Package dirsync provides the directory durability policy for file publishers.
package dirsync

import (
	"errors"
	"os"
)

// Sync flushes directory entries after publication. Unix failures are returned
// to the publisher; a failed flush must never acknowledge durable publication.
func Sync(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
