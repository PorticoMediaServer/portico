package mediaengine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// bookParts is an audiobook's files in order when it is in several, each with its length, or
// nil for anything else. A part's length is the one its probe stored, the same length and
// place the book's page shows for it; a part not probed yet is measured now. A book on remote
// storage is joined the same way, each part read through its source by range.
func (e *Engine) bookParts(ctx context.Context, r playback.PlanRequest, remoteID int64, first mediafacts.Facts) ([]streaming.JoinedPart, error) {
	if r.Item.Kind != db.KindAudiobook || len(r.Version.Parts) < 2 {
		return nil, nil
	}
	var storage remote.Storage
	if remoteID != 0 {
		var err error
		if storage, _, err = e.remote(ctx, remoteID); err != nil {
			return nil, playback.ErrSourceUnavailable
		}
	}
	folders := e.folders()
	parts := make([]streaming.JoinedPart, 0, len(r.Version.Parts))
	for n, part := range r.Version.Parts {
		place, err := e.db.FileOnDisk(ctx, part.FileID)
		if storage != nil {
			_, place, err = e.db.RemoteFileOf(ctx, part.FileID)
		}
		if errors.Is(err, db.ErrNotFound) {
			return nil, playback.ErrSourceUnavailable
		}
		if err != nil {
			return nil, err
		}
		path := place.Path
		if storage == nil {
			if path, err = libraryfile.Resolve(place.Path, folders); err != nil {
				return nil, playback.ErrSourceUnavailable
			}
		}
		length, shape := place.DurationMS, place.AudioShape
		// A remote part's packet clock is measured by its scan where the library reads whole
		// files; at play time it is not read whole over the network, and its length is used.
		measurePackets := storage == nil && place.PacketLengthUS <= 0
		if length <= 0 || shape == "" || measurePackets {
			facts := first
			if n > 0 && storage != nil {
				facts, err = e.inspectRemote(ctx, remoteID, path, place.Size)
			} else if n > 0 {
				facts, err = e.probe.Inspect(ctx, path)
			}
			if err != nil {
				return nil, playback.ErrSourceUnavailable
			}
			if measurePackets {
				if err := e.probe.CompleteAudioPackets(ctx, path, &facts); err != nil {
					return nil, playback.ErrSourceUnavailable
				}
				if facts.Packets != nil {
					place.PacketLengthUS = facts.Packets.LengthUS
				}
			}
			if length <= 0 {
				length = int64(math.Round(facts.Duration * 1000))
			}
			if shape == "" {
				shape = soundShape(facts)
			}
		}
		if length <= 0 {
			return nil, playback.ErrSourceUnavailable
		}
		// Both routes use the page's packet clock. Conversion fills encoder
		// padding with silence instead of shortening each part.
		durationUS := length * 1000
		if place.PacketLengthUS > 0 {
			durationUS = place.PacketLengthUS
		}
		parts = append(parts, streaming.JoinedPart{Path: path, Size: place.Size, Modified: time.Unix(0, place.ModifiedNS), DurationUS: durationUS, Shape: shape,
			Remote: storage})
	}
	return parts, nil
}

// soundShape is a measured file's first sound track as db.FileOnDisk.AudioShape writes it.
func soundShape(facts mediafacts.Facts) string {
	for _, s := range facts.Streams {
		if s.Type == "audio" {
			return fmt.Sprintf("%s|%d|%d|%s", s.Codec, s.Detail.SampleRate, s.Channels, s.ChannelLayout)
		}
	}
	return ""
}
