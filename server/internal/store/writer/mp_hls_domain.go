package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"strings"
)

const (
	MPHLSReserve         CommandID = 730
	MPHLSPlan            CommandID = 731
	MPHLSWindowClaim     CommandID = 732
	MPHLSArtifactPublish CommandID = 733
	MPHLSObserveDemand   CommandID = 734
	MPHLSWindowCancel    CommandID = 735
	MPHLSWindowExit      CommandID = 736
	MPDirectDelivery     CommandID = 737
	MPHLSArtifactRetire  CommandID = 738
	MPHLSReconcile       CommandID = 739
)

type mpTimelineKind uint8

const (
	mpSourceKeyframes mpTimelineKind = 1
	mpConvertedGrid   mpTimelineKind = 2
)

type mpHLSArtifactKind uint8

const (
	mpMediaSegment mpHLSArtifactKind = 1
	mpPrivateInit  mpHLSArtifactKind = 2
)

type mpHLSEffect struct {
	asset                                                                                                                                                                                             string
	mode                                                                                                                                                                                              string
	physicalExit                                                                                                                                                                                      mpPhysicalExitFacts
	state                                                                                                                                                                                             string
	media, execution, windowExecution, planDigest, artifactDigest, artifactName, rendition, backend, status, exitDigest, readPin, sourceVersion                                                       string
	incarnation                                                                                                                                                                                       [16]byte
	revision, generation, windowRevision, windowGeneration, windowStart, windowEnd, highestProduced, highestServed, intervalOrdinal, startUS, endUS, decodeFromUS, size, reservedBytes, cleanupCursor int64
	artifactKind                                                                                                                                                                                      mpHLSArtifactKind
	physicalPending, windowPending, throttled, ready, released                                                                                                                                        bool
}

func mpHLSResult(p mpPhysicalState) mpHLSEffect {
	return mpHLSEffect{asset: p.asset, mode: p.mode, physicalExit: p.exit, state: p.state, reservedBytes: p.reservedBytes, media: p.media, execution: p.execution, incarnation: p.incarnation, revision: p.revision, generation: p.generation, highestProduced: -1, highestServed: -1, physicalPending: !p.cleaned}
}
func mpHLSName(s string) bool {
	if len(s) == 0 || len(s) > 128 || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
func applyMPHLSReserve(ctx context.Context, t *commandTurn, f mpPhysicalFence, expected, next int64) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPHLSReserveEffects(ctx, t, f, expected, next, mpRecipeStandalone)
}
func applyMPHLSReserveEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, expected, next int64, recipe mpMediaRecipeKind) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPHLSReserve, recipe); err != nil {
		return
	}
	if expected < 0 || next <= expected {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if p.mode != "hls" {
		return out, ErrConflict
	}
	// Parent's governed storage reservation covers the ACTUAL delta, not merely
	// this durable intent. No release/eviction happens on growth or cancellation.
	if expected == 0 {
		err = mpPrepOne(ctx, t, "mp.hls.reserve_insert", f.media, next)
	} else {
		err = mpPrepOne(ctx, t, "mp.hls.reserve_update", next, f.media, expected)
	}
	if err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.reservedBytes = next
	return
}

type mpHLSPlanInput struct {
	digest, timelineDigest, backend, strategy string
	timeline                                  mpTimelineKind
	renditions, segmentCount, durationUS      int64
}

func (q mpHLSPlanInput) valid() bool {
	if !lowerHex(q.digest, 64) || !lowerHex(q.timelineDigest, 64) || !mpPrepTexts(q.backend) || q.renditions < 1 || q.renditions > 32 || q.segmentCount < 1 || q.durationUS < 1 {
		return false
	}
	return (q.timeline == mpSourceKeyframes && q.strategy == "copy_remux") || (q.timeline == mpConvertedGrid && (q.strategy == "audio_conversion" || q.strategy == "video_conversion"))
}
func applyMPHLSPlan(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpHLSPlanInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPHLSPlanEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPHLSPlanEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpHLSPlanInput, recipe mpMediaRecipeKind) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPHLSPlan, recipe); err != nil {
		return
	}
	if !q.valid() {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if p.mode != "hls" {
		return out, ErrConflict
	}
	// Only a verified immutable timeline commitment enters SQL. Actual keyframe
	// observations/converted-grid construction and transforms occur outside W.
	if err = mpPrepOne(ctx, t, "mp.hls.plan_insert", f.media, q.digest, int64(q.timeline), q.timelineDigest, q.backend, q.strategy, q.renditions, q.segmentCount, q.durationUS, f.source.size, f.source.modifiedNS); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.planDigest = q.digest
	out.backend = q.backend
	out.status = "staging"
	return
}

