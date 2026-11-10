package streaming

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func (h *HLS) sessionActiveLocked(id string) bool {
	for key := range h.active {
		if producerSession(key) == id {
			return true
		}
	}
	return false
}

// startRendition uses the same process and restart budgets as video. Only an
// explicitly requested track is admitted; all windows stop with their session.
func (h *HLS) startRendition(ctx context.Context, id string, r AudioRenditionPlan, from int) error {
	return h.startRenditionOwned(ctx, id, r, from)
}

func (h *HLS) startRenditionOwned(ctx context.Context, id string, r AudioRenditionPlan, from int) error {
	if r.Failure != "" {
		return ErrAudioRenditionUnavailable
	}
	key := renditionKey(id, r.Ordinal)
	h.mu.Lock()
	defer h.mu.Unlock()
	// The stream must still be live and still offer exactly this track.
	s := h.streams[id]
	if s == nil {
		return ErrGrantEnded
	}
	if e := finishedStreamError(s.state); e != nil {
		return e
	}
	if s.plan == nil || r.Ordinal < 0 || r.Ordinal >= len(s.plan.Renditions) || s.plan.Renditions[r.Ordinal] != r {
		return ErrGrantEnded
	}
	if h.moving[id] != nil || h.memoryFull[id] {
		return ErrSegmentPreparing
	}
	if h.cleaning[id] {
		return ErrSegmentPreparing
	}
	if w := h.windows[key]; w != nil {
		if w.coversDemand(from) {
			return nil
		}
		if w.relocating {
			h.mu.Unlock()
			select {
			case <-w.done:
			case <-ctx.Done():
				h.mu.Lock()
				return ctx.Err()
			}
			err := h.startRenditionOwned(ctx, id, r, from)
			h.mu.Lock()
			return err
		}
		if !h.restartAllowedLocked(id) {
			return nil
		}
		w.relocating = true
		w.retired = true
		w.cancel()
		h.mu.Unlock()
		select {
		case <-w.done:
		case <-ctx.Done():
			h.mu.Lock()
			return ctx.Err()
		}
		err := h.startRenditionOwned(ctx, id, r, from)
		h.mu.Lock()
		return err
	}
	if h.windows[key] != nil {
		return nil
	}
	if !h.roomToProduce() {
		return ErrConversionSpace
	}
	run, cancel := context.WithCancel(h.ctx)
	w := &hlsWindow{
		audioConversion: r.Action == "convert", start: from, served: -1, produced: from - 1, cancel: cancel, done: make(chan struct{}),
	}
	h.active[key] = cancel
	h.windows[key] = w
	h.noteSessionServedLocked(id)
	h.producers.Add(1)
	supervise.Go("playback.hls.audio-rendition", func() {
		defer h.producers.Done()
		defer func() {
			h.mu.Lock()
			if h.windows[key] == w {
				delete(h.active, key)
				delete(h.windows, key)
			}
			close(w.done)
			h.signalLocked(h.streams[id])
			h.mu.Unlock()
		}()
		h.produceRendition(context.WithValue(run, producerWindowKey{}, w), id, r, from, w)
	})
	return nil
}

