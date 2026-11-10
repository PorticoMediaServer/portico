package streaming

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func planTestDevice(t *testing.T, name string) *CapabilityDocument {
	t.Helper()
	raw, err := os.ReadFile("testdata/client-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents map[string]CapabilityDocument
	if err := json.Unmarshal(raw, &documents); err != nil {
		t.Fatal(err)
	}
	d, ok := documents[name]
	if !ok {
		t.Fatalf("no shipped profile %q", name)
	}
	if err := d.Accept(); err != nil {
		t.Fatal(err)
	}
	return &d
}

// Plan takes plain values only. A file the browser plays is sent as it is; a
// file it cannot play is converted, or refused when the owner has turned
// conversion off.
func TestPlanFromFileFacts(t *testing.T) {
	browser := planTestDevice(t, "chromium")
	playable := FileFacts{
		ID: "m1", Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 600, Size: 300 << 20, Width: 1920, Height: 1080,
		DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true,
		Streams: []mediafacts.Stream{
			{Index: 0, Type: "video", Codec: "h264", Detail: mediafacts.StreamDetail{
				Profile: "High", Level: 40, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr", CodecTag: "avc1"}},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Detail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}},
		},
	}
	plan, err := Plan(PlanRequest{File: playable, Device: browser, Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	if err != nil || plan.Strategy != DeliveryOriginal || plan.SourceID != "m1" {
		t.Fatalf("a playable file was not sent as it is: %+v %v", plan.Strategy, err)
	}

	unplayable := playable
	unplayable.Container, unplayable.VideoCodec = "avi", "mpeg4"
	unplayable.Streams = append([]mediafacts.Stream(nil), playable.Streams...)
	unplayable.Streams[0] = mediafacts.Stream{Index: 0, Type: "video", Codec: "mpeg4", Detail: mediafacts.StreamDetail{
		Profile: "Advanced Simple", Width: 720, Height: 480, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr"}}
	request := PlanRequest{File: unplayable, Device: browser, Settings: DefaultDeliveryConfiguration(), TranscodingEnabled: true}
	plan, err = Plan(request)
	if err != nil || plan.VideoAction != "convert" || len(plan.ReasonCodes) == 0 {
		t.Fatalf("an unplayable picture was not converted with a reason: %+v %v", plan, err)
	}
	request.TranscodingEnabled = false
	var refused ErrDeliveryRefused
	if _, err = Plan(request); !errors.As(err, &refused) && !errors.Is(err, ErrTranscodingDisabled) {
		t.Fatalf("conversion turned off was not refused: %v", err)
	}
}
