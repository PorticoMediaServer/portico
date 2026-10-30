package playback

// MemoryOutputAvailable reports this installation's initialized private root,
// rather than promising memory solely because a shared tmpfs mount exists.
// A failed ownership/setup check at startup therefore remains visible in Settings.
func (s *Service) MemoryOutputAvailable(_ string, setting string) bool {
	return s != nil && s.hls != nil && MemoryOutputAvailable(s.hls.memoryRoot, setting)
}
