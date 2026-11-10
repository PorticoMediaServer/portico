package streaming

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// ErrNoConverter says this server has no ffmpeg, so only original files play.
var ErrNoConverter = errors.New("This server cannot convert media: ffmpeg was not found.")

// Options configure the streams of one server process.
type Options struct {
	// Folder is where conversions are written. Everything in it is removed at
	// start: no stream survives a restart, so nothing in it can be asked for.
	Folder string
	// MemoryFolder is an optional private folder on a memory filesystem that
	// conversions are written to first, within the owner's memory setting.
	MemoryFolder string
	// FFmpeg is the converter to run; empty finds the bundled or installed one.
	FFmpeg string
	// Settings are the owner's delivery settings, read whenever they matter.
	Settings DeliverySettings
	// Libraries returns the library folders configured now. A file is opened
	// only if, with symbolic links resolved, it is inside one of them.
	Libraries func() []string
}

// Streams is every stream this server process is serving, in memory. A stream
// is found by the grant in its address or by the session that owns it. Nothing
// here is stored: after a restart every old address answers a hidden 404 and the
// player starts again.
type Streams struct {
	hls       *HLS
	libraries func() []string
	// byGrant and bySession are guarded by hls.mu, the same mutex that orders
	// producer admission, so stopping a stream and starting its converter can
	// never interleave.
	byGrant   map[string]*stream
	bySession map[string]*stream
}

// StartRequest is one stream to serve.
type StartRequest struct {
	// SessionID is the playback session this stream belongs to. A session has
	// one stream; starting another replaces it, and the old address stops working.
	SessionID string
	// Plan is how the file reaches the device, from Plan.
	Plan DeliveryPlan
	// Request is what Plan was made from. With it, a stream whose picture turns
	// out not to be copyable plans again by itself and converts; without it that
	// stream fails with its reason.
	Request *PlanRequest
	// Path is the file inside a library folder, with the size and modified time
	// the scanner recorded; a file that no longer matches is refused. Source
	// replaces all three for any other kind of source.
	Path     string
	Size     int64
	Modified time.Time
	Source   Source
	// PositionMs is where the viewer starts, so the converter starts there too.
	PositionMs int64
	// Subtitles are the text subtitles offered in the playlist, the selected one
	// first. Burn is the subtitle a plan with burn-in draws into the picture.
	Subtitles []TextRenditionPlan
	Burn      *BurnSubtitle
	// Replaced is called when this stream gives way to another by itself: a
	// hardware encoder that changed its output mid-stream carries on in software
	// under a new address. Nil fails such a stream instead.
	Replaced func(Stream)
	// Failed is called, once, when this stream's conversion fails for good. The
	// stream's address has stopped working by then.
	Failed func()
}

// Stream is what a player is given.
type Stream struct {
	// Grant is the unguessable part of the address. URL is the address to load.
	Grant string
	URL   string
	// Mode is "direct" for the original file by byte range, "hls" otherwise.
	Mode string
}

// StreamStatus is what the session layer can say about a session's stream.
type StreamStatus struct {
	Stream Stream
	// State is "playing", "stopped" or "failed". Failure says why.
	State   string
	Failure *ConversionFailure
	// Plan is the plan as it stands, after any fallback the stream made.
	Plan DeliveryPlan
	// Execution is what the converter actually ran; Speed is its latest reported
	// rate against real time, zero when it is not running.
	Execution ConversionExecution
	Speed     float64
}

