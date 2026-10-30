//go:build !linux && !darwin

package operations

import "errors"

func processCPU() Fact {
	return unavailable("Process CPU measurement is not supported on this platform.")
}
func volumeAvailable(path string) Fact {
	return unavailable("Volume measurement is not supported on this platform.")
}

func mediaVolume(string) (string, uint64, error) {
	return "", 0, errors.New("volume measurement unsupported")
}
