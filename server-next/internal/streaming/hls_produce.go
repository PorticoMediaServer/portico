package streaming

import (
	"context"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming/vod"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

func (h *HLS) produce(ctx context.Context, id string, from int, owned *hlsWindow) {
	defer func() {
		if h.windowTeardown != nil {
			h.windowTeardown(id, owned)
		}
		h.mu.Lock()
		if h.windows[id] == owned {
			delete(h.active, id)
			delete(h.windows, id)
		}
		close(owned.done)
		h.signalLocked(h.streams[id])
		h.mu.Unlock()
	}()
	supervise.Chaos("playback.hls.produce")
	live, e := h.liveStream(id)
	if e != nil || live.mode != "hls" {
		return
	}
	duration := live.duration
	ctx = context.WithValue(ctx, producerWindowKey{}, owned)
	delivery := h.planCopy(id)
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
	h.mu.Lock()
	if s := h.streams[id]; s != nil && h.windows[id] == owned && s.state == streamLive {
		s.output = "building"
	}
	h.mu.Unlock()
	input, remoteInput, e := h.openWindowInput(ctx, id, delivery)
	if e != nil {
		if ctx.Err() == nil {
			h.failedWith(ctx, id, FailureSourceUnreadable, "")
		}
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
		timeline, e = h.copyTimelineFor(ctx, id, input, remoteInput, duration)
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
	if e = writeNamedManifest(dir, "generation", []byte(outputStamp)); e != nil {
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
		case plan != nil && plan.VideoAction == "none":
			args = append(args, "-map", strings.TrimSuffix(audioMap, "?"), "-vn", "-sn", "-dn")
		case plan != nil && (plan.AudioAction == "none" || len(plan.Renditions) > 0):
			args = append(args, "-map", videoMap, "-an", "-sn", "-dn")
		default:
			args = append(args, "-map", videoMap, "-map", audioMap, "-sn", "-dn")
		}
		if plan != nil {
			args = append(args, codecArgs...)
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
			args = append(
				args,
				"-output_ts_offset",
				offset,
				"-muxdelay",
				"0",
				"-muxpreload",
				"0",
				"-start_number",
				strconv.Itoa(from),
				"-hls_segment_filename",
				filepath.Join(outputDir, "segment-%06d."+ext),
				filepath.Join(outputDir, "window.m3u8"),
			)
		} else {
			args = append(args, "-hls_playlist_type", "event", "-hls_segment_filename", filepath.Join(dir, "segment-%06d."+ext), filepath.Join(dir, "master.m3u8"))
		}
		return args, stages, nil
	}
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
				return subtitles.WindowCommand{Args: args, Hardware: conversionJobHardware(plan, stages), Started: func() { h.recordConversionStart(ctx, id, plan, args, stages, fallbackLaunch) }}, err
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
			observe, finish := h.observeConversion(id)
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
		job := mediaexec.Job{
			Executable: h.binary,
			Args:       args,
			Files:      input.ExtraFiles(),
			InputFD:    input.MountFD,
			InputMount: input.MountPath,
			ReadPaths:  input.ReadPaths(),
			WriteDirs:  []string{dir},
			Fonts:      true,
			Hardware:   conversionJobHardware(plan, stages),
		}
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
		launchCtx := childprocess.WithStarted(processCtx, func() { h.recordConversionStart(ctx, id, plan, args, stages, fallbackLaunch) })
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
	if e != nil && !throttled() && ctx.Err() == nil && delivery != nil && delivery.VideoAction == "convert" && delivery.HardwareBackend != "" &&
		delivery.HardwareBackend != string(decoder.BackendSoftware) {
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
	if e != nil && !throttled() && ctx.Err() == nil && converterKilled(e) {
		// Killed from outside, most often by the kernel when memory ran out.
		// That says nothing about the source, so the stream is not failed: this
		// window ends, and the next request for a segment starts another, within
		// the same restart budget a seeking player has.
		h.mu.Lock()
		again := h.restartAllowedLocked(id)
		h.mu.Unlock()
		if again {
			log.Printf("Conversion %s: the converter was killed; it starts again on the next request", id)
			return
		}
	}
	if e != nil && !throttled() && ctx.Err() == nil {
		h.failedWith(ctx, id, classifyConverterFailure(diagnostic), diagnostic)
		return
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
	h.mu.Lock()
	if w := h.windows[id]; w == owned && produced > w.produced {
		w.produced = produced
	}
	if s := h.streams[id]; s != nil && h.windows[id] == owned && e == nil && (timeline == nil || produced >= timeline.count()-1) {
		s.output = "complete"
	}
	h.mu.Unlock()
}

// watchWindow enforces the throttle and published progress. Conversion that has
// run far enough ahead of the viewer is stopped rather than paused: a stopped
// converter costs nothing, and the fixed grid means the next demand restarts it
// exactly where the viewer is instead of where it left off.
func (h *HLS) watchWindow(
	ctx context.Context,
	id, dir, staging string,
	from, limit int,
	cancel context.CancelFunc,
	plan *DeliveryPlan,
	timeline *copyTimeline,
	fragmentClock *convertedFragmentClock,
	owned *hlsWindow,
	guards ...func(context.Context) error,
) {
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
			if produced > window.produced {
				window.produced = produced
				// Everybody waiting for a segment looks again now, not on their next tick.
				h.signalLocked(h.streams[id])
			}
			ahead := (window.produced - max(window.served, window.start-1)) * HLSSegmentSeconds
			// A copy window that has used up its cut list stops the same way a
			// throttled one does: the next request continues from there.
			exhausted := limit >= 0 && window.produced >= limit-1 && limit < h.timelines[id].count()-1
			if (cfg.ThrottleBufferSeconds > 0 && window.produced >= from && ahead >= cfg.ThrottleBufferSeconds) || exhausted {
				window.throttled = true
				h.cancelRenditionsLocked(id)
				h.mu.Unlock()
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

// recordStages keeps what the conversion graph actually did, so the owner's
// playback details can say where each operation ran without working it out again.
func (h *HLS) recordStages(ctx context.Context, id string, plan *DeliveryPlan, stages []decoder.HardwareStage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.streams[id]; s != nil {
		s.stages = append([]decoder.HardwareStage{}, stages...)
	}
}

// failed fails the stream's conversion, and reports whether it did. A window
// that was moved, spilled or cancelled fails nothing: its error is its own
// cancellation, and the stream it belonged to carries on with another window.
func (h *HLS) failed(ctx context.Context, id string) bool {
	h.mu.Lock()
	moving := h.moving[id] != nil
	if w, _ := ctx.Value(producerWindowKey{}).(*hlsWindow); w != nil {
		moving = moving || w.spillCancelled || w.retired
	}
	h.mu.Unlock()
	if moving || h.memoryOutputPressure(id) {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.streams[id]
	if s == nil || s.state != streamLive {
		return false
	}
	s.state, s.output = streamFailed, "failed"
	h.signalLocked(s)
	if s.failedHook != nil {
		supervise.Go("streaming.failed", s.failedHook)
	}
	return true
}
