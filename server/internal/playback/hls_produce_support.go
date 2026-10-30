package playback

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"runtime"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
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
	var process *storage.ProcessError
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
	case strings.Contains(d, "decoder not found"), strings.Contains(d, "unknown decoder"), strings.Contains(d, "decoding for stream") && strings.Contains(d, "failed"), strings.Contains(d, "could not find codec parameters"):
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
	h.rememberConversionFailure(ctx, id, code, diagnostic)
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_artifacts SET failure_code=?,diagnostic=? WHERE session_id=?`, code, diagnostic, id)
}

// A clean retry or new published output followed by throttling establishes
// that software can convert this window. Earlier output is not retry evidence.
func hardwareFailureConfirmed(softwareErr error, cancelled, throttled bool, from, before, produced int) bool {
	return !cancelled && (softwareErr == nil || throttled && produced >= from && produced > before)
}

// recordSoftwareFallback rewrites the stored plan after a hardware encoder failed
// on a real source, so every later window of this session starts in software and
// the technical details say what happened.
func (h *HLS) recordSoftwareFallback(ctx context.Context, id, backend, diagnostic string) {
	write, cancel := persist(ctx)
	defer cancel()
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_delivery_plans SET hardware_backend='software',hardware_native=0,hardware_device='',reason_codes=CASE WHEN instr(reason_codes,?)>0 THEN reason_codes ELSE reason_codes||','||? END WHERE session_id=?`, ReasonHardwareFailedSoftware, ReasonHardwareFailedSoftware, id)
	_, _ = h.producerWrite(write, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_artifacts SET failure_code=?,diagnostic=? WHERE session_id=?`, "hardware_encoder_failed:"+backend, diagnostic, id)
	h.rememberConversionFailure(ctx, id, "hardware_encoder_failed:"+backend, diagnostic)
	h.hardwareFailures.Add(1)
}

// fallbackCeilings is what the member of a presentation is allowed right now,
// for the conversion fallback, which re-plans with no request to say. It
// applies to a presentation admission has called remote: a v1 presentation
// unless its stored reach is local, a legacy session once a request has shown
// it remote. The member's cap is read as it is now, so one lowered since the
// presentation was admitted takes effect; a cap that cannot be read is an
// error, never no cap. Any other presentation has none to add.
func (h *HLS) fallbackCeilings(ctx context.Context, tx *sql.Tx, plan *DeliveryPlan, account string) (*ReplanCeilings, error) {
	if plan.TargetReach != reachRemote && (plan.Target == nil || plan.TargetReach == reachLocal) {
		return nil, nil
	}
	ceiling, err := memberAwayCeilingsTx(ctx, tx, account, true)
	if err != nil {
		return nil, err
	}
	current := &ceiling
	if h.memberCeiling == nil {
		return current, nil
	}
	limit, err := h.memberCeiling(ctx, tx, account)
	if err != nil {
		return nil, err
	}
	current.MaxVideoBitrateBPS = narrowCeiling(current.MaxVideoBitrateBPS, limit)
	return current, nil
}

// downgradeCopyPlan turns a plan that copies the picture into one that converts
// it, for a source whose keyframes could not be indexed. It is refused when the
// owner has turned conversion off: a wrong timeline is not something to fall
// back to.
func (h *HLS) downgradeCopyPlan(ctx context.Context, id string, plan *DeliveryPlan, replacement ...bool) (*DeliveryPlan, error) {
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	gate, err := dbwork.Begin(ctx, h.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return nil, err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	ctx = gate.Context()
	if owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner); owner != nil {
		var live bool
		run := owner.run
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed'))`, id, run.generation, run.requestID, run.itemID, run.assetID).Scan(&live)
		if err != nil {
			return nil, err
		}
		if owner.id != id || !live {
			return nil, ErrOccurrenceChanged
		}
	}

	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled); err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrTranscodingDisabled
	}
	source, err := h.loadSessionDeliverySourceTx(ctx, tx, id, plan.SourceID)
	if err != nil {
		return nil, err
	}
	var p identity.Principal
	if err = tx.QueryRowContext(ctx, `SELECT session_hash,account_id,profile_id FROM playback_sessions WHERE id=? AND state NOT IN('stopped','ended','failed')`, id).Scan(&p.Hash, &p.AccountID, &p.ProfileID); err != nil {
		return nil, err
	}
	cfg := h.configuration()
	policy := ResolveDeliveryPolicy(nil, NetworkUnknown, LocalityUnknown, "unknown", cfg)
	if plan.Policy != nil {
		policy = *plan.Policy
	}
	current, err := h.fallbackCeilings(ctx, tx, plan, p.AccountID)
	if err != nil {
		return nil, err
	}
	target, replanned, err := replanTarget(plan, cfg, current, false)
	if err != nil {
		return nil, err
	}
	failures, err := routeFailuresTx(ctx, tx, p, plan.SourceID)
	if err != nil {
		return nil, err
	}
	// The presentation is already playing in one engine; its re-plan stays there.
	client := capabilityDocumentFor(ctx, tx, p).Profile(plan.Engine)
	excluded := map[DeliveryStrategy]RouteRejection{}
	for route, rejection := range failures[client.Engine] {
		excluded[route] = rejection
	}
	for _, route := range []DeliveryStrategy{DeliveryOriginal, DeliveryCopyRemux, DeliveryAudioConversion} {
		excluded[route] = RouteRejection{Code: ReasonCopyTimelineUnavailable}
	}
	discInput, err := hasDiscSessionTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	var next DeliveryPlan
	richReplacement := len(replacement) > 0 && replacement[0] && plan.VideoAction == "convert" && plan.ConversionTarget != nil
	if richReplacement {
		// A different encoder has different initialization bytes. Retire the old
		// generation while retaining its codec, range, layout and source custody.
		next = *plan
		t := *plan.ConversionTarget
		if target.MaxVideoHeight > 0 && (t.Height == 0 || t.Height > target.MaxVideoHeight) {
			t.Height = target.MaxVideoHeight
		}
		if target.MaxVideoBitrateBPS > 0 && (t.VideoBitrateBPS == 0 || t.VideoBitrateBPS > target.MaxVideoBitrateBPS) {
			t.VideoBitrateBPS = target.MaxVideoBitrateBPS
			t.RateControl = "bitrate"
		}
		if target.MaxAudioBitrateBPS > 0 {
			if t.AudioCodec == "flac" {
				return nil, ErrQualityUnavailable
			}
			if t.AudioBitrateBPS == 0 || t.AudioBitrateBPS > target.MaxAudioBitrateBPS {
				t.AudioBitrateBPS = target.MaxAudioBitrateBPS
			}
		}
		t, err = t.Normalized()
		if err != nil {
			return nil, err
		}
		next.ConversionTarget = &t
		next.HardwareBackend, next.HardwareNative, next.HardwareDevice = "software", false, ""
		next.OutputContainer, next.OutputVideoCodec, next.OutputAudioCodec = t.Packaging, t.VideoCodec, t.AudioCodec
		next.TargetWidth, next.TargetHeight, next.VideoBitrateBPS, next.AudioBitrateBPS = t.Width, t.Height, t.VideoBitrateBPS, t.AudioBitrateBPS
		next.ReasonCodes = append(append([]string{}, plan.ReasonCodes...), ReasonHardwareFailedSoftware)
	} else {
		next, err = planDelivery(DeliveryInput{Disc: discInput, SourceID: source.ID, Source: &source, Client: client, Server: decoder.CurrentToolchain(), Policy: policy, Target: target, Config: cfg, TranscodingEnabled: true, AudioStream: plan.AudioStream, HasAudioChoice: plan.AudioStream >= 0, Excluded: excluded, FMP4Output: h.fragmentedOutput(), BurnIn: plan.BurnIn != nil, Remote: source.Remote})
		if err != nil {
			return nil, err
		}
		if next.VideoAction != "convert" {
			return nil, ErrIncompatible
		}
	}
	next.SourceSize, next.SourceModifiedNS, next.FactsRevision = plan.SourceSize, plan.SourceModifiedNS, plan.FactsRevision
	next.BurnIn = plan.BurnIn
	next.QualityID = plan.QualityID
	next.Target, next.TargetReach = replanned.target, replanned.reach
	if !richReplacement {
		next.ReasonCodes = append(next.ReasonCodes, ReasonCopyTimelineUnavailable)
	}
	if plan.Trace != nil && plan.Trace.Audio != nil && next.Trace != nil && next.Trace.Audio != nil {
		next.Trace.Audio.ChosenBy = plan.Trace.Audio.ChosenBy
	}
	if err = reserveHLS(tx, id, id, &next, cfg); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM playback_delivery_plans WHERE session_id=?`, id); err != nil {
		return nil, err
	}
	if err = persistDeliveryPlan(tx, id, &next); err != nil {
		return nil, err
	}
	if len(replacement) > 0 && replacement[0] {
		if s := h.discPlayback; s != nil && s.GrantReplacingTx != nil {
			if err = s.GrantReplacingTx(ctx, tx, id); err != nil {
				return nil, err
			}
		}
		grant := identity.Token()
		if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=generation+1,grant_token=?,grant_hash=? WHERE id=?`, grant, identity.Digest(grant), id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_subtitle_state SET generation=generation+1 WHERE session_id=?`, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE playback_subtitle_presentations SET generation=generation+1 WHERE session_id=?`, id); err != nil {
			return nil, err
		}
		// The session playing this media learns its new generation and URL in
		// the same transaction (V10): never a rotated grant behind an old URL.
		if s := h.discPlayback; s != nil && s.GrantReplacedTx != nil {
			var generation int
			var mode string
			if err = tx.QueryRowContext(ctx, `SELECT generation,mode FROM playback_sessions WHERE id=?`, id).Scan(&generation, &mode); err != nil {
				return nil, err
			}
			url, _ := streamURLTx(tx, id, generation, grant, mode)
			if err = s.GrantReplacedTx(ctx, tx, id, generation, url); err != nil {
				return nil, err
			}
		}
	}
	if next.ConversionTarget != nil {
		if err = StoreConversionTarget(ctx, tx, id, *next.ConversionTarget); err != nil {
			return nil, err
		}
	}
	if err = gate.Commit(); err != nil {
		return nil, err
	}
	return &next, nil
}

