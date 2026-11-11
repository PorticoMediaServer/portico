package playback

import "github.com/PorticoMediaServer/portico/server-next/internal/db"

// Counter takes the server's lifetime statistics (package console): plays started, time
// played, data sent, people and the most streams at once, each day.
type Counter interface {
	Add(metric string, n int64)
	Most(metric string, n int64)
	Person(userID int64)
}

// CountInto gives the service somewhere to count. Without it nothing is counted.
func (s *Service) CountInto(c Counter) { s.totals = c }

// kindName is a title's kind as the statistics name it.
func kindName(kind db.ItemKind) string {
	switch kind {
	case db.KindMovie:
		return "film"
	case db.KindEpisode:
		return "episode"
	case db.KindTrack:
		return "music"
	case db.KindAudiobook:
		return "audiobook"
	}
	return "other"
}

// split is the part of a metric's name after the metric: the kind of media and how it is
// delivered, as in "plays.film.converted".
func split(sess *Session) string { return "." + kindName(sess.Item.Kind) + "." + sess.Plan.Route }

// StreamFacts says, for the grant in a media address, how its bytes are counted: the split of
// the metric and whether the stream is away from home. ok is false for no running stream.
func (s *Service) StreamFacts(grant string) (metricSplit string, away, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.Stream.Grant == grant && grant != "" {
			return split(&sess.Session), sess.Network != NetworkLocal, true
		}
	}
	return "", false, false
}