// New prepares the streams of this process. A server without ffmpeg still
// serves original files; Start answers ErrNoConverter for anything else.
func New(ctx context.Context, o Options) (*Streams, error) {
	binary := o.FFmpeg
	if binary == "" {
		binary = mediatools.Resolve("ffmpeg")
	}
	var h *HLS
	if _, err := exec.LookPath(binary); err != nil {
		if err = os.MkdirAll(o.Folder, 0700); err != nil {
			return nil, err
		}
		if err = emptyDirectory(o.Folder); err != nil {
			return nil, err
		}
		h = &HLS{
			streams: map[string]*stream{}, root: o.Folder, active: map[string]context.CancelFunc{},
			windows: map[string]*hlsWindow{}, freed: map[string]int{}, usage: map[string]*hlsUsage{},
			timelines: map[string]*copyTimeline{}, ctx: ctx,
		}
	} else if h, err = NewHLS(ctx, o.Folder, binary); err != nil {
		return nil, err
	}
	h.ConfigureSettings(o.Settings)
	if o.MemoryFolder != "" && h.binary != "" {
		// Memory output is a preference. A folder that cannot be used leaves
		// conversions on disk.
		if err := h.ConfigureMemory(o.MemoryFolder); err == nil {
			_ = h.emptyMemory()
		}
	}
	libraries := o.Libraries
	if libraries == nil {
		libraries = func() []string { return nil }
	}
	s := &Streams{hls: h, libraries: libraries, byGrant: map[string]*stream{}, bySession: map[string]*stream{}}
	h.replace = s.replace
	return s, nil
}

// Run keeps conversions inside their budgets until ctx ends.
func (s *Streams) Run(ctx context.Context) { s.hls.Run(ctx) }

// Shutdown stops every stream and waits for the converters to exit.
func (s *Streams) Shutdown(ctx context.Context) error {
	s.hls.mu.Lock()
	for _, st := range s.byGrant {
		if st.state == streamLive {
			st.state = streamStopped
			s.hls.signalLocked(st)
		}
	}
	s.hls.mu.Unlock()
	err := s.hls.Shutdown(ctx)
	if err == nil {
		// Every converter has exited, so what they wrote can go now and not at
		// the next start.
		_ = emptyDirectory(s.hls.root)
		if s.hls.memoryRoot != "" {
			_ = s.hls.emptyMemory()
		}
	}
	return err
}

// Start registers a stream for a session and returns its address. It does not
// wait for the converter: Ready does. A session that already has a stream gets
// a new one with a new grant, and the old one is stopped.
func (s *Streams) Start(ctx context.Context, r StartRequest) (Stream, error) {
	if r.SessionID == "" {
		return Stream{}, errors.New("a stream needs its session")
	}
	mode := "hls"
	if r.Plan.Mode == "direct" || r.Plan.Strategy == DeliveryOriginal {
		mode = "direct"
	}
	if mode == "hls" && s.hls.binary == "" {
		return Stream{}, ErrNoConverter
	}
	source := r.Source
	if source == nil {
		source = libraryFile{path: r.Path, size: r.Size, modified: r.Modified, roots: s.libraries}
	}
	// A file that cannot be opened is refused now, in plain words, not as a
	// failed conversion a moment later.
	probe, _, _, err := source.Open(ctx)
	if err != nil {
		return Stream{}, err
	}
	probe.Close()
	plan := r.Plan
	if plan.SourceSize == 0 && !r.Modified.IsZero() {
		plan.SourceSize, plan.SourceModifiedNS = r.Size, r.Modified.UnixNano()
	}
	st := &stream{
		id: token(), session: r.SessionID, grant: token(), mode: mode, source: source, plan: &plan, request: r.Request,
		duration: plan.Duration, text: append([]TextRenditionPlan{}, r.Subtitles...), burn: r.Burn,
		changed: make(chan struct{}), replaced: r.Replaced, failedHook: r.Failed, served: -1,
	}
	s.Stop(r.SessionID)
	if err := s.install(st); err != nil {
		return Stream{}, err
	}
	if mode == "hls" {
		if err := s.hls.start(ctx, st.id, s.hls.startIndex(st.id, float64(r.PositionMs)/1000)); err != nil &&
			!errors.Is(err, ErrSegmentPreparing) && !errors.Is(err, ErrConversionSpace) {
			s.stop(st)
			return Stream{}, err
		}
	}
	return st.public(), nil
}

func (s *Streams) install(st *stream) error {
	h := s.hls
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil {
		return h.ctx.Err()
	}
	if s.bySession[st.session] != nil {
		// Another start for this session won the race; it is the stream now.
		return errors.New("this playback session was started again")
	}
	if err := s.admitLocked(st.plan); err != nil {
		return err
	}
	h.streams[st.id] = st
	s.byGrant[st.grant] = st
	s.bySession[st.session] = st
	return nil
}

