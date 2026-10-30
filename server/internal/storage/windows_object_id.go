package storage

import "fmt"

func observedWindowsID(volume uint64, id [16]byte) (string, error) {
	if id == [16]byte{} {
		return "", ErrObservedSourceUnsupported
	}
	return fmt.Sprintf("volume:%d:file:%x", volume, id), nil
}
