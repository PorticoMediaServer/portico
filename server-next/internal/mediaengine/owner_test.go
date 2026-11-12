package mediaengine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// changed names the fields of the runtime's configuration that differ between two.
func changed(a, b streaming.DeliveryConfiguration) []string {
	var out []string
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := 0; i < va.NumField(); i++ {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, va.Type().Field(i).Name)
		}
	}
	slices.Sort(out)
	return out
}

// Each of the owner's streaming settings changes exactly the one thing in the plan's input
// that it is for, and a server on which nothing was saved plans as it did before it read any
// settings (but for naming the conversion mode, which means the same).
func TestEachStreamingSettingChangesExactlyWhatItIsFor(t *testing.T) {
	saved := settings.Streaming.Get(nil) // what a server with nothing saved has
	base := delivery(saved)
	if fields := changed(streaming.DefaultDeliveryConfiguration().Normalized(), base); !slices.Equal(fields, []string{"ConversionMode"}) ||
		!base.AudioConversionAllowed(true) || !base.VideoConversionAllowed(true) {
		t.Fatalf("with nothing saved the plan's settings differ from before in %v", fields)
	}
	for name, c := range map[string]struct {
		set    func(*settings.StreamingSettings)
		fields []string
		holds  func(streaming.DeliveryConfiguration) bool
	}{
		"convert sound only": {func(s *settings.StreamingSettings) { s.ConvertWhenNeeded = settings.ConvertSoundOnly }, []string{"ConversionMode"},
			func(c streaming.DeliveryConfiguration) bool {
				return c.AudioConversionAllowed(true) && !c.VideoConversionAllowed(true)
			}},
		"convert never": {func(s *settings.StreamingSettings) { s.ConvertWhenNeeded = settings.ConvertNever }, []string{"ConversionMode"},
			func(c streaming.DeliveryConfiguration) bool {
				return !c.AudioConversionAllowed(true) && !c.VideoConversionAllowed(true)
			}},
		"software encoding": {func(s *settings.StreamingSettings) { s.GraphicsDevice = "processor" }, []string{"HardwareBackend", "HardwareSelection"},
			func(c streaming.DeliveryConfiguration) bool { return c.HardwareBackend == "software" }},
		"one kind of graphics device": {func(s *settings.StreamingSettings) { s.GraphicsDevice = "qsv" }, []string{"HardwareBackend"},
			func(c streaming.DeliveryConfiguration) bool { return c.HardwareBackend == "qsv" }},
		"hardware decoding off": {func(s *settings.StreamingSettings) { s.HardwareDecoding = false }, []string{"SoftwareDecode"},
			func(c streaming.DeliveryConfiguration) bool { return c.SoftwareDecode }},
		"three conversions at once": {func(s *settings.StreamingSettings) { s.ConversionsAtOnce = 3 }, []string{"MaxConversions"},
			func(c streaming.DeliveryConfiguration) bool { return c.MaxConversions == 3 }},
		"tone mapping off": {func(s *settings.StreamingSettings) { s.ToneMapping = false }, []string{"ToneMapping"},
			func(c streaming.DeliveryConfiguration) bool { return !c.ToneMapping }},
		// These three are not the planner's: the away limits and the upload speed are applied
		// where a play is admitted (playback/away.go), the folder and program by SetConversion.
		"away limit":        {func(s *settings.StreamingSettings) { s.AwayVideoKbps, s.AwayMusicKbps = 4000, 320 }, nil, nil},
		"upload speed":      {func(s *settings.StreamingSettings) { s.UploadSpeedKbps = 20000 }, nil, nil},
		"folder and ffmpeg": {func(s *settings.StreamingSettings) { s.ConversionFolder, s.FFmpegProgram = "/mnt/fast", "/opt/ffmpeg" }, nil, nil},
	} {
		owner := saved
		c.set(&owner)
		got := delivery(owner)
		if fields := changed(base, got); !slices.Equal(fields, c.fields) || c.holds != nil && !c.holds(got) {
			t.Errorf("%s: changed %v, want %v: %+v", name, fields, c.fields, got)
		}
	}
}