func (h *HLS) produceRendition(ctx context.Context, id string, r AudioRenditionPlan, from int, w *hlsWindow) {
	failed := func(code string) {
		if code == FailureOutOfSpace && h.retryMemoryOutput(id) {
			return
		}
		// Relocation and shutdown cancel the old window, including its plan
		// load. They must not publish a source failure into the shared plan.
		if ctx.Err() != nil {
			return
		}
		if r.Default {
			h.failedWith(ctx, id, code, "")
		} else {
			h.renditionFailed(ctx, id, r, code)
		}
	}
	plan := h.planCopy(id)
	if plan == nil {
		return
	}
	dir, err := h.createStreamingOutput(ctx, id, plan)
	if err != nil {
		failed(FailureOutOfSpace)
		return
	}
	input, remoteInput, err := h.openWindowInput(ctx, id, plan)
	if err != nil {
		if ctx.Err() == nil {
			failed(FailureSourceUnreadable)
		}
		return
	}
	defer input.Close()
	ctx, releaseInput := input.BorrowContext(ctx)
	defer releaseInput()
	if remoteInput != nil {
		defer remoteInput.Close()
	}
	offset := strconv.Itoa(from * HLSSegmentSeconds)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-protocol_whitelist", "file,pipe"}
	readerOptions, readerURL, residual := input.Options, input.Argument, int64(0)
	if input.InputAt != nil {
		var at int64
		readerOptions, readerURL, at, err = input.InputAt(ctx, int64(from*HLSSegmentSeconds*1000))
		if err != nil {
			failed(FailureConverter)
			return
		}
		residual = int64(from*HLSSegmentSeconds*1000) - at
	} else {
		args = append(args, "-ss", offset)
	}
	args = append(args, readerOptions...)
	args = append(args, "-i", readerURL, "-ss", strconv.FormatFloat(float64(residual)/1000, 'f', 3, 64), "-map", "0:"+strconv.Itoa(r.StreamIndex), "-vn", "-sn", "-dn")
	audioTarget := decoder.ConversionTarget{
		AudioCodec:         r.Codec,
		AudioChannels:      r.Channels,
		AudioBitrateBPS:    r.BitrateBPS,
		AudioSampleRate:    r.SampleRate,
		AudioChannelLayout: r.ChannelLayout,
		AudioBitDepth:      r.BitDepth,
		Packaging:          renditionPackaging(r),
	}
	graph, err := decoder.BuildConversion(
		decoder.ConversionRequest{
			Target:          &audioTarget,
			Toolchain:       decoder.CurrentToolchain(),
			CopyAudio:       r.Action == "copy",
			ConvertAudio:    r.Action == "convert",
			AudioCodec:      r.Codec,
			AudioChannels:   r.Channels,
			AudioBitrateBPS: r.BitrateBPS,
			Downmix:         r.Downmix == DownmixITULimited,
			AudioSampleRate: r.SampleRate,
		},
	)
	if err != nil {
		failed(FailureConverter)
		return
	}
	args = append(args, graph.Audio...)
	fragmented := renditionPackaging(r) == "fmp4_hls"
	initWindow := fmt.Sprintf("audio-%d-init-%d.mp4", r.Ordinal, from)
	outputDir := dir
	clock := &convertedFragmentClock{}
	if fragmented || input.InputAt != nil {
		outputDir = filepath.Join(dir, fmt.Sprintf("audio-window-%d-%d", r.Ordinal, from))
		_ = os.RemoveAll(outputDir)
		if err := os.MkdirAll(outputDir, 0700); err != nil {
			failed(FailureOutOfSpace)
			return
		}
		defer os.RemoveAll(outputDir)
	}

	if fragmented {
		outputArgs, _, err := decoder.HLSPackagingArgs("fmp4_hls", initWindow)
		if err != nil {
			failed(FailureConverter)
			return
		}
		args = append(args, outputArgs...)
		args = append(args, "-strict", "-2")
	}
	args = append(
		args,
		"-max_muxing_queue_size",
		"1024",
		"-f",
		"hls",
		"-hls_time",
		"6",
		"-hls_list_size",
		"0",
		"-hls_flags",
		"independent_segments+temp_file",
		"-output_ts_offset",
		offset,
		"-muxdelay",
		"0",
		"-muxpreload",
		"0",
		"-start_number",
		strconv.Itoa(from),
		"-hls_segment_filename",
		filepath.Join(outputDir, strings.ReplaceAll(renditionSegmentFile(r, 0), "000000", "%06d")),
		filepath.Join(outputDir, fmt.Sprintf("audio-%d-window.m3u8", r.Ordinal)),
	)
	run, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	watchDone := make(chan struct{})
	supervise.Go("playback.hls.watch-audio", func() {
		defer close(watchDone)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
			}
			keep := func() bool {
				ownedIO, err := childprocess.OwnedIOContext(ctx)
				if err != nil {
					return false
				}
				if input.Validate != nil {
					if err := input.Validate(ownedIO); err != nil {
						cancel()
						failed(FailureConverter)
						return false
					}
				}
				h.mu.Lock()
				produced := w.produced
				h.mu.Unlock()
				for produced+1 < hlsSegmentCount(plan.Duration) && segmentReady(filepath.Join(dir, renditionSegmentFile(r, produced+1))) {
					produced++
				}
				if input.InputAt != nil && !fragmented {
					for next := produced + 1; next < hlsSegmentCount(plan.Duration) && segmentReady(filepath.Join(outputDir, renditionSegmentFile(r, next))); next++ {
						if err := os.Rename(filepath.Join(outputDir, renditionSegmentFile(r, next)), filepath.Join(dir, renditionSegmentFile(r, next))); err != nil {
							cancel()
							failed(FailureConverter)
							return false
						}
						produced = next
					}
				}
				if fragmented {
					var publishErr error
					produced, publishErr = promoteConvertedFragments(outputDir, dir, initWindow, from, &r, clock)
					if publishErr != nil {
						cancel()
						failed(FailureConverter)
						return false
					}
				}

				h.mu.Lock()
				w.produced = produced
				served := max(w.served, w.start-1)
				h.mu.Unlock()
				limit := max(12, h.configuration().ThrottleBufferSeconds)
				edge := served + (limit+5)/6
				if segmentReady(filepath.Join(dir, renditionSegmentFile(r, edge))) {
					h.mu.Lock()
					w.throttled = true
					h.mu.Unlock()
					cancel()
					return false
				}
				return true
			}()
			if !keep {
				return
			}
		}
	})
	if input.File != nil {
		_, _ = input.File.Seek(0, io.SeekStart)
	}
	tail := &tailWriter{limit: 4096}
	if remoteInput != nil {
		err = remoteInput.Run(run, dir, func(url string) (subtitles.WindowCommand, error) {
			copied := append([]string{}, args...)
			for i := 0; i+1 < len(copied); i++ {
				if copied[i] == "-i" {
					copied[i+1] = url
					break
				}
				if copied[i] == "-protocol_whitelist" {
					copied[i+1] = "http,tcp"
				}
			}
			return subtitles.WindowCommand{Args: copied}, nil
		})
		if err != nil {
			_, _ = tail.Write([]byte(windowConverterDiagnostic(err, "", h.root, h.memoryRoot)))
		}
	} else {
		var progress *vod.ConversionProgressWriter
		if r.Action == "convert" {
			observe, finish := h.observeConversion(id)
			defer finish()
			args = append([]string{"-progress", "pipe:1", "-stats_period", "1", "-nostats"}, args...)
			progress = vod.NewConversionProgressWriter(observe)
		}
		if input.Validate != nil {
			if err = validateOwnedInput(run, input.Validate); err != nil {
				cancel()
				<-watchDone
				failed(FailureConverter)
				return
			}
		}
		for _, extra := range input.Extra {
			_, _ = extra.Seek(0, io.SeekStart)
		}
		var cmd *exec.Cmd
		if cmd, err = mediaexec.CommandContext(run, mediaexec.Job{Executable: h.binary, Args: args, Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath,
			ReadPaths: input.ReadPaths(), WriteDirs: []string{dir}}); err == nil {
			cmd.WaitDelay = 2 * time.Second
			cmd.Stderr = tail
			if progress != nil {
				cmd.Stdout = progress
			}
			err = childprocess.Run(run, cmd)
		}
	}
	cancel()
	<-watchDone
	if input.Validate != nil {
		if err := validateOwnedInput(ctx, input.Validate); err != nil {
			failed(FailureConverter)
			return
		}
	}
	if err := childprocess.CheckOwnedIO(ctx); err != nil {
		return
	}
	if input.InputAt != nil && !fragmented {
		for next := from; next < hlsSegmentCount(plan.Duration); next++ {
			source := filepath.Join(outputDir, renditionSegmentFile(r, next))
			if !segmentReady(source) {
				continue
			}
			if err := os.Rename(source, filepath.Join(dir, renditionSegmentFile(r, next))); err != nil {
				failed(FailureConverter)
				return
			}
		}
	}
	if fragmented {
		if _, publishErr := promoteConvertedFragments(outputDir, dir, initWindow, from, &r, clock); publishErr != nil {
			failed(FailureConverter)
			return
		}
	}

	h.mu.Lock()
	throttled := w.throttled
	h.mu.Unlock()
	if err != nil && !throttled && ctx.Err() == nil {
		failed(classifyConverterFailure(tail.String()))
	}
}

