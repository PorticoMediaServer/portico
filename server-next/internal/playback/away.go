package playback

import (
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// The owner's limits on plays away from home (Settings › Streaming): the most a play away
// from home may take, and the home connection's upload speed, of which away streams share
// 80%. They act only when a play starts and when an away viewer changes quality: a stream
// already playing is never lowered, paused or stopped by them. Nobody at home is limited.
// A viewer is offered only what fits and is never told there is a limit; when not even the
// lowest quality fits in what is left of the upload speed, the play is refused.

// ErrUploadFull: the away streams already use the share of the upload speed this play needs.
var ErrUploadFull = errors.New("this server's internet connection is full right now")

// uploadSharePercent is the part of the upload speed away streams share. The rest is left
// for everything else the household sends.
const uploadSharePercent = 80

// cdKbps is what CD quality takes.
const cdKbps = 1411

// UseSettings gives the service the owner's settings. Without them every limit is off.
func (s *Service) UseSettings(st *settings.Settings) { s.settings = st }

// room is the most a play may take, in kbps. limited is false when nothing limits it; upload
// says the upload speed is what limits it. self is the session being changed and deviceID the
// device asking: neither counts against this play, since a start on a device replaces what
// that device plays. admitting is false for a change that keeps the quality (a subtitle or
// sound track): no limit applies to it, so a limit lowered after the stream began never
// lowers it.
func (s *Service) room(kind db.ItemKind, network, self string, deviceID int64, admitting bool) (kbps int, limited, upload bool) {
	if network == NetworkLocal || !admitting {
		return 0, false, false
	}
	owner := settings.Streaming.Get(s.settings)
	kbps = owner.AwayVideoKbps
	if audioKind(kind) {
		kbps = owner.AwayMusicKbps
	}
	limited = kbps > 0
	if owner.UploadSpeedKbps <= 0 {
		return kbps, limited, false
	}
	s.mu.Lock()
	left := owner.UploadSpeedKbps*uploadSharePercent/100 - s.awayInUseLocked(self, deviceID)
	s.mu.Unlock()
	if !limited || left < kbps {
		return left, true, true
	}
	return kbps, true, false
}

// awayInUseLocked is what the away streams take, each at the quality it is playing, leaving
// out self and whatever deviceID plays (a start there replaces it). Music counts like video.
// Caller holds s.mu.
func (s *Service) awayInUseLocked(self string, deviceID int64) int {
	used := 0
	for _, other := range s.sessions {
		if other.State != StateEnded && other.ID != self && other.Device.ID != deviceID && other.Network != NetworkLocal {
			used += other.awayKbps
		}
	}
	return used
}

// measure is what a quality takes on this version, in kbps, or 0 when that is not known.
func measure(version db.PlaybackVersion, kind db.ItemKind, menu []QualityOption, q Quality) int {
	original := int(version.Bitrate / 1000)
	switch q.Mode {
	case QualityOriginal:
		return original
	case QualityCD:
		return min(cdKbps, max(original, 1)) // a file smaller than CD quality plays as it is
	case QualityLimit:
		if q.MaxVideoBitrateKbps == 0 && q.MaxAudioBitrateKbps == 0 {
			return original
		}
		return q.MaxVideoBitrateKbps + q.MaxAudioBitrateKbps
	}
	if q.ID == "" { // automatic, starting at the original
		return original
	}
	for _, o := range menu {
		if o.ID == q.ID {
			if o.SameAsOriginal && original > 0 {
				return original
			}
			return o.MaxVideoBitrateKbps + o.MaxAudioBitrateKbps
		}
	}
	return original
}

// fit narrows a play away from home to kbps: the menu keeps only what fits, and a quality that
// does not fit becomes the best one that does (automatic stays automatic, starting there).
// When nothing fits in what is left of the upload speed the play is refused; when nothing
// fits under the owner's own limit, the lowest quality is the closest there is.
func fit(version db.PlaybackVersion, kind db.ItemKind, menu []QualityOption, q Quality, kbps int, upload bool) ([]QualityOption, Quality, error) {
	fits := func(q Quality) bool {
		m := measure(version, kind, menu, q)
		return m > 0 && m <= kbps
	}
	var kept []QualityOption
	var best, lowest *QualityOption
	for i, o := range menu {
		if o.Mode == QualityAutomatic {
			kept = append(kept, o)
			continue
		}
		if o.Mode == QualityFixed || o.Mode == QualityLimit {
			lowest = &menu[i]
		}
		if !fits(optionQuality(o)) {
			continue
		}
		kept = append(kept, o)
		if best == nil && (o.Mode == QualityFixed || o.Mode == QualityLimit) {
			best = &menu[i]
		}
	}
	if fits(q) {
		return kept, q, nil
	}
	if best == nil {
		if upload {
			return nil, q, ErrUploadFull
		}
		if lowest == nil {
			return menu, q, nil
		}
		best = lowest
		kept = append(kept, *lowest)
	}
	out := optionQuality(*best)
	if q.Mode == QualityAutomatic {
		out.Mode = QualityAutomatic
	}
	return kept, out, nil
}

// UploadHeld is what the upload speed is holding back right now, for the owner's Dashboard
// ("Upload speed is holding back 2 streams"). It is counted in memory and starts again from
// nothing when the server does.
type UploadHeld struct {
	// Narrowed is how many streams are playing away from home at a lower quality than they
	// asked for because of the upload speed.
	Narrowed int
	// Refused is how many starts were refused for it in the last five minutes.
	Refused int
}

// uploadRefusalsKept is how long a refused start counts.
const uploadRefusalsKept = 5 * time.Minute

// UploadHeld counts what the upload speed is holding back.
func (s *Service) UploadHeld() UploadHeld {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out UploadHeld
	for _, sess := range s.sessions {
		if sess.State != StateEnded && sess.Network != NetworkLocal && sess.uploadHeld {
			out.Narrowed++
		}
	}
	out.Refused = len(s.recentRefusalsLocked())
	return out
}

// uploadRefused notes a start refused because the upload speed is full.
func (s *Service) uploadRefused() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A burst of retries is bounded: the Dashboard has no use for more than "many".
	if s.refusals = append(s.recentRefusalsLocked(), s.now()); len(s.refusals) > 1000 {
		s.refusals = s.refusals[len(s.refusals)-1000:]
	}
}

// recentRefusalsLocked drops the refusals that no longer count. Caller holds s.mu.
func (s *Service) recentRefusalsLocked() []time.Time {
	cut := s.now().Add(-uploadRefusalsKept)
	for len(s.refusals) > 0 && s.refusals[0].Before(cut) {
		s.refusals = s.refusals[1:]
	}
	return s.refusals
}
