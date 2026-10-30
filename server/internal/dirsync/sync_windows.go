//go:build windows

// Package dirsync provides the directory durability policy for file publishers.
package dirsync

// Sync is a no-op on Windows, matching backup and migration persistence.
// Windows does not support flushing the read-only directory handles used by Go.
// Publishers still flush and close file contents before atomic rename/link;
// directory-entry crash durability depends on the filesystem's journal.
func Sync(string) error { return nil }