type mpHLSRenditionInput struct {
	planDigest, id, codec, container, initName string
	ordinal, bitrate, width, height            int64
}

func (q mpHLSRenditionInput) valid() bool {
	return lowerHex(q.planDigest, 64) && mpHLSName(q.id) && mpPrepTexts(q.codec) && (q.container == "mpegts" || q.container == "fmp4") && q.ordinal >= 0 && q.ordinal < 32 && q.bitrate > 0 && q.width >= 0 && q.height >= 0 && ((q.container == "fmp4" && mpHLSName(q.initName)) || (q.container == "mpegts" && q.initName == ""))
}
func applyMPHLSRendition(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpHLSRenditionInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSPlan); err != nil {
		return
	}
	if !q.valid() {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if err = mpPrepOne(ctx, t, "mp.hls.rendition_insert", f.media, q.id, q.ordinal, q.codec, q.container, q.initName, q.bitrate, q.width, q.height, f.media, q.planDigest, q.ordinal); err != nil {
		return
	}
	// Seal ONLY when all declared contiguous ordinals are present, <=32 rows.
	if err = mpPrepOne(ctx, t, "mp.hls.plan_stage_advance", f.media, q.planDigest); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.planDigest = q.planDigest
	out.rendition = q.id
	return
}

type mpHLSWindowFence struct {
	physical                         mpPhysicalFence
	execution, planDigest            string
	start, end, revision, generation int64 // generation is WINDOW execution generation
}

func (f mpHLSWindowFence) valid() bool {
	return f.physical.valid() && mpPrepTexts(f.execution) && lowerHex(f.planDigest, 64) && f.start >= 0 && f.end >= f.start && f.end-f.start < 900 && mpPrepRevision(f.revision) && mpPrepRevision(f.generation)
}
func captureMPHLSWindow(ctx context.Context, t *commandTurn, f mpHLSWindowFence, fresh bool) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !f.valid() {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f.physical, fresh)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if _, err = mpHLSWindowNativeBytes(ctx, t, f); err != nil {
		return
	}
	r, err := mpPrepRow(ctx, t, "mp.hls.window_current", f.physical.media, f.start)
	if err != nil {
		return
	}
	var occurrenceGeneration, writesRetired, exited, cleaned int64
	var sourceRequest string
	err = r.Scan(&out.windowExecution, &out.windowGeneration, &out.windowRevision, &occurrenceGeneration, &out.windowEnd, &out.planDigest, &writesRetired, &exited, &cleaned, &out.exitDigest, &out.cleanupCursor, &out.status, &out.highestProduced, &out.backend, &sourceRequest)
	if err != nil {
		return
	}
	out.windowStart = f.start
	out.windowPending = cleaned == 0
	if writesRetired < 0 || writesRetired > 1 || exited < 0 || exited > 1 || cleaned < 0 || cleaned > exited {
		return out, ErrResolveRequired
	}
	if out.windowExecution != f.execution || out.windowGeneration != f.generation || out.windowRevision != f.revision || out.windowEnd != f.end || occurrenceGeneration != f.physical.generation || out.planDigest != f.planDigest || sourceRequest != f.physical.request {
		return out, ErrConflict
	}
	if fresh && (writesRetired != 0 || exited != 0 || (out.status != "running" && out.status != "throttled")) {
		return out, ErrConflict
	}
	return
}

type mpHLSWindowClaimInput struct {
	expectedExecution                                               string
	execution, planDigest                                           string
	start, end, expectedRevision, expectedGeneration, reservedBytes int64
}

