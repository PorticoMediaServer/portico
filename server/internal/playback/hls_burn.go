package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func (h *HLS) prepareBurnInput(ctx context.Context, id, dir string, position float64, plan *DeliveryPlan, input *assets.PlaybackInput, remote subtitles.WindowInput) (*decoder.BurnIn, func(), error) {
	noop := func() {}
	if plan == nil || plan.BurnIn == nil {
		return nil, noop, nil
	}
	if h.subtitleService == nil {
		return nil, noop, errors.New("subtitle service unavailable")
	}
	asset, offset, e := h.subtitleService.SelectedBurnInput(ctx, id, plan.BurnIn.ResourceID, plan.BurnIn.Revision)
	if e != nil {
		return nil, noop, e
	}
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
	b := &decoder.BurnIn{File: file, Format: asset.Format, PositionUS: int64(position * 1e6), OffsetUS: offset}
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

func (h *HLS) openWindowInput(ctx context.Context, id string, plan *DeliveryPlan) (*assets.PlaybackInput, subtitles.WindowInput, error) {
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	input, remote, err := h.openWindowInputOwned(ctx, id, plan)
	if err != nil {
		return input, remote, err
	}
	check, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		if input != nil {
			input.Close()
		}
		if remote != nil {
			remote.Close()
		}
		return nil, nil, err
	}
	check()
	return input, remote, nil
}
func (h *HLS) openWindowInputOwned(ctx context.Context, id string, plan *DeliveryPlan) (*assets.PlaybackInput, subtitles.WindowInput, error) {
	if plan != nil && plan.SourceContainer == "strm" {
		if h.WindowInputs == nil {
			return nil, nil, errors.New("remote window inputs unavailable")
		}
		input, e := h.WindowInputs.OpenPlaybackWindow(ctx, id)
		return &assets.PlaybackInput{}, input, e
	}
	if input, handled, err := h.openDiscInput(ctx, id); handled {
		return input, nil, err
	}
	input, e := assets.OpenPlaybackInput(ctx, h.db, h.SourceStorage, id)
	return input, nil, e
}
