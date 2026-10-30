package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrConversionSpace is the answer when the volume holding generated media has
// no room left to start another conversion. It is a beyond-target condition:
// nothing a viewer does at target load reaches it, and it is retryable because
// the eviction sweep may free room within seconds.
var ErrConversionSpace = errors.New("not enough free space to start media conversion; retry shortly")

// errMediaConversionFailed is a real fault: this source could not be converted.
// It is never an end of the presentation, so it must not be answered as one.
var errMediaConversionFailed = errors.New("media conversion failed")

// finishedSessionError answers a producer or segment wait whose session is
// already over. A stopped or ended presentation is a clean end: it gets the
// same hidden 404 the grant's own resolution answers (ErrGrantEnded), so a
// player that reads the final bytes of a short title as the client stops the
// session sees "ended", never a 500 from an untyped internal error. A failed
// session met by an already-admitted read stays the real conversion fault; a
// later grant resolution hides it as ended, as ResolveGrant already does
// (NEW: E-mobile-27).
func finishedSessionError(state string) error {
	switch state {
	case "stopped", "ended":
		return ErrGrantEnded
	case "failed":
		return errMediaConversionFailed
	}
	return nil
}

// Only owner-configured conversion limits govern admission. CPU count and
// measured server load are diagnostics, never a playback quality or admission rule.

// HLS owns conversion lifetimes only; planning and grants remain in Service.
type HLS struct {
	discPlayback *Service // exact active private disc reader, configured by Service

	subtitleService *subtitles.Service
	WindowInputs    subtitles.WindowInputs
	// fmp4 turns on fragmented MP4 output for plans that need it.
	fmp4          bool
	finite        *FiniteHLS
	SourceStorage *storage.Client
	// memberCeiling reads what a member is allowed now, for a re-plan that no
	// request asked for. See ConfigureMemberCeiling.
	memberCeiling    MemberCeilingReader
	db               *sql.DB
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
	occurrences      occurrenceRegistry
	cleanupMu        sync.Mutex
	cleaning         map[string]bool
	active           map[string]context.CancelFunc
	activeRuns       map[string]*occurrenceRun
	windows          map[string]*hlsWindow
	// restarts bounds how often one session may restart its producer, and
	// segmentWaits shares one wait between every request for the same segment.
	// Both exist for clients on bad networks; see hls_bad_network.go.
	restarts     map[string]*restartBudget
	segmentWaits map[string]*segmentWait
	// abandoned counts conversions reclaimed because nobody was fetching from
	// them any more.
	abandoned atomic.Uint64
	// conversionsRefused counts new conversions turned away by the host-capacity
	// bound, so an owner whose machine is too small for their library sees the
	// number rather than guessing from latency.
	conversionsRefused atomic.Uint64
	// hardwareFailures counts windows whose hardware encoder failed on a real
	// source and were run again in software.
	hardwareFailures atomic.Uint64
	// reclaimed is the lowest slot each session still retains, so retention
	// never re-walks the whole played region.
	reclaimed map[string]int
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
	run              *occurrenceRun
	generation       int
	publicationError error
	videoConversion  bool
	audioConversion  bool
	start            int
	served           int
	produced         int
	throttled        bool
	relocating       bool
	cancel           context.CancelFunc
	done             chan struct{}
}

// coversDemand keeps near-edge sequential fetches and the first output of a
// newly relocated window together. Requested position is never delivered progress.
func (w *hlsWindow) coversDemand(from int) bool {
	return !w.relocating && !w.throttled && from >= w.start && from <= max(w.start, w.produced)+2
}

// MemberCeilingReader reads, inside the caller's transaction, the remote video
// bitrate cap an account is under right now, in bits per second; zero is none.
// The layer that owns member limits provides it.
type MemberCeilingReader func(ctx context.Context, tx *sql.Tx, account string) (int, error)

// ConfigureMemberCeiling wires the member limits the conversion fallback has
// to respect. It re-plans a presentation with no request to carry the member's
// current ceiling, so it reads it here. A server with no member limits
// configures none; a limit that cannot be read fails the fallback and is never
// taken for no limit.
func (h *HLS) ConfigureMemberCeiling(read MemberCeilingReader) { h.memberCeiling = read }

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

func NewHLS(ctx context.Context, db *sql.DB, root, binary string) (*HLS, error) {
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
	helper, e := os.Executable()
	if e != nil {
		return nil, e
	}
	// A new server process has no running converters from its previous life.
	if _, e = dbwork.ExecWrite(ctx, db, dbwork.ClassEstablishedPlayback, `DELETE FROM playback_conversion_observations`); e != nil {
		return nil, e
	}
	outputRoot, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	return &HLS{fmp4: true, SourceStorage: storage.New(helper), db: db, root: root, binary: resolved, outputRoot: outputRoot, active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, reclaimed: map[string]int{}, usage: map[string]*hlsUsage{}, timelines: map[string]*copyTimeline{}, ffprobe: siblingProbe(resolved), ctx: ctx}, nil
}

