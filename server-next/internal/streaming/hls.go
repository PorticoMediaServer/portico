package streaming

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming/vod"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// ErrConversionSpace is the answer when the volume holding generated media has
// no room left to start another conversion. It is a beyond-target condition:
// nothing a viewer does at target load reaches it, and it is retryable because
// the eviction sweep may free room within seconds.
var ErrConversionSpace = errors.New("not enough free space to start media conversion; retry shortly")

// errMediaConversionFailed is a real fault: this source could not be converted.
// It is never an end of the presentation, so it must not be answered as one.
var errMediaConversionFailed = errors.New("media conversion failed")

// Only owner-configured conversion limits govern admission. CPU count and
// measured server load are diagnostics, never a playback quality or admission rule.

// HLS owns conversion lifetimes only; grants and direct files are in Streams.
type HLS struct {
	// streams is every stream this process is serving, by stream id. It is the
	// whole of what the old server kept in the database about a running stream.
	streams map[string]*stream
	// failures is the recent conversion failures, newest last, for the owner.
	failures []ConversionFailure
	// fileTimelines is the cut list of each file a stream has copied from, so a
	// second stream of the same file does not index it again.
	fileTimelines map[timelineKey]*copyTimeline
	// replace swaps a stream whose output changed shape for a new one. Streams
	// sets it; nil fails such a stream.
	replace func(id string, next *DeliveryPlan, position float64) bool
	// fmp4 turns on fragmented MP4 output for plans that need it.
	fmp4             bool
	root, binary     string
	outputRoot       *os.Root
	memoryRoot       string
	memoryOutputRoot *os.Root
	inMemory         map[string]bool
	diskOnly         map[string]bool
	memoryFull       map[string]bool
	moving           map[string]chan struct{}
	memoryFacts      func(string) (memoryFacts, error)
	memoryCheck      func(string) error
	memorySpills     atomic.Uint64
	memoryRefused    atomic.Uint64
	memorySweepMu    sync.Mutex
	outputBusy       map[string]int
	mu               sync.Mutex
	cleaning         map[string]bool
	active           map[string]context.CancelFunc
	windows          map[string]*hlsWindow
	// restarts bounds how often one session may restart its producer, and
	// segmentWaits shares one wait between every request for the same segment.
	// Both exist for clients on bad networks; see hls_bad_network.go.
	restarts     map[string]*restartBudget
	segmentWaits map[string]*segmentWait
	// abandoned counts conversions freed because nobody was fetching from
	// them any more.
	abandoned atomic.Uint64
	// conversionsRefused counts new conversions turned away by the host-capacity
	// bound, so an owner whose machine is too small for their library sees the
	// number rather than guessing from latency.
	conversionsRefused atomic.Uint64
	// hardwareFailures counts windows whose hardware encoder failed on a real
	// source and were run again in software.
	hardwareFailures atomic.Uint64
	// freed is the lowest slot each session still retains, so retention
	// never re-walks the whole played region.
	freed map[string]int
	// timelines holds the cut list of every live copy session; a session absent
	// from it is on the fixed six-second grid. ffprobe indexes those cut lists.
	timelines map[string]*copyTimeline
	ffprobe   string
	// usage is the measured footprint of each generated session and when a viewer
	// last saw a segment of it. It is what makes the per-second sweep cheap and
	// what orders eviction under the global byte budget; see hls_sweep.go.
	usage           map[string]*hlsUsage
	budgetSwept     time.Time
	orphanMu        sync.Mutex
	orphanDir       *os.File
	orphanMemoryDir *os.File
	// measure is the sweep's directory-measurement seam. It is nil in
	// production, where measureDirectory runs; a test replaces it to count the
	// walks a sweep actually performs. Nothing else writes it.
	measure func(dir string) (int64, int, error)
	// Filesystem seams are installed only before concurrent playback in tests.
	outputOpen   func(*os.Root, string, string) (*os.File, error)
	outputRename func(string, string) error
	// execute replaces only converter execution in tests. Set before starting
	// a session; nil uses the ordinary confined child-process executor.
	execute func(context.Context, mediaexec.Job) (error, string)
	// windowTeardown runs in tests as a producer exits, before its window is
	// unregistered. Set before starting a session; nil in production.
	windowTeardown func(id string, window *hlsWindow)
	// producers joins every produce goroutine, so shutdown can wait for the
	// converters it just cancelled instead of leaving them to be killed with the
	// process and orphan their ffmpeg children.
	producers sync.WaitGroup
	settings  DeliverySettings
	ctx       context.Context
}

// hlsWindow is one producer run. A session has at most one at a time; a seek
// past its converted edge replaces it with a window that starts where the
// viewer actually is, which is the whole point of the fixed segment grid.
type hlsWindow struct {
	// retired is set when this window is moved or its stream stops, and
	// spillCancelled when its output is being moved off memory: a failure such a
	// window reports afterwards is its own cancellation, not the stream's.
	retired, spillCancelled bool
	publicationError        error
	videoConversion         bool
	audioConversion         bool
	start                   int
	served                  int
	produced                int
	throttled               bool
	relocating              bool
	cancel                  context.CancelFunc
	done                    chan struct{}
}

