//go:build !linux

package playback

import "errors"

func checkMemoryOutput(string) error {
	return errors.New("memory conversion output requires Linux tmpfs")
}
func readMemoryFacts(string) (memoryFacts, error) {
	return memoryFacts{}, errors.New("memory facts unavailable")
}