// admitLocked holds a new picture conversion to the owner's limits: all
// conversions, and those on the hardware encoder or in software. A play that
// needs no picture conversion is never held back. Caller holds hls.mu.
func (s *Streams) admitLocked(plan *DeliveryPlan) error {
	cfg := s.hls.configuration()
	if plan.VideoAction != "convert" || cfg.MaxConversions <= 0 && cfg.MaxHardwareConversions <= 0 && cfg.MaxSoftwareConversions <= 0 {
		return nil
	}
	hardwarePlan := func(p *DeliveryPlan) bool { return p.HardwareBackend != "" && p.HardwareBackend != "software" }
	total, hardware, software := 0, 0, 0
	for _, other := range s.byGrant {
		if other.mode != "hls" || other.state != streamLive || other.plan.VideoAction != "convert" {
			continue
		}
		total++
		if hardwarePlan(other.plan) {
			hardware++
		} else {
			software++
		}
	}
	usesHardware := hardwarePlan(plan)
	if cfg.MaxConversions > 0 && total >= cfg.MaxConversions ||
		usesHardware && cfg.MaxHardwareConversions > 0 && hardware >= cfg.MaxHardwareConversions ||
		!usesHardware && cfg.MaxSoftwareConversions > 0 && software >= cfg.MaxSoftwareConversions {
		s.hls.conversionsRefused.Add(1)
		return ErrConversionCapacity
	}
	return nil
}

func (st *stream) public() Stream {
	url := "/v1/media/" + st.grant + "/original"
	if st.mode == "hls" {
		url = "/v1/media/" + st.grant + "/master.m3u8"
	}
	return Stream{Grant: st.grant, URL: url, Mode: st.mode}
}

// token is 128 random bits in URL-safe text: a grant nobody can guess, or the
// name of a stream's output folder.
func token() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// startIndex is the segment covering a position in a stream's timeline.
func (h *HLS) startIndex(id string, positionSeconds float64) int {
	index := 0
	if positionSeconds > 0 {
		index = int(positionSeconds) / HLSSegmentSeconds
	}
	if timeline := h.sessionTimeline(h.ctx, id); timeline != nil {
		index = timeline.indexAt(positionSeconds)
	}
	if index < 0 || index >= HLSMaxSegments {
		return 0
	}
	return index
}

// Stop ends a session's stream: its address stops working at once, its
// converter is cancelled and waited for, and its output is removed. Stopping a
// session with no stream does nothing.
func (s *Streams) Stop(sessionID string) {
	s.hls.mu.Lock()
	st := s.bySession[sessionID]
	s.hls.mu.Unlock()
	if st != nil {
		s.stop(st)
	}
}

