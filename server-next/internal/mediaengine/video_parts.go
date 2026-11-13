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
		return nil, playback.ErrUnsupported
	}
	parts := make([]streaming.JoinedPart, 0, len(r.Version.Parts))
	for _, part := range r.Version.Parts {
		version := r.Version
		version.FileID = part.FileID
		path, place, _, facts, err := e.source(ctx, version)
		if err != nil {
			return nil, err
		}
		length := place.DurationMS
		if length <= 0 {
			length = int64(facts.Duration * 1000)
		}
		if length <= 0 {
			return nil, playback.ErrSourceUnavailable
		}
		var shape strings.Builder
		for _, s := range facts.Streams {
			if s.Type == "audio" || s.Type == "video" {
				fmt.Fprintln(&shape, s.Index, s.Type, s.Codec, s.Language, s.Title, s.Default)
				fmt.Fprintln(&shape, s.Detail.Profile, s.Detail.PixelFormat, s.Detail.BitDepth, s.Detail.Width, s.Detail.Height, s.Detail.SampleAspectRatio)
				fmt.Fprintln(&shape, s.ColorTransfer, s.ColorPrimaries, s.Detail.DynamicRange, s.Detail.SampleRate, s.Channels, s.ChannelLayout)
			}
		}
		parts = append(parts, streaming.JoinedPart{Path: path, Size: place.Size, Modified: time.Unix(0, place.ModifiedNS), DurationUS: length * 1000, Shape: shape.String()})
	}
	// The mixed-part filter joins sound only. Do not hand it a video or
	// silently play just the first file of a version it cannot yet join.
	if streaming.PartsDiffer(parts) {
		return nil, playback.ErrUnsupported
	}
	return parts, nil
}
