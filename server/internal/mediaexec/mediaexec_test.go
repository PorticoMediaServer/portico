package mediaexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
)

func TestMain(m *testing.M) {
	// This test binary is the limits shim, as the server binary is in production.
	if handled, err := RunHelper(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("PORTICO_TEST_CRASHED_SERVER") != "" && crashedServerHook != nil {
		crashedServerHook()
		return
	}
	os.Exit(m.Run())
}

// crashedServerHook is the fake server of the restart test (Unix only).
var crashedServerHook func()

// resetForTest forgets the configuration and every decided posture.
func resetForTest(t *testing.T) {
	t.Helper()
	clear := func() {
		configured.Lock()
		configured.helper, configured.ledger, configured.instance, configured.libraries = "", "", "", nil
		configured.Unlock()
		postures.Lock()
		postures.byTool, postures.last = map[string]*postureEntry{}, nil
		postures.Unlock()
		networkPosture.Lock()
		networkPosture.unconfined, networkPosture.reason = false, ""
		networkPosture.Unlock()
		sandboxProbe = probeSandbox
	}
	clear()
	t.Cleanup(clear)
}

// sandboxed returns ffmpeg and ffprobe if this host sandboxes them, and skips
// otherwise (fails with PORTICO_REQUIRE_DECODER_SANDBOX=1, as on the runner).
func sandboxed(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	if p := Decide(ffmpeg); !p.Sandboxed {
		if os.Getenv("PORTICO_REQUIRE_DECODER_SANDBOX") == "1" {
			t.Fatalf("no decoder sandbox: %s", p.Reason)
		}
		t.Skipf("no decoder sandbox here: %s", p.Reason)
	}
	return ffmpeg, ffprobe
}

func sample(t *testing.T, ffmpeg, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "tone.wav")
	out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "pcm_s16le", "-y", path).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	return path
}

func run(t *testing.T, job Job) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, err := CommandContext(ctx, job)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

func probeArgs(input string) []string {
	return []string{"-v", "error", "-protocol_whitelist", "file,pipe,http,tcp", "-show_entries", "format=format_name", "-of", "csv=p=0", input}
}

// SEC-11: the sandbox a decoder gets reads the input it was handed and nothing
// else, writes only its own folder, and has no network.
func TestSandboxConfinesReadsWritesAndNetwork(t *testing.T) {
	resetForTest(t)
	ffmpeg, ffprobe := sandboxed(t)
	dir := t.TempDir()
	media := sample(t, ffmpeg, dir)
	secret := filepath.Join(t.TempDir(), "secret.wav")
	raw, _ := os.ReadFile(media)
	if err := os.WriteFile(secret, raw, 0600); err != nil {
		t.Fatal(err)
	}

	input, err := os.Open(media)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	descriptor := "/dev/fd/3"
	if out, err := run(t, Job{Executable: ffprobe, Args: probeArgs(descriptor), Files: []*os.File{input}}); err != nil || !strings.Contains(out, "wav") {
		t.Fatalf("the inherited input must be readable: %v %q", err, out)
	}
	if out, err := run(t, Job{Executable: ffprobe, Args: probeArgs(secret)}); err == nil {
		t.Fatalf("an undeclared file was readable: %q", out)
	}
	if out, err := run(t, Job{Executable: ffprobe, Args: probeArgs(secret), ReadPaths: []string{secret}}); err != nil || !strings.Contains(out, "wav") {
		t.Fatalf("a declared file must be readable: %v %q", err, out)
	}

	output, elsewhere := t.TempDir(), t.TempDir()
	encode := func(target string) []string {
		return []string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:a", "pcm_s16le", "-y", target}
	}
	if out, err := run(t, Job{Executable: ffmpeg, Args: encode(filepath.Join(output, "a.wav")), WriteDirs: []string{output}}); err != nil {
		t.Fatalf("the job's folder must be writable: %v %q", err, out)
	}
	if _, err := run(t, Job{Executable: ffmpeg, Args: encode(filepath.Join(elsewhere, "b.wav")), WriteDirs: []string{output}}); err == nil {
		t.Fatal("a folder outside the job's own was writable")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "b.wav")); err == nil {
		t.Fatal("the undeclared write happened")
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, media) })}
	go server.Serve(listener)
	defer server.Close()
	url := "http://" + listener.Addr().String() + "/tone.wav"
	if out, err := run(t, Job{Executable: ffprobe, Args: probeArgs(url)}); err == nil {
		t.Fatalf("a job without a declared bridge reached the network: %q", out)
	}
	if out, err := run(t, Job{Executable: ffprobe, Args: probeArgs(url), Loopback: listener.Addr().String()}); err != nil || !strings.Contains(out, "wav") {
		t.Fatalf("the declared loopback bridge must be reachable: %v %q", err, out)
	}
}

