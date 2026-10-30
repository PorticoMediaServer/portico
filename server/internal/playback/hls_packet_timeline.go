package playback

import (
	"bytes"
	"context"
	"io"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"strconv"
	"strings"
	"time"
)

// packetTimelineWriter retains one timestamp per grid line while ffprobe walks
// an unindexed container. Packet output is streamed, never collected in memory.
type packetTimelineWriter struct {
	pending               []byte
	start, duration, last float64
	seen                  bool
	packets               int
	times                 []float64
	valid                 []bool
	next                  int
}

func newPacketTimelineWriter(start, duration float64) *packetTimelineWriter {
	count := max(0, int(math.Ceil(duration/HLSSegmentSeconds))-1)
	return &packetTimelineWriter{start: start, duration: duration, times: make([]float64, count), valid: make([]bool, count)}
}
func (w *packetTimelineWriter) Write(p []byte) (int, error) {
	length := len(p)
	for len(p) > 0 {
		newline := bytes.IndexByte(p, '\n')
		if newline < 0 {
			if len(w.pending)+len(p) > 4096 {
				return 0, errCopyTimeline
			}
			w.pending = append(w.pending, p...)
			break
		}
		if len(w.pending)+newline > 4096 {
			return 0, errCopyTimeline
		}
		w.pending = append(w.pending, p[:newline]...)
		p = p[newline+1:]
		w.packets++
		if w.packets > 20_000_000 {
			return 0, errCopyTimeline
		}
		stamp, flags, _ := strings.Cut(strings.TrimSpace(string(w.pending)), ",")
		w.pending = w.pending[:0]
		if !strings.HasPrefix(flags, "K") {
			continue
		}
		value, err := strconv.ParseFloat(stamp, 64)
		value -= w.start
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		if w.seen && value < w.last-0.001 {
			return 0, errCopyTimeline
		}
		for w.next < len(w.times) && float64(w.next+1)*HLSSegmentSeconds < value-0.05 {
			w.times[w.next], w.valid[w.next] = w.last, w.seen
			w.next++
		}
		w.last, w.seen = value, true
	}
	return length, nil
}
func (w *packetTimelineWriter) timeline() (*copyTimeline, error) {
	if len(w.pending) > 0 {
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return nil, err
		}
	}
	for w.next < len(w.times) {
		w.times[w.next], w.valid[w.next] = w.last, w.seen
		w.next++
	}
	if !w.seen {
		return nil, errCopyTimeline
	}
	return buildCopyTimeline(w.duration, w.times, w.valid)
}
func probePacketTimeline(ctx context.Context, probe timelinePacketProbe, start, duration float64) (*copyTimeline, error) {
	if probe == nil || !hlsDurationSupported(duration) {
		return nil, errCopyTimeline
	}
	writer := newPacketTimelineWriter(start, duration)
	if err := probe(ctx, writer); err != nil {
		return nil, err
	}
	return writer.timeline()
}

func localPacketTimelineProbe(ffprobe string, input *assets.PlaybackInput) timelinePacketProbe {
	return func(ctx context.Context, writer io.Writer) error {
		ctx, releaseInput := input.BorrowContext(ctx)
		defer releaseInput()
		if input.File != nil {
			_, _ = input.File.Seek(0, io.SeekStart)
		}
		args := []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-fflags", "+genpts", "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", input.Argument}
		cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffprobe, Args: args, Files: input.ExtraFiles(), ReadPaths: input.ReadPaths()})
		if err != nil {
			return err
		}
		cmd.WaitDelay = time.Second
		cmd.Stdout = writer
		if err := cmd.Run(); err != nil {
			return errCopyTimeline
		}
		return nil
	}
}