// start admits one producer window for a session. A window already covering the
// requested slot is left alone; a window behind it is replaced, because a viewer
// who has jumped forward is not served by a converter still grinding through the
// part they skipped.
func (h *HLS) start(ctx context.Context, id string, from int) error {
	return h.startGeneration(ctx, id, from, 0)
}
func (h *HLS) startGeneration(ctx context.Context, id string, from, expected int) error {
	if dbwork.Snapshot(ctx) != nil {
		return errOccurrenceClaimed
	}
	intent, err := h.occurrences.intent(id)
	if err != nil {
		return err
	}
	defer intent.release()
	// Resolve durable state before taking the producer mutex. A starved read
	// pool must not hold up already-running segment producers.
	var sessionState, expires, artifactStatus string
	var videoConversion bool
	var generation int
	var itemID int64
	var requestID, assetID string
	if e := dbwork.QueryRow(ctx, h.db, `SELECT ps.state,ps.expires_at,COALESCE(a.status,''),COALESCE(d.video_action,'convert')='convert',ps.generation,ps.request_id,ps.item_id,ps.asset_id FROM playback_sessions ps LEFT JOIN playback_artifacts a ON a.session_id=ps.id LEFT JOIN playback_delivery_plans d ON d.session_id=ps.id WHERE ps.id=?`, id).Scan(&sessionState, &expires, &artifactStatus, &videoConversion, &generation, &requestID, &itemID, &assetID); e != nil {
		return e
	}
	if expected > 0 && generation != expected {
		return ErrOccurrenceChanged
	}
	if e := finishedSessionError(sessionState); e != nil {
		return e
	}
	if expires <= time.Now().UTC().Format(time.RFC3339) {
		return ErrGrantEnded
	}
	if artifactStatus == "failed" {
		return nil
	}
	if !intent.current() {
		return errOccurrenceClaimed
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil {
		return h.ctx.Err()
	}
	if h.moving[id] != nil || h.memoryFull[id] {
		return ErrSegmentPreparing
	}
	if h.cleaning[id] {
		return errors.New("playback output is being reclaimed")
	}
	if _, ok := h.active[id]; ok {
		window := h.windows[id]
		if window != nil && window.generation != generation {
			return errOccurrenceClaimed
		}
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
			err := h.startGeneration(ctx, id, from, generation)
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
		window.cancel()
		h.cancelOccurrenceRenditionsLocked(id, window.run)
		h.mu.Unlock()
		<-window.done
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
	window := &hlsWindow{generation: generation, videoConversion: videoConversion, start: from, served: -1, produced: from - 1, cancel: cancel, done: make(chan struct{})}
	run := &occurrenceRun{generation: generation, requestID: requestID, itemID: itemID, assetID: assetID, cancel: cancel, done: window.done}
	window.run = run
	if err := intent.install(run, func() { h.installOccurrenceLocked(id, run, cancel); h.windows[id] = window }); err != nil {
		cancel()
		return err
	}
	h.producers.Add(1)
	supervise.Go("playback.hls.produce", func() {
		defer h.producers.Done()
		h.produce(ctx, id, from, generation, window, intent.entry, run)
	})
	// A producer starts because somebody asked for it, so this is the moment the
	// viewer was last known to be there. Without it a conversion that is never
	// fetched from has no "served" time at all and could never be reclaimed.
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

func (h *HLS) produce(ctx context.Context, id string, from, generation int, owned *hlsWindow, entry *occurrenceEntry, ownedRun *occurrenceRun) {
	defer func() {
		h.mu.Lock()
		if h.windows[id] == owned {
			h.removeOccurrenceLocked(id, ownedRun)
			delete(h.windows, id)
		}
		close(owned.done)
		h.mu.Unlock()
		h.occurrences.finished(id, entry, ownedRun)
	}()
	supervise.Chaos("playback.hls.produce")
	var aid, state, video string
	var duration float64
	var actualGeneration int
	e := dbwork.QueryRow(ctx, h.db, `SELECT ps.asset_id,ps.state,a.video_codec,ps.duration,ps.generation FROM playback_sessions ps JOIN catalog_assets a ON a.token=ps.asset_id WHERE ps.id=? AND ps.mode='hls' AND ps.generation=? AND ps.request_id=? AND ps.item_id=? AND ps.asset_id=?`, id, generation, ownedRun.requestID, ownedRun.itemID, ownedRun.assetID).Scan(&aid, &state, &video, &duration, &actualGeneration)
	if e != nil || actualGeneration != generation || state == "stopped" || state == "ended" || state == "failed" {
		return
	}
	ctx = h.occurrenceContext(withProducerGeneration(ctx, generation), id, entry, ownedRun)
	delivery, e := loadDeliveryPlan(ctx, h.db, id)
	if e != nil {
		h.failed(ctx, id)
		return
	}
	if e = h.refreshLegacyConversionProbe(ctx, id, generation, delivery); e != nil {
		h.failed(ctx, id)
		return
	}
	h.mu.Lock()
	if w := h.windows[id]; w == owned && delivery != nil {
		w.audioConversion = delivery.AudioAction == "convert"
	}
	h.mu.Unlock()
	h.selectOutputBackend(ctx, id, delivery)
	dir, e := h.createStreamingOutput(ctx, id, delivery)
	if e != nil {
		if !errors.Is(e, ErrSegmentPreparing) && ctx.Err() == nil {
			h.failed(ctx, id)
		}
		return
	}
	if _, e = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'building') ON CONFLICT(session_id) DO UPDATE SET directory=excluded.directory,status='building',error=''`, id, dir); e != nil {
		return
	}
	input, remoteInput, e := h.openWindowInput(ctx, id, delivery)
	if e != nil {
		h.failed(ctx, id)
		return
	}
	defer input.Close()
	ctx, releaseInput := input.BorrowContext(ctx)
	defer releaseInput()
	if remoteInput != nil {
		defer remoteInput.Close()
	}
	path := input.Argument
	if delivery != nil {
		if e = h.loadTextRenditions(ctx, id, delivery); e != nil {
			h.failed(ctx, id)
			return
		}
	}
	if duration > 0 && !hlsDurationSupported(duration) {
		h.failedWith(ctx, id, "hls_duration_unsupported", "")
		return
	}
	grid := hlsSegmentCount(duration) > 0
	// A copied picture is cut on the source's own keyframes; see
	// hls_copy_timeline.go. A source that cannot be indexed is converted instead,
	// and the stored plan is changed to say so.
	var timeline *copyTimeline
	if grid && delivery != nil && delivery.VideoAction == "copy" {
		timeline, e = h.copyTimelineFor(ctx, id, aid, input, remoteInput, duration)
		if e != nil {
			if delivery, e = h.downgradeCopyPlan(ctx, id, delivery); e != nil {
				h.failedWith(ctx, id, FailureCopyTimeline, "")
				return
			}
		}
	}
	if grid {
		manifest := hlsTimelineManifest(duration)
		if timeline != nil {
			manifest = copyTimelineManifest(timeline)
		}
		if delivery != nil && delivery.OutputContainer == "fmp4_hls" {
			manifest = fragmentedManifest(manifest)
		}
		if e = publishRenditionManifests(dir, delivery, manifest); e != nil {
			h.outputSetupFailed(ctx, id)
			return
		}
	}
	if e = writeNamedManifest(dir, "generation", []byte(strconv.Itoa(generation))); e != nil {
		h.outputSetupFailed(ctx, id)
		return
	}
	h.mu.Lock()
	if timeline != nil {
		h.timelines[id] = timeline
	} else {
		delete(h.timelines, id)
	}
	h.mu.Unlock()
	position := float64(from) * HLSSegmentSeconds
	staging := ""
	if timeline != nil {
		if from >= timeline.count() {
			return
		}
		position = timeline.Boundaries[from]
	}
	if input.InputAt != nil || timeline != nil || grid && delivery != nil && delivery.OutputContainer == "fmp4_hls" {
		staging = filepath.Join(dir, "window-"+strconv.Itoa(from))
		_ = os.RemoveAll(staging)
		if e = os.MkdirAll(staging, 0700); e != nil {
			h.outputSetupFailed(ctx, id)
			return
		}
		defer os.RemoveAll(staging)
	}
	burn, cleanupBurn, burnErr := h.prepareBurnInput(ctx, id, dir, position, delivery, input, remoteInput)
	if burnErr != nil {
		if h.retryMemoryPathError(id, dir, burnErr) {
			return
		}
		h.failedWith(ctx, id, FailureConverter, "subtitle input unavailable")
		return
	}
	defer cleanupBurn()
	fragmentClock := &convertedFragmentClock{}
	build := func(plan *DeliveryPlan) ([]string, []decoder.HardwareStage, error) {
		offset := strconv.FormatFloat(position, 'f', 6, 64)
		protocols := "file,pipe"
		if remoteInput != nil {
			protocols = "http,tcp"
		}
		args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", protocols}
		if plan != nil && plan.VideoAction == "copy" {
			args = append(args, "-fflags", "+genpts+discardcorrupt")
		}
		if grid && from > 0 && input.InputAt == nil {
			// Input-side seek, so the decoder skips rather than decodes-and-drops.
			// A copy window seeks a hair past its keyframe: the demuxer lands on
			// the index entry at or before the target, and rounding the target
			// down by a tick must not send it to the keyframe before.
			seek := offset
			if timeline != nil {
				seek = strconv.FormatFloat(position+0.002, 'f', 6, 64)
			}
			args = append(args, "-ss", seek)
		}
		var inputArgs, codecArgs []string
		var stages []decoder.HardwareStage
		var extraInput []string
		videoMap := "0:v:0"
		if plan != nil {
			var err error
			graph, graphErr := plan.conversionGraph(burn)
			err = graphErr
			inputArgs, codecArgs, stages = graph.Input, append(graph.Video, graph.Audio...), graph.Stages
			extraInput = graph.ExtraInput
			if graph.VideoMap != "" {
				videoMap = graph.VideoMap
			}
			if err != nil {
				return nil, nil, err
			}
		}
		args = append(args, inputArgs...)
		readerOptions := input.Options
		residual := int64(0)
		if input.InputAt != nil {
			options, url, at, err := input.InputAt(ctx, int64(math.Round(position*1000)))
			if err != nil {
				return nil, nil, err
			}
			readerOptions = options
			path = url
			residual = int64(math.Round(position*1000)) - at
		}
		args = append(args, readerOptions...)
		args = append(args, "-i", path)
		args = append(args, extraInput...)
		if input.InputAt != nil {
			// Burn-in may already occupy input 1. Chapters are the final input.
			chapterIndex := 1
			for _, arg := range extraInput {
				if arg == "-i" {
					chapterIndex++
				}
			}
			args = append(args, "-protocol_whitelist", "file,pipe", "-format_whitelist", "ffmetadata", "-f", "ffmetadata", "-i", "pipe:4", "-map_chapters", strconv.Itoa(chapterIndex))
			if residual > 0 {
				args = append(args, "-ss", strconv.FormatFloat(float64(residual)/1000, 'f', 3, 64))
			}
		}
		// The plan names the programme audio by absolute stream index. A plan
		// without a choice (an older session) takes the first track, as before.
		audioMap := "0:a:0?"
		if plan != nil && plan.AudioStream >= 0 {
			audioMap = "0:" + strconv.Itoa(plan.AudioStream)
		}
		switch {
		case (plan != nil && plan.VideoAction == "none") || (plan == nil && video == ""):
			args = append(args, "-map", strings.TrimSuffix(audioMap, "?"), "-vn", "-sn", "-dn")
		case plan != nil && (plan.AudioAction == "none" || len(plan.Renditions) > 0):
			args = append(args, "-map", videoMap, "-an", "-sn", "-dn")
		default:
			args = append(args, "-map", videoMap, "-map", audioMap, "-sn", "-dn")
		}
		if plan != nil {
			args = append(args, codecArgs...)
		} else {
			// Existing sessions predate immutable delivery snapshots. Preserve their
			// original output policy; new sessions always consume their stored plan.
			if video == "h264" {
				args = append(args, "-c:v", "copy")
			} else if video != "" {
				args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "21", "-pix_fmt", "yuv420p", "-force_key_frames", "expr:gte(t,n_forced*6)")
			}
			args = append(args, "-c:a", "aac", "-b:a", "192k", "-ac", "2")
		}
		fragmented := plan != nil && plan.OutputContainer == "fmp4_hls"
		if fragmented {
			args = append(args, "-map_metadata", "-1", "-fflags", "+bitexact")
		}
		if plan != nil {
			profile := 0
			videoCodec := ""
			if plan.VideoAction != "none" {
				videoCodec = plan.OutputVideoCodec
			}
			if plan.VideoAction == "copy" {
				profile, _ = deliveredDolbyVision(plan)
			}
			copiedAudioCodec := ""
			if plan.AudioAction == "copy" {
				copiedAudioCodec = plan.OutputAudioCodec
			}
			args = append(args, decoder.HLSMuxArgs(fragmented, videoCodec, copiedAudioCodec, profile)...)
		}
		if plan != nil && plan.VideoAction == "convert" && plan.AudioAction == "copy" && grid && from > 0 {
			args = append(args, "-bsf:a", "noise=drop='lt(pts*tb,0)'")
		}
		args = append(args, "-max_muxing_queue_size", "1024")
		if timeline != nil {
			args = append(args, "-muxdelay", "0", "-muxpreload", "0")
			return append(args, copySegmentArgs(timeline, from, staging, fragmented)...), stages, nil
		}
		packaging := "mpegts_hls"
		if fragmented {
			packaging = "fmp4_hls"
		}
		initName := "init-" + strconv.Itoa(from) + ".mp4"
		if !grid {
			initName = "init.mp4"
		}
		outputArgs, ext, err := decoder.HLSPackagingArgs(packaging, initName)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, outputArgs...)
		args = append(args, "-f", "hls", "-hls_time", strconv.Itoa(HLSSegmentSeconds), "-hls_list_size", "0", "-hls_flags", "independent_segments+temp_file")
		outputDir := dir
		if fragmented && staging != "" {
			outputDir = staging
		}
		if grid {
			// Absolute source timestamps and a grid-aligned start number are what
			// make a restarted window continuous with everything already produced.
			args = append(args, "-output_ts_offset", offset, "-muxdelay", "0", "-muxpreload", "0", "-start_number", strconv.Itoa(from), "-hls_segment_filename", filepath.Join(outputDir, "segment-%06d."+ext), filepath.Join(outputDir, "window.m3u8"))
		} else {
			args = append(args, "-hls_playlist_type", "event", "-hls_segment_filename", filepath.Join(dir, "segment-%06d."+ext), filepath.Join(dir, "master.m3u8"))
		}
		return args, stages, nil
	}
	_, _ = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_hls_windows(session_id,start_index,status,started_at_ms) VALUES(?,?,'running',?) ON CONFLICT(session_id,start_index) DO UPDATE SET status='running',started_at_ms=excluded.started_at_ms`, id, from, time.Now().UnixMilli())
	processCtx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	watchCtx, stopWatch := context.WithCancel(processCtx)
	watchDone := make(chan struct{})
	if grid {
		limit := -1
		if timeline != nil {
			limit = min(from+copyWindowSegments, timeline.count()) - 1
		}
		supervise.Go("playback.hls.watch-window", func() {
			defer close(watchDone)
			h.watchWindow(watchCtx, id, dir, staging, from, limit, cancel, delivery, timeline, fragmentClock, owned, input.Validate)
		})
	} else {
		close(watchDone)
	}
	fallbackLaunch := false
	run := func(plan *DeliveryPlan) (error, string) {
		if remoteInput != nil {
			// The remote-input runner starts and reaps the converter itself, and
			// Run returns only once that child is gone, so the reservation is taken
			// in the command builder and released after Run returns.
			releaseRemote := func() {}
			err := remoteInput.Run(processCtx, dir, func(url string) (subtitles.WindowCommand, error) {
				path = url
				args, stages, err := build(plan)
				if err == nil {
					h.recordStages(ctx, id, plan, stages)
				}
				if err == nil && plan != nil && hardwareEncodeStage(stages) {
					cell := decoder.PermitCell{Identity: decoder.PermitIdentity(h.binary), Backend: decoder.HardwareBackend(plan.HardwareBackend), Device: plan.HardwareDevice}
					release, reserved := decoder.HardwarePermits().Reserve(cell)
					if !reserved {
						return subtitles.WindowCommand{}, errHardwarePermitUnavailable
					}
					releaseRemote = release
				}
				return subtitles.WindowCommand{Args: args, Hardware: conversionJobHardware(plan, stages), Started: func() { h.recordConversionStart(ctx, id, plan, args, stages, generation, fallbackLaunch) }}, err
			})
			releaseRemote()
			return err, windowConverterDiagnostic(err, path, h.root, h.memoryRoot)
		}

		args, stages, err := build(plan)
		if err != nil {
			return err, sanitizeConverterOutput(err.Error(), h.root, h.memoryRoot)
		}
		if len(stages) > 0 {
			h.recordStages(ctx, id, plan, stages)
		}
		for _, extra := range input.Extra {
			_, _ = extra.Seek(0, io.SeekStart)
		}
		// Every child starts at the beginning of the source. On platforms where
		// /dev/fd shares the file offset with this process, the index probe that
		// ran before has moved it.
		if input.File != nil {
			_, _ = input.File.Seek(0, io.SeekStart)
		}
		// The one media executor (mediaexec): sandboxed where the platform can,
		// the input only as descriptor 3, writes only into this session's folder,
		// and its own process group, so cancelling reaches the whole sandbox.
		var progress *vod.ConversionProgressWriter
		if plan != nil && (plan.VideoAction == "convert" || plan.AudioAction == "convert" || plan.BurnIn != nil) {
			observe, finish := h.observeConversion(ctx, id, generation)
			defer finish()
			args = append([]string{"-progress", "pipe:1", "-stats_period", "1", "-nostats"}, args...)
			progress = vod.NewConversionProgressWriter(observe)
		}
		// One reservation per hardware converter, taken before the child starts and
		// released only after it has been waited or cancelled and reaped. A slot
		// this adapter cannot give right now returns to the existing fallback: the
		// same path a refused hardware encoder already takes.
		releasePermit := func() {}
		// Only a hardware ENCODE takes a session slot: a hardware decoder, tone
		// mapper or filter does not open an encoder session, and counting it would
		// refuse conversions the card can run.
		if plan != nil && hardwareEncodeStage(stages) {
			cell := decoder.PermitCell{Identity: decoder.PermitIdentity(h.binary), Backend: decoder.HardwareBackend(plan.HardwareBackend), Device: plan.HardwareDevice}
			var reserved bool
			releasePermit, reserved = decoder.HardwarePermits().Reserve(cell)
			if !reserved {
				return errHardwarePermitUnavailable, ""
			}
		}
		defer releasePermit()
		if input.Validate != nil {
			if err := validateOwnedInput(processCtx, input.Validate); err != nil {
				return err, ""
			}
		}
		job := mediaexec.Job{Executable: h.binary, Args: args, Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths(), WriteDirs: []string{dir}, Fonts: true, Hardware: conversionJobHardware(plan, stages)}
		if h.execute != nil {
			return h.execute(processCtx, job)
		}
		cmd, err := mediaexec.CommandContext(processCtx, job)
		if err != nil {
			return err, ""
		}
		cmd.WaitDelay = 2 * time.Second
		if progress != nil {
			cmd.Stdout = progress
		}
		tail := &tailWriter{limit: 4096}
		cmd.Stderr = tail
		launchCtx := childprocess.WithStarted(processCtx, func() { h.recordConversionStart(ctx, id, plan, args, stages, generation, fallbackLaunch) })
		err = childprocess.Run(launchCtx, cmd)
		if err == nil && input.Validate != nil {
			err = validateOwnedInput(processCtx, input.Validate)
		}
		return err, sanitizeConverterOutput(tail.String(), input.Argument, h.root, h.memoryRoot)
	}
	e, diagnostic := run(delivery)
	stopWatch()
	<-watchDone
	h.mu.Lock()
	var publicationError error
	if w := h.windows[id]; w == owned {
		publicationError = w.publicationError
	}
	h.mu.Unlock()
	if publicationError != nil {
		h.outputPublicationFailed(ctx, id, delivery, position, publicationError)
		return
	}
	throttled := func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		window := h.windows[id]
		return window == owned && window.throttled
	}
	// A hardware encoder that probed clean at startup can still refuse a real
	// source: a resolution the block does not take, a driver that has since
	// wedged, a session limit on a consumer GPU. The viewer is not told about
	// any of that. The same window is run again in software, and the stored plan
	// is changed so later windows do not repeat the failure.
	// A quota refusal is not a failing encoder: the adapter is healthy and busy.
	// The window still runs in software, but nothing here may record a device
	// failure, trip the adapter's circuit or rewrite the stored plan as if the
	// hardware had broken.
	if e != nil && errors.Is(e, errHardwarePermitUnavailable) && !throttled() && ctx.Err() == nil && delivery != nil {
		// No reason code is added: the adapter did not fail, it was full.
		software := *delivery
		software.HardwareBackend, software.HardwareNative, software.HardwareDevice = string(decoder.BackendSoftware), false, ""
		delivery = &software
		watchCtx, stopWatch = context.WithCancel(processCtx)
		watchDone = make(chan struct{})
		if grid {
			supervise.Go("playback.hls.watch-quota-window", func() {
				defer close(watchDone)
				h.watchWindow(watchCtx, id, dir, staging, from, -1, cancel, delivery, timeline, fragmentClock, owned, input.Validate)
			})
		} else {
			close(watchDone)
		}
		fallbackLaunch = true
		e, diagnostic = run(delivery)
		stopWatch()
		<-watchDone
	}
	if e != nil && !throttled() && ctx.Err() == nil && delivery != nil && delivery.VideoAction == "convert" && delivery.HardwareBackend != "" && delivery.HardwareBackend != string(decoder.BackendSoftware) {
		failedBackend := delivery.HardwareBackend
		failedDevice := delivery.HardwareDevice
		software := *delivery
		software.HardwareBackend, software.HardwareNative, software.HardwareDevice = string(decoder.BackendSoftware), false, ""
		software.ReasonCodes = append(append([]string{}, delivery.ReasonCodes...), ReasonHardwareFailedSoftware)
		h.recordSoftwareFallback(ctx, id, failedBackend, diagnostic)
		delivery = &software
		if staging != "" {
			_ = os.RemoveAll(staging)
			_ = os.MkdirAll(staging, 0700)
		}
		fragmentClock = &convertedFragmentClock{}
		beforeRetry := highestProducedSegment(dir, from)
		h.mu.Lock()
		if w := h.windows[id]; w == owned {
			beforeRetry = max(beforeRetry, w.produced)
		}
		h.mu.Unlock()
		watchCtx, stopWatch = context.WithCancel(processCtx)
		watchDone = make(chan struct{})
		if grid {
			supervise.Go("playback.hls.watch-software-window", func() {
				defer close(watchDone)
				h.watchWindow(watchCtx, id, dir, staging, from, -1, cancel, delivery, timeline, fragmentClock, owned, input.Validate)
			})
		} else {
			close(watchDone)
		}
		fallbackLaunch = true
		e, diagnostic = run(delivery)
		stopWatch()
		<-watchDone
		h.mu.Lock()
		softwareProduced, softwareThrottled, currentWindow := from-1, false, false
		if w := h.windows[id]; w == owned {
			publicationError = w.publicationError
			softwareProduced, softwareThrottled, currentWindow = w.produced, w.throttled, true
		}
		h.mu.Unlock()
		if publicationError != nil {
			h.outputPublicationFailed(ctx, id, delivery, position, publicationError)
			return
		}
		retryCancelled := ctx.Err() != nil || !currentWindow || processCtx.Err() != nil && !softwareThrottled
		if hardwareFailureConfirmed(e, retryCancelled, softwareThrottled, from, beforeRetry, softwareProduced) {
			decoder.RecordHardwareDeviceFailure(decoder.HardwareBackend(failedBackend), failedDevice)
		}
	}
	if e != nil && !throttled() && ctx.Err() == nil {
		h.failedWith(ctx, id, classifyConverterFailure(diagnostic), diagnostic)
		return
	}
	status := "complete"
	if throttled() {
		status = "throttled"
	} else if e != nil {
		status = "failed"
	}
	if input.Validate != nil {
		if err := validateOwnedInput(ctx, input.Validate); err != nil {
			h.failedWith(ctx, id, FailureConverter, "")
			return
		}
	}
	if err := childprocess.CheckOwnedIO(ctx); err != nil {
		return
	}
	// Record the final edge here as well as on the watcher's tick: a window that
	// finishes inside one tick would otherwise publish no progress at all.
	produced := from - 1
	if timeline != nil {
		if delivery != nil && delivery.OutputContainer == "fmp4_hls" {
			var promoteErr error
			produced, promoteErr = promoteFMP4Segments(staging, dir, timeline, from, e == nil)
			if promoteErr != nil {
				h.outputPublicationFailed(ctx, id, delivery, position, promoteErr)
				return
			}
		} else {
			produced = promoteCopySegments(staging, dir, from, e == nil)
		}
	} else if grid {
		if delivery != nil && delivery.OutputContainer == "fmp4_hls" {
			var publishErr error
			produced, publishErr = promoteConvertedFragments(staging, dir, "init-"+strconv.Itoa(from)+".mp4", from, nil, fragmentClock)
			if publishErr != nil {
				h.outputPublicationFailed(ctx, id, delivery, position, publishErr)
				return
			}
		} else {
			if staging != "" {
				produced = promoteCopySegments(staging, dir, from, e == nil)
			} else {
				produced = highestProducedSegment(dir, from)
			}
		}
	}

	_, _ = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_hls_windows SET status=?,highest_produced=max(highest_produced,?) WHERE session_id=? AND start_index=?`, status, produced, id, from)
	if e == nil && (timeline == nil || produced >= timeline.count()-1) {
		_, _ = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_artifacts SET status='complete' WHERE session_id=?`, id)
	}
}