// How a request reached the server is told to the planner: home or away, mobile data when the
// app says so, and the app's language.
func TestTheConnectionIsToldToThePlanner(t *testing.T) {
	for network, want := range map[string]streaming.DeliveryContext{
		playback.NetworkLocal:    {NetworkClass: streaming.NetworkLocal, AppLanguage: "fr-CA"},
		playback.NetworkRemote:   {NetworkClass: streaming.NetworkRemote, AppLanguage: "fr-CA"},
		playback.NetworkCellular: {NetworkClass: streaming.NetworkRemote, TransportClass: "cellular", AppLanguage: "fr-CA"},
	} {
		var got streaming.DeliveryContext
		connection(&got, playback.PlanRequest{Network: network, AppLanguage: "fr-CA"})
		if got != want {
			t.Errorf("%s: %+v, want %+v", network, got, want)
		}
	}
}

// The conversion-device setting reaches the decoder package, where pictures, previews and
// linear outputs are planned: with software encoding chosen it sees an FFmpeg without the
// graphics-device filters, with Automatic or a named device it sees them.
func TestTheDeviceSettingReachesEveryJobMadeFromFFmpegsFacts(t *testing.T) {
	defer decoder.RestoreToolchain(decoder.CurrentToolchain())
	defer decoder.SetSoftwareEncodingChosen(nil)
	for device, software := range map[string]bool{"processor": true, "automatic": false, "": false, "vaapi": false} {
		if got := softwareEncoding(settings.StreamingSettings{GraphicsDevice: device}); got != software {
			t.Fatalf("graphics device %q counted as software encoding: %v", device, got)
		}
	}
	// An engine on which nothing was saved has Automatic.
	(&Engine{}).shareDeviceSetting()
	decoder.RestoreToolchain(&decoder.ToolchainFacts{VulkanAvailable: true})
	if !decoder.CurrentToolchain().VulkanAvailable {
		t.Fatal("with Automatic the graphics-device filters were hidden")
	}
	decoder.SetSoftwareEncodingChosen(func() bool { return softwareEncoding(settings.StreamingSettings{GraphicsDevice: "processor"}) })
	if decoder.CurrentToolchain().VulkanAvailable {
		t.Fatal("with software encoding chosen the decoder still sees a Vulkan device")
	}
}

// The same through the real settings store, as the console saves it: with software encoding
// saved, a thumbnail of a Dolby Vision profile 5 film is not planned on the graphics device
// (it is refused, as on a server without one), and one of an HDR10 film is planned with no
// device filter; with Automatic saved again the profile 5 thumbnail uses the device, with no
// restart in between.
func TestSavingTheDeviceSettingChangesWhatAThumbnailIsPlannedWith(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	store, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.RestoreToolchain(decoder.CurrentToolchain())
	defer decoder.SetSoftwareEncodingChosen(nil)
	(&Engine{settings: store}).shareDeviceSetting()
	// What the trial at start found on a server with a working graphics device.
	decoder.RestoreToolchain(&decoder.ToolchainFacts{VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true, "zscale": true, "tonemap": true}})

	save := func(device string) {
		t.Helper()
		change, _ := json.Marshal(map[string]string{"graphicsDevice": device})
		if _, err := store.Write(ctx, 0, settings.Streaming.Name(), change, -1, time.Now()); err != nil {
			t.Fatalf("saving graphics device %q: %v", device, err)
		}
	}
	thumbnail := func(video decoder.PreviewVideoFacts) (string, error) {
		args, err := decoder.AnalysisArgs("input", decoder.AnalysisSpec{Kind: "image", MaxDurationUS: 60000000, SourceVideo: video})
		return strings.Join(args, " "), err
	}
	profile5 := decoder.PreviewVideoFacts{DynamicRange: "dolby_vision", DolbyVisionProfile: 5}
	usesDevice := func(args string) bool {
		return strings.Contains(args, "vulkan") || strings.Contains(args, "opencl") || strings.Contains(args, "libplacebo")
	}

	save("processor")
	if args, err := thumbnail(profile5); err == nil {
		t.Fatalf("with software encoding saved a profile 5 thumbnail was planned: %s", args)
	}
	if args, err := thumbnail(decoder.PreviewVideoFacts{DynamicRange: "hdr10"}); err != nil || usesDevice(args) {
		t.Fatalf("with software encoding saved an HDR10 thumbnail: %s %v", args, err)
	}
	save("automatic")
	if args, err := thumbnail(profile5); err != nil || !usesDevice(args) {
		t.Fatalf("with Automatic saved again a profile 5 thumbnail does not use the device: %s %v", args, err)
	}
}
