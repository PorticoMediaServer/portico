// Package atomicfile writes a file so that a reader never sees a partial one.
//
// os.WriteFile into a directory something else is already reading is a file
// that exists, is the right name, and is the wrong length for as long as the
// write takes. A crash in the middle leaves it that way permanently, and the
// next read treats it as valid because nothing about it says otherwise. That is
// how a truncated subtitle track or a half-written channel logo becomes a
// permanent defect in a library rather than a transient one.
//
// The fix is the one the artwork worker already used and nothing else did:
// write to a temporary name in the same directory, flush it, and rename it into
// place. Rename within a directory is atomic on every filesystem this server
// supports, so a reader sees either the old file or the whole new one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write creates or replaces path with data. The temporary file is created in
// the same directory, because a rename across filesystems is a copy and is not
// atomic.
func Write(path string, data []byte, perm os.FileMode) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	// Removing the temporary file is harmless after a successful rename — it no
	// longer exists under this name — and essential after a failure.
	defer os.Remove(name)
	if err = temporary.Chmod(perm); err != nil {
		temporary.Close()
		return err
	}
	if _, err = temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	// The flush matters as much as the rename. Without it the rename can reach
	// the disk before the bytes do, which on a power cut leaves a correctly
	// named file full of zeroes — worse than a partial one, because it looks
	// deliberate.
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	// Best effort: the rename is durable on the platforms where this succeeds,
	// and the platforms where it does not (Windows) do not offer it at all.
	syncDirectory(directory)
	return nil
}
