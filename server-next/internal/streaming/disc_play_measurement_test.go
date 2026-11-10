//go:build linux || darwin

package streaming

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/disc"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// These regressions exercise the pure measured-snapshot, title-selection,
// preparation-context and configured-input helpers. They use source fixtures
// only: no decoder, helper, provider, source actor or real media bytes.

func discPlaylistNumber(n int) *int { return &n }

func discMeasurementStructure() disc.Probe {
	return disc.Probe{
		Schema: 1, Kind: "bluray", Packaging: "iso", Encryption: "none",
		Titles: []disc.Title{
			{DiscOrder: 1, Playlist: discPlaylistNumber(800), DurationMs: 3_600_000, AngleCount: 1},
			{DiscOrder: 2, Playlist: discPlaylistNumber(801), DurationMs: 300_000, AngleCount: 1},
		},
	}
}

func discMeasurementTitle() disc.Title { return discMeasurementStructure().Titles[0] }

func discMeasurementFacts() mediafacts.Facts {
	return mediafacts.Facts{
		Container: "m2ts", VideoCodec: "h264", AudioCodec: "flac",
		Width: 1920, Height: 1080,
		Streams: []mediafacts.Stream{
			{Index: 0, Type: "video", Codec: "h264", Default: true, Detail: mediafacts.StreamDetail{Width: 1920, Height: 1080, BitDepth: 8, DynamicRange: mediafacts.RangeSDR}},
			{
				Index:    1,
				Type:     "audio",
				Codec:    "flac",
				Channels: 2,
				Default:  true,
				Language: "eng",
				Detail:   mediafacts.StreamDetail{StreamBreadth: mediafacts.StreamBreadth{AudioVariant: "flac"}, SampleRate: 48_000, BitsPerSample: 24},
			},
		},
		Chapters: []mediafacts.Chapter{{Title: "Opening", Start: 0, End: 120}},
	}
}
