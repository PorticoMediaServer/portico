package playback

// Streams is the engine set with SetEngine, or nil before it is set.
func (s *Service) Streams() Streams { return s.streams }
