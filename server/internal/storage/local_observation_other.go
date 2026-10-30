//go:build !darwin && !linux && !windows

package storage

import "os"

func observedObjectBinding(os.FileInfo, string) (string, string, error) {
	return "", "", ErrObservedSourceUnsupported
}

func checkLocalRoot(string) error { return nil }