func (h *HLS) renditionFile(ctx context.Context, id, name string, p *DeliveryPlan) (string, error) {
	if !renditionArtifactDeclared(name, p) {
		return "", ErrHLSArtifactNotFound
	}
	path := filepath.Join(h.outputDir(id), name)
	parts := renditionSegmentName.FindStringSubmatch(name)
	if parts == nil {
		if isRenditionInit(name) {
			var ordinal int
			fmt.Sscanf(name, "audio-%d-init.mp4", &ordinal)
			if !segmentReady(path) {
				if err := h.startRendition(ctx, id, p.Renditions[ordinal], 0); err != nil {
					return "", err
				}
				return "", ErrSegmentPreparing
			}
		}
		return path, nil
	}
	ordinal, _ := strconv.Atoi(parts[1])
	index, _ := strconv.Atoi(parts[2])
	key := renditionKey(id, ordinal)
	if segmentReady(path) {
		h.noteRenditionServed(id, key, index)
		return path, nil
	}
	if err := h.startRendition(ctx, id, p.Renditions[ordinal], index); err != nil {
		return "", err
	}
	return h.awaitSegment(ctx, key, index, func(ctx context.Context) (string, error) {
		wait, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			h.mu.Lock()
			s := h.streams[id]
			if s == nil || s.plan == nil || ordinal >= len(s.plan.Renditions) {
				h.mu.Unlock()
				return "", ErrGrantEnded
			}
			state, failure, changed := s.state, s.plan.Renditions[ordinal].Failure, s.changed
			h.mu.Unlock()
			if e := finishedStreamError(state); e != nil {
				return "", e
			}
			if failure != "" {
				return "", ErrAudioRenditionUnavailable
			}
			current := filepath.Join(h.outputDir(id), name)
			if current != path {
				path = current
				if err := h.startRendition(ctx, id, p.Renditions[ordinal], index); err != nil {
					return "", err
				}
			}
			if segmentReady(path) {
				h.noteRenditionServed(id, key, index)
				return path, nil
			}
			select {
			case <-wait.Done():
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "", ErrSegmentPreparing
			case <-changed:
			case <-tick.C:
			}
		}
	})
}

// renditionFailed marks one extra audio track of a stream as unavailable. The
// stream itself carries on; only a request for that track is refused.
func (h *HLS) renditionFailed(ctx context.Context, id string, r AudioRenditionPlan, code string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if w, _ := ctx.Value(producerWindowKey{}).(*hlsWindow); w != nil && (w.retired || w.spillCancelled) {
		return
	}
	s := h.streams[id]
	if s == nil || s.state != streamLive || s.plan == nil || r.Ordinal >= len(s.plan.Renditions) ||
		s.plan.Renditions[r.Ordinal].StreamIndex != r.StreamIndex {
		return
	}
	next := *s.plan
	next.Renditions = append([]AudioRenditionPlan{}, s.plan.Renditions...)
	next.Renditions[r.Ordinal].Failure = code
	s.plan = &next
	h.signalLocked(s)
}

func (h *HLS) noteRenditionServed(id, key string, index int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.noteSessionServedLocked(id)
	if w := h.windows[key]; w != nil {
		w.served = max(w.served, index)
	}
}