func applyMPHLSWindowClaim(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpHLSWindowClaimInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSWindowClaim); err != nil {
		return
	}
	if !mpPrepTexts(q.execution) || !lowerHex(q.planDigest, 64) || q.start < 0 || q.end < q.start || q.end-q.start >= 900 || q.expectedRevision < 0 || q.expectedGeneration < 0 || q.expectedRevision == math.MaxInt64 || q.expectedGeneration == math.MaxInt64 || (q.expectedRevision == 0) != (q.expectedGeneration == 0) || (q.expectedRevision == 0 && q.expectedExecution != "") || (q.expectedRevision > 0 && (!mpPrepTexts(q.expectedExecution) || q.expectedExecution == q.execution)) || q.reservedBytes <= 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if p.mode != "hls" {
		return out, ErrConflict
	}
	planRow, e := mpPrepRow(ctx, t, "mp.hls.claim_plan", f.media, q.planDigest, q.end, q.reservedBytes)
	if e != nil {
		return out, e
	}
	var renditionCount int64
	if err = planRow.Scan(&renditionCount, &out.backend); err != nil {
		return
	}
	if renditionCount < 1 || renditionCount > 32 {
		return out, ErrResolveRequired
	}
	if q.expectedRevision == 0 {
		if err = mpPrepOne(ctx, t, "mp.hls.window_insert", f.media, q.start, q.start-1, f.nowMS, f.generation, q.execution, f.request, q.end, q.planDigest); err != nil {
			return
		}
	} else {
		// A same-key restart MUST consume the exact exited+cleaned old execution.
		// An expired lease, failed status or process cancellation cannot satisfy it.
		if err = mpPrepOne(ctx, t, "mp.hls.window_reclaim", f.nowMS, q.execution, f.generation, q.end, q.planDigest, f.request, f.media, q.start, q.expectedRevision, q.expectedGeneration, q.expectedExecution, f.generation, f.request); err != nil {
			return
		}
	}
	if err = mpHLSWriteCount(ctx, t, "mp.hls.window_progress_seed", renditionCount, f.media, q.start, q.expectedGeneration+1, q.start-1, f.media, q.planDigest); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.windowExecution = q.execution
	out.windowGeneration = q.expectedGeneration + 1
	out.windowRevision = q.expectedRevision + 1
	out.windowStart = q.start
	out.windowEnd = q.end
	out.planDigest = q.planDigest
	out.status = "running"
	out.windowPending = true
	out.reservedBytes = q.reservedBytes
	return
}

// Inert verified-work projection. Parent MUST validate external producer
// custody, immutable checkpoint digest/size, timeline and origin version before
// this method. No files/decoder/network/manifest parsing runs under W.
type mpHLSPublishedArtifact struct {
	execution, planDigest, rendition, name, digest              string
	windowGeneration, index, startUS, endUS, decodeFromUS, size int64
	kind                                                        mpHLSArtifactKind
}

func (q mpHLSPublishedArtifact) valid(f mpHLSWindowFence) bool {
	if q.execution != f.execution || q.windowGeneration != f.generation || q.planDigest != f.planDigest || !mpHLSName(q.rendition) || !mpHLSName(q.name) || !lowerHex(q.digest, 64) || q.size <= 0 {
		return false
	}
	switch q.kind {
	case mpMediaSegment:
		return q.index >= f.start && q.index <= f.end && q.startUS >= 0 && q.endUS > q.startUS && q.decodeFromUS >= 0 && q.decodeFromUS <= q.startUS
	case mpPrivateInit:
		return q.index == -1 && q.startUS == 0 && q.endUS == 0 && q.decodeFromUS == 0
	}
	return false
}
func applyMPHLSArtifactPublish(ctx context.Context, t *commandTurn, f mpHLSWindowFence, q mpHLSPublishedArtifact) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSArtifactPublish); err != nil {
		return
	}
	if !q.valid(f) {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, true); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.artifact_insert", f.physical.media, q.rendition, q.index, int64(q.kind), q.name, q.digest, q.size, q.startUS, q.endUS, q.decodeFromUS, f.start, f.generation, f.execution, f.physical.generation, f.planDigest, f.physical.media, q.rendition, int64(q.kind), q.name, int64(q.kind), f.start, f.generation, f.execution, f.revision, f.physical.request, f.physical.generation); err != nil {
		return
	}
	if q.kind == mpMediaSegment {
		if err = mpPrepOne(ctx, t, "mp.hls.progress_advance", q.index, f.physical.media, f.start, f.generation, q.rendition, q.index-1); err != nil {
			return
		}
	}
	// Contiguous per-rendition progress, not max(arbitrarily produced segment).
	// The exact declared set is <=32 and its count is checked before the barrier.
	row, e := mpPrepRow(ctx, t, "mp.hls.barrier", f.physical.media, f.start, f.generation, f.physical.media, f.start, f.generation, f.physical.media, f.start, f.physical.media, f.start, f.generation, f.physical.media, f.planDigest)
	if e != nil {
		return out, e
	}
	if err = row.Scan(&out.highestProduced, &out.status); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.window_publish_revision", f.physical.media, f.start, f.revision, f.generation, f.execution, f.physical.request); err != nil {
		return
	}
	readyRow, e := mpPrepRow(ctx, t, "mp.hls.delivery_ready", f.start, f.start, f.physical.media, f.physical.revision, f.physical.generation, f.physical.execution)
	if e != nil {
		return out, e
	}
	var ready int64
	if err = readyRow.Scan(&ready); err != nil {
		return
	}
	if ready < 0 || ready > 1 {
		return out, ErrResolveRequired
	}
	out.ready = ready == 1
	out.revision++
	out.windowRevision++
	out.artifactKind = q.kind
	out.artifactDigest = q.digest
	out.artifactName = q.name
	out.rendition = q.rendition
	out.intervalOrdinal = q.index
	out.startUS = q.startUS
	out.endUS = q.endUS
	out.decodeFromUS = q.decodeFromUS
	out.size = q.size
	return
}

