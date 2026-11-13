package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
)

// Use is one thing holding a channel's input: a viewer or a recording.
type Use struct {
	Record bool
	UserID int64
	// Name is what the account's own people see it as: the device or the programme.
	Name string
	// Away is a viewer outside the home networks, for the owner's Now playing.
	Away bool
}

// NoTuner says every tuner of the source is busy, and with what.
type NoTuner struct{ Using []Use }

func (e *NoTuner) Error() string { return "every tuner is busy" }

// Tuning settings: how much can be rewound (in time and at most in bytes, per channel), and
// how long a source that stops sending is tried again before the channel is said to have
// stopped.
type Tuning struct {
	Buffer    time.Duration
	UserAgent string
	Stretch   time.Duration
	MaxBytes  int64
	Retry     time.Duration
}

// DefaultTuning is the owner's defaults: 30 minutes of rewind but never more than 1.5 GB a
// channel (30 minutes of a broadcast HD channel is about 4 GB, so the rewind of such a
// channel is shorter), and 60 seconds of retrying. The server reads the owner's live document
// for the stretch and retry time.
var DefaultTuning = Tuning{Buffer: 8 * time.Second, UserAgent: "Portico", Stretch: 30 * time.Minute, MaxBytes: 1536 << 20, Retry: 60 * time.Second}

const (
	segmentLength = 4 * time.Second
	lingerAfter   = time.Minute // a channel's input ends a minute after its last viewer leaves
	packet        = 188         // MPEG-TS packets: a segment always ends on one
)

// StreamClient reads a channel's stream: a connection that lasts as long as anyone watches,
// so it has no overall time limit, only one on connecting and on the first answer.
func StreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{Transport: transport}
}

// Tuners runs every channel's shared input. It is running state: after a restart nothing is
// tuned and the rewind stretch starts again, and the folder is emptied at start.
type Tuners struct {
	dir    string
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
	tuning func() Tuning

	mu     sync.Mutex
	inputs map[int64]*Input
}

// NewTuners empties dir and makes the tuner manager.
func NewTuners(dir string, client *http.Client, log *slog.Logger, now func() time.Time, tuning func() Tuning) *Tuners {
	os.RemoveAll(dir)
	return &Tuners{dir: dir, client: client, log: log, now: now, tuning: tuning, inputs: map[int64]*Input{}}
}

// Input is one channel's connection to its source, shared by everyone watching or recording
// it, kept as short segment files reaching back over the rewind stretch.
type Input struct {
	ChannelID, SourceID int64
	locator             string
	dir                 string
	t                   *Tuners

	mu       sync.Mutex
	changed  *sync.Cond
	uses     map[*Use]bool
	segments []segment
	started  time.Time
	stopped  error // why the source stopped sending, once it has
	left     time.Time
	cancel   context.CancelFunc
	done     chan struct{}
}

type segment struct {
	path  string
	start time.Time
	size  int64
	open  bool
}

// Tune joins a channel's input, starting it when nobody has it yet. A channel already tuned
// can always be joined; a new one needs a free tuner of its source, or NoTuner says what
// is using them.
func (t *Tuners) Tune(channelID, sourceID int64, tuners int, locator string, use *Use) (*Input, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if in := t.inputs[channelID]; in != nil && in.alive() {
		in.join(use)
		return in, nil
	}
	busy := 0
	var using []Use
	var idle []int64
	for id, in := range t.inputs {
		if in.SourceID == sourceID && in.alive() {
			busy++
			in.mu.Lock()
			for u := range in.uses {
				using = append(using, *u)
			}
			if len(in.uses) == 0 {
				idle = append(idle, id)
			}
			in.mu.Unlock()
		}
	}
	// An input nobody holds lingers for a minute in case its viewer comes back; a tuner that is
	// needed now is taken back from it at once.
	for _, id := range idle {
		if busy < max(tuners, 1) {
			break
		}
		t.inputs[id].cancel()
		delete(t.inputs, id)
		busy--
	}
	if busy >= max(tuners, 1) {
		return nil, &NoTuner{Using: using}
	}
	ctx, cancel := context.WithCancel(context.Background())
	in := &Input{ChannelID: channelID, SourceID: sourceID, locator: locator, t: t, uses: map[*Use]bool{}, started: t.now(),
		dir: filepath.Join(t.dir, fmt.Sprintf("%d-%d", channelID, t.now().UnixNano())), cancel: cancel, done: make(chan struct{})}
	in.changed = sync.NewCond(&in.mu)
	in.uses[use] = true
	t.inputs[channelID] = in
	go in.pump(ctx)
	return in, nil
}