// D-MEDIA-6: without a sandbox the job still runs, with the baseline, and the
// owner's diagnostics say why; an owner who requires the sandbox gets a refusal.
func TestBaselineIsExplicitAndRequiredFailsClosed(t *testing.T) {
	resetForTest(t)
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	sandboxProbe = func(string) (string, bool, error) {
		return "", false, errors.New("bubblewrap (bwrap) is not installed on this server")
	}
	argv, err := Argv(Job{Executable: ffmpeg, Args: []string{"-version"}})
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := resolveExecutable(ffmpeg)
	if len(argv) != 2 || argv[0] != exe || argv[1] != "-version" {
		t.Fatalf("an unconfigured baseline job runs the tool directly: %q", argv)
	}
	p := Current()
	if p.Sandboxed || p.Mode != ModeBaseline || !strings.Contains(p.Reason, "bwrap") {
		t.Fatalf("%+v", p)
	}
	var found bool
	for _, item := range capabilityreport.All() {
		if item.Capability == capabilityreport.DecoderSandbox {
			found = true
			if item.Available || item.Code != "decoder_sandbox_unavailable" || !strings.Contains(item.Detail, "without a sandbox because bubblewrap") || !strings.Contains(item.Detail, "no core dumps") && runtime.GOOS != "windows" {
				t.Fatalf("diagnostics: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("the posture is not in the owner's diagnostics")
	}
	if out, err := run(t, Job{Executable: ffmpeg, Args: []string{"-hide_banner", "-version"}}); err != nil || !strings.Contains(out, "ffmpeg version") {
		t.Fatalf("baseline jobs must run: %v %q", err, out)
	}

	resetForTest(t)
	t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	sandboxProbe = func(string) (string, bool, error) { return "", false, errors.New("no sandbox") }
	if _, err = Argv(Job{Executable: ffmpeg, Args: []string{"-version"}}); !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("required sandbox: %v", err)
	}

	resetForTest(t)
	t.Setenv("PORTICO_DECODER_SANDBOX", "off")
	sandboxProbe = func(string) (string, bool, error) {
		t.Fatal("the owner turned the sandbox off; it must not be probed")
		return "", false, nil
	}
	if p := Decide(ffmpeg); p.Sandboxed || p.Setting != SettingOff || !strings.Contains(p.Reason, "PORTICO_DECODER_SANDBOX=off") {
		t.Fatalf("%+v", p)
	}
}

// Every job's environment is the short allowlist: nothing the server was
// started with (keys, tokens, database paths) reaches a decoder.
func TestEnvironmentCarriesNoServerSecrets(t *testing.T) {
	resetForTest(t)
	t.Setenv("PORTICO_HOSTED_TOKEN", "secret")
	t.Setenv("PATH", os.Getenv("PATH"))
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	cmd, err := Command(Job{Executable: ffmpeg, Args: []string{"-version"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range cmd.Env {
		if strings.Contains(kv, "secret") || strings.HasPrefix(kv, "PORTICO_") {
			t.Fatalf("server environment leaked: %q", kv)
		}
	}
	if cmd.SysProcAttr == nil && runtime.GOOS != "windows" {
		t.Fatal("no process group")
	}
}

func TestJobValidation(t *testing.T) {
	resetForTest(t)
	for _, job := range []Job{
		{},
		{Executable: "/bin/sh", ReadPaths: []string{"relative"}},
		{Executable: "/bin/sh", WriteDirs: []string{"/definitely/not/here"}},
		{Executable: "/bin/sh", Loopback: "10.0.0.1:80"},
		{Executable: "/bin/sh", Loopback: "127.0.0.1:0"},
		{Executable: "relative", PreConfined: true},
	} {
		if _, err := Argv(job); err == nil {
			t.Errorf("accepted %+v", job)
		}
	}
}

// Text subtitles burn in a local-file HLS window (hls.go's job shape): libass
// finds a font through the pack, sandboxed and with the baseline alike.
func TestBurnInFindsFontsSandboxedAndBaseline(t *testing.T) {
	for _, mode := range []string{"auto", "off"} {
		t.Run(mode, func(t *testing.T) {
			resetForTest(t)
			t.Setenv("PORTICO_DECODER_SANDBOX", mode)
			ffmpeg := decodertest.QualifiedFFmpeg(t)
			if filters, _ := exec.Command(ffmpeg, "-hide_banner", "-filters").Output(); !strings.Contains(string(filters), " ass ") {
				t.Skip("this FFmpeg has no libass")
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
			out, err := run(t, Job{Executable: ffmpeg, Args: []string{"-nostdin", "-v", "debug", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=1", "-vf", "ass=filename=" + script, "-frames:v", "1", "-pix_fmt", "gray", "-f", "rawvideo", "-y", frames}, WriteDirs: []string{output}, Fonts: true})
			if err != nil {
				t.Fatalf("burn-in failed: %v %s", err, out)
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
			t.Logf("%s subtitle drew %d bright pixels", mode, bright)
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "fontselect:") {
					t.Log(strings.TrimSpace(line))
				}
			}
			if bright < 100 {
				t.Fatalf("the subtitle drew %d bright pixels (%s): %s", bright, mode, out)
			}
		})
	}
}

// A descriptor-bound directory input is a Linux sandbox job only; nothing
// else may carry it, and it never invents a second mount point.
func TestDirectoryInputJobValidation(t *testing.T) {
	base := Job{Executable: "/usr/bin/true", InputFD: 3, InputMount: "/disc"}
	if err := validate(&base); (err == nil) != (runtime.GOOS == "linux") {
		t.Fatalf("platform validation: %v", err)
	}
	for name, job := range map[string]Job{
		"wrong descriptor": {Executable: "/usr/bin/true", InputFD: 4, InputMount: "/disc"},
		"relative mount":   {Executable: "/usr/bin/true", InputFD: 3, InputMount: "disc"},
		"unclean mount":    {Executable: "/usr/bin/true", InputFD: 3, InputMount: "/disc/../disc"},
		"preconfined":      {Executable: "/usr/bin/true", InputFD: 3, InputMount: "/disc", PreConfined: true},
	} {
		if err := validate(&job); err == nil {
			t.Errorf("%s: admitted", name)
		}
	}
}
