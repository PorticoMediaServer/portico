package mediaengine

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Prober reads a file's container and tracks for the scanner, with ffprobe in its sandbox.
// Tracks are stored in the file's own order, so a track's place among its kind is the same
// here as in what Plan reads from the file.
type Prober struct {
	// FFprobe is the tool to run; empty finds the bundled or installed one.
	FFprobe string
}

var streamKinds = map[string]db.StreamKind{"video": db.StreamVideo, "audio": db.StreamAudio, "subtitle": db.StreamSubtitle}

// Probe never calls a file damaged: ffprobe failing does not say whether the file or the
// moment was at fault, so the file keeps the title its name gave it and is read again by
// the next scan.
func (p Prober) Probe(ctx context.Context, path string) (scan.Facts, error) {
	facts, err := mediafacts.Probe{Binary: p.FFprobe}.InspectScan(ctx, path)
	if err != nil {
		return scan.Facts{}, err
	}
	out := scan.Facts{Container: facts.Container, DurationMS: int64(math.Round(facts.Duration * 1000)), Bitrate: facts.BitRate}
	for _, s := range facts.Streams {
		d := s.Detail
		stream := db.Stream{Kind: streamKinds[s.Type], Codec: s.Codec, Profile: d.Profile, Language: s.Language, Title: s.Title,
			Default: s.Default, Forced: s.Forced, HearingImpaired: s.HearingImpaired, Bitrate: d.BitRate, Width: d.Width, Height: d.Height,
			FrameRate: d.FrameRate, BitDepth: d.BitDepth, Channels: s.Channels, ChannelLayout: s.ChannelLayout, SampleRate: d.SampleRate}
		if d.DynamicRange != mediafacts.RangeSDR {
			stream.HDR = d.DynamicRange
		}
		out.Streams = append(out.Streams, stream)
	}
	for _, c := range facts.Chapters {
		out.Chapters = append(out.Chapters, db.Marker{StartMS: int64(math.Round(c.Start * 1000)), EndMS: int64(math.Round(c.End * 1000)), Title: c.Title})
	}
	return out, nil
}