// Using lists what holds each tuned channel of a source, for the owner's Tuners page.
func (t *Tuners) Using(sourceID int64) map[int64][]Use {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := map[int64][]Use{}
	for id, in := range t.inputs {
		if in.SourceID == sourceID && in.alive() {
			in.mu.Lock()
			for u := range in.uses {
				out[id] = append(out[id], *u)
			}
			in.mu.Unlock()
		}
	}
	return out
}

// Close ends every input at once, as at shutdown.
func (t *Tuners) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, in := range t.inputs {
		in.cancel()
		delete(t.inputs, id)
	}
}

// Run ends inputs nobody has held for a minute, until ctx ends.
func (t *Tuners) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Close()
			return
		case <-tick.C:
		}
		t.mu.Lock()
		for id, in := range t.inputs {
			in.mu.Lock()
			idle := len(in.uses) == 0 && t.now().Sub(in.left) >= lingerAfter
			ended := in.stopped != nil && len(in.uses) == 0
			in.mu.Unlock()
			if idle || ended {
				in.cancel()
				delete(t.inputs, id)
			}
		}
		t.mu.Unlock()
	}
}

func (in *Input) alive() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.stopped == nil
}

func (in *Input) join(use *Use) {
	in.mu.Lock()
	in.uses[use] = true
	in.mu.Unlock()
}

// Leave lets go of the input; it ends a minute after the last one leaves.
func (in *Input) Leave(use *Use) {
	in.mu.Lock()
	delete(in.uses, use)
	if len(in.uses) == 0 {
		in.left = in.t.now()
	}
	in.mu.Unlock()
}

// Stopped says why the source stopped sending, or nil while it sends.
func (in *Input) Stopped() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.stopped
}

// Window is the stretch that can be played: from the oldest kept moment to live.
func (in *Input) Window() (oldest, live time.Time) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.segments) == 0 {
		return in.started, in.started
	}
	return in.segments[0].start, in.t.now()
}

// pump reads the source into segment files, trying again for the retry time when it stops,
// and drops segments older than the rewind stretch.
func (in *Input) pump(ctx context.Context) {
	defer close(in.done)
	defer os.RemoveAll(in.dir)
	if err := os.MkdirAll(in.dir, 0o700); err != nil {
		in.stop(err)
		return
	}
	// A source that drops is connected again at once. It is given up on only when it has sent
	// nothing for the retry time and at least three connections in a row have brought nothing,
	// as playback gives up on a stream that fails three times without progress. Any data
	// starts the count again, so a source that blips now and then keeps playing.
	var failingSince time.Time
	failures := 0
	for ctx.Err() == nil {
		delivered, err := in.read(ctx)
		if ctx.Err() != nil {
			break
		}
		if delivered {
			failingSince, failures = in.t.now(), 0 // it dropped just now, after sending
		} else {
			failures++
		}
		if failingSince.IsZero() {
			failingSince = in.t.now()
		}
		if failures >= 3 && in.t.now().Sub(failingSince) >= in.t.tuning().Retry {
			in.stop(fmt.Errorf("the channel stopped: %w", err))
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	in.stop(context.Canceled)
}

func (in *Input) stop(err error) {
	in.mu.Lock()
	if in.stopped == nil {
		in.stopped = err
	}
	if n := len(in.segments); n > 0 {
		in.segments[n-1].open = false
	}
	in.changed.Broadcast()
	in.mu.Unlock()
}

// read copies one connection to the source into segments until it ends, and says whether it
// brought any data.
func (in *Input) read(ctx context.Context) (delivered bool, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.locator, nil)
	if err != nil {
		return false, err
	}
	agent := in.t.tuning().UserAgent
	if agent == "" {
		agent = "Portico"
	}
	req.Header.Set("User-Agent", agent)
	resp, err := in.t.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the source answered %d", resp.StatusCode)
	}
	buf := make([]byte, packet*348) // about 64 KiB
	var file *os.File
	var held int
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	for {
		n, err := resp.Body.Read(buf[held:])
		held += n
		whole := held / packet * packet
		if whole > 0 {
			now := in.t.now()
			if file == nil || in.segmentDue(now) {
				if file != nil {
					file.Close()
				}
				if file, err = in.newSegment(now); err != nil {
					return delivered, err
				}
			}
			if _, werr := file.Write(buf[:whole]); werr != nil {
				return delivered, werr
			}
			delivered = true
			in.mu.Lock()
			in.segments[len(in.segments)-1].size += int64(whole)
			in.changed.Broadcast()
			in.mu.Unlock()
			held = copy(buf, buf[whole:held])
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			return delivered, err
		}
	}
}

