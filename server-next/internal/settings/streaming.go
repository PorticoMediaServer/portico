package settings

import (
	"path/filepath"
	"slices"
	"strings"
)

// StreamingSettings is Settings › Streaming: how plays are converted and how much a play away
// from home may take. Every field applies to plays that start, and quality changes made,
// after it is saved; a stream already playing is never lowered, paused or stopped by one.
type StreamingSettings struct {
	// ConvertWhenNeeded is what the server may convert for a device that cannot play a file
	// as it is: "picture-and-sound", "sound" (the picture is never converted) or "never".
	ConvertWhenNeeded string `json:"convertWhenNeeded"`
	// AwayVideoKbps is the most a film or episode played away from home may take, in kbps
	// (a rung of the quality ladder or a number the owner typed); 0 is Original, no limit.
	AwayVideoKbps int `json:"awayVideoKbps"`
	// AwayMusicKbps is the same for music and audiobooks.
	AwayMusicKbps int `json:"awayMusicKbps"`
	// UploadSpeedKbps is the home connection's upload speed, 0 when the owner has not given
	// it. Away streams share 80% of it: a play away from home is offered only what fits in
	// what is left, and is refused when not even the lowest quality fits.
	UploadSpeedKbps int `json:"uploadSpeedKbps"`
	// GraphicsDevice converts video: "automatic" (the best one found), "processor" (no
	// graphics device), or an observed physical device ID. Older backend names still work.
	// A missing or unavailable choice falls through the other devices, then the processor.
	GraphicsDevice string `json:"graphicsDevice"`
	// HardwareDecoding lets the graphics device read the file as well as write the
	// conversion. Turning it off is for troubleshooting.
	HardwareDecoding bool `json:"hardwareDecoding"`
	// ConversionsAtOnce is how many videos may be converted at once, 0 for no limit. A play
	// that needs converting at the limit waits and starts by itself.
	ConversionsAtOnce int `json:"conversionsAtOnce"`
	// ProcessorConversionsAtOnce limits picture conversions on the processor alone; 0 is no limit.
	ProcessorConversionsAtOnce int `json:"processorConversionsAtOnce"`
	// BackgroundConversionsAtOnce caps prepared copies made in the background, independently
	// of plays. Zero adds no owner cap; work already running completes.
	BackgroundConversionsAtOnce int `json:"backgroundConversionsAtOnce"`
	// ToneMapping converts an HDR picture to standard range for a screen that cannot show
	// HDR. With it off such a picture is converted without it and looks washed out; that is
	// for troubleshooting.
	ToneMapping bool `json:"toneMapping"`
	// EncoderSpeed is the processor encoder's speed/size trade-off.
	EncoderSpeed string `json:"encoderSpeed"`
	// PlanningPolicy chooses the cheapest route or more conversion for compatibility.
	PlanningPolicy string `json:"planningPolicy"`
	// Repackaging allows changing the container without converting the picture.
	Repackaging bool `json:"repackaging"`
	// ConversionFormat is automatic, or H.264 for devices that misreport newer formats.
	ConversionFormat string `json:"conversionFormat"`
	// ConvertAheadSeconds paces a converter once it is this far ahead of the viewer.
	ConvertAheadSeconds int `json:"convertAheadSeconds"`
	// KeepForRewindingSeconds retains played pieces; 0 keeps none behind the current piece.
	KeepForRewindingSeconds int `json:"keepForRewindingSeconds"`
	// SpacePerStreamMiB bounds one stream's generated output, removing old pieces first.
	SpacePerStreamMiB int `json:"spacePerStreamMiB"`
	// MemoryLimit bounds aggregate RAM output, with the existing spill to disk when full.
	MemoryLimit string `json:"memoryLimit"`
	// ConversionFolder is a folder of the owner's choosing for conversions; empty is the
	// server's own data folder. The server keeps a folder of its own inside it and never
	// empties anything else. It applies to streams started after it is saved.
	ConversionFolder string `json:"conversionFolder"`
	// FFmpegProgram is the full path of the ffmpeg to convert with; empty is the one the
	// server found. It is checked before it is used, and streams that are playing finish with
	// the one they started with.
	FFmpegProgram string `json:"ffmpegProgram"`
}

// The values of ConvertWhenNeeded.
const (
	ConvertPictureAndSound = "picture-and-sound"
	ConvertSoundOnly       = "sound"
	ConvertNever           = "never"
)

// GraphicsDevices are the values GraphicsDevice takes besides "automatic" and "processor".
var GraphicsDevices = []string{"nvenc", "qsv", "vaapi", "videotoolbox", "amf"}

