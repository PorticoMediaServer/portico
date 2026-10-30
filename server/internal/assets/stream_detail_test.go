package assets

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"testing"
)

func TestStreamDetailParsesPlanningFacts(t *testing.T) {
	var s probeStreamDetail
	s.Profile, s.Level, s.PixelFormat, s.AvgFrameRate, s.FieldOrder, s.SampleAspect, s.BitRate, s.Refs = "Main 10", 153, "yuv420p10le", "24000/1001", "progressive", "1:1", "N/A", 4
	s.ColorTransfer = "smpte2084"
	s.Tags = map[string]string{"BPS-eng": "25000000"}
	d := streamDetail("video", "hevc", 3840, 2160, s)
	if d.BitDepth != 10 || d.FrameRate != 23.976 || d.Interlaced || d.Anamorphic || d.BitRate != 25000000 || d.DynamicRange != RangeHDR10 || d.Level != 153 || d.Height != 2160 {
		t.Fatalf("unexpected detail %+v", d)
	}
	s.FieldOrder, s.SampleAspect, s.ColorTransfer = "tt", "64:45", "bt2020-10"
	d = streamDetail("video", "mpeg2video", 720, 576, s)
	if !d.Interlaced || !d.Anamorphic || d.DynamicRange != RangeSDR {
		t.Fatalf("interlace, anamorphic or wide-gamut SDR misread: %+v", d)
	}
	if got := pixelBitDepth("p010le"); got != 10 {
		t.Fatalf("p010le depth %d", got)
	}
	if got := pixelBitDepth("yuv420p"); got != 8 {
		t.Fatalf("yuv420p depth %d", got)
	}
	var a probeStreamDetail
	a.Profile, a.SampleRate = "Dolby TrueHD + Dolby Atmos", "48000"
	if d = streamDetail("audio", "truehd", 0, 0, a); d.ObjectAudio != "atmos" || d.SampleRate != 48000 {
		t.Fatalf("atmos misread: %+v", d)
	}
}

func TestDolbyVisionSideData(t *testing.T) {
	var s probeStreamDetail
	s.ColorTransfer = "smpte2084"
	s.SideData = append(s.SideData, probeSideData{Type: "DOVI configuration record", Profile: 8, Level: 6, Compatibility: 1, RPU: 1, BaseLayer: 1})
	d := streamDetail("video", "hevc", 3840, 2160, s)
	if d.DolbyVisionRPU || d.DolbyVisionBaseLayer || d.DolbyVisionMetadataKnown {
		t.Fatal("configuration flags became actual packet observations")
	}
	if d.DynamicRange != RangeDolbyVision || d.DolbyVisionProfile != 8 || d.DolbyVisionCompatibility != 1 {
		t.Fatalf("dolby vision misread: %+v", d)
	}
}

func TestInspectRecordsDetailForRealMedia(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	decodertest.QualifiedFFprobe(t)
	path := filepath.Join(t.TempDir(), "sample.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-ac", "6", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "ac3", "-disposition:a", "visual_impaired", path).CombinedOutput()
	if err != nil {
		decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
	}
	facts, err := Probe{}.Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var video, audio *Stream
	for i := range facts.Streams {
		switch facts.Streams[i].Type {
		case "video":
			video = &facts.Streams[i]
		case "audio":
			audio = &facts.Streams[i]
		}
	}
	if video == nil || audio == nil {
		t.Fatalf("streams missing: %+v", facts.Streams)
	}
	if video.Detail.BitDepth != 8 || video.Detail.FrameRate != 25 || video.Detail.Height != 180 || video.Detail.DynamicRange != RangeSDR || video.Detail.Profile == "" {
		t.Fatalf("video detail %+v", video.Detail)
	}
	if audio.Channels != 6 || audio.Detail.SampleRate != 48000 || !audio.Detail.AudioDescription {
		t.Fatalf("audio detail %+v channels %d", audio.Detail, audio.Channels)
	}
	var restored Stream
	restored.DecodeDetail(video.EncodeDetail())
	expected := video.Detail
	expected.StreamBreadth = StreamBreadth{}
	if restored.Detail != expected {
		t.Fatalf("detail did not survive storage: %+v", restored.Detail)
	}
}
