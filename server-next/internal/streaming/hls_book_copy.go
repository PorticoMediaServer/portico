package streaming

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/supervise"
)

// One pending output-packet read for this stream's next batch. It is neither
// a scan result nor a library fact; stop/seek/shutdown cancel and reap it.
type bookCopyAhead struct {
	from   int
	phase  int64
	err    error
	done   chan struct{}
	cancel context.CancelFunc
}

func bookCopyGrid(duration float64) *copyTimeline {
	boundaries := make([]float64, hlsSegmentCount(duration)+1)
	for n := range boundaries {
		boundaries[n] = float64(n) * HLSSegmentSeconds
	}
	boundaries[len(boundaries)-1] = duration
	return &copyTimeline{Boundaries: boundaries}
}

func bookCopyObservationArgs(input *PlaybackInput, plan *DeliveryPlan) []string {
	args := []string{}
	if input.Skip > 0 {
		args = append(args, "-ss", strconv.FormatFloat(input.Skip, 'f', 6, 64))
	}
	track := "0:a:0"
	if plan.AudioStream >= 0 {
		track = "0:" + strconv.Itoa(plan.AudioStream)
	}
	return append(args, "-map", track, "-vn", "-sn", "-dn", "-c:a", "copy", "-frames:a", "1", "-f", "framecrc", "pipe:1")
}

// FFmpeg's segment muxer rounds its first packet into microseconds and adds
// that value even to explicit cut times. Cancel precisely that rounding. TS
// uses 90 kHz; the MP4 audio track uses its sample rate.
func bookCopyPhase(data []byte, fragmented bool) (int64, error) {
	var numerator, denominator, rate int64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "#tb 0:") {
			fmt.Sscanf(line, "#tb 0: %d/%d", &numerator, &denominator)
		} else if strings.HasPrefix(line, "#sample_rate 0:") {
			fmt.Sscanf(line, "#sample_rate 0: %d", &rate)
		} else if line != "" && line[0] != '#' {
			fields := strings.Split(line, ",")
			if len(fields) < 4 || numerator <= 0 || denominator <= 0 {
				break
			}
			pts, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
			if err != nil {
				break
			}
			if !fragmented {
				rate = 90000
			}
			seconds := float64(pts) * float64(numerator) / float64(denominator)
			if rate <= 0 || seconds < 0 || seconds > 1 {
				break
			}
			ticks := math.Round(seconds * float64(rate))
			return int64(math.Round(ticks * 1e6 / float64(rate))), nil
		}
	}
	return 0, errCopyTimeline
}

func (h *HLS) readBookCopyPhase(ctx context.Context, id string, from int, plan *DeliveryPlan) (int64, error) {
	input, remote, err := h.openWindowInput(ctx, id, plan, float64(from)*HLSSegmentSeconds)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	if remote != nil {
		remote.Close()
		return 0, errCopyTimeline
	}
	ctx, release := input.BorrowContext(ctx)
	defer release()
	args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe"}
	args = append(args, input.Options...)
	args = append(args, "-i", input.Argument)
	args = append(args, bookCopyObservationArgs(input, plan)...)
	for _, f := range input.ExtraFiles() {
		_, _ = f.Seek(0, io.SeekStart)
	}
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: h.binary, Args: args,
		Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths()})
	if err != nil {
		return 0, err
	}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{buffer: &out, limit: 4096}
	if err := childprocess.Run(ctx, cmd); err != nil {
		return 0, err
	}
	return bookCopyPhase(out.Bytes(), plan.OutputContainer == "fmp4_hls")
}

func (h *HLS) takeBookCopyAhead(ctx context.Context, id string, from int) (int64, bool, error) {
	h.mu.Lock()
	s := h.streams[id]
	if s == nil || s.state != streamLive {
		h.mu.Unlock()
		return 0, false, ErrGrantEnded
	}
	ahead := s.bookAhead
	if ahead == nil || ahead.from != from {
		if ahead != nil {
			ahead.cancel()
			s.bookAhead = nil
		}
		h.mu.Unlock()
		return 0, true, nil // Cold start: sound first, then look ahead.
	}
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return 0, false, ctx.Err()
	case <-ahead.done:
	}
	h.mu.Lock()
	if s.bookAhead == ahead {
		s.bookAhead = nil
	}
	h.mu.Unlock()
	return ahead.phase, ahead.err != nil, nil
}

func (h *HLS) warmBookCopy(parent context.Context, id string, from int, plan *DeliveryPlan, owned *hlsWindow) {
	h.mu.Lock()
	s := h.streams[id]
	if s == nil || s.state != streamLive || h.windows[id] != owned || owned.retired || parent.Err() != nil || h.ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	previous := s.bookAhead
	if previous != nil && previous.from == from {
		h.mu.Unlock()
		return
	}
	if previous != nil {
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(h.ctx)
	ahead := &bookCopyAhead{from: from, done: make(chan struct{}), cancel: cancel}
	s.bookAhead = ahead
	h.producers.Add(1)
	h.mu.Unlock()
	supervise.Go("playback.hls.book-lookahead", func() {
		defer h.producers.Done()
		defer close(ahead.done)
		defer cancel()
		if previous != nil {
			select {
			case <-previous.done:
			case <-ctx.Done():
				ahead.err = ctx.Err()
				return
			}
		}
		ahead.phase, ahead.err = h.readBookCopyPhase(ctx, id, from, plan)
	})
}
