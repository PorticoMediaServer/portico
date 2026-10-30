package remotesources

// sourcePacerBackoff projects only the actual current serve process's report.
// It does not infer provider state from capacity contention or a loopback HTTP
// response, persist private log text, create a deadline or authorize any work.
func (s *Service) sourcePacerBackoff(source, generation string) bool {
	s.serveMu.Lock()
	slot := s.serveLeases[source]
	if slot == nil || slot.generation != generation || slot.CheckLocal() != nil {
		s.serveMu.Unlock()
		return false
	}
	process := slot.process
	s.serveMu.Unlock()
	return process != nil && process.PacerObservation().Backoff
}

// sourceOperationInUse samples the shared source operation budget through the
// current serve process for this exact generation. The budget also admits
// direct WebDAV requests and saved-source listings; it is not a serve-only
// count. Mounted media reads outside that budget are not measured here. One
// permit is one admitted operation, never a socket or a distinct viewer.
// Re-fencing the slot, generation and process rejects replacement/retirement
// during the sample. Without that live process handle the count is unknown,
// even when direct reads hold permits on the source budget.
func (s *Service) sourceOperationInUse(source, generation string) (int, bool) {
	if s == nil || source == "" || generation == "" {
		return 0, false
	}
	s.serveMu.Lock()
	slot := s.serveLeases[source]
	if slot == nil || slot.generation != generation || slot.CheckLocal() != nil {
		s.serveMu.Unlock()
		return 0, false
	}
	process := slot.process
	s.serveMu.Unlock()
	if process == nil {
		return 0, false
	}
	active := process.OperationCounts().Active
	s.serveMu.Lock()
	current := s.serveLeases[source]
	ok := current == slot && current.generation == generation && current.process == process && current.CheckLocal() == nil
	s.serveMu.Unlock()
	if !ok {
		return 0, false
	}
	return active, true
}