// watchWindow enforces the throttle and published progress. Conversion that has
// run far enough ahead of the viewer is stopped rather than paused: a stopped
// converter costs nothing, and the fixed grid means the next demand restarts it
// exactly where the viewer is instead of where it left off.
func (h *HLS) watchWindow(ctx context.Context, id, dir, staging string, from, limit int, cancel context.CancelFunc, plan *DeliveryPlan, timeline *copyTimeline, fragmentClock *convertedFragmentClock, owned *hlsWindow, guards ...func(context.Context) error) {
	cfg := h.configuration()
	interval := 500 * time.Millisecond
	if staging != "" {
		// A copy window's segments only become visible when this loop promotes
		// them, so it runs at the pace a player waits at.
		interval = 100 * time.Millisecond
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		keep := func() bool {
			ownedIO, err := childprocess.OwnedIOContext(ctx)
			if err != nil {
				return false
			}
			for _, guard := range guards {
				if guard != nil {
					if err := guard(ownedIO); err != nil {
						h.publicationFailed(id, owned, err)
						cancel()
						return false
					}
				}
			}
			produced := from - 1
			if staging != "" {
				if plan != nil && plan.OutputContainer == "fmp4_hls" {
					var err error
					if timeline != nil {
						produced, err = promoteFMP4Segments(staging, dir, timeline, from, false)
					} else {
						produced, err = promoteConvertedFragments(staging, dir, "init-"+strconv.Itoa(from)+".mp4", from, nil, fragmentClock)
					}
					if err != nil {
						h.publicationFailed(id, owned, err)
						cancel()
						return false
					}
				} else {
					produced = promoteCopySegments(staging, dir, from, false)
				}
			} else {
				produced = highestProducedSegment(dir, from)
			}
			h.mu.Lock()
			window := h.windows[id]
			if window != owned {
				h.mu.Unlock()
				return false
			}
			changed := produced > window.produced
			if changed {
				window.produced = produced
			}
			h.mu.Unlock()
			if changed {
				_, _ = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_hls_windows SET highest_produced=? WHERE session_id=? AND start_index=?`, produced, id, from)
			}
			h.mu.Lock()
			if h.windows[id] != owned {
				h.mu.Unlock()
				return false
			}
			ahead := (window.produced - max(window.served, window.start-1)) * HLSSegmentSeconds
			// A copy window that has used up its cut list stops the same way a
			// throttled one does: the next request continues from there.
			exhausted := limit >= 0 && window.produced >= limit-1 && limit < h.timelines[id].count()-1
			if (cfg.ThrottleBufferSeconds > 0 && window.produced >= from && ahead >= cfg.ThrottleBufferSeconds) || exhausted {
				window.throttled = true
				owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner)
				if owner != nil {
					h.cancelOccurrenceRenditionsLocked(id, owner.run)
				}
				h.mu.Unlock()
				_, _ = h.producerWrite(ctx, id, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_hls_demand(session_id,highest_served,throttled) VALUES(?,?,1) ON CONFLICT(session_id) DO UPDATE SET throttled=1`, id, produced)
				cancel()
				return false
			}
			h.mu.Unlock()
			return true
		}()
		if !keep {
			return
		}
	}
}

