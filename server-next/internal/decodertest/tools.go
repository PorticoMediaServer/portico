// Package decodertest keeps real-media tests on the same verified toolchain.
package decodertest

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func Unavailable(t testing.TB, reason string) {
	t.Helper()
	if os.Getenv("PORTICO_REQUIRE_QUALIFIED_FFMPEG") == "1" {
		t.Fatal(reason)
	}
	t.Skip(reason)
}

// The bundle is an immutable test fixture. Qualify the pair once per test
// process, not once for every generated frame/packet assertion. Production
// resolution remains uncached and verifies every newly discovered bundle.
type toolPair struct{ ffmpeg, probe string }

var bundleTools = sync.OnceValues(func() (toolPair, error) {
	ffmpeg, err := mediatools.DevelopmentTool("ffmpeg")
	if err != nil {
		return toolPair{}, err
	}
	probe, err := exec.LookPath(filepath.Join(filepath.Dir(ffmpeg), mediatools.Name("ffprobe")))
	return toolPair{ffmpeg, probe}, err
})

// named is the FFmpeg the person running the tests chose, as the server reads it:
// PORTICO_FFMPEG, and PORTICO_FFPROBE or else the ffprobe beside that FFmpeg. "" when
// neither is set. A test run against a bundle must run against it, never quietly
// against whatever FFmpeg is on the PATH.
func named(tool string) string {
	ffmpeg := os.Getenv("PORTICO_FFMPEG")
	if tool == "ffmpeg" {
		return ffmpeg
	}
	if probe := os.Getenv("PORTICO_FFPROBE"); probe != "" {
		return probe
	}
	if ffmpeg != "" {
		return filepath.Join(filepath.Dir(ffmpeg), mediatools.Name("ffprobe"))
	}
	return ""
}

func qualified(t testing.TB, tool string) string {
	t.Helper()
	if path := named(tool); path != "" {
		found, err := exec.LookPath(path)
		if err != nil {
			t.Fatalf("the %s named by PORTICO_FFMPEG or PORTICO_FFPROBE cannot be run: %v", tool, err)
		}
		return found
	}
	pair, err := bundleTools()
	if err == nil {
		if tool == "ffprobe" {
			return pair.probe
		}
		return pair.ffmpeg
	}
	if !os.IsNotExist(err) {
		t.Fatalf("qualified %s: %v", tool, err)
	}
	if os.Getenv("PORTICO_REQUIRE_QUALIFIED_FFMPEG") == "1" {
		t.Fatalf("qualified %s bundle required: %v", tool, err)
	}
	path, err := exec.LookPath(mediatools.Name(tool))
	if err != nil {
		Unavailable(t, tool+" unavailable")
	}
	return path
}
func QualifiedFFmpeg(t testing.TB) string  { t.Helper(); return qualified(t, "ffmpeg") }
func QualifiedFFprobe(t testing.TB) string { t.Helper(); return qualified(t, "ffprobe") }
