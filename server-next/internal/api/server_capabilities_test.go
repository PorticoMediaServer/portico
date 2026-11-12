package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/dvr"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
)

// toolsEngine is a media engine that says what its tools can do.
type toolsEngine struct {
	*fakeEngine
	caps mediaengine.Capabilities
}

func (e *toolsEngine) Capabilities(context.Context) mediaengine.Capabilities { return e.caps }

func capability(t *testing.T, all ServerCapabilities, area, name string) Capability {
	t.Helper()
	for _, c := range all.Items {
		if c.Area == area && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s %s in %+v", area, name, all.Items)
	return Capability{}
}

// "What this server can do" says what works and, for what does not, why: FFmpeg, each
// graphics backend tried, the folders the server writes to, and the decoder's protection. It
// is the engine's own answer, and the folders are looked at again only after a minute. Nobody
// but the owner may read it.
func TestWhatThisServerCanDoSaysWhatWorksAndWhyNot(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	clock := newRigClock(time.Now())
	s.setClock(clock.Now)
	read := func() ServerCapabilities { t.Helper(); return get[ServerCapabilities](t, s, owner, "/v1/admin/capabilities") }

	// Before the engine has answered, FFmpeg and the graphics devices are being checked.
	all := read()
	if c := capability(t, all, "ffmpeg", "ffmpeg"); c.State != "checking" {
		t.Fatalf("before the engine: %+v", c)
	}
	if c := capability(t, all, "folders", "data"); c.State != "works" || c.Path != s.dataDir {
		t.Fatalf("the data folder: %+v", c)
	}
	for _, name := range []string{"decoder_sandbox", "decoder_network_isolation"} {
		if c := capability(t, all, "protection", name); c.State == "" || c.State == "works" && c.Detail == "" {
			t.Fatalf("the protection: %+v", c)
		}
	}

	conversions, recordings := t.TempDir(), filepath.Join(t.TempDir(), "Recorded TV")
	s.setDVROptions(dvr.Options{Folder: recordings})
	engine := &toolsEngine{fakeEngine: &fakeEngine{}, caps: mediaengine.Capabilities{Converter: true, FFmpeg: "/opt/ffmpeg", Version: "7.1",
		ConversionFolder: conversions, Graphics: []decoder.HardwareProbe{
			{Backend: decoder.BackendNVENC, Reason: decoder.ProbeEncodeFailed},
			{Backend: decoder.BackendVAAPI, Available: true, Encoder: "h264_vaapi"},
		}, VulkanProblem: "no device"}}
	s.playback.SetEngine(engine, engine)
	clock.Add(time.Minute)
	all = read()
	if c := capability(t, all, "ffmpeg", "ffmpeg"); c.State != "works" || c.Path != "/opt/ffmpeg" || c.Detail != "FFmpeg 7.1" {
		t.Fatalf("FFmpeg: %+v", c)
	}
	if c := capability(t, all, "graphics", "graphics"); c.State != "works" {
		t.Fatalf("the graphics devices: %+v", c)
	}
	if c := capability(t, all, "graphics", "nvenc"); c.State != "does_not_work" || c.Reason != decoder.ProbeEncodeFailed || c.Detail == "" {
		t.Fatalf("NVENC: %+v", c)
	}
	if c := capability(t, all, "graphics", "vaapi"); c.State != "works" {
		t.Fatalf("VA-API: %+v", c)
	}
	if c := capability(t, all, "graphics", "vulkan"); c.State != "does_not_work" || c.Reason != "vulkan_unavailable" {
		t.Fatalf("Vulkan: %+v", c)
	}
	if c := capability(t, all, "folders", "conversions"); c.State != "works" || c.Path != conversions {
		t.Fatalf("the conversions folder: %+v", c)
	}
	if c := capability(t, all, "folders", "recordings"); c.State != "not_used" {
		t.Fatalf("a recordings folder not made yet: %+v", c)
	}
	if c := capability(t, all, "folders", "backups"); c.State != "works" {
		t.Fatalf("the backups folder: %+v", c)
	}

	// A folder the server cannot write to says so, and is looked at again only after a minute.
	if os.Geteuid() != 0 {
		os.Chmod(conversions, 0o500)
		t.Cleanup(func() { os.Chmod(conversions, 0o700) })
		if c := capability(t, read(), "folders", "conversions"); c.State != "works" {
			t.Fatalf("looked at again within the minute: %+v", c)
		}
		clock.Add(time.Minute)
		if c := capability(t, read(), "folders", "conversions"); c.State != "does_not_work" || c.Reason != "not_writable" {
			t.Fatalf("a folder it cannot write to: %+v", c)
		}
	}

	// No graphics device works; software encoding chosen; no FFmpeg at all.
	engine.caps.Graphics = engine.caps.Graphics[:1]
	if c := capability(t, read(), "graphics", "graphics"); c.State != "does_not_work" || c.Reason != decoder.HardwareFactReasonNoDevice {
		t.Fatalf("no graphics device works: %+v", c)
	}
	engine.caps.SoftwareEncoding = true
	if c := capability(t, read(), "graphics", "graphics"); c.State != "not_used" || c.Reason != "software_encoding_chosen" {
		t.Fatalf("software encoding chosen: %+v", c)
	}
	engine.caps = mediaengine.Capabilities{ConversionFolder: conversions}
	all = read()
	if c := capability(t, all, "ffmpeg", "ffmpeg"); c.State != "does_not_work" || c.Reason != "ffmpeg_not_found" {
		t.Fatalf("no FFmpeg: %+v", c)
	}
	if c := capability(t, all, "graphics", "graphics"); c.State != "does_not_work" || c.Reason != "no_ffmpeg" {
		t.Fatalf("graphics without FFmpeg: %+v", c)
	}

	for _, token := range []string{"", memberToken(t, s, owner)} {
		if code := status(t, s, "GET", "/v1/admin/capabilities", token, nil); code != 401 && code != 403 {
			t.Errorf("someone other than the owner was answered %d", code)
		}
	}
}
