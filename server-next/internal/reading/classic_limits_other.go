//go:build !linux && !darwin && !windows

package reading

func archiveLimits() error { return ErrFormatLater }
