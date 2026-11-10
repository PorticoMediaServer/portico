package streaming

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// ErrGrantEnded answers a media address whose stream is over: stopped, failed,
// replaced, or lost with a server restart. It is a hidden 404.
var ErrGrantEnded = errors.New("This playback has ended. Start it again.")

// ErrSegmentPreparing answers a request for output the converter has not
// written yet. It is retryable.
var ErrSegmentPreparing = errors.New("Media is still being prepared. Retry playback.")

// ErrIncompatible says no conversion this server can run would play the source.
var ErrIncompatible = errors.New("source requires a playback conversion unavailable on this server")

// Stream states. A stream is live from Start until it is stopped or fails.
const (
	streamLive    = ""
	streamStopped = "stopped"
	streamFailed  = "failed"
)

// stream is one deliverable version of a title while it plays: what the old
// server kept in its session, plan, pin and artifact rows. Every field after the
// identity is guarded by HLS.mu. Nothing here outlives the process.
type stream struct {
	// id names the output folder and keys every producer map. It is never
	// reused, so a producer that outlives its stream can only touch its own.
	id      string
	session string
	grant   string
	// mode is "direct" for the original file by byte range and "hls" for
	// everything ffmpeg produces.
	mode   string
	source Source
	// plan is the plan being produced. A producer copies it when it starts and
	// writes back only what it learns: a software fallback, a copy that had to
	// become a conversion.
	plan *DeliveryPlan
	// request is what the plan was made from, kept so the producer can plan
	// again when the picture cannot be copied after all. Nil refuses that.
	request  *PlanRequest
	duration float64
	text     []TextRenditionPlan
	burn     *BurnSubtitle
	// openInput replaces opening source as one file, and disc keeps the output on
	// disk: both are for a disc title.
	openInput inputOpener
	disc      bool

	// served is the furthest segment handed to the viewer: the position the
	// throttle and the per-stream storage ceiling are measured against.
	served int
	state  string
	// output is where the first conversion stands: "", "building", "complete"
	// or "failed".
	output  string
	failure *ConversionFailure
	// stages and launches are what the converter actually ran, for the owner's
	// playback details.
	stages    []decoder.HardwareStage
	execution ConversionExecution
	speed     float64
	speedAt   time.Time
	// changed is closed and replaced whenever a waiter might have something
	// new to look at: output published, the stream stopped or failed.
	changed chan struct{}
	// replaced tells the session layer this stream gave way to another, and
	// failedHook that its conversion failed.
	replaced   func(Stream)
	failedHook func()
}

// ConversionFailure is why a stream failed or fell back, for the people who can
// read it: a class a viewer can be told and the converter's own last words with
// paths removed.
type ConversionFailure struct {
	Code       string    `json:"code"`
	Diagnostic string    `json:"diagnostic,omitempty"`
	At         time.Time `json:"at"`
	Strategy   string    `json:"strategy,omitempty"`
	Backend    string    `json:"backend,omitempty"`
	Video      string    `json:"video,omitempty"`
	Audio      string    `json:"audio,omitempty"`
}

// finishedStreamError answers a producer or segment wait whose stream is
// already over. A stopped stream is a clean end and gets the same hidden 404 an
// unknown grant does, so a player reading the last bytes of a short title as
// the client stops never sees a server error. A failed stream met by a read
// that was already admitted stays the real conversion fault.
func finishedStreamError(state string) error {
	switch state {
	case streamStopped:
		return ErrGrantEnded
	case streamFailed:
		return errMediaConversionFailed
	}
	return nil
}

// liveStream returns the stream if it can still be served.
func (h *HLS) liveStream(id string) (*stream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil {
		return nil, ErrGrantEnded
	}
	return s, finishedStreamError(s.state)
}

// signalLocked wakes everybody waiting on this stream. Caller holds h.mu.
func (h *HLS) signalLocked(s *stream) {
	if s == nil {
		return
	}
	close(s.changed)
	s.changed = make(chan struct{})
}

// planCopy is the plan a producer run works from. Caller must not hold h.mu.
func (h *HLS) planCopy(id string) *DeliveryPlan {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil || s.plan == nil {
		return nil
	}
	plan := *s.plan
	return &plan
}

// producerWindowKey carries the window a producer runs for, so a failure it
// reports can be told from the failure of a window that was moved or spilled.
type producerWindowKey struct{}

// cancelRenditionsLocked stops the audio rendition producers of one stream.
// Caller holds h.mu; only captured cancel functions are invoked.
func (h *HLS) cancelRenditionsLocked(id string) {
	for key, cancel := range h.active {
		if key != id && producerSession(key) == id && cancel != nil {
			cancel()
		}
	}
}

func validateOwnedInput(ctx context.Context, validate func(context.Context) error) error {
	owned, err := childprocess.OwnedIOContext(ctx)
	if err != nil {
		return err
	}
	if err = validate(owned); err != nil {
		return err
	}
	return childprocess.CheckOwnedIO(ctx)
}
