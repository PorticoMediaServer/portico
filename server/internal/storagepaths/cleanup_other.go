//go:build !linux && !darwin

package storagepaths

// Until this platform has a complete volume-descendant enumerator, decline
// destructive cleanup. Startup may still use the existing work directories.
func cleanupMounts() ([]Mount, error) { return nil, ErrCleanupUnavailable }
