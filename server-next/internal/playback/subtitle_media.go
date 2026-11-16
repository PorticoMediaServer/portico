package playback

import "context"

// SubtitleMediaVisible checks the profile behind a live playback grant now,
// including rating and library limits. A subtitle address never names a path.
func (s *Service) SubtitleMediaVisible(ctx context.Context, grant string) bool {
	s.mu.Lock()
	var found *Session
	for _, sess := range s.sessions {
		if sess.State != StateEnded && sess.Stream.Grant == grant && grant != "" {
			copy := sess.Session
			found = &copy
			break
		}
	}
	s.mu.Unlock()
	if found == nil {
		return false
	}
	visible, err := s.db.Visible(ctx, found.Viewer(), found.Item.ID)
	return err == nil && visible
}
