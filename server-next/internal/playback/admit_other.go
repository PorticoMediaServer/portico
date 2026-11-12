package playback

// AdmitOther admits a stream held outside these sessions (a live channel watched) by the same
// count a title's start makes, in the same step: hold reserves its place, under the lock title
// starts count under, so two starts at once, of channels or of a channel and a title, cannot
// both take the last place. What signInID plays now is not counted: the start replaces it.
// accountLimit and totalLimit of 0 are none.
func (s *Service) AdmitOther(userID, signInID int64, accountLimit, totalLimit int, hold func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, total := 0, 0
	for _, other := range s.sessions {
		if other.State == StateEnded || signInID != 0 && other.SignInID == signInID {
			continue
		}
		total++
		if other.UserID == userID {
			account++
		}
	}
	if s.OtherStreams != nil {
		a, t := s.OtherStreams(userID, signInID)
		account, total = account+a, total+t
	}
	if accountLimit > 0 && account >= accountLimit {
		return &LimitReached{Scope: "account", Limit: accountLimit}
	}
	if totalLimit > 0 && total >= totalLimit {
		return &LimitReached{Scope: "server", Limit: totalLimit}
	}
	hold()
	return nil
}
