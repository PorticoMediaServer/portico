//go:build !linux && !darwin

package reading

// Windows cannot enforce the worker's resource limits, so the helper is refused
// before any staging or provider read.
func helperPlatformSupported() bool { return false }