// recordStages publishes what the conversion graph actually did, so the
// occurrence diagnostic can say where each operation ran without re-deriving it.
// persist is the context a "this must be recorded" write runs under. The
// original used context.Background(), which is unbounded: called while serving
// a .ts segment, a contended gate would hold the write — and the media-body slot
// behind it — long past the request's own deadline. WithoutCancel keeps the
// intent (the record survives the request being abandoned) and the timeout keeps
// the promise that nothing on the request path is unbounded.
func persist(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

func (h *HLS) recordStages(ctx context.Context, id string, plan *DeliveryPlan, stages []decoder.HardwareStage) {
	raw, err := json.Marshal(stages)
	if err != nil {
		return
	}
	backend := string(decoder.BackendSoftware)
	if plan != nil && plan.HardwareBackend != "" {
		backend = plan.HardwareBackend
	}
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_hls_stages(session_id,backend,stages_json) VALUES(?,?,json_object('stages',json(?))) ON CONFLICT(session_id) DO UPDATE SET backend=excluded.backend,stages_json=json_set(CASE WHEN json_type(playback_hls_stages.stages_json)='object' THEN playback_hls_stages.stages_json ELSE json_object('stages',json(playback_hls_stages.stages_json)) END,'$.stages',json(?))`, id, backend, string(raw), string(raw))
}

// producerGenerationKey carries the session generation a producer started
// for. A subtitle change (burn-in on or off) replans a session under a new
// generation while the old producer may still be winding down; its failure
// must not fail the presentation that replaced it.
type producerGenerationKey struct{}

func withProducerGeneration(ctx context.Context, generation int) context.Context {
	return context.WithValue(ctx, producerGenerationKey{}, generation)
}

// failed fails the session's conversion, and reports whether it did: a
// producer whose generation has been replaced fails nothing.
func (h *HLS) failed(ctx context.Context, id string) bool {
	h.mu.Lock()
	moving := h.moving[id] != nil
	if owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner); owner != nil && owner.run != nil {
		moving = moving || owner.run.spillCancelled
	}
	h.mu.Unlock()
	if moving || failureDuringClaim(ctx) || h.memoryOutputPressure(id) {
		return false
	}
	write, cancel := persist(ctx)
	defer cancel()
	query, args := `UPDATE playback_sessions SET state='failed' WHERE id=? AND state NOT IN ('stopped','ended')`, []any{id}
	if generation, ok := ctx.Value(producerGenerationKey{}).(int); ok {
		query, args = query+` AND generation=?`, append(args, generation)
	}
	result, err := h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, query, args...)
	if err != nil {
		return false
	}
	if err == nil {
		if n, e := result.RowsAffected(); e == nil && n == 0 {
			if _, fenced := ctx.Value(producerGenerationKey{}).(int); fenced {
				return false
			}
		}
	}
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_artifacts SET status='failed',error='Media conversion failed; source may use unsupported streams.' WHERE session_id=?`, id)
	return true
}
func (h *HLS) Ready(ctx context.Context, id string) error {
	if h.finite != nil {
		plan, err := loadDeliveryPlan(ctx, h.db, id)
		if err != nil {
			return err
		}
		if plan != nil && plan.Profile == finiteDeliveryProfile {
			_, err = h.finite.ensure(ctx, id)
			return err
		}
	}
	if e := h.start(ctx, id, 0); e != nil {
		return e
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var status, state string
		var generation int
		if err := dbwork.QueryRow(ctx, h.db, `SELECT ps.state,COALESCE(a.status,''),ps.generation FROM playback_sessions ps LEFT JOIN playback_artifacts a ON a.session_id=ps.id WHERE ps.id=?`, id).Scan(&state, &status, &generation); err != nil {
			return err
		}
		if e := finishedSessionError(state); e != nil {
			return e
		}
		if status == "failed" {
			return errMediaConversionFailed
		}
		if info, e := os.Stat(filepath.Join(h.outputDir(id), "master.m3u8")); e == nil && info.Size() > 0 {
			stamp, err := os.ReadFile(filepath.Join(h.outputDir(id), "generation"))
			if err == nil && string(stamp) == strconv.Itoa(generation) {
				return nil
			}
		}
		select {
		case <-wait.Done():
			return errors.New("conversion startup not ready; retry the same requestId")
		case <-tick.C:
		}
	}
}

