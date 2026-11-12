package api

import (
	"net/http"
	"time"
)

// The owner's activity popover, on every owner page (console rule 9): the streams, at most
// three of them and how many there are, the scans and tasks running, a major problem and a
// new version. It is read from what the Dashboard and Now playing already read; nothing is
// kept for it.

// ActivitySummary is GET /v1/admin/activity, for the owner alone.
type ActivitySummary struct {
	ObservedAt time.Time `json:"observedAt"`
	ServerName string    `json:"serverName"`
	// Streams are the first three of Now playing, in its order, and how many there are.
	Streams ActivityStreams `json:"streams"`
	// Scans and Tasks are the Dashboard's.
	Scans []DashboardScan `json:"scans"`
	Tasks DashboardTasks  `json:"tasks"`
	// Alerts are the Dashboard's alerts the popover shows: the major ones (a red dot on the
	// activity glyph, console rule 4) and update_available, most urgent first.
	Alerts []DashboardAlert `json:"alerts"`
	// LastScanAt is when a scan last finished listing a library folder, for "Last scan" when
	// nothing is running; absent before the first scan.
	LastScanAt *time.Time `json:"lastScanAt,omitempty"`
}

// ActivityStreams counts every stream, paused ones included, and how many are converted, and
// holds the first three. The popover's line for the rest is Count minus len(Items) streams, of
// which Converting minus the items whose route is converted are converted.
type ActivityStreams struct {
	Count      int          `json:"count"`
	Converting int          `json:"converting"`
	Items      []NowPlaying `json:"items"`
}

// activityStreams is how many streams the popover shows (console rule 9).
const activityStreams = 3

func (s *Server) activitySummary(r *http.Request, _ None) (ActivitySummary, error) {
	board, err := s.dashboard(r, None{})
	if err != nil {
		return ActivitySummary{}, err
	}
	playing, err := s.nowPlaying(r, None{})
	if err != nil {
		return ActivitySummary{}, err
	}
	libraries, err := s.db.AdminLibraries(r.Context(), "")
	if err != nil {
		return ActivitySummary{}, err
	}
	out := ActivitySummary{ObservedAt: board.ObservedAt, ServerName: s.serverName(), Scans: board.Scans, Tasks: board.Tasks,
		Alerts: []DashboardAlert{}, Streams: ActivityStreams{Count: len(playing.Items), Converting: board.NowPlaying.Converting,
			Items: playing.Items[:min(len(playing.Items), activityStreams)]}}
	for _, a := range board.Alerts {
		if a.Major || a.Code == "update_available" {
			out.Alerts = append(out.Alerts, a)
		}
	}
	var last int64
	for _, l := range libraries {
		for _, f := range l.Folders {
			last = max(last, f.LastScanAt)
		}
	}
	out.LastScanAt = timePtr(last)
	return out, nil
}
