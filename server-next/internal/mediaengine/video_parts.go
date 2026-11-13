package mediaengine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// A film or episode in several files is one stream, with the version's clock.
func (e *Engine) videoParts(ctx context.Context, r playback.PlanRequest, remoteID int64) ([]streaming.JoinedPart, error) {
	if (r.Item.Kind != db.KindMovie && r.Item.Kind != db.KindEpisode) || len(r.Version.Parts) < 2 {
		return nil, nil
	}
	if remoteID != 0 {
		return nil, nil
	}
	parts := make([]streaming.JoinedPart, 0, len(r.Version.Parts))
	for _, part := range r.Version.Parts {
		version := r.Version
		version.FileID = part.FileID
		path, place, partRemoteID, facts, err := e.source(ctx, version)
		if err != nil || partRemoteID != 0 {
			return nil, nil
		}
		length := place.DurationMS
		if length <= 0 {
			length = int64(facts.Duration * 1000)
		}
		if length <= 0 {
			return nil, nil
		}
		var shape strings.Builder
		for _, s := range facts.Streams {
			if s.Type == "audio" || s.Type == "video" {
				// The selected track maps by index, not descriptive metadata.
				fmt.Fprintln(&shape, s.Index, s.Type, s.Codec)
				fmt.Fprintln(&shape, s.Detail.Profile, s.Detail.PixelFormat, s.Detail.BitDepth, s.Detail.Width, s.Detail.Height, s.Detail.SampleAspectRatio)
				fmt.Fprintln(&shape, s.ColorTransfer, s.ColorPrimaries, s.Detail.DynamicRange, s.Detail.SampleRate, s.Channels, s.ChannelLayout)
			}
		}
		parts = append(parts, streaming.JoinedPart{Path: path, Size: place.Size, Modified: time.Unix(0, place.ModifiedNS), DurationUS: length * 1000, Shape: shape.String()})
	}
	// The mixed-part filter joins sound only. Until video conversion and remote
	// joining support these parts, preserve the existing first-file playback.
	if streaming.PartsDiffer(parts) {
		return nil, nil
	}
	return parts, nil
}