type mpHLSDemandInput struct {
	highestServed int64
	throttled     bool
}

func applyMPHLSObserveDemand(ctx context.Context, t *commandTurn, f mpHLSWindowFence, q mpHLSDemandInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSObserveDemand); err != nil {
		return
	}
	if q.highestServed < -1 {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, true); err != nil {
		return
	}
	if q.highestServed > out.highestProduced {
		return out, ErrConflict
	}
	flag := int64(0)
	if q.throttled {
		flag = 1
	}
	demandRow, e := mpPrepRow(ctx, t, "mp.hls.demand", f.physical.media, q.highestServed, flag)
	if e != nil {
		return out, e
	}
	if err = demandRow.Scan(&out.highestServed, &flag); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.throttle", flag, f.physical.media, f.start, f.execution, f.generation, f.revision, f.physical.request); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.window_control_revision", f.physical.media, f.start, f.execution, f.generation, f.revision, f.physical.request); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.windowRevision++
	out.throttled = q.throttled
	if q.throttled {
		out.status = "throttled"
	} else {
		out.status = "running"
	}
	return
	// Serving cached bytes never synchronously waits for this optional telemetry.
}
func applyMPHLSWindowCancel(ctx context.Context, t *commandTurn, f mpHLSWindowFence) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSWindowCancel); err != nil {
		return
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.window_cancel", int64(1), f.physical.media, f.start, f.generation, f.execution, f.revision, f.physical.request); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.windowRevision++
	out.status = "failed"
	out.windowPending = true
	return
}

type mpHLSWindowExitInput struct {
	execution, digest              string
	generation, cursor, nextCursor int64
	finished                       bool
}

func applyMPHLSWindowExit(ctx context.Context, t *commandTurn, f mpHLSWindowFence, q mpHLSWindowExitInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSWindowExit); err != nil {
		return
	}
	if q.execution != f.execution || q.generation != f.generation || !lowerHex(q.digest, 64) || q.cursor < 0 || q.nextCursor < q.cursor || q.nextCursor-q.cursor > 64 {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if out.cleanupCursor != q.cursor {
		return out, ErrConflict
	}
	done := int64(0)
	if q.finished {
		done = 1
	}
	if err = mpPrepOne(ctx, t, "mp.hls.window_exit", q.digest, q.nextCursor, done, f.physical.media, f.start, f.generation, f.execution, f.revision, q.cursor, q.digest, f.physical.request, f.physical.generation); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.windowRevision++
	out.cleanupCursor = q.nextCursor
	out.exitDigest = q.digest
	out.windowPending = !q.finished
	return
	// No reservation/object/source-pin release merely because the producer exited.
}

type mpDirectDeliveryInput struct {
	preparedVersion, digest                   string
	sourceVersion, descriptorVersion, backend string
	size                                      int64
}