var artifactName = regexp.MustCompile(`^(master\.m3u8|video\.m3u8|text-[0-9]{1,2}\.m3u8|audio-[0-9]{1,2}\.m3u8|init\.mp4|segment-[0-9]{6}\.(?:ts|m4s)|audio-[0-9]{1,2}-init\.mp4|audio-[0-9]{1,2}-[0-9]{6}\.(?:ts|m4s))$`)

func (h *HLS) File(grant, name string) (string, error) {
	return h.FileContext(context.Background(), grant, name)
}

// FileContext resolves one artifact and, for a segment the converter has not
// reached, moves the converter. A seek is nothing more than a request for a
// slot further along the grid: if it already exists the read is instant, and if
// it does not the session's own producer restarts there rather than converting
// everything in between.
func (h *HLS) FileContext(ctx context.Context, grant, name string) (string, error) {
	if !artifactName.MatchString(name) {
		return "", errors.New("unknown stream artifact")
	}
	var id string
	var generation int
	var duration float64
	e := dbwork.QueryRow(ctx, h.db, `SELECT id,duration,generation FROM playback_sessions WHERE grant_hash=? AND mode='hls' AND state NOT IN('stopped','ended','failed') AND expires_at>?`, identity.Digest(grant), time.Now().UTC().Format(time.RFC3339)).Scan(&id, &duration, &generation)
	if errors.Is(e, sql.ErrNoRows) {
		// The grant was already resolved, so an empty answer here means the
		// session stopped, ended, failed or expired in between. ResolveGrant
		// hides a failed session behind this same ended answer too, so this is
		// the grant's end, not an unknown grant. A real conversion fault is the
		// separate failed-artifact path while the grant is still live.
		return "", ErrGrantEnded
	}
	if e != nil {
		return "", e
	}
	if err := h.waitOutputMove(ctx, id); err != nil {
		return "", err
	}
	index, segment := hlsSegmentIndex(name)
	if strings.HasPrefix(name, "segment-") && !segment {
		return "", errors.New("unknown stream artifact")
	}
	stamp, stampErr := os.ReadFile(filepath.Join(h.outputDir(id), "generation"))
	if stampErr != nil || string(stamp) != strconv.Itoa(generation) {
		// Retire interrupted owned initial publication, then use the same absent
		// output restart path as eviction. A valid different generation stays fenced.
		h.retireIncompleteOutput(id)
		if os.IsNotExist(stampErr) || !validOutputGeneration(h.outputDir(id)) {
			if _, err := os.Stat(h.outputDir(id)); os.IsNotExist(err) {
				from, segment := hlsSegmentIndex(name)
				if !segment {
					from = 0
				}
				if segment && from >= hlsSegmentCount(duration) {
					return "", errors.New("unknown stream artifact")
				}
				if err := h.start(ctx, id, from); err != nil {
					return "", err
				}
			}
		}
		return "", ErrSegmentPreparing
	}
	delivery, err := loadDeliveryPlan(ctx, h.db, id)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(name, "audio-") {
		if delivery == nil || len(delivery.Renditions) == 0 {
			return "", errors.New("unknown stream artifact")
		}
		return h.renditionFile(ctx, id, name, delivery)
	}
	path := filepath.Join(h.outputDir(id), name)
	if name == "init.mp4" {
		return h.awaitSegment(ctx, id, -1, func(ctx context.Context) (string, error) {
			wait, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				current := filepath.Join(h.outputDir(id), name)
				if current != path {
					path = current
					if err := h.start(ctx, id, 0); err != nil {
						return "", err
					}
				}
				if segmentReady(path) {
					return path, nil
				}
				select {
				case <-wait.Done():
					return "", ErrSegmentPreparing
				case <-tick.C:
				}
			}
		})
	}
	if !segment || hlsSegmentCount(duration) == 0 {
		return path, nil
	}
	count := hlsSegmentCount(duration)
	if timeline := h.sessionTimeline(ctx, id); timeline != nil {
		count = timeline.count()
	}
	if index >= count {
		// A well-formed segment slot at or past a known grid is the end of this
		// presentation, not an unknown artifact: a short title's last read can
		// land here while the session is still live. Answer the grant's own
		// hidden ended 404 rather than an internal 500. Malformed names and
		// generation mismatches keep their own refusals above.
		if count > 0 {
			return "", ErrGrantEnded
		}
		// A timeline that reports no slots is a broken plan, so it stays a fault.
		return "", errors.New("unknown stream artifact")
	}
	cfg := h.configuration()
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		h.noteServed(ctx, id, index, cfg)
		return path, nil
	}
	if e = h.start(ctx, id, index); e != nil {
		return "", e
	}
	// Everybody waiting for this segment waits once. Ten reconnecting clients
	// asking for the same segment used to run ten poll loops between them.
	return h.awaitSegment(ctx, id, index, func(ctx context.Context) (string, error) {
		wait, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			var state, status string
			if err := dbwork.QueryRow(ctx, h.db, `SELECT ps.state,COALESCE(a.status,'') FROM playback_sessions ps LEFT JOIN playback_artifacts a ON a.session_id=ps.id WHERE ps.id=?`, id).Scan(&state, &status); err != nil {
				return "", err
			}
			if e := finishedSessionError(state); e != nil {
				return "", e
			}
			if status == "failed" {
				return "", errMediaConversionFailed
			}
			current := filepath.Join(h.outputDir(id), name)
			if current != path {
				path = current
				if err := h.start(ctx, id, index); err != nil {
					return "", err
				}
			}
			if segmentReady(path) {
				h.noteServed(ctx, id, index, cfg)
				return path, nil
			}
			select {
			case <-wait.Done():
				return "", ErrSegmentPreparing
			case <-tick.C:
			}
		}
	})
}

