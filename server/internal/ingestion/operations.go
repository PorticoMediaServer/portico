package ingestion

// Quiescent is the orchestration adapter's cancellation acknowledgement. A
// durable cancelled status alone is not evidence that the worker has returned.
func (s *Service) Quiescent(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, active := s.active[id]
	return !active
}