// copyTimelineFor returns the cut list for a session's source: from memory, from
// the per-file cache for local files, a session copy for windows, or a probe.
func (h *HLS) copyTimelineFor(ctx context.Context, id, asset string, input *assets.PlaybackInput, window subtitles.WindowInput, duration float64) (*copyTimeline, error) {
	h.mu.Lock()
	if t := h.timelines[id]; t != nil {
		h.mu.Unlock()
		return t, nil
	}
	h.mu.Unlock()
	if window != nil {
		evidence, err := h.windowTimelineEvidence(ctx, id)
		if err != nil {
			return nil, err
		}
		if t := h.loadWindowTimeline(id, evidence); t != nil {
			return t, nil
		}
		probe := func(ctx context.Context, args ...string) ([]byte, error) { return window.Probe(ctx, args) }
		t, err := probeCopyTimeline(ctx, probe, nil, duration)
		if err != nil {
			return nil, err
		}
		if err := h.storeWindowTimeline(id, evidence, t); err != nil {
			return nil, err
		}
		return t, nil
	}
	var size, modified int64
	if err := dbwork.QueryRow(ctx, h.db, `SELECT size,modified_ns FROM playback_source_pins WHERE session_id=? AND asset_id=?`, id, asset).Scan(&size, &modified); err != nil {
		return nil, err
	}
	if input.InputAt == nil {
		if t, err := loadCopyTimeline(ctx, h.db, asset, size, modified); err == nil && t != nil {
			return t, nil
		}
	}
	if h.ffprobe == "" {
		return nil, errCopyTimeline
	}
	t, err := probeCopyTimeline(ctx, localTimelineProbe(h.ffprobe, input), localPacketTimelineProbe(h.ffprobe, input), duration)
	if err != nil {
		return nil, err
	}
	if input.InputAt != nil {
		h.storeDiscTimeline(ctx, id, t)
	} else {
		storeCopyTimeline(ctx, h.db, asset, size, modified, t)
	}
	return t, nil
}

