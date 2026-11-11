package mediaengine

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// An account held to a bitrate away from home gets a conversion within it there and the
// file as it is at home; an account that may not convert is refused a file its device cannot
// play. An account with no limits gets the file as it is, and the conversion.
func TestPlansAreHeldToTheAccountsLimits(t *testing.T) {
	film := streaming.FileFacts{
		ID: "m1", Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 600, Size: 600 << 20, Width: 1920, Height: 1080,
		DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true,
		Streams: []mediafacts.Stream{
			{Index: 0, Type: "video", Codec: "h264", Detail: mediafacts.StreamDetail{
				Profile: "High", Level: 40, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr", CodecTag: "avc1", BitRate: 8_000_000}},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Detail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}},
		},
	}
	plan := func(file streaming.FileFacts, network streaming.NetworkClass, limits db.PlayLimits) (streaming.DeliveryPlan, error) {
		request := streaming.PlanRequest{File: file, Settings: streaming.DefaultDeliveryConfiguration(), TranscodingEnabled: true,
			FragmentedOutput: true, Connection: streaming.DeliveryContext{NetworkClass: network}}
		request.Choice.AssetID, request.Choice.QualityID = file.ID, "original"
		holdToLimits(&request, limits)
		return streaming.Plan(request)
	}
	capped := db.PlayLimits{RemoteMaxBitrateKbps: 2000}
	if p, err := plan(film, streaming.NetworkRemote, db.PlayLimits{}); err != nil || p.Strategy != streaming.DeliveryOriginal {
		t.Fatalf("an account with no cap, away: %v %v, want the file as it is", p.Strategy, err)
	}
	if p, err := plan(film, streaming.NetworkRemote, capped); err != nil || p.VideoAction != "convert" || p.VideoBitrateBPS == 0 || p.VideoBitrateBPS > 2_000_000 {
		t.Fatalf("an account capped at 2,000 kbps, away: %v at %d bps (%v), want a conversion within the cap", p.VideoAction, p.VideoBitrateBPS, err)
	}
	if p, err := plan(film, streaming.NetworkLocal, capped); err != nil || p.Strategy != streaming.DeliveryOriginal {
		t.Fatalf("a capped account at home: %v %v, want the file as it is", p.Strategy, err)
	}

	old := film
	old.Container, old.VideoCodec = "avi", "mpeg4"
	old.Streams = append([]mediafacts.Stream(nil), film.Streams...)
	old.Streams[0] = mediafacts.Stream{Index: 0, Type: "video", Codec: "mpeg4", Detail: mediafacts.StreamDetail{
		Profile: "Advanced Simple", Width: 720, Height: 480, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr"}}
	if p, err := plan(old, streaming.NetworkLocal, db.PlayLimits{}); err != nil || p.VideoAction != "convert" {
		t.Fatalf("an account that may convert: %v %v, want a conversion", p.VideoAction, err)
	}
	if p, err := plan(old, streaming.NetworkLocal, db.PlayLimits{NoConversion: true}); err == nil {
		t.Fatalf("an account that may not convert was given %v of a file its device cannot play", p.Strategy)
	}
	if p, err := plan(film, streaming.NetworkLocal, db.PlayLimits{NoConversion: true}); err != nil || p.Strategy != streaming.DeliveryOriginal {
		t.Fatalf("an account that may not convert, a file its device plays: %v %v, want the file as it is", p.Strategy, err)
	}
}