// coversDemand keeps near-edge sequential fetches and the first output of a
// newly relocated window together. Requested position is never delivered progress.
func (w *hlsWindow) coversDemand(from int) bool {
	return !w.relocating && !w.throttled && from >= w.start && from <= max(w.start, w.produced)+2
}

// ConfigureSettings wires owner administration into conversion lifetimes. A nil
// source keeps the published defaults.
func (h *HLS) ConfigureSettings(s DeliverySettings) { h.settings = s }

func (h *HLS) configuration() DeliveryConfiguration { return deliveryConfiguration(h.settings) }

// fragmentedOutput reports whether this producer can write fragmented MP4 HLS.
// Planning asks, so a build that cannot is never handed a plan that needs it.
func (h *HLS) fragmentedOutput() bool { return h.fragmentedOutputFor(decoder.CurrentToolchain()) }

func (h *HLS) fragmentedOutputFor(facts *decoder.ToolchainFacts) bool {
	if h == nil || !h.fmp4 {
		return false
	}
	return facts != nil && facts.Muxers["mp4"] && facts.Muxers["hls"]
}

func NewHLS(ctx context.Context, root, binary string) (*HLS, error) {
	if binary == "" {
		binary = mediatools.Resolve("ffmpeg")
	}
	resolved, e := exec.LookPath(binary)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	// A new server process has no streams, so nothing left in the folder can be
	// asked for again.
	if e = emptyDirectory(root); e != nil {
		return nil, e
	}
	outputRoot, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	return &HLS{
		fmp4: true, streams: map[string]*stream{}, root: root, binary: resolved, outputRoot: outputRoot,
		active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, freed: map[string]int{},
		usage: map[string]*hlsUsage{}, timelines: map[string]*copyTimeline{}, ffprobe: siblingProbe(resolved), ctx: ctx,
	}, nil
}

// emptyDirectory removes everything inside a folder and keeps the folder.
func emptyDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err = os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// start admits one producer window for a session. A window already covering the
// requested slot is left alone; a window behind it is replaced, because a viewer
// who has jumped forward is not served by a converter still grinding through the
// part they skipped.
//
// The stream's state is read under the same mutex that installs the window, and
// stopping a stream sets that state under it too: a stream that has been stopped
// can never gain a producer, however the two requests race.
func (h *HLS) start(ctx context.Context, id string, from int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil {
		return ErrGrantEnded
	}
	if e := finishedStreamError(s.state); e != nil {
		return e
	}
	if s.output == "failed" {
		return nil
	}
	videoConversion := s.plan == nil || s.plan.VideoAction == "convert"
	if h.ctx.Err() != nil {
		return h.ctx.Err()
	}
	if h.moving[id] != nil || h.memoryFull[id] {
		return ErrSegmentPreparing
	}
	if h.cleaning[id] {
		return errors.New("playback output is being freed")
	}
	if _, ok := h.active[id]; ok {
		window := h.windows[id]
		// A two-rendition audio session has no grid; one run produces everything.
		if window == nil {
			return nil
		}
		if window.coversDemand(from) {
			return nil
		}
		if window.relocating {
			h.mu.Unlock()
			select {
			case <-window.done:
			case <-ctx.Done():
				h.mu.Lock()
				return ctx.Err()
			}
			err := h.start(ctx, id, from)
			h.mu.Lock()
			return err
		}
		if !h.restartAllowedLocked(id) {
			// The producer this session already has keeps running. The caller's
			// poll answers "not ready yet", which is retryable and is exactly what
			// it would have answered while a restart was in progress anyway.
			return nil
		}
		window.relocating = true
		window.retired = true
		window.cancel()
		h.cancelRenditionsLocked(id)
		h.mu.Unlock()
		select {
		case <-window.done:
		case <-ctx.Done():
			h.mu.Lock()
			return ctx.Err()
		}
		h.mu.Lock()

	}
	if _, ok := h.active[id]; ok {
		return nil
	}
	if !h.roomToProduce() {
		// Beyond-target behaviour only, and never a surprise: the caller turns this
		// into the typed, retryable 503 it already answers for a busy source.
		return ErrConversionSpace
	}
	ctx, cancel := context.WithCancel(h.ctx)
	window := &hlsWindow{
		videoConversion: videoConversion, start: from, served: -1, produced: from - 1, cancel: cancel, done: make(chan struct{}),
	}
	h.active[id] = cancel
	h.windows[id] = window
	h.producers.Add(1)
	supervise.Go("playback.hls.produce", func() {
		defer h.producers.Done()
		h.produce(ctx, id, from, window)
	})
	// A producer starts because somebody asked for it, so this is the moment the
	// viewer was last known to be there. Without it a conversion that is never
	// fetched from has no "served" time at all and could never be freed.
	h.noteSessionServedLocked(id)
	return nil
}