func (in *Input) segmentDue(now time.Time) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return now.Sub(in.segments[len(in.segments)-1].start) >= segmentLength
}

func (in *Input) newSegment(now time.Time) (*os.File, error) {
	path := filepath.Join(in.dir, fmt.Sprintf("%d.ts", now.UnixNano()))
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if n := len(in.segments); n > 0 {
		in.segments[n-1].open = false
	}
	in.segments = append(in.segments, segment{path: path, start: now, open: true})
	// Keep the rewind stretch and one segment more, within the channel's bytes; a reader on a
	// dropped file keeps reading it, since a removed file stays readable while open. When the
	// disk is short of room for new files, keep only the newest: the rewind shortens, live
	// keeps playing, and the database's disk never fills.
	tuning := in.t.tuning()
	var held int64
	for _, seg := range in.segments {
		held += seg.size
	}
	short := !diskspace.Room(in.dir, diskspace.ProducerFloor)
	for len(in.segments) > 2 && (now.Sub(in.segments[1].start) > tuning.Stretch || tuning.MaxBytes > 0 && held > tuning.MaxBytes || short) {
		held -= in.segments[0].size
		os.Remove(in.segments[0].path)
		in.segments = in.segments[1:]
	}
	return file, nil
}

// Read follows the input from the moment at (or from live when at is zero) for as long as it
// is read: it waits for what is still to come. A moment older than the stretch starts at its
// oldest point. Closing ends the reading.
func (in *Input) Read(ctx context.Context, at time.Time, w io.Writer) error {
	return in.ReadFrom(ctx, at, w, nil)
}

// ReadFrom is Read, and tells began, before anything is written, the moment the reading
// really starts with: segments are read whole, so that is the start of the one holding at,
// at or a few seconds before it.
func (in *Input) ReadFrom(ctx context.Context, at time.Time, w io.Writer, began func(time.Time)) error {
	in.mu.Lock()
	next := len(in.segments) - 1
	if !at.IsZero() {
		next = 0
		for n, s := range in.segments {
			if !s.start.After(at) {
				next = n
			}
		}
	}
	var current segment
	if next >= 0 && next < len(in.segments) {
		current = in.segments[next]
	}
	in.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { in.mu.Lock(); in.changed.Broadcast(); in.mu.Unlock() })
	defer stop()
	var offset int64
	chunk := make([]byte, 64<<10)
	for ctx.Err() == nil {
		if current.path == "" {
			in.mu.Lock()
			for len(in.segments) == 0 && in.stopped == nil && ctx.Err() == nil {
				in.changed.Wait()
			}
			if len(in.segments) == 0 {
				err := in.stopped
				in.mu.Unlock()
				return err
			}
			current, offset = in.segments[0], 0
			in.mu.Unlock()
		}
		if began != nil {
			began(current.start)
			began = nil
		}
		file, err := os.Open(current.path)
		if err != nil {
			return err
		}
		for {
			if ctx.Err() != nil {
				file.Close()
				return ctx.Err()
			}
			n, rerr := file.ReadAt(chunk, offset)
			if n > 0 {
				if _, werr := w.Write(chunk[:n]); werr != nil {
					file.Close()
					return werr
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				offset += int64(n)
				continue
			}
			if rerr != nil && rerr != io.EOF {
				file.Close()
				return rerr
			}
			// At the end of what is written: wait for more of this segment or the next one.
			in.mu.Lock()
			var following *segment
			for {
				idx := -1
				for k, s := range in.segments {
					if s.path == current.path {
						idx = k
						current.open, current.size = s.open, s.size
					}
				}
				if idx >= 0 && idx+1 < len(in.segments) {
					following = &in.segments[idx+1]
				}
				if idx < 0 && len(in.segments) > 0 {
					// Read too slowly for the stretch: carry on from its oldest point.
					following, current.open, current.size = &in.segments[0], false, offset
					break
				}
				if current.size > offset || following != nil && !current.open || in.stopped != nil || ctx.Err() != nil {
					break
				}
				in.changed.Wait()
			}
			stopped := in.stopped
			var next segment
			if following != nil {
				next = *following
			}
			in.mu.Unlock()
			if current.size > offset {
				continue
			}
			file.Close()
			switch {
			case next.path != "":
				current, offset = next, 0
			case stopped != nil:
				return stopped
			default:
				return ctx.Err()
			}
			break
		}
	}
	return ctx.Err()
}
