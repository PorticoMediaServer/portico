package httpapi

import (
	"io"
	"net/http"
	"time"
)

// The server sets no WriteTimeout on purpose: a stream and a byte range
// legitimately outlive any fixed budget, and a global one would cut off a
// healthy download. The consequence, until now, was that nothing bounded a
// single write. Once a client stops reading and the socket buffer fills,
// fmt.Fprintf on an event stream blocks indefinitely — no RST arrives from a
// phone that went into a tunnel, sometimes for many minutes. The goroutine, its
// lane slot, its open file and, for a mounted source, its helper process are all
// held for that whole time. The bounded stream-lifetime timer never fires,
// because the goroutine is stuck inside the write rather than in its select.
//
// Two shapes, for the two kinds of write:
//
//   - A frame deadline for server-sent events, armed for one frame and cleared
//     as soon as that frame has been written and flushed. Each frame is small
//     and a legitimate client accepts one in milliseconds, so the budget is
//     invisible when healthy and reclaims a dead socket within one frame. It
//     covers the frame, never the stream: the quiet time between frames belongs
//     to the heartbeat and the stream lifetime. Leaving it armed has no effect on
//     a heartbeat paced faster than the budget and kills one paced slower.
//   - A rolling deadline for media bodies, refreshed on every successful write.
//     This is the one that has to be right: a fixed deadline would cut off a
//     slow-but-alive client part way through a long file, which is exactly the
//     client least able to recover. What is bounded is the gap between two
//     successful writes, not the length of the response.

// These are variables rather than constants so a test can shrink them; nothing
// in the server writes to them.
var (
	// streamFrameDeadline bounds one server-sent event frame.
	streamFrameDeadline = 20 * time.Second
	// mediaWriteDeadline bounds the gap between two successful body writes. Long
	// enough for any real buffering pause on a phone, short enough to reclaim a
	// socket whose peer has gone without saying so.
	mediaWriteDeadline = 60 * time.Second
	// deadlineRefreshInterval keeps the rolling refresh off the per-write path
	// without letting the effective window drift far from mediaWriteDeadline.
	deadlineRefreshInterval = 5 * time.Second
)

// frameWrite writes one server-sent event frame and flushes it under the frame
// deadline, then clears the deadline. Clearing is what makes the budget honest:
// the deadline bounds this frame's write, and the idle time until the next
// frame is bounded by that stream's heartbeat and lifetime instead. A response
// writer that cannot take a deadline (a test recorder, a wrapper that does not
// unwrap) is still written: the deadline is a reclamation mechanism, not a
// correctness one.
//
// Because it clears the connection's write deadline, it belongs to a response
// that owns that deadline: every caller is a realtime-lane event stream, which
// never carries the rolling response deadline.
func frameWrite(w http.ResponseWriter, write func(out io.Writer) (int, error)) error {
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(streamFrameDeadline))
	_, err := write(w)
	if err == nil {
		// The flush is part of the frame: bytes that cannot reach the peer within
		// the budget have failed in the same way a blocked write does.
		err = controller.Flush()
	}
	// Clear after the attempt, never before it. On success the frame is on the
	// connection and the deadline is spent; on failure the stream is ending, and
	// clearing keeps a keep-alive connection from carrying the deadline into the
	// next response.
	_ = controller.SetWriteDeadline(time.Time{})
	return err
}

// rollingDeadlineWriter refreshes a write deadline as the body makes progress,
// so what is bounded is a stall rather than the transfer.
type rollingDeadlineWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	refreshed  time.Time
	window     time.Duration
	interval   time.Duration
	chunkSize  int
}

// withRollingDeadline wraps w so every write is covered by a deadline that moves
// with the transfer.
func withRollingDeadline(w http.ResponseWriter) *rollingDeadlineWriter {
	wrapped := &rollingDeadlineWriter{ResponseWriter: w, controller: http.NewResponseController(w), window: mediaWriteDeadline, interval: deadlineRefreshInterval}
	wrapped.refresh()
	return wrapped
}

// JSON encoders can submit an entire document in one Write. Chunk that write so
// a large response making progress can refresh its budget, while an HTTP/2 peer
// withholding flow-control credit cannot keep a lane occupied indefinitely.
// Arm lazily: database work before the first response byte has its own budget.
func withResponseDeadline(w http.ResponseWriter) *rollingDeadlineWriter {
	return &rollingDeadlineWriter{ResponseWriter: w, controller: http.NewResponseController(w), window: 15 * time.Second, interval: time.Second, chunkSize: 16 << 10}
}

func (r *rollingDeadlineWriter) refresh() {
	r.refreshed = time.Now()
	_ = r.controller.SetWriteDeadline(r.refreshed.Add(r.window))
}

func (r *rollingDeadlineWriter) Write(p []byte) (int, error) {
	if r.chunkSize == 0 {
		if time.Since(r.refreshed) >= r.interval {
			r.refresh()
		}
		return r.ResponseWriter.Write(p)
	}
	written := 0
	for len(p) > 0 {
		if time.Since(r.refreshed) >= r.interval {
			r.refresh()
		}
		chunk := len(p)
		if chunk > r.chunkSize {
			chunk = r.chunkSize
		}
		n, err := r.ResponseWriter.Write(p[:chunk])
		written += n
		if err != nil {
			return written, err
		}
		if n != chunk {
			return written, io.ErrShortWrite
		}
		p = p[n:]
	}
	return written, nil
}

// Unwrap keeps http.NewResponseController and net/http's own feature detection
// working through the wrapper.
func (r *rollingDeadlineWriter) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *rollingDeadlineWriter) Flush() {
	if time.Since(r.refreshed) >= r.interval {
		r.refresh()
	}
	_ = r.controller.Flush()
}

// Finish after compression's close, so trailers and net/http's buffered bytes
// reach the client while still bounded. Clear only after that explicit flush.
func (r *rollingDeadlineWriter) finishResponse() {
	defer r.release()
	r.Flush()
}

// release drops the deadline once the body is done, so a keep-alive connection
// carries nothing from this response into the next.
func (r *rollingDeadlineWriter) release() { _ = r.controller.SetWriteDeadline(time.Time{}) }