func applyMPDirectDelivery(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpDirectDeliveryInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return applyMPDirectDeliveryEffects(ctx, t, f, q, mpRecipeStandalone)
}
func applyMPDirectDeliveryEffects(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpDirectDeliveryInput, recipe mpMediaRecipeKind) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpMediaRecipePurpose(t, MPDirectDelivery, recipe); err != nil {
		return
	}
	if !mpPrepTexts(q.sourceVersion, q.backend) || !mpPrepText(q.descriptorVersion, true) || q.size <= 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if p.mode == "direct" {
		switch q.backend {
		case "native":
			if q.size != f.source.size || q.descriptorVersion != "" || q.preparedVersion != "" || q.digest != "" {
				return out, ErrConflict
			}
		case "prepared":
			if !mpPrepTexts(q.preparedVersion) || !lowerHex(q.digest, 64) || q.sourceVersion != q.digest || q.descriptorVersion != "" {
				return out, ErrInvalid
			}
			if err = mpPrepOne(ctx, t, "mp.hls.prepared_version", f.media, q.preparedVersion, q.digest, q.size); err != nil {
				return
			}
		default:
			return out, ErrInvalid
		}

	} else if p.mode == "remote" {
		if q.backend != "remote" || q.descriptorVersion == "" || q.preparedVersion != "" || q.digest != "" {
			return out, ErrInvalid
		}
		if err = mpPrepOne(ctx, t, "mp.hls.remote_version", f.media, q.sourceVersion, q.descriptorVersion, q.size); err != nil {
			return
		}
	} else {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.hls.direct_insert", f.media, q.sourceVersion, q.descriptorVersion, q.backend, q.size, f.generation, f.execution, q.preparedVersion, q.digest); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.direct_ready", f.media, f.revision, f.generation, f.execution); err != nil {
		return
	}
	out.revision++
	out.sourceVersion = q.sourceVersion
	out.artifactDigest = q.digest
	out.size = q.size
	out.backend = q.backend
	out.state = "ready"
	out.ready = true
	return
}

// Body/range read custody is one actual reader execution, never expiry-based
// cleanup. Parent verifies the read grant and exact descriptor/remote version.
type mpDeliveryReadInput struct {
	pin, execution, sourceVersion, digest string
	from, size                            int64
}

func applyMPDeliveryReadAcquire(ctx context.Context, t *commandTurn, f mpPhysicalFence, q mpDeliveryReadInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPDirectDelivery); err != nil {
		return
	}
	if !mpPrepTexts(q.pin, q.execution, q.sourceVersion) || (q.digest != "" && !lowerHex(q.digest, 64)) || q.from < 0 || q.size <= 0 || q.size > math.MaxInt64-q.from {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, true)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if err = mpPrepOne(ctx, t, "mp.hls.read_pin", q.pin, f.media, q.execution, f.generation, q.sourceVersion, q.digest, q.from, q.size, f.media, q.sourceVersion, q.from+q.size, f.generation, f.execution, q.digest); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.readPin = q.pin
	out.sourceVersion = q.sourceVersion
	out.size = q.size
	return
}
func applyMPDeliveryReadRelease(ctx context.Context, t *commandTurn, f mpPhysicalFence, pin, execution, exitDigest string) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPDirectDelivery); err != nil {
		return
	}
	if !mpPrepTexts(pin, execution) || !lowerHex(exitDigest, 64) {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	// The parent's original-body-lifetime bridge verifies this actual reader exit.
	if err = mpPrepOne(ctx, t, "mp.hls.read_unpin", pin, f.media, execution, f.generation); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.readPin = pin
	out.exitDigest = exitDigest
	out.released = true
	return
}

type mpHLSArtifactFence struct {
	rendition, name, digest string
	index, size             int64
	kind                    mpHLSArtifactKind
}

func (q mpHLSArtifactFence) valid() bool {
	return mpHLSName(q.rendition) && mpHLSName(q.name) && lowerHex(q.digest, 64) && q.size > 0 && ((q.kind == mpMediaSegment && q.index >= 0) || (q.kind == mpPrivateInit && q.index == -1))
}
func applyMPHLSArtifactRetire(ctx context.Context, t *commandTurn, f mpHLSWindowFence, q mpHLSArtifactFence) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSArtifactRetire); err != nil {
		return
	}
	if !q.valid() {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.artifact_retire", f.physical.media, q.rendition, q.index, int64(q.kind), q.name, q.digest, q.size, f.start, f.generation, f.execution); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.artifactKind = q.kind
	out.artifactDigest = q.digest
	out.artifactName = q.name
	out.rendition = q.rendition
	out.size = q.size
	out.status = "deleting"
	return
}

