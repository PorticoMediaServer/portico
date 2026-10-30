//go:build windows

package backup

import "os"

// Windows has no supported FlushFileBuffers operation on a directory handle.
// Files are flushed before publication; NTFS journals the subsequent renames.
func syncDirectory(string) error { return nil }

func syncDirectoryHandle(*os.File) error { return nil }
