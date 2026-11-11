package inputbridge

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// The test executable dispatches the same confinement helpers as the server.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// On a host with IPv6 loopback only, the bridge serves on [::1] and a real ffprobe reads a
// file through it: confined in its own network namespace where this host has the decoder's
// isolated bridge, and as a plain loopback job with the sandbox off.
func TestAHostWithIPv6LoopbackOnlyReadsAFile(t *testing.T) {
	if probe, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback}); err != nil {
		t.Skip("this host has no IPv6 loopback")
	} else {
		probe.Close()
	}
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	real := listenIPv4
	listenIPv4 = func(int) (*net.TCPListener, error) { return nil, syscall.EADDRNOTAVAIL }
	defer func() { listenIPv4 = real }()

	path := filepath.Join(t.TempDir(), "probe.mkv")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24",
		"-t", "2", "-pix_fmt", "yuv420p", "-c:v", "libx264", path).CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()

	bridge, err := Open(context.Background(), file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bridge.URL(), "http://[::1]:") {
		t.Fatalf("the tool is told %s", bridge.URL())
	}
	reservation, err := bridge.Reserve()
	if err != nil {
		t.Fatalf("reserving an IPv6 bridge: %v", err)
	}
	reservation.Close()
	bridge.Close()

	// A host that has IPv4 loopback, where only one bind failed, is refused, not moved to [::1].
	failOnce := true
	listenIPv4 = func(port int) (*net.TCPListener, error) {
		if failOnce || port != 0 {
			failOnce = false
			return nil, syscall.EADDRINUSE
		}
		return real(0)
	}
	if b, err := Open(context.Background(), file, info.Size()); err == nil {
		b.Close()
		t.Fatalf("a host with IPv4 loopback was told %s", b.URL())
	}
	listenIPv4 = func(int) (*net.TCPListener, error) { return nil, syscall.EADDRNOTAVAIL }

	modes := []string{"off"}
	if decoder.ProbeConfinement(context.Background()) == nil {
		modes = append(modes, "required")
	}
	for _, mode := range modes {
		t.Setenv("PORTICO_DECODER_SANDBOX", mode)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		facts, err := Inspect(ctx, &processpool.Supervisor{Limit: 1}, ffprobe, file, info.Size())
		cancel()
		if err != nil || facts.VideoCodec != "h264" || facts.Width != 320 {
			t.Fatalf("sandbox %s: %v %+v", mode, err, facts)
		}
	}
}