// ConversionCapacity reports the host-capacity bound on simultaneous
// conversions, how many are running, and how many starts it has turned away.
// An owner whose machine is too small for their household sees the third number
// grow; nothing else in the server would tell them.
type ConversionCapacity struct {
	Active   int    `json:"active"`
	Capacity int    `json:"capacity"`
	Refused  uint64 `json:"refused"`
}

// Conversions snapshots conversion admission.
func (h *HLS) Conversions() ConversionCapacity {
	if h == nil {
		return ConversionCapacity{}
	}
	h.mu.Lock()
	active := 0
	for id, window := range h.windows {
		if _, running := h.active[id]; running && window.videoConversion {
			active++
		}
	}
	h.mu.Unlock()
	return ConversionCapacity{Active: active, Capacity: h.configuration().MaxConversions, Refused: h.conversionsRefused.Load()}
}

// Shutdown cancels every producer and waits for them. HLS.Run cancelled them and
// returned immediately, so ffmpeg children could outlive the process that
// started them, holding a source file open across a restart.
func (h *HLS) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.cancelAll()
	h.closeOrphanDirectory()
	var closeErr error
	if h.memoryOutputRoot != nil {
		closeErr = h.memoryOutputRoot.Close()
	}
	if h.outputRoot != nil {
		closeErr = h.outputRoot.Close()
	}
	done := make(chan struct{})
	supervise.Go("playback.hls.shutdown", func() { h.producers.Wait(); close(done) })
	select {
	case <-done:
		return closeErr
	case <-ctx.Done():
		// The caller's budget is the bound. Saying so by name is what lets the
		// drain barrier report which loop did not finish.
		return errors.Join(ctx.Err(), closeErr)
	}
}

// Run is the maintenance loop: once a second it cancels producers whose stream
// is over or whose viewer has gone, and keeps generated output inside its
// budgets. It returns when ctx ends, having cancelled every producer.
func (h *HLS) Run(ctx context.Context) {
	defer h.cancelAll()
	defer h.closeOrphanDirectory()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.sweep(ctx)
		}
	}
}

// remove ends one stream's place on disk and in every producer map. The caller
// has already stopped its producers and waited for them; a reader still copying
// a segment out delays removal until the next call.
func (h *HLS) remove(id string) error {
	if !generatedDirectoryID.MatchString(id) {
		return errors.New("invalid generated artifact identity")
	}
	h.mu.Lock()
	if h.sessionActiveLocked(id) || h.moving[id] != nil || h.cleaning[id] || h.outputBusy[id] > 0 {
		h.mu.Unlock()
		return ErrSegmentPreparing
	}
	if h.cleaning == nil {
		h.cleaning = map[string]bool{}
	}
	h.cleaning[id] = true
	memory := h.inMemory[id]
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.cleaning, id); h.mu.Unlock() }()
	// RemoveAll does not follow a directory symlink. The roots are configured by
	// the server and id has no separators or dot segments.
	if memory {
		if e := os.RemoveAll(filepath.Join(h.memoryRoot, id)); e != nil {
			return e
		}
	}
	if e := os.RemoveAll(filepath.Join(h.root, id)); e != nil {
		return e
	}
	if e := os.Remove(filepath.Join(h.root, diskPlacementPrefix+id)); e != nil && !os.IsNotExist(e) {
		return e
	}
	h.mu.Lock()
	delete(h.diskOnly, id)
	delete(h.inMemory, id)
	delete(h.memoryFull, id)
	delete(h.usage, id)
	delete(h.freed, id)
	delete(h.timelines, id)
	delete(h.restarts, id)
	delete(h.streams, id)
	h.mu.Unlock()
	return nil
}

var generatedDirectoryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

// observeConversion keeps the converter's own reports of how fast it is going,
// for the owner's view of what is playing. The stream's entry goes with it.
func (h *HLS) observeConversion(id string) (func(vod.ConversionProgress), func()) {
	var last time.Time
	observe := func(p vod.ConversionProgress) {
		if p.ObservedAt.Sub(last) < time.Second || math.IsNaN(p.Speed) || math.IsInf(p.Speed, 0) || p.Speed < 0 {
			return
		}
		last = p.ObservedAt
		h.mu.Lock()
		if s := h.streams[id]; s != nil {
			s.speed, s.speedAt = p.Speed, p.ObservedAt
		}
		h.mu.Unlock()
	}
	finish := func() {
		h.mu.Lock()
		if s := h.streams[id]; s != nil {
			s.speed, s.speedAt = 0, time.Time{}
		}
		h.mu.Unlock()
	}
	return observe, finish
}

// converterKilled reports whether a converter ended because it was killed, not
// because it gave up on its input: ended by a signal, or by the exit status a
// sandbox reports for a child that was.
func converterKilled(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	code := exit.ExitCode()
	return code == -1 || code == 128+9
}