// Streaming is the streaming settings document. Playback and the media engine read it.
var Streaming = Define("streaming", StreamingSettings{ConvertWhenNeeded: ConvertPictureAndSound, GraphicsDevice: "automatic",
	HardwareDecoding: true, ToneMapping: true, EncoderSpeed: "veryfast", PlanningPolicy: "maximum_fidelity",
	Repackaging: true, ConversionFormat: "auto", ConvertAheadSeconds: 60, KeepForRewindingSeconds: 180,
	SpacePerStreamMiB: 4096, MemoryLimit: "quarter"}, checkStreaming)

func checkStreaming(s *StreamingSettings) error {
	switch s.ConvertWhenNeeded {
	case ConvertPictureAndSound, ConvertSoundOnly, ConvertNever:
	default:
		return &Invalid{Field: "convertWhenNeeded", Message: "Choose Picture and sound, Sound only or Never."}
	}
	for _, f := range []struct {
		name  string
		value int
	}{{"awayVideoKbps", s.AwayVideoKbps}, {"awayMusicKbps", s.AwayMusicKbps}, {"uploadSpeedKbps", s.UploadSpeedKbps}} {
		// 10 Gbit/s is more than any home connection or any file.
		if f.value < 0 || f.value > 10_000_000 {
			return &Invalid{Field: f.name, Message: "Give a speed in kbps, or 0 for no limit."}
		}
	}
	if s.GraphicsDevice != "automatic" && s.GraphicsDevice != "processor" && !slices.Contains(GraphicsDevices, s.GraphicsDevice) &&
		(s.GraphicsDevice == "" || len(s.GraphicsDevice) > 512 || strings.ContainsAny(s.GraphicsDevice, "\x00\r\n")) {
		return &Invalid{Field: "graphicsDevice", Message: "Choose Automatic, Processor only or one of the graphics devices listed."}
	}
	if !slices.Contains([]int{0, 1, 2, 3, 4, 6}, s.ProcessorConversionsAtOnce) {
		return &Invalid{Field: "processorConversionsAtOnce", Message: "Choose no limit, 1, 2, 3, 4 or 6 conversions."}
	}
	if !slices.Contains([]string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow"}, s.EncoderSpeed) {
		return &Invalid{Field: "encoderSpeed", Message: "Choose one of the listed encoder speeds."}
	}
	if !slices.Contains([]string{"maximum_fidelity", "minimize_server_work", "maximum_compatibility"}, s.PlanningPolicy) {
		return &Invalid{Field: "planningPolicy", Message: "Choose the least conversion or more conversion for compatibility."}
	}
	if s.ConversionFormat != "auto" && s.ConversionFormat != "h264" {
		return &Invalid{Field: "conversionFormat", Message: "Choose Automatic or H.264."}
	}
	if !slices.Contains([]int{20, 60, 120, 300, 600}, s.ConvertAheadSeconds) {
		return &Invalid{Field: "convertAheadSeconds", Message: "Choose 20, 60, 120, 300 or 600 seconds."}
	}
	if !slices.Contains([]int{0, 60, 180, 600, 1800, 3600}, s.KeepForRewindingSeconds) {
		return &Invalid{Field: "keepForRewindingSeconds", Message: "Choose no keeping, 1, 3, 10, 30 or 60 minutes."}
	}
	if !slices.Contains([]int{1024, 2048, 4096, 8192, 16384, 32768}, s.SpacePerStreamMiB) {
		return &Invalid{Field: "spacePerStreamMiB", Message: "Choose 1, 2, 4, 8, 16 or 32 GB."}
	}
	if !slices.Contains([]string{"quarter", "half", "2gb", "4gb", "8gb", "16gb"}, s.MemoryLimit) {
		return &Invalid{Field: "memoryLimit", Message: "Choose a quarter or half of free memory, or one of the listed sizes."}
	}
	s.ConversionFolder, s.FFmpegProgram = strings.TrimSpace(s.ConversionFolder), strings.TrimSpace(s.FFmpegProgram)
	for _, f := range []struct{ name, value string }{{"conversionFolder", s.ConversionFolder}, {"ffmpegProgram", s.FFmpegProgram}} {
		if f.value != "" && (!filepath.IsAbs(f.value) || len(f.value) > 4096) {
			return &Invalid{Field: f.name, Message: "Give the full path, or leave it empty for the server's own."}
		}
	}
	for _, f := range []struct {
		name  string
		value int
	}{
		{"conversionsAtOnce", s.ConversionsAtOnce}, {"backgroundConversionsAtOnce", s.BackgroundConversionsAtOnce},
	} {
		if f.value < 0 || f.value > 1000 {
			return &Invalid{Field: f.name, Message: "Give a number of conversions, or 0 for no limit."}
		}
	}
	return nil
}
