//go:build !windows

package storagepaths

func distinctNativeVolumes(_, _ string) (bool, error) { return false, nil }