// noteServed records the viewer's real position in the conversion, which is what
// both the throttle and the retention window are measured against.
func (h *HLS) noteServed(ctx context.Context, id string, index int, cfg DeliveryConfiguration) {
	h.noteSessionServed(id)
	h.mu.Lock()
	restart := false
	if window := h.windows[id]; window != nil {
		if index > window.served {
			window.served = index
		}
		restart = window.throttled
	}
	from := h.reclaimed[id]
	moving := h.moving[id] != nil
	h.mu.Unlock()
	if moving {
		return
	}
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `INSERT INTO playback_hls_demand(session_id,highest_served,throttled) VALUES(?,?,0) ON CONFLICT(session_id) DO UPDATE SET highest_served=max(playback_hls_demand.highest_served,excluded.highest_served),throttled=0`, id, index)
	dir, release := h.retainOutput(id)
	if release == nil {
		return
	}
	defer release()
	if _, reclaimed := prunePlayedSegments(dir, from, index, cfg.PlayedRetentionSeconds); reclaimed > from {
		h.mu.Lock()
		if h.reclaimed[id] < reclaimed {
			h.reclaimed[id] = reclaimed
		}
		h.mu.Unlock()
	}
	if restart {
		_ = h.start(ctx, id, index+1)
	}
}

func (h *HLS) Run(ctx context.Context) {
	wake := worker.NewSignal()
	unregister := watchHLSMaintenance(wake)
	defer unregister()
	defer h.cancelAll()
	defer h.closeOrphanDirectory()
	worker.Run(ctx, "playback.hls-maintenance", wake, func(ctx context.Context) time.Duration {
		h.sweep(ctx)
		cleanupErr := h.CleanupGenerated(ctx)
		if cleanupErr != nil || len(h.activeSnapshot()) > 0 || h.hasMemoryOutput() {
			return time.Second
		}
		return 0
	})
}

