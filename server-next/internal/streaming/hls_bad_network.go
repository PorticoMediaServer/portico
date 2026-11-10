package streaming

import (
	"os"
	"time"
)

// Playback on a bad network is not playback that fails. It is playback that
// keeps asking: a phone on a train reconnects every few seconds, a television
// on congested wifi abandons a segment request and immediately makes another,
// a person scrubbing over a satellite link sends twenty seeks before the first
// one has been answered. Every one of those is a healthy client behaving
// correctly for its circumstances, and every one of them used to cost this
// server the same as a real new viewer.
//
// Three things here, each bounding one of those:
//
//   - A conversion whose viewer has gone silent is freed, without ending
//     their session. The segments stay on disk and the producer restarts the
//     moment they come back.
//   - Many requests waiting for the same segment share one wait, instead of each
//     polling the database ten times a second.
//   - A session may restart its producer often, but not without limit, so a
//     scrub storm cannot become a storm of ffmpeg restarts.

const (
	// hlsAbandonedAfter is how long a conversion runs without anybody fetching a
	// segment from it before it is freed. It is deliberately longer than any
	// pause a network causes and far shorter than the session lifetime: a viewer
	// who walks away mid-film, or whose phone loses signal in a tunnel, stops
	// costing the server a converter within two minutes and loses nothing.
	hlsAbandonedAfter = 2 * time.Minute
	// hlsRestartWindow and hlsRestartBudget bound producer restarts per session.
	// A person scrubbing makes a few seeks a second at most and never reaches
	// this; a client in a reconnect loop, or one whose seek requests are being
	// retried by a proxy, reaches it immediately.
	hlsRestartWindow = 10 * time.Second
	hlsRestartBudget = 12
)

// hlsAbandonedAfterValue is the live threshold; the constant above is its
// default and its documentation.
var hlsAbandonedAfterValue = hlsAbandonedAfter

// hlsAbandonedAfterForTest moves the free threshold a test needs to cross.
func hlsAbandonedAfterForTest(v time.Duration) time.Duration {
	previous := hlsAbandonedAfterValue
	hlsAbandonedAfterValue = v
	return previous
}

// restartBudget is one session's producer-restart allowance.
type restartBudget struct {
	window time.Time
	count  int
}

// segmentWait is one shared wait for one segment of one session.
type segmentWait struct {
	done chan struct{}
	path string
	err  error
}

// segmentReady is the file check a wait ends on, factored out so the leader and
// a late follower answer identically.
func segmentReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}
