package streaming

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// Conversion failure codes. The viewer sees the class; the owner also sees the
// converter's own words (with paths removed) on the diagnostics page.
const (
	FailureConverter        = "converter_failed"
	FailureDecoderMissing   = "source_codec_not_decodable"
	FailureEncoderMissing   = "encoder_unavailable"
	FailureFilterMissing    = "filter_unavailable"
	FailureSourceUnreadable = "source_unreadable"
	FailureOutOfSpace       = "conversion_storage_full"
	FailureCopyTimeline     = "copy_timeline_unavailable"

	// ReasonHardwareFailedSoftware is added to a plan whose hardware encoder
	// failed on the real source and was replaced by software mid-session.
	ReasonHardwareFailedSoftware = "hardware_encode_failed_software_retry"
	// ReasonCopyTimelineUnavailable is added to a plan that wanted to copy the
	// picture but whose source keyframes could not be indexed.
	ReasonCopyTimelineUnavailable = "copy_timeline_unavailable"
)

// tailWriter keeps the end of a stream, because a converter's last words are the
// ones that say why it stopped.
type tailWriter struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	if len(w.data) > w.limit {
		w.data = append([]byte{}, w.data[len(w.data)-w.limit:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}

// sanitizeConverterOutput removes what a diagnostic must never carry: the source
// path (on Windows the converter is given the file's own path) and the server's
// generated-media root. Control characters are dropped and the text is bounded.
func sanitizeConverterOutput(text string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) < 4 {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[path]")
		text = strings.ReplaceAll(text, filepath.ToSlash(secret), "[path]")
	}
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if len(text) > 2000 {
		text = text[len(text)-2000:]
	}
	return text
}

// Local and bridge jobs use the same conservative encoder-backend grants.
func conversionJobHardware(plan *DeliveryPlan, stages ...[]decoder.HardwareStage) bool {
	for _, graph := range stages {
		for _, stage := range graph {
			if stage.Execution == decoder.ExecutionHardware {
				return true
			}
		}
	}
	return plan != nil && plan.HardwareBackend != "" && plan.HardwareBackend != string(decoder.BackendSoftware)
}

func windowConverterDiagnostic(err error, secrets ...string) string {
	var process *processpool.ProcessError
	if errors.As(err, &process) {
		return sanitizeConverterOutput(process.Stderr, secrets...)
	}
	return ""
}

// classifyConverterFailure reads the converter's diagnostics for the causes an
// owner can act on. Anything else is the general class.
func classifyConverterFailure(diagnostic string) string {
	d := strings.ToLower(diagnostic)
	switch {
	case strings.Contains(d, "no space left"), strings.Contains(d, "disk full"), strings.Contains(d, "not enough space"):
		return FailureOutOfSpace
	case strings.Contains(d, "unknown encoder"), strings.Contains(d, "encoder not found"):
		return FailureEncoderMissing
	case strings.Contains(d, "no such filter"), strings.Contains(d, "filter not found"):
		return FailureFilterMissing
	case strings.Contains(d, "decoder not found"),
		strings.Contains(d, "unknown decoder"),
		strings.Contains(d, "decoding for stream") && strings.Contains(d, "failed"),
		strings.Contains(d, "could not find codec parameters"):
		return FailureDecoderMissing
	case strings.Contains(d, "invalid data found when processing input"), strings.Contains(d, "no such file"), strings.Contains(d, "input/output error"), strings.Contains(d, "permission denied"):
		return FailureSourceUnreadable
	}
	return FailureConverter
}

// hardwareEncodeStage reports whether this plan's encoder runs on the device. It
// reads the plan's own stages, so a hardware decoder with a software encoder does
// not reserve an encode session.
func hardwareEncodeStage(stages []decoder.HardwareStage) bool {
	for _, stage := range stages {
		if stage.Operation == decoder.StageEncode && stage.Execution == decoder.ExecutionHardware {
			return true
		}
	}
	return false
}

// errHardwarePermitUnavailable says this adapter has no session slot left right
// now. It is capacity, not a failing encoder: the window runs in software without
// recording a device failure, tripping the adapter's circuit or rewriting the
// stored plan, and no other adapter is tried in this version.
var errHardwarePermitUnavailable = errors.New("hardware_permit_unavailable")

// failedWith fails a session and records why, for the people who can read it.
func (h *HLS) failedWith(ctx context.Context, id, code, diagnostic string) {
	if code == FailureOutOfSpace && h.retryMemoryOutput(id) {
		return
	}
	if !h.failed(ctx, id) {
		return
	}
	h.rememberConversionFailure(id, code, diagnostic)
}

// A clean retry or new published output followed by throttling establishes
// that software can convert this window. Earlier output is not retry evidence.
func hardwareFailureConfirmed(softwareErr error, cancelled, throttled bool, from, before, produced int) bool {
	return !cancelled && (softwareErr == nil || throttled && produced >= from && produced > before)
}

// recordSoftwareFallback changes the stream's plan after a hardware encoder
// failed on a real source, so every later window of this stream starts in
// software and the technical details say what happened.
func (h *HLS) recordSoftwareFallback(ctx context.Context, id, backend, diagnostic string) {
	h.mu.Lock()
	if s := h.streams[id]; s != nil && s.plan != nil {
		next := *s.plan
		next.HardwareBackend, next.HardwareNative, next.HardwareDevice = string(decoder.BackendSoftware), false, ""
		if !containsReason(next.ReasonCodes, ReasonHardwareFailedSoftware) {
			next.ReasonCodes = append(append([]string{}, next.ReasonCodes...), ReasonHardwareFailedSoftware)
		}
		s.plan = &next
	}
	h.mu.Unlock()
	h.rememberConversionFailure(id, "hardware_encoder_failed:"+backend, diagnostic)
	h.hardwareFailures.Add(1)
}

func containsReason(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// rememberConversionFailure keeps why a conversion failed or fell back: on the
// stream, for whoever asks about this playback, and in a short list of recent
// failures for the owner. Neither outlives the process; the log has the rest.
func (h *HLS) rememberConversionFailure(id, code, diagnostic string) {
	diagnostic = sanitizeConverterOutput(diagnostic, h.root, h.memoryRoot)
	failure := ConversionFailure{Code: code, Diagnostic: diagnostic, At: time.Now()}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.streams[id]; s != nil {
		if s.plan != nil {
			failure.Strategy, failure.Backend = string(s.plan.Strategy), s.plan.HardwareBackend
			failure.Video, failure.Audio = s.plan.SourceVideoCodec, s.plan.SourceAudioCodec
		}
		s.failure = &failure
	}
	h.failures = append(h.failures, failure)
	if len(h.failures) > recentFailures {
		h.failures = append([]ConversionFailure{}, h.failures[len(h.failures)-recentFailures:]...)
	}
}

// recentFailures is how many conversion failures the owner can look back over.
const recentFailures = 200

// downgradeCopyPlan turns a plan that copies the picture into one that converts
// it, for a source whose keyframes could not be indexed. It plans again from
// the request the stream started with, with the copying routes ruled out. It
// is refused when the owner has turned conversion off: a wrong timeline is not
// something to fall back to.
func (h *HLS) downgradeCopyPlan(ctx context.Context, id string, plan *DeliveryPlan) (*DeliveryPlan, error) {
	h.mu.Lock()
	var request *PlanRequest
	if s := h.streams[id]; s != nil && s.state == streamLive {
		request = s.request
	}
	h.mu.Unlock()
	if request == nil {
		return nil, ErrIncompatible
	}
	if !request.TranscodingEnabled {
		return nil, ErrTranscodingDisabled
	}
	input := request.deliveryInput()
	// The stream is already playing in one engine; its new plan stays there.
	input.Engine = plan.Engine
	input.AudioStream, input.HasAudioChoice = plan.AudioStream, plan.AudioStream >= 0
	input.BurnIn = plan.BurnIn != nil
	input.Config = h.configuration()
	input.TranscodingEnabled = true
	input.FMP4Output = h.fragmentedOutput()
	excluded := map[string]map[DeliveryStrategy]RouteRejection{}
	for _, engine := range []string{plan.Engine, input.Client.Engine, EngineNative, EngineMPV} {
		routes := map[DeliveryStrategy]RouteRejection{}
		for route, rejection := range input.ExcludedByEngine[engine] {
			routes[route] = rejection
		}
		for route, rejection := range input.Excluded {
			routes[route] = rejection
		}
		for _, route := range []DeliveryStrategy{DeliveryOriginal, DeliveryCopyRemux, DeliveryAudioConversion} {
			routes[route] = RouteRejection{Code: ReasonCopyTimelineUnavailable}
		}
		excluded[engine] = routes
	}
	input.ExcludedByEngine, input.Excluded = excluded, excluded[plan.Engine]
	next, err := planDelivery(input)
	if err != nil {
		return nil, err
	}
	if next.VideoAction != "convert" {
		return nil, ErrIncompatible
	}
	next.AirPlay = plan.AirPlay
	if plan.AirPlay {
		next.Renditions = nil
	}
	next.SourceSize, next.SourceModifiedNS, next.FactsRevision = plan.SourceSize, plan.SourceModifiedNS, plan.FactsRevision
	next.BurnIn = plan.BurnIn
	next.QualityID = plan.QualityID
	next.Target, next.TargetReach = plan.Target, plan.TargetReach
	next.ReasonCodes = append(next.ReasonCodes, ReasonCopyTimelineUnavailable)
	if plan.Trace != nil && plan.Trace.Audio != nil && next.Trace != nil && next.Trace.Audio != nil {
		next.Trace.Audio.ChosenBy = plan.Trace.Audio.ChosenBy
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil || s.state != streamLive {
		return nil, ErrGrantEnded
	}
	stored := next
	s.plan = &stored
	return &next, nil
}

// softwarePlan is the same conversion run by a software encoder, for a stream
// whose hardware encoder changed its initialization mid-stream. A different
// encoder has different initialization bytes, so the stream that plays it is a
// new one; the plan keeps its codec, range and layout.
func softwarePlan(plan *DeliveryPlan) (*DeliveryPlan, error) {
	if plan.VideoAction != "convert" || plan.ConversionTarget == nil {
		return nil, ErrIncompatible
	}
	next := *plan
	t, err := plan.ConversionTarget.Normalized()
	if err != nil {
		return nil, err
	}
	next.ConversionTarget = &t
	next.HardwareBackend, next.HardwareNative, next.HardwareDevice = "software", false, ""
	next.OutputContainer, next.OutputVideoCodec, next.OutputAudioCodec = t.Packaging, t.VideoCodec, t.AudioCodec
	next.TargetWidth, next.TargetHeight, next.VideoBitrateBPS, next.AudioBitrateBPS = t.Width, t.Height, t.VideoBitrateBPS, t.AudioBitrateBPS
	next.ReasonCodes = append(append([]string{}, plan.ReasonCodes...), ReasonHardwareFailedSoftware)
	next.TextRenditions = nil
	return &next, nil
}

// copyTimelineFor returns the cut list for a stream's source: from the stream,
// from this process's per-file cache for local files, or from a probe.
func (h *HLS) copyTimelineFor(
	ctx context.Context, id string, input *PlaybackInput, window subtitles.WindowInput, duration float64,
) (*copyTimeline, error) {
	h.mu.Lock()
	if t := h.timelines[id]; t != nil {
		h.mu.Unlock()
		return t, nil
	}
	key := timelineKey{}
	if s := h.streams[id]; s != nil && s.plan != nil && window == nil && input.InputAt == nil {
		key = timelineKey{s.plan.SourceID, s.plan.SourceSize, s.plan.SourceModifiedNS}
	}
	cached := h.fileTimelines[key]
	h.mu.Unlock()
	if window != nil {
		probe := func(ctx context.Context, args ...string) ([]byte, error) { return window.Probe(ctx, args) }
		return probeCopyTimeline(ctx, probe, nil, duration)
	}
	if key.source != "" && cached != nil {
		return cached, nil
	}
	if h.ffprobe == "" {
		return nil, errCopyTimeline
	}
	t, err := probeCopyTimeline(ctx, localTimelineProbe(h.ffprobe, input), localPacketTimelineProbe(h.ffprobe, input), duration)
	if err != nil {
		return nil, err
	}
	if key.source != "" {
		h.mu.Lock()
		if h.fileTimelines == nil || len(h.fileTimelines) >= fileTimelineLimit {
			h.fileTimelines = map[timelineKey]*copyTimeline{}
		}
		h.fileTimelines[key] = t
		h.mu.Unlock()
	}
	return t, nil
}

// timelineKey names one version of one file. A cut list is a property of the
// file's bytes, so a second stream of the same file does not index it again.
type timelineKey struct {
	source         string
	size, modified int64
}

// fileTimelineLimit bounds the per-file cut lists kept in memory. They are
// rebuilt by one ffprobe pass, so the cache is simply emptied when it fills.
const fileTimelineLimit = 256

// sessionTimeline is the cut list a request for this stream's segments is
// checked against; a stream that is not copying its picture has none.
func (h *HLS) sessionTimeline(ctx context.Context, id string) *copyTimeline {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.timelines[id]
}

// siblingProbe finds ffprobe next to the ffmpeg this producer runs, which is
// where every packaged build puts it, and falls back to the search path.
func siblingProbe(ffmpeg string) string {
	name := "ffprobe"
	if runtime.GOOS == "windows" {
		name = "ffprobe.exe"
	}
	if ffmpeg != "" {
		candidate := filepath.Join(filepath.Dir(ffmpeg), name)
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return ""
}

// ConfigureProbe names the ffprobe used to index copied sources.
func (h *HLS) ConfigureProbe(path string) {
	if resolved, err := exec.LookPath(path); err == nil {
		h.ffprobe = resolved
	}
}