// CleanupGenerated reclaims only server-owned, terminal/expired output. The same
// per-session claim fences start and finite conversion; all IO runs outside the
// producer mutex.
// Database-provided paths are never followed; live producers retain their output.
func (h *HLS) CleanupGenerated(ctx context.Context) error {
	h.cleanupMu.Lock()
	defer h.cleanupMu.Unlock()
	rows, err := h.db.QueryContext(ctx, `SELECT a.session_id FROM (SELECT session_id FROM playback_artifacts UNION SELECT session_id FROM playback_hls_reservations) a LEFT JOIN playback_sessions ps ON ps.id=a.session_id WHERE ps.id IS NULL OR ps.state IN ('stopped','ended','failed') OR ps.expires_at<=? ORDER BY a.session_id LIMIT 128`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return err
		}
		if !generatedDirectoryID.MatchString(id) {
			return errors.New("invalid generated artifact identity")
		}
		h.mu.Lock()
		if h.sessionActiveLocked(id) || h.moving[id] != nil || h.cleaning[id] || h.outputBusy[id] > 0 {
			h.mu.Unlock()
			continue
		}
		if h.cleaning == nil {
			h.cleaning = map[string]bool{}
		}
		h.cleaning[id] = true
		h.mu.Unlock()
		err = func() error {
			defer func() { h.mu.Lock(); delete(h.cleaning, id); h.mu.Unlock() }()
			var reclaim bool
			if e := h.db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?)`, id, time.Now().UTC().Format(time.RFC3339)).Scan(&reclaim); e != nil {
				return e
			}
			if !reclaim {
				return nil
			}
			// RemoveAll does not follow a directory symlink. The root is configured by
			// the server and id has no separators/dot segments; source files are excluded.
			if e := os.RemoveAll(h.outputDir(id)); e != nil {
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
			h.mu.Unlock()
			gated, e := dbwork.Begin(ctx, h.db, dbwork.ClassPlaybackStart)
			if e != nil {
				return e
			}
			tx := gated.Tx()
			defer gated.Rollback()
			if _, e = tx.ExecContext(ctx, `DELETE FROM playback_artifacts WHERE session_id=?`, id); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `DELETE FROM playback_hls_reservations WHERE session_id=?`, id); e != nil {
				return e
			}
			return gated.Commit()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

var generatedDirectoryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

// observeConversion records only actual child reports. Producer identity prevents
// a replaced window from publishing into, or clearing, its successor's facts.
func (h *HLS) observeConversion(ctx context.Context, id string, generation int) (func(vod.ConversionProgress), func()) {
	producer := strconv.FormatInt(time.Now().UnixNano(), 10)
	var last time.Time
	observe := func(p vod.ConversionProgress) {
		if p.ObservedAt.Sub(last) < time.Second || math.IsNaN(p.Speed) || math.IsInf(p.Speed, 0) || p.Speed < 0 {
			return
		}
		last = p.ObservedAt
		write, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		_ = dbwork.WithWriteTx(write, h.db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(write, `INSERT INTO playback_conversion_observations(session_id,generation,producer,speed,observed_ms)
    SELECT id,generation,?,?,? FROM playback_sessions WHERE id=? AND generation=? AND state NOT IN('stopped','ended','failed')
    ON CONFLICT(session_id,producer) DO UPDATE SET speed=excluded.speed,observed_ms=excluded.observed_ms`, producer, p.Speed, p.ObservedAt.UnixMilli(), id, generation)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				return nil
			}
			return apievents.Append(tx, apievents.AdminAudience, "admin.sessions", "session", id, "", nil)
		})
	}
	finish := func() {
		write, cancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
		_ = dbwork.WithWriteTx(write, h.db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
			result, err := tx.ExecContext(write, `DELETE FROM playback_conversion_observations WHERE session_id=? AND producer=?`, id, producer)
			if err != nil {
				return err
			}
			removed, err := result.RowsAffected()
			if err != nil || removed == 0 {
				return err
			}
			return apievents.Append(tx, apievents.AdminAudience, "admin.sessions", "session", id, "", nil)
		})
	}
	return observe, finish
}

// watchHLSMaintenance follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchHLSMaintenance(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"playback_sessions", "playback_v1_sessions", "playback_source_pins", "playback_artifacts",
		"playback_hls_reservations", "playback_hls_demand", "playback_hls_windows", "playback_delivery_plans",
		"playback_owner_policy", "catalog_assets", "catalog_asset_links", "inventory_objects",
		"library_sources", "server_settings", "console_settings_revision", "authorization_*",
		"accounts", "direct_profiles", "direct_memberships", "profile_*",
		"media_process_*",
	)
}
