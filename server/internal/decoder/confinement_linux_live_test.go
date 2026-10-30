//go:build linux

package decoder

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary is its own sandbox helper, like the server binary.
func TestMain(m *testing.M) {
	if handled, err := RunHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A short live HLS job runs end to end through bubblewrap: ffmpeg inside the
// sandbox PUTs its playlist and segments through the sandbox proxy to the
// loopback gateway. Skipped where bubblewrap or user namespaces aren't
// available, or ffmpeg isn't installed.
func TestLiveHLSWritesThroughTheLinuxSandbox(t *testing.T) {
	if !CheckConfinement(context.Background()) {
		t.Skip("bubblewrap with user namespaces is unavailable here")
	}
	liveHLS(t, func(cmd *exec.Cmd, ffmpeg string) {
		if len(cmd.Args) < 2 || cmd.Args[1] != linuxHelperFlag {
			t.Fatalf("not the sandbox helper: %q", cmd.Args)
		}
	})
}

// BE-MEDIA-02: where there is no sandbox (Docker without user namespaces, no
// bubblewrap, or the owner turned it off) the same live job runs with the
// baseline instead of being refused.
func TestLiveHLSRunsWithTheBaselineWithoutASandbox(t *testing.T) {
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	liveHLS(t, func(cmd *exec.Cmd, ffmpeg string) {
		if cmd.Args[0] != ffmpeg || strings.Contains(strings.Join(cmd.Args, " "), "bwrap") {
			t.Fatalf("not the baseline: %q", cmd.Args)
		}
	})
}

func TestLinearOutputCommandKeepsGPUGrantInsideSandbox(t *testing.T) {
	if !bridgeSandbox() {
		t.Skip("bubblewrap with user namespaces is unavailable here")
	}
	for _, gpu := range []bool{false, true} {
		plan := LinearEncoding{Video: true, ConvertVideo: true}
		if gpu {
			plan.HDRToSDR = true
			plan.Toolchain = &ToolchainFacts{VulkanAvailable: true, Filters: map[string]bool{"libplacebo": true}}
		}
		args, hardware, err := linearHLSArgs(linearTestInput, linearTestOutput, plan)
		if err != nil || hardware != gpu {
			t.Fatal(hardware, err)
		}
		output := strings.TrimPrefix(linearTestOutput, "http://127.0.0.1:43210")
		cmd, err := confinedOutputCommand(realTool(t), args, "127.0.0.1:43210", output, hardware)
		if err != nil {
			t.Fatal(err)
		}
		if len(cmd.Args) != 3 || cmd.Args[1] != linuxHelperFlag {
			t.Fatal("GPU filter bypassed the decoder sandbox", cmd.Args)
		}
		spec, err := decodeLinux(cmd.Args[2])
		if err != nil || spec.Hardware != gpu || spec.OutputPath != output || spec.Endpoint != "127.0.0.1:43210" || spec.OutputDirectory != "" {
			t.Fatal("GPU grant widened or lost the output scope", spec, err)
		}
	}
}

func liveHLS(t *testing.T, check func(*exec.Cmd, string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if ffmpeg, err = filepath.EvalSymlinks(ffmpeg); err != nil {
		t.Fatal(err)
	}
	libraries, err := ResolveLibraries(ffmpeg, ffmpeg, nil)
	if err != nil {
		t.Skipf("ffmpeg's libraries can't be resolved: %v", err)
	}
	output := "/output/" + strings.Repeat("c", 64) + "/"
	var mu sync.Mutex
	written := map[string]int{}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gateway := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutPrefix(r.URL.Path, output)
		if r.Method != "PUT" || !ok {
			http.Error(w, "no", 404)
			return
		}
		n, _ := countBody(r)
		mu.Lock()
		written[name] += n
		mu.Unlock()
		w.WriteHeader(201)
	})}
	go func() { _ = gateway.Serve(listener) }()
	defer gateway.Close()
	endpoint := listener.Addr().String()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10", "-t", "3", "-c:v", "mpeg2video",
		"-f", "hls", "-hls_time", "1", "-hls_list_size", "12", "-method", "PUT", "-hls_segment_filename", "http://" + endpoint + output + "segment-%09d.ts", "http://" + endpoint + output + "index.m3u8"}
	cmd, err := confinedOutputCommand(ffmpeg, args, endpoint, output, false, libraries...)
	if err != nil {
		t.Fatal(err)
	}
	check(cmd, ffmpeg)
	configureInheritedFiles(cmd)
	done := make(chan error, 1)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		t.Fatal("the confined job did not finish")
	}
	if err != nil {
		t.Fatalf("confined live HLS job failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if written["index.m3u8"] == 0 {
		t.Fatalf("no playlist written: %v", written)
	}
	segments := 0
	for name, n := range written {
		if strings.HasPrefix(name, "segment-") && n > 0 {
			segments++
		}
	}
	if segments == 0 {
		t.Fatalf("no segments written: %v", written)
	}
}

func countBody(r *http.Request) (int, error) {
	buf := make([]byte, 32<<10)
	total := 0
	for {
		n, err := r.Body.Read(buf)
		total += n
		if err != nil {
			return total, nil
		}
	}
}

// Text subtitles burn inside the sandbox: libass finds a font through the
// decoder's font pack even though the sandbox shows it no host directory, and
// on a host with no fonts at all (the production bundle on a minimal Debian).
func TestConfinedBurnInDrawsText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if !CheckConfinement(ctx) {
		t.Skip("bubblewrap with user namespaces is unavailable here")
	}
	ffmpeg := os.Getenv("PORTICO_FFMPEG")
	if ffmpeg == "" {
		var err error
		if ffmpeg, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("ffmpeg is not installed")
		}
	}
	ffmpeg, err := filepath.EvalSymlinks(ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := ResolveLibraries(ffmpeg, ffmpeg, nil)
	if err != nil {
		t.Skipf("ffmpeg's libraries can't be resolved: %v", err)
	}
	output, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(output, "selected.ass")
	if err = os.WriteFile(script, []byte("[Script Info]\nScriptType: v4.00+\nPlayResX: 160\nPlayResY: 90\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, Bold, Alignment\nStyle: Default,Arial,24,&H00FFFFFF,1,5\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:05.00,Default,BURN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	frames := filepath.Join(output, "frames.gray")
	args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=1", "-vf", "ass=filename=" + script, "-frames:v", "1", "-pix_fmt", "gray", "-f", "rawvideo", frames}
	cmd, err := confinedHLSCommand(ffmpeg, args, "127.0.0.1:19503", output, false, libraries...)
	if err != nil {
		t.Fatal(err)
	}
	configureInheritedFiles(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		t.Fatalf("confined burn-in failed: %v: %s", err, stderr.String())
	}
	body, err := os.ReadFile(frames)
	if err != nil || len(body) != 160*90 {
		t.Fatalf("frame %d bytes: %v", len(body), err)
	}
	bright := 0
	for _, v := range body {
		if v > 100 {
			bright++
		}
	}
	if bright < 100 {
		t.Fatalf("the subtitle drew %d bright pixels inside the sandbox: %s", bright, stderr.String())
	}
}
