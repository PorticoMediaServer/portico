package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

var ErrBusy = errors.New("storage source has an outstanding operation; retry when it recovers")

// The supervisor used to admit four helper processes for the whole server, and
// three of those four for anything that was not playback. That number was never
// a policy: it was a zero-valued field falling through to a default. One
// storage.Client is shared by the scanner's ffprobe, the remote probe, prepared
// encodes, downloads, artwork extraction and playback, so two downloads and two
// viewers were enough to make everything else answer "busy" — against a target
// of two hundred viewers.
//
// Two things fix it. The byte path stopped holding a helper per stream for local
// files on Unix: the helper validates, passes the descriptor back and exits, so
// the long-lived reader now exists only for Portico-managed network mounts and
// on Windows. And the one counter became separate pools, because the kinds of
// work are scarce in different ways:
//
//   - Playback holds a process for the length of a stream. Measured on an Apple
//     Silicon Mac: thirty-two idle helpers cost 286 MiB of real memory, so 8.9
//     MiB and seven descriptors each. 128 is therefore about 1.1 GiB and 900
//     descriptors at full saturation — survivable on the 4-8 GiB home server this
//     targets, and several times the simultaneous mounted-source streams a
//     household plausibly runs. It is the ceiling that matters on Windows, where
//     every direct play still takes one; revisit it when the Windows source
//     hand-off lands.
//   - Foreground helpers remain short-lived and independently bounded.
//   - Background scans, analysis, artwork and optimization share one CPU budget
//     of half the available cores, with a floor of one, plus low OS priority.
//
// A playback admission that finds its pool full waits, briefly and with the
// caller's context, rather than refusing at once: fifty viewers pressing play in
// the same second are a burst, not an overload. The honest 503 stays as the
// answer past the wait, so the client-visible contract is unchanged.
var (
	// DefaultPlaybackLimit bounds long-lived helper processes.
	DefaultPlaybackLimit = 128
	// DefaultHelperLimit bounds short probes, scans and encodes.
	DefaultHelperLimit = max(8, 2*runtime.NumCPU())
	// DefaultBackgroundLimit is one shared budget for scans, analysis, artwork
	// and optimization. It preserves progress without letting background FFmpeg
	// processes consume every core under foreground load.
	DefaultBackgroundLimit = max(1, runtime.NumCPU()/2)
	// PlaybackAdmissionWait is how long a playback helper waits for a slot before
	// it is refused. The caller's context bounds it too, so an abandoned request
	// never holds a place in the queue.
	PlaybackAdmissionWait = 5 * time.Second
)

// pool names the scarce thing an operation competes for.
type pool int

const (
	poolHelper pool = iota
	poolPlayback
	poolBackground
	poolScanProbe
)

func poolFor(ctx context.Context, key string) pool {
	if strings.HasPrefix(key, "playback:") {
		return poolPlayback
	}
	if strings.HasPrefix(key, "scan:") {
		return poolScanProbe
	}
	for _, prefix := range []string{"analysis:", "optimization:", "inventory:", "inventory-stream:", "lyrics:"} {
		if strings.HasPrefix(key, prefix) {
			return poolBackground
		}
	}
	if strings.HasSuffix(key, ":art") || dbwork.ClassFrom(ctx, dbwork.ClassInteractive).Background() {
		return poolBackground
	}
	return poolHelper
}

// openingKeys are the operations a viewer is waiting on: opening a stream, or
// opening the descriptor a stream reads through. Those are the ones worth
// queueing briefly, because fifty people pressing play in the same second is a
// burst. Everything else under the playback prefix — a probe, a version check, a
// descriptor validation — is work the server does for itself and has somewhere
// else to be, so it keeps the immediate answer it always had.
var openingKeys = []string{"playback:reader:", "playback:descriptor:", "playback:observed:"}

