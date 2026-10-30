package atomicfile

// Windows has no directory handle to flush: a directory cannot be opened for
// the kind of synchronisation Unix offers, and NTFS journals the rename itself.
// The rename is still atomic, which is the property this package exists for.
func syncDirectory(string) {}