func (s *Streams) stop(st *stream) {
	h := s.hls
	h.mu.Lock()
	if st.state == streamLive {
		st.state = streamStopped
	}
	delete(s.byGrant, st.grant)
	if s.bySession[st.session] == st {
		delete(s.bySession, st.session)
	}
	waits := []<-chan struct{}{}
	for key, cancel := range h.active {
		if producerSession(key) == st.id {
			if w := h.windows[key]; w != nil {
				w.retired = true
				waits = append(waits, w.done)
			}
			cancel()
		}
	}
	h.signalLocked(st)
	h.mu.Unlock()
	for _, done := range waits {
		<-done
	}
	// A reader may still be copying a segment out, or the output may be moving
	// off memory. Removal waits for it, briefly, and the sweep finishes the job
	// if it has to.
	for attempt := 0; attempt < 50; attempt++ {
		if err := h.remove(st.id); !errors.Is(err, ErrSegmentPreparing) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.mu.Lock()
	delete(h.streams, st.id)
	h.mu.Unlock()
}

// Restart discards what a session's stream has converted and starts converting
// again at a position, under the same address. A stream of the original file
// has nothing to restart.
func (s *Streams) Restart(ctx context.Context, sessionID string, positionMs int64) error {
	st, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if st.mode != "hls" {
		return nil
	}
	s.hls.restart(ctx, st.id, float64(positionMs)/1000)
	_, err = s.hls.liveStream(st.id)
	return err
}

// Ready waits until a session's stream has something to play: at once for the
// original file, and until the playlist is published for a conversion. It
// answers ErrSegmentPreparing if that takes more than twenty seconds (ask
// again), the stream's failure if it fails first, and ctx's error if the caller
// gives up.
func (s *Streams) Ready(ctx context.Context, sessionID string) error {
	st, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if st.mode != "hls" {
		return nil
	}
	return s.hls.Ready(ctx, st.id)
}

// Status reports a session's stream, and false when it has none.
func (s *Streams) Status(sessionID string) (StreamStatus, bool) {
	h := s.hls
	h.mu.Lock()
	defer h.mu.Unlock()
	st := s.bySession[sessionID]
	if st == nil {
		return StreamStatus{}, false
	}
	out := StreamStatus{Stream: st.public(), State: "playing", Failure: st.failure, Plan: *st.plan, Execution: st.execution}
	if st.state != streamLive {
		out.State = st.state
	}
	if time.Since(st.speedAt) < 5*time.Second {
		out.Speed = st.speed
	}
	return out, true
}

// RecentFailures is the conversion failures since this process started, oldest
// first, for the owner's troubleshooting page.
func (s *Streams) RecentFailures() []ConversionFailure {
	s.hls.mu.Lock()
	defer s.hls.mu.Unlock()
	return append([]ConversionFailure{}, s.hls.failures...)
}

// Conversions reports how many conversions are running against the owner's limit.
func (s *Streams) Conversions() ConversionCapacity { return s.hls.Conversions() }

func (s *Streams) session(sessionID string) (*stream, error) {
	s.hls.mu.Lock()
	defer s.hls.mu.Unlock()
	st := s.bySession[sessionID]
	if st == nil {
		return nil, ErrGrantEnded
	}
	return st, finishedStreamError(st.state)
}

func (s *Streams) grant(grant string) (*stream, error) {
	s.hls.mu.Lock()
	defer s.hls.mu.Unlock()
	st := s.byGrant[grant]
	if st == nil || st.state != streamLive {
		// A failed stream is hidden behind the same answer as one that ended.
		return nil, ErrGrantEnded
	}
	return st, nil
}

// replace swaps a stream for the same conversion under a new plan and a new
// address, at the position the old one had reached, and tells the session
// layer. It reports false when nobody would be told: the caller fails the
// stream instead. It runs on the old stream's producer, so the old converter is
// stopped by a goroutine of its own.
func (s *Streams) replace(id string, plan *DeliveryPlan, position float64) bool {
	h := s.hls
	h.mu.Lock()
	old := h.streams[id]
	if old == nil || old.state != streamLive || old.replaced == nil || s.bySession[old.session] != old {
		h.mu.Unlock()
		return false
	}
	next := &stream{
		id: token(), session: old.session, grant: token(), mode: "hls", source: old.source, plan: plan, request: old.request,
		duration: old.duration, text: old.text, burn: old.burn, openInput: old.openInput, disc: old.disc,
		changed: make(chan struct{}), replaced: old.replaced, failedHook: old.failedHook, served: -1,
	}
	old.state = streamStopped
	delete(s.byGrant, old.grant)
	h.streams[next.id] = next
	s.byGrant[next.grant] = next
	s.bySession[next.session] = next
	h.signalLocked(old)
	h.mu.Unlock()
	supervise.Go("streaming.replace", func() {
		s.stop(old)
		_ = h.start(h.ctx, next.id, int(position)/HLSSegmentSeconds)
		next.replaced(next.public())
	})
	return true
}

// emptyMemory removes stream folders left in the memory folder by an earlier
// process. The ownership marker stays.
func (h *HLS) emptyMemory() error {
	entries, err := os.ReadDir(h.memoryRoot)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.inMemory = map[string]bool{}
	h.mu.Unlock()
	for _, entry := range entries {
		if entry.Name() == memoryOwnershipMarker {
			continue
		}
		if err = os.RemoveAll(h.memoryRoot + string(os.PathSeparator) + entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// inputOpener opens a stream's source for one converter run. It exists for
// sources that are not one library file, such as a disc title.
type inputOpener func(context.Context) (*PlaybackInput, subtitles.WindowInput, error)
