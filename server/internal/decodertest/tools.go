// Package decodertest keeps real-media tests on the same verified toolchain.
package decodertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
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

func qualified(t testing.TB, tool string) string {
	t.Helper()
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