func waitsForSlot(key string) bool {
	for _, prefix := range openingKeys {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

type Supervisor struct {
	mu               sync.Mutex
	active           map[string]bool
	counts           [4]int
	waitQueues       [4][]uint64
	nextWaitID       uint64
	backgroundNormal atomic.Bool
	// notify is closed and replaced on every release, which wakes every waiter to
	// re-check its own pool. The queues are short and only one waiter can win, so
	// a broadcast is correct rather than wasteful.
	notify chan struct{}

	// Limit, when set, overrides both pools. It exists for tests that want a
	// supervisor of a known size; the server leaves it zero and takes the
	// measured defaults.
	Limit int

	admitted atomic.Uint64
	queued   atomic.Uint64
	refused  atomic.Uint64
	waitNs   atomic.Uint64
}

// SupervisorStats is the observable state of one supervisor.
type SupervisorStats struct {
	PlaybackActive     int    `json:"playbackActive"`
	PlaybackCapacity   int    `json:"playbackCapacity"`
	HelperActive       int    `json:"helperActive"`
	HelperCapacity     int    `json:"helperCapacity"`
	BackgroundActive   int    `json:"backgroundActive"`
	BackgroundCapacity int    `json:"backgroundCapacity"`
	ScanProbeActive    int    `json:"scanProbeActive"`
	ScanProbeCapacity  int    `json:"scanProbeCapacity"`
	Admitted           uint64 `json:"admitted"`
	Queued             uint64 `json:"queued"`
	Refused            uint64 `json:"refused"`
	QueueWaitMillis    uint64 `json:"queueWaitMillis"`
}

// Stats snapshots the supervisor for diagnostics.
func (s *Supervisor) Stats() SupervisorStats {
	if s == nil {
		return SupervisorStats{}
	}
	s.mu.Lock()
	playback, helper, background, scan := s.counts[poolPlayback], s.counts[poolHelper], s.counts[poolBackground], s.counts[poolScanProbe]
	playbackLimit, helperLimit, backgroundLimit, scanLimit := s.limitLocked(poolPlayback), s.limitLocked(poolHelper), s.limitLocked(poolBackground), s.limitLocked(poolScanProbe)
	s.mu.Unlock()
	return SupervisorStats{
		PlaybackActive: playback, PlaybackCapacity: playbackLimit,
		HelperActive: helper, HelperCapacity: helperLimit,
		BackgroundActive: background, BackgroundCapacity: backgroundLimit,
		ScanProbeActive: scan, ScanProbeCapacity: scanLimit,
		Admitted: s.admitted.Load(), Queued: s.queued.Load(), Refused: s.refused.Load(),
		QueueWaitMillis: s.waitNs.Load() / uint64(time.Millisecond),
	}
}

func (s *Supervisor) limitLocked(p pool) int {
	if s.Limit > 0 {
		return s.Limit
	}
	if p == poolPlayback {
		return max(1, DefaultPlaybackLimit)
	}
	if p == poolBackground {
		if s.backgroundNormal.Load() {
			return max(1, runtime.NumCPU())
		}
		return max(1, DefaultBackgroundLimit)
	}
	if p == poolScanProbe {
		if s.backgroundNormal.Load() {
			return max(1, runtime.NumCPU())
		}
		return max(1, DefaultBackgroundLimit)
	}
	return max(1, DefaultHelperLimit)
}

func (s *Supervisor) Run(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error) error {
	return s.run(ctx, key, cmd, consume, nil, nil)
}

// RunOwned transfers an owner cleanup callback to the supervisor. It runs once
// after physical Wait, or a failure that proves no child started. Cancellation
// may return to the caller earlier; it never releases this ownership callback.
// The callback must not block on another supervised operation.
func (s *Supervisor) RunOwned(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit func()) error {
	return s.run(ctx, key, cmd, consume, afterExit, nil)
}

// RunOwnedCompletion reports retirement only after owner cleanup and physical
// permit release. It is suitable for a scoped runtime shutdown barrier.
func (s *Supervisor) RunOwnedCompletion(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit, afterRetired func()) error {
	return s.run(ctx, key, cmd, consume, afterExit, afterRetired)
}

// RunOwnedLinear supervises a producer whose admission is already governed by a
// durable physical source allocation. Generic helper concurrency is not a tuner
// or viewer cap. This still rejects duplicate keys and retains physical ownership
// through Wait, including uninterruptible processes after cancellation.
func (s *Supervisor) RunOwnedLinear(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit, afterRetired func()) error {
	return s.runAdmission(ctx, key, cmd, consume, afterExit, afterRetired, false)
}
func (s *Supervisor) run(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit, afterRetired func()) error {
	return s.runAdmission(ctx, key, cmd, consume, afterExit, afterRetired, true)
}

// admit takes a slot in the key's pool. Background and scan-probe work waits in
// FIFO order until a slot opens or its context ends; capacity is not a failure
// of an ingest probe. Playback keeps its bounded client-facing wait.
func (s *Supervisor) admit(ctx context.Context, key string, limited bool) (pool, error) {
	p := poolFor(ctx, key)
	if (p == poolBackground || p == poolScanProbe) && !dbwork.WaitBackground(ctx) {
		return p, ctx.Err()
	}
	var timer *time.Timer
	var timeout <-chan time.Time
	start := time.Now()
	queued := false
	var ticket uint64
	defer func() {
		if timer != nil {
			timer.Stop()
		}
		if queued {
			s.waitNs.Add(uint64(time.Since(start)))
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = map[string]bool{}
	}
	if s.notify == nil {
		s.notify = make(chan struct{})
	}
	for {
		if err := ctx.Err(); err != nil {
			s.removeQueuedLocked(p, ticket)
			s.signalLocked()
			return p, err
		}
		// A duplicate key is a fencing rule, not a capacity one: the same source
		// operation is already running, and a second is refused outright.
		if s.active[key] {
			s.removeQueuedLocked(p, ticket)
			s.signalLocked()
			s.refused.Add(1)
			return p, ErrBusy
		}
		first := len(s.waitQueues[p]) == 0 || ticket != 0 && s.waitQueues[p][0] == ticket
		if !limited || s.counts[p] < s.limitLocked(p) && first {
			s.removeQueuedLocked(p, ticket)
			s.active[key] = true
			s.counts[p]++
			s.admitted.Add(1)
			return p, nil
		}
		background := p == poolBackground || p == poolScanProbe
		if !background && !waitsForSlot(key) {
			s.refused.Add(1)
			return p, ErrBusy
		}
		if ticket == 0 {
			s.nextWaitID++
			ticket = s.nextWaitID
			s.waitQueues[p] = append(s.waitQueues[p], ticket)
			queued = true
			s.queued.Add(1)
			if !background {
				timer = time.NewTimer(PlaybackAdmissionWait)
				timeout = timer.C
			}
		}
		notify := s.notify
		s.mu.Unlock()
		select {
		case <-notify:
			s.mu.Lock()
		case <-ctx.Done():
			// The caller gave up; their own error is the honest one.
			s.mu.Lock()
			s.removeQueuedLocked(p, ticket)
			s.signalLocked()
			return p, ctx.Err()
		case <-timeout:
			s.mu.Lock()
			s.removeQueuedLocked(p, ticket)
			s.signalLocked()
			s.refused.Add(1)
			return p, ErrBusy
		}
	}
}

func (s *Supervisor) removeQueuedLocked(p pool, ticket uint64) {
	if ticket == 0 {
		return
	}
	for i, id := range s.waitQueues[p] {
		if id == ticket {
			s.waitQueues[p] = append(s.waitQueues[p][:i:i], s.waitQueues[p][i+1:]...)
			return
		}
	}
}

func (s *Supervisor) signalLocked() {
	if s.notify != nil {
		close(s.notify)
		s.notify = make(chan struct{})
	}
}

// release frees the slot and wakes every waiter.
func (s *Supervisor) release(key string, p pool) {
	s.mu.Lock()
	delete(s.active, key)
	if s.counts[p] > 0 {
		s.counts[p]--
	}
	s.signalLocked()
	s.mu.Unlock()
}

func (s *Supervisor) runAdmission(ctx context.Context, key string, cmd *exec.Cmd, consume func(io.Reader) error, afterExit, afterRetired func(), limited bool) error {
	var completed sync.Once
	complete := func() {
		completed.Do(func() {
			if afterExit != nil {
				afterExit()
			}
		})
	}
	var retired sync.Once
	retire := func() {
		retired.Do(func() {
			if afterRetired != nil {
				afterRetired()
			}
		})
	}
	if e := ctx.Err(); e != nil {
		complete()
		retire()
		return e
	}
	p, e := s.admit(ctx, key, limited)
	if e != nil {
		complete()
		retire()
		return e
	}
	release := func() {
		complete()
		s.release(key, p)
		retire()
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.Getenv("TMPDIR")}
	for _, v := range cmd.Env {
		// A decoder's font configuration (decoder/fonts.go) is the one variable
		// its builder may add.
		if strings.HasPrefix(v, "FONTCONFIG_FILE=") {
			env = append(env, v)
		}
	}
	cmd.Env = env
	if (p == poolBackground || p == poolScanProbe) && !s.backgroundNormal.Load() {
		lowerBackgroundPriority(cmd)
	}
	configureProcess(cmd)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		release()
		return e
	}
	// The last bytes of the child's error output travel with its exit error, so
	// "exit status 1" is never all a log line or a reason can say.
	stderr := &tailWriter{max: stderrTailBytes}
	cmd.Stderr = stderr
	if e = childprocess.Start(ctx, cmd); e != nil {
		stdout.Close()
		release()
		return e
	}
	if (p == poolBackground || p == poolScanProbe) && !s.backgroundNormal.Load() {
		applyBackgroundPriority(cmd)
	}
	done := make(chan error, 1)
	supervise.Go("storage.supervisor.wait", func() {
		// The handoff is deferred so a contained panic still releases the slot and
		// still answers the caller, rather than turning a crash into a hang.
		var readErr, waitErr error
		waited := false
		defer func() {
			if !waited {
				// A consumer panic (or Goexit) must not retire a live helper.
				// Keep its capacity until the process is killed and reaped; the
				// outer supervisor still records the original panic.
				killProcess(cmd)
				waitErr = cmd.Wait()
				readErr = errors.New("storage helper consumer ended before process wait")
			}
			stdout.Close()
			release()
			if readErr != nil {
				done <- readErr
			} else {
				done <- waitErr
			}
		}()
		readErr = consume(stdout)
		if readErr != nil {
			killProcess(cmd)
		}
		waitErr = cmd.Wait()
		waited = true
		if waitErr != nil {
			waitErr = withStderr(waitErr, stderr.String())
		}
	})
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		killProcess(cmd)
		_ = stdout.Close()
		return ctx.Err()
	}
	// A process stuck in an uninterruptible kernel operation retains its slot
	// until Wait actually returns. Caller cancellation never frees quarantine.
}

func (s *Supervisor) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[poolHelper] + s.counts[poolPlayback] + s.counts[poolBackground] + s.counts[poolScanProbe]
}

// stderrTailBytes is how much of a child's error output is kept (its end).
const stderrTailBytes = 2048

// tailWriter keeps the last max bytes written to it.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// privateTokens are the per-job secrets that appear in a decoder's URLs (a
// gateway route, a job's output path); they never reach a log.
var privateTokens = regexp.MustCompile(`[a-fA-F0-9]{32,}`)

// ProcessError is a child's exit error with the end of its error output.
type ProcessError struct {
	Err    error
	Stderr string
}

func (e *ProcessError) Error() string { return e.Err.Error() + ": " + e.Stderr }
func (e *ProcessError) Unwrap() error { return e.Err }

func withStderr(err error, tail string) error {
	tail = strings.Join(strings.Fields(privateTokens.ReplaceAllString(strings.ToValidUTF8(tail, "?"), "…")), " ")
	if tail == "" {
		return err
	}
	return &ProcessError{Err: err, Stderr: tail}
}