// External immutable-object deletion observation DATA, after joined readers and
// artifact gate/global object-reference validation; cancellation is not deletion.
func applyMPHLSArtifactCleanup(ctx context.Context, t *commandTurn, f mpHLSWindowFence, q mpHLSArtifactFence, deletionDigest string) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSReconcile); err != nil {
		return
	}
	if !q.valid() || !lowerHex(deletionDigest, 64) {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.artifact_delete", f.physical.media, q.rendition, q.index, int64(q.kind), q.digest, q.size, f.start, f.generation, f.execution); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.artifactDigest = q.digest
	out.artifactKind = q.kind
	out.artifactName = q.name
	out.rendition = q.rendition
	out.size = q.size
	out.exitDigest = deletionDigest
	out.released = true
	return
}
func applyMPHLSReservationRelease(ctx context.Context, t *commandTurn, f mpPhysicalFence, bytes int64) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSReconcile); err != nil {
		return
	}
	if bytes <= 0 {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if !p.exited || p.stopMS == 0 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.hls.reserve_release", f.media, bytes); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.reservedBytes = 0
	out.released = true
	return
}

func mpHLSWriteCount(ctx context.Context, t *commandTurn, id stmt.ID, want int64, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil || want < 1 || want > 32 {
		return ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	r, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return ErrResolveRequired
	}
	return nil
}
func applyMPHLSArtifactReadAcquire(ctx context.Context, t *commandTurn, f mpHLSWindowFence, a mpHLSArtifactFence, q mpDeliveryReadInput) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPDirectDelivery); err != nil {
		return
	}
	if !a.valid() || !mpPrepTexts(q.pin, q.execution) || q.digest != a.digest || q.sourceVersion != a.digest || q.from < 0 || q.size <= 0 || q.size > math.MaxInt64-q.from || q.from+q.size > a.size {
		return out, ErrInvalid
	}
	if _, err = captureMPPhysical(ctx, t, f.physical, true); err != nil {
		return
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.artifact_read_pin", q.pin, f.physical.media, q.execution, f.physical.generation, q.sourceVersion, q.digest, q.from, q.size, f.physical.media, a.rendition, a.index, int64(a.kind), a.digest, q.from+q.size, f.physical.generation); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.readPin = q.pin
	out.artifactDigest = a.digest
	out.artifactName = a.name
	out.artifactKind = a.kind
	out.size = q.size
	return
}
func applyMPHLSWindowMetadataCleanup(ctx context.Context, t *commandTurn, f mpHLSWindowFence, rendition string) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSReconcile); err != nil {
		return
	}
	if !mpHLSName(rendition) {
		return out, ErrInvalid
	}
	if out, err = captureMPHLSWindow(ctx, t, f, false); err != nil {
		return
	}
	if err = mpPrepOne(ctx, t, "mp.hls.window_progress_delete", f.physical.media, f.start, f.generation, rendition, f.execution); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f.physical); err != nil {
		return
	}
	out.revision++
	out.rendition = rendition
	out.released = true
	return
}

func applyMPDirectDeliveryRetire(ctx context.Context, t *commandTurn, f mpPhysicalFence, sourceVersion string) (out mpHLSEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPHLSReconcile); err != nil {
		return
	}
	if !mpPrepTexts(sourceVersion) {
		return out, ErrInvalid
	}
	p, err := captureMPPhysical(ctx, t, f, false)
	if err != nil {
		return out, err
	}
	out = mpHLSResult(p)
	if !p.exited || p.stopMS == 0 {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.hls.direct_delete", f.media, sourceVersion, f.generation, f.execution); err != nil {
		return
	}
	if err = bumpMPPin(ctx, t, f); err != nil {
		return
	}
	out.revision++
	out.sourceVersion = sourceVersion
	out.released = true
	return
}

// Exact native text octets before copying the selected row. Parent admission
// must cover these actual bytes; this check grants no independent memory credit.
func mpHLSWindowNativeBytes(ctx context.Context, t *commandTurn, f mpHLSWindowFence) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	if !f.valid() {
		return 0, ErrInvalid
	}
	row, err := mpPrepRow(ctx, t, "mp.hls.window_octets", f.physical.media, f.start, f.execution, f.generation, f.revision, f.physical.generation, f.physical.request)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes < 0 || bytes > 65536 {
		return 0, ErrInvalid
	}
	return
}