// sessionTimeline is the cut list a request for this session's segments is
// checked against. A session not in memory (the server restarted under it) reads
// its session copy for a remote window or the per-file cache for a local file;
// a session that is not a copy session has none.
func (h *HLS) sessionTimeline(ctx context.Context, id string) *copyTimeline {
	h.mu.Lock()
	t := h.timelines[id]
	h.mu.Unlock()
	if t != nil {
		return t
	}
	var container, action string
	if err := dbwork.QueryRow(ctx, h.db, `SELECT source_container,video_action FROM playback_delivery_plans WHERE session_id=?`, id).Scan(&container, &action); err == nil && container == "strm" {
		if action != "copy" {
			return nil
		}
		evidence, err := h.windowTimelineEvidence(ctx, id)
		if err != nil {
			return nil
		}
		return h.loadWindowTimeline(id, evidence)
	}
	if t, disc := h.discTimeline(ctx, id); disc {
		return t
	}
	var asset string
	var size, modified int64
	err := dbwork.QueryRow(ctx, h.db, `SELECT d.video_action,pin.asset_id,pin.size,pin.modified_ns FROM playback_delivery_plans d JOIN playback_source_pins pin ON pin.session_id=d.session_id WHERE d.session_id=?`, id).Scan(&action, &asset, &size, &modified)
	if err != nil || action != "copy" {
		return nil
	}
	t, err = loadCopyTimeline(ctx, h.db, asset, size, modified)
	if err != nil || t == nil {
		return nil
	}
	h.mu.Lock()
	h.timelines[id] = t
	h.mu.Unlock()
	return t
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

// conversionFailure is what the diagnostics reader publishes about a failed or
// degraded conversion.
type conversionFailure struct {
	Code       string `json:"code"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

func readConversionFailure(ctx context.Context, tx *sql.Tx, session string) (*conversionFailure, error) {
	var out conversionFailure
	err := tx.QueryRowContext(ctx, `SELECT failure_code,diagnostic FROM playback_artifacts WHERE session_id=?`, session).Scan(&out.Code, &out.Diagnostic)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && out.Code == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
