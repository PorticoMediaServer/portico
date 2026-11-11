package settings

import "slices"

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
	// graphics device), or one kind of device by name (nvenc, qsv, vaapi, videotoolbox,
	// amf). One that is missing or cannot take a conversion leaves it to the processor.
	GraphicsDevice string `json:"graphicsDevice"`
	// HardwareDecoding lets the graphics device read the file as well as write the
	// conversion. Turning it off is for troubleshooting.
	HardwareDecoding bool `json:"hardwareDecoding"`
	// ConversionsAtOnce is how many videos may be converted at once, 0 for no limit. A play
	// that needs converting at the limit waits and starts by itself.
	ConversionsAtOnce int `json:"conversionsAtOnce"`
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
	HardwareDecoding: true}, checkStreaming)

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
	if s.GraphicsDevice != "automatic" && s.GraphicsDevice != "processor" && !slices.Contains(GraphicsDevices, s.GraphicsDevice) {
		return &Invalid{Field: "graphicsDevice", Message: "Choose Automatic, Processor only or one of the graphics devices listed."}
	}
	if s.ConversionsAtOnce < 0 || s.ConversionsAtOnce > 1000 {
		return &Invalid{Field: "conversionsAtOnce", Message: "Give a number of conversions, or 0 for no limit."}
	}
	return nil
}
