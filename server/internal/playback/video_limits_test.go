package playback

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func limitedDevice() CapabilityDocument {
	d := BaselineCapabilityDocument()
	d.Client.Family = "android"
	d.Engines.Native.Evidence = EvidenceProbed
	d.Engines.Native.Video = []ClientVideoCodec{{
		Codec: "h264", BitDepths: []int{8, 10}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60, DynamicRanges: []string{"sdr"},
		// 4K only at 30; 1080p at 60; 10-bit only up to 1080p30.
		Limits: []ClientVideoLimit{
			{BitDepths: []int{8}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 30},
			{BitDepths: []int{8}, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 60},
			{BitDepths: []int{10}, MaxWidth: 1920, MaxHeight: 1080, MaxFrameRate: 30},
		},
	}}
	return d
}

func h264Stream(depth, width, height int, rate float64) *SourceVideo {
	return &SourceVideo{Codec: "h264", StreamDetail: assets.StreamDetail{Profile: "High", BitDepth: depth, Width: width, Height: height, FrameRate: rate, DynamicRange: assets.RangeSDR}}
}

func TestOperatingPointsAdmitEachPictureAgainstItsOwnLimit(t *testing.T) {
	d := limitedDevice()
	raw, _ := json.Marshal(d)
	parsed, err := ParseCapabilityDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := parsed.Profile(EngineNative)
	transport := ClientTransport{Transport: TransportDirect, Video: []string{"h264"}}
	cases := []struct {
		name  string
		video *SourceVideo
		want  string
	}{
		{"4K at 24 fits the 4K point", h264Stream(8, 3840, 2160, 23.976), ""},
		{"1080p at 60 fits the 1080p point", h264Stream(8, 1920, 1080, 59.94), ""},
		{"4K at 60 fits no point", h264Stream(8, 3840, 2160, 60), RejectFrameRate},
		{"10-bit 1080p at 24 fits the 10-bit point", h264Stream(10, 1920, 1080, 24), ""},
		{"10-bit 4K is beyond the 10-bit point", h264Stream(10, 3840, 2160, 24), RejectResolution},
		{"12-bit has no point", h264Stream(12, 1920, 1080, 24), RejectVideoBitDepth},
	}
	for _, c := range cases {
		rejects := videoRejections(c.video, client, transport, true, false)
		got := ""
		if len(rejects) > 0 {
			got = rejects[0].Code
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %q", c.name, rejects, c.want)
		}
	}
}

func TestConversionKeepsMotionBeforeSize(t *testing.T) {
	codec := limitedDevice().Engines.Native.Video[0]
	codec.Limits = normalizeVideoLimits(codec.Limits)
	point := codec.forConversion(8, 3840, 2160, 60)
	if point.MaxHeight != 1080 || point.MaxFrameRate != 60 {
		t.Fatalf("a 4K60 source should become 1080p60, got %dx%d@%v", point.MaxWidth, point.MaxHeight, point.MaxFrameRate)
	}
	point = codec.forConversion(8, 3840, 2160, 24)
	if point.MaxHeight != 2160 || point.MaxFrameRate != 30 {
		t.Fatalf("a 4K24 source should stay 4K, got %dx%d@%v", point.MaxWidth, point.MaxHeight, point.MaxFrameRate)
	}
	point = codec.forConversion(10, 3840, 2160, 24)
	if point.MaxHeight != 1080 {
		t.Fatalf("a 10-bit picture uses the 10-bit point, got %d", point.MaxHeight)
	}
	if !codec.decodesDepth(10) || codec.decodesDepth(12) {
		t.Fatal("depths decoded are the depths with an operating point")
	}
}

func TestOperatingPointsAreBoundedAndCanonical(t *testing.T) {
	d := limitedDevice()
	d.Engines.Native.Video[0].Limits = append(d.Engines.Native.Video[0].Limits, ClientVideoLimit{})
	if err := d.Accept(); err == nil {
		t.Fatal("a point that bounds nothing was accepted")
	}
	a, b := limitedDevice(), limitedDevice()
	l := b.Engines.Native.Video[0].Limits
	l[0], l[2] = l[2], l[0]
	if a.Accept() != nil || b.Accept() != nil || a.Revision != b.Revision {
		t.Fatal("the order of operating points changed the revision")
	}
	many := limitedDevice()
	for i := 0; i < maxVideoLimits; i++ {
		many.Engines.Native.Video[0].Limits = append(many.Engines.Native.Video[0].Limits, ClientVideoLimit{MaxHeight: 480})
	}
	if many.Accept() == nil {
		t.Fatal("too many operating points were accepted")
	}
}
