package streaming

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

func (h *HLS) prepareBurnInput(ctx context.Context, id, dir string, position float64, plan *DeliveryPlan, input *PlaybackInput, remote subtitles.WindowInput) (*decoder.BurnIn, func(), error) {
	noop := func() {}
	if plan == nil || plan.BurnIn == nil {
		return nil, noop, nil
	}
	h.mu.Lock()
	var asset *BurnSubtitle
	if s := h.streams[id]; s != nil {
		asset = s.burn
	}
	h.mu.Unlock()
	var e error
	if asset == nil {
		return nil, noop, errors.New("selected subtitle unavailable")
	}
	if plan == nil || plan.VideoAction != "convert" {
		return nil, noop, errors.New("subtitle selection requires video conversion")
	}
	scratch, e := os.MkdirTemp(dir, "burn-")
	if e != nil {
		return nil, noop, e
	}
	cleanup := func() { _ = os.RemoveAll(scratch) }
	ext := map[string]string{"ass": "ass", "ssa": "ssa", "pgs": "sup", "vobsub": "idx", "mks": "mks"}[asset.Format]
	file := filepath.Join(scratch, "selected."+ext)
	if e = os.WriteFile(file, asset.Data, 0600); e != nil {
		cleanup()
		return nil, noop, e
	}
	if asset.Format == "vobsub" {
		if e = os.WriteFile(filepath.Join(scratch, "selected.sub"), asset.Companion, 0600); e != nil {
			cleanup()
			return nil, noop, e
		}
	}
	b := &decoder.BurnIn{File: file, Format: asset.Format, PositionUS: int64(position * 1e6), OffsetUS: asset.OffsetUS}
	if plan.Trace != nil && plan.Trace.Video != nil {
		b.Width, b.Height = plan.Trace.Video.Width, plan.Trace.Video.Height
	}
	if asset.Format == "ass" || asset.Format == "ssa" {
		b.FontsDirectory, e = h.extractBurnFonts(ctx, input, remote, dir)
		if e != nil {
			cleanup()
			return nil, noop, e
		}
	} else {
		b.InputStartUS, e = decoder.BurnInputOrigin(ctx, h.ffprobe, file, asset.Format)
		if e != nil {
			cleanup()
			return nil, noop, e
		}
	}
	return b, cleanup, nil
}

// BurnSubtitle is the subtitle a stream burns into its picture: the file's own
// bytes in the format the plan names, with the viewer's timing nudge.
type BurnSubtitle struct {
	// Format is "ass", "ssa", "pgs", "vobsub" or "mks".
	Format string
	Data   []byte
	// Companion is the .sub file of a VobSub pair.
	Companion []byte
	OffsetUS  int64
}

// openWindowInput opens the stream's source for one converter run. The second
// result is for a source ffmpeg reaches over a private local address: a file on
// remote storage.
func (h *HLS) openWindowInput(ctx context.Context, id string, plan *DeliveryPlan) (*PlaybackInput, subtitles.WindowInput, error) {
	s, err := h.liveStream(id)
	if err != nil {
		return nil, nil, err
	}
	if s.openInput != nil {
		return s.openInput(ctx)
	}
	if remote, ok := s.source.(windowSource); ok {
		return remote.openWindow(ctx, h.binary, h.ffprobe, h.processes)
	}
	input, err := converterInput(ctx, s.source)
	return input, nil, err
}
