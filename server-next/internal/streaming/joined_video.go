package streaming

import (
	"context"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// Index each original container, rather than seeking the concat demuxer (which
// cannot seek across file descriptors). Reuse the existing per-file cut cache;
// a changed later part must not reuse the first part's old whole-title index.
func (h *HLS) joinedVideoTimeline(ctx context.Context, parts []JoinedPart, roots func() []string) (*copyTimeline, error) {
	result := &copyTimeline{Boundaries: []float64{0}}
	offset := 0.0
	for _, part := range parts {
		key := timelineKey{part.Path, part.Size, part.Modified.UnixNano()}
		h.mu.Lock()
		timeline := h.fileTimelines[key]
		h.mu.Unlock()
		if timeline == nil {
			var err error
			if timeline, err = h.partTimeline(ctx, part, roots); err != nil {
				return nil, err
			}
			h.mu.Lock()
			if h.fileTimelines == nil || len(h.fileTimelines) >= fileTimelineLimit {
				h.fileTimelines = map[timelineKey]*copyTimeline{}
			}
			h.fileTimelines[key] = timeline
			h.mu.Unlock()
		}
		for _, at := range timeline.Boundaries[1:] {
			result.Boundaries = append(result.Boundaries, offset+at)
		}
		offset += float64(part.DurationUS) / 1e6
	}
	if result.count() > HLSMaxSegments {
		return nil, errCopyTimeline
	}
	return result, nil
}

// partTimeline indexes one part: a local file by its descriptor, a part on remote storage
// through its own bridge, as a single remote file is indexed.
func (h *HLS) partTimeline(ctx context.Context, part JoinedPart, roots func() []string) (*copyTimeline, error) {
	duration := float64(part.DurationUS) / 1e6
	if part.Remote != nil {
		input, window, err := remoteFile{storage: part.Remote, path: part.Path, size: part.Size, modified: part.Modified}.openBridge(ctx, h.binary, h.ffprobe, h.processes)
		if err != nil {
			return nil, err
		}
		defer input.Close()
		defer window.Close()
		return probeCopyTimeline(ctx, func(ctx context.Context, args ...string) ([]byte, error) { return window.Probe(ctx, args) }, nil, duration)
	}
	input, err := converterInput(ctx, libraryFile{path: part.Path, size: part.Size, modified: part.Modified, roots: roots})
	if err != nil {
		return nil, err
	}
	defer input.Close()
	return probeCopyTimeline(ctx, localTimelineProbe(h.ffprobe, input), localPacketTimelineProbe(h.ffprobe, input), duration)
}

// A copied picture's cut can precede its nominal six-second slot. Open the
// part holding the actual cut, and end the batch on that same cut list.
func (h *HLS) joinedVideoInput(ctx context.Context, id string, plan *DeliveryPlan, position float64, timeline *copyTimeline) (*PlaybackInput, subtitles.WindowInput, error) {
	input, window, err := h.openWindowInput(ctx, id, plan, position)
	if err == nil && input.Until > 0 {
		input.Until = timeline.Boundaries[timeline.indexAt(input.Until)]
	}
	return input, window, err
}

// Video parts have container indexes. The selected part is already the one
// holding this position, so seeking inside it never asks concat to cross files
// and avoids reading a long part from its beginning on every seek.
func joinedVideoSeekArgs(args []string, input *PlaybackInput, plan *DeliveryPlan) []string {
	if input.Positioned && plan != nil && plan.VideoAction != "none" && input.Skip > 0 {
		seek := input.Skip
		if plan.VideoAction == "copy" {
			seek += 0.002
		}
		// Concat can report negative audio priming as its start. Relative
		// -ss would then seek before the intended keyframe and replay a GOP.
		return append(args, "-seek_timestamp", "1", "-ss", strconv.FormatFloat(seek, 'f', 6, 64))
	}
	return args
}
