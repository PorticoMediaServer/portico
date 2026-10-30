//go:build linux

package decoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Linux decoders get a new user/mount/PID/network namespace. They cannot see
// host libraries, private data or the host network. The only input is a private
// Unix-socket proxy to the already-authorized numeric loopback gateway.
// The trusted outer helper owns that proxy; the inner helper only bridges it to
// the same numeric endpoint within the isolated network namespace.
type linuxSpec struct {
	OutputDirectory      string
	Executable           string
	Arguments, Libraries []string
	Endpoint             string
	// OutputPath is the one gateway path (/output/<64 hex>/) a live HLS job may
	// PUT its playlist and segments to; empty for every other decoder.
	OutputPath          string
	Probe               bool
	Hardware            bool
	HardwareEnvironment [][2]string
	// FontConfig is the decoder's FONTCONFIG_FILE and FontDirs the read-only
	// font directories it names (fonts.go): libass draws text subtitles.
	FontConfig string
	FontDirs   []string
}

// withFonts gives a decoder the font pack; without one (the temporary
// directory is unwritable) it runs as before and text subtitles draw nothing.
func withFonts(v linuxSpec) linuxSpec {
	if pack, e := Fonts(); e == nil {
		v.FontConfig, v.FontDirs = pack.Config, append([]string{pack.Dir}, pack.Host...)
	}
	return v
}

func encodeLinux(v linuxSpec) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeLinux(text string) (linuxSpec, error) {
	var v linuxSpec
	if len(text) > 128<<10 {
		return v, ErrInvalidConfiguration
	}
	b, e := base64.RawURLEncoding.DecodeString(text)
	if e != nil || json.Unmarshal(b, &v) != nil {
		return v, ErrInvalidConfiguration
	}
	if v.Probe {
		return v, nil
	}
	host, port, e := net.SplitHostPort(v.Endpoint)
	n, _ := strconv.Atoi(port)
	if e != nil || host != "127.0.0.1" || n < 1 || n > 65535 || !filepath.IsAbs(v.Executable) || filepath.Clean(v.Executable) != v.Executable || len(v.Arguments) > 256 || len(v.Libraries) > maxLibraryFiles {
		return v, ErrInvalidConfiguration
	}
	for _, p := range v.Libraries {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return v, ErrInvalidConfiguration
		}
	}
	if len(v.HardwareEnvironment) > 5 || !v.Hardware && len(v.HardwareEnvironment) > 0 {
		return v, ErrInvalidConfiguration
	}
	for _, kv := range v.HardwareEnvironment {
		switch kv[0] {
		case "LIBVA_DRIVER_NAME", "LIBVA_DRIVERS_PATH", "CUDA_VISIBLE_DEVICES", "NVIDIA_VISIBLE_DEVICES", "NVIDIA_DRIVER_CAPABILITIES":
		default:
			return v, ErrInvalidConfiguration
		}
		if len(kv[1]) > 4096 || strings.ContainsRune(kv[1], '\x00') {
			return v, ErrInvalidConfiguration
		}
	}
	if v.OutputDirectory != "" && (!filepath.IsAbs(v.OutputDirectory) || filepath.Clean(v.OutputDirectory) != v.OutputDirectory) {
		return v, ErrInvalidConfiguration
	}
	if v.OutputPath != "" && !outputPath.MatchString(v.OutputPath) {
		return v, ErrInvalidConfiguration
	}
	if len(v.FontDirs) > 8 || v.FontConfig != "" && (!filepath.IsAbs(v.FontConfig) || filepath.Clean(v.FontConfig) != v.FontConfig) {
		return v, ErrInvalidConfiguration
	}
	for _, p := range v.FontDirs {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return v, ErrInvalidConfiguration
		}
	}
	return v, nil
}

// command runs a bridge job in the Linux decoder sandbox when bubblewrap and
// this helper work here: the trusted outer helper owns a Unix-socket proxy to
// the bridge, and the inner helper, inside new user/mount/PID/network
// namespaces, bridges it to the same numeric endpoint. Otherwise the job goes
// to mediaexec as it is. Optional mode keeps the existing filesystem sandbox
// or baseline with a separate unconfined-network report. Required mode refuses
// that fallback: a filesystem-only sandbox cannot satisfy the requirement.
func (d decoderJob) command() (*exec.Cmd, error) {
	if !bridgeSandbox() {
		return mediaCommand(d.mediaJob())
	}
	helper, e := os.Executable()
	if e != nil {
		return nil, e
	}
	spec := linuxSpec{Executable: d.executable, Arguments: d.args, Endpoint: d.endpoint, Libraries: d.libraries, OutputDirectory: d.outputDir, OutputPath: d.outputPath, Hardware: d.hardware}
	if d.hardware {
		// The storage supervisor strips the outer helper's environment. Carry
		// only the existing driver allowlist in its validated private spec.
		spec.HardwareEnvironment = mediaexec.HardwareEnvironment()
	}
	if d.outputDir != "" || d.outputPath != "" {
		// Jobs that produce pictures may burn in text subtitles (fonts.go).
		spec = withFonts(spec)
	}
	job := mediaexec.Job{Executable: helper, Args: []string{linuxHelperFlag, encodeLinux(spec)}, PreConfined: true, Background: d.background}
	if d.outputDir != "" {
		job.MaxFileBytes = mediaexec.DefaultOutputBytes
	}
	return mediaCommand(job)
}

const linuxHelperFlag = "--portico-decoder-linux"

var bridgeProbe struct {
	once sync.Once
	ok   bool
}

// bridgeSandbox is whether the bubblewrap bridge sandbox works on this host,
// probed once for real, and the owner has not turned the sandbox off.
func bridgeSandbox() bool {
	if mediaexec.SandboxSetting() == mediaexec.SettingOff {
		return false
	}
	bridgeProbe.once.Do(func() { bridgeProbe.ok = ProbeConfinement(context.Background()) == nil })
	return bridgeProbe.ok
}

// configureInheritedFiles tells the helper how many descriptors it inherits.
// The helper's argv is the tail of the command (after mediaexec's shim, when
// there is one), so the count goes on the end.
func configureInheritedFiles(cmd *exec.Cmd) {
	for i, arg := range cmd.Args {
		if i > 0 && arg == linuxHelperFlag && i+2 == len(cmd.Args) {
			cmd.Args = append(cmd.Args, "--fds", strconv.Itoa(len(cmd.ExtraFiles)))
			return
		}
	}
}
func inheritedFiles(n int) ([]*os.File, error) {
	if n < 0 || n > 32 {
		return nil, ErrInvalidConfiguration
	}
	files := []*os.File{}
	for i := 0; i < n; i++ {
		f := os.NewFile(uintptr(3+i), "decoder-lifetime")
		if f == nil {
			return nil, ErrInvalidConfiguration
		}
		files = append(files, f)
	}
	return files, nil
}
func sandboxArgs(v linuxSpec, socket string, n int) ([]string, error) {
	helper, e := os.Executable()
	if e != nil {
		return nil, e
	}
	helper, e = filepath.EvalSymlinks(helper)
	if e != nil {
		return nil, e
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv", "--setenv", "PATH", "/usr/bin:/bin", "--setenv", "TMPDIR", os.TempDir(), "--setenv", "TMP", os.TempDir(), "--setenv", "TEMP", os.TempDir(), "--dev", "/dev", "--tmpfs", os.TempDir(), "--chdir", os.TempDir(), "--ro-bind", helper, "/portico-helper"}
	if v.Hardware {
		args = append(args, mediaexec.LinuxHardwareSandboxArgs(v.HardwareEnvironment)...)
	}
	// The Go server is normally static. Dynamic deployments get its exact ELF
	// dependencies as well, never a whole /lib or /usr tree.
	helperLibs, e := ResolveLibraries(helper, helper, nil)
	if e != nil {
		return nil, e
	}
	// The decoder's own exact ELF dependencies are always bound, whatever the
	// caller passed: a caller that passed none (the audio render measurement,
	// NEW-29) got a sandbox where a dynamically linked ffprobe couldn't load
	// libm.so.6, and every measurement failed as "unreadable".
	decoderLibs, e := ResolveLibraries(v.Executable, v.Executable, nil)
	if e != nil {
		return nil, e
	}
	files := append(append(append([]string{}, v.Libraries...), helperLibs...), decoderLibs...)
	if !v.Probe {
		files = append(files, v.Executable)
	}
	seen := map[string]bool{}
	dirs := []string{}
	for _, p := range files {
		if seen[p] {
			continue
		}
		seen[p] = true
		info, e := os.Stat(p)
		if e != nil || !info.Mode().IsRegular() {
			return nil, ErrInvalidConfiguration
		}
		args = append(args, "--ro-bind", p, p)
		dirs = append(dirs, filepath.Dir(p))
	}
	if len(dirs) > 0 {
		args = append(args, "--setenv", "LD_LIBRARY_PATH", strings.Join(dirs, ":"))
	}
	if socket != "" {
		args = append(args, "--ro-bind", socket, "/portico-input.sock")
	}
	if v.OutputDirectory != "" {
		args = append(args, "--bind", v.OutputDirectory, v.OutputDirectory)
	}
	if v.FontConfig != "" {
		// After the /tmp tmpfs, so a pack under /tmp is visible inside it.
		for _, d := range v.FontDirs {
			args = append(args, "--ro-bind-try", d, d)
		}
		args = append(args, "--setenv", "FONTCONFIG_FILE", v.FontConfig)
	}
	args = append(args, "--remount-ro", "/", "/portico-helper", "--portico-decoder-inner", encodeLinux(v), "--fds", strconv.Itoa(n))
	return args, nil
}
func CheckConfinement(ctx context.Context) bool { return ProbeConfinement(ctx) == nil }

// ProbeConfinement runs the same bubblewrap sandbox a decoder gets, doing
// nothing, and says why it failed (the owner's diagnostics show this), or nil.
func ProbeConfinement(ctx context.Context) error {
	binary, e := exec.LookPath("bwrap")
	if e != nil {
		return fmt.Errorf("%w: bwrap (bubblewrap) is not on the server's PATH", ErrConfinementUnavailable)
	}
	args, e := sandboxArgs(linuxSpec{Probe: true}, "", 0)
	if e != nil {
		return fmt.Errorf("%w: the sandbox helper can't be prepared: %v", ErrConfinementUnavailable, e)
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, binary, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir(), "TMP=" + os.TempDir(), "TEMP=" + os.TempDir()}
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 512}
	if e = cmd.Run(); e != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = e.Error()
		}
		return fmt.Errorf("%w: the bubblewrap probe failed (user namespaces may be disabled or restricted): %s", ErrConfinementUnavailable, detail)
	}
	return nil
}

// limitedWriter keeps the first n bytes of a probe's error output.
type limitedWriter struct {
	w interface{ WriteString(string) (int, error) }
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		keep := p
		if len(keep) > l.n {
			keep = keep[:l.n]
		}
		_, _ = l.w.WriteString(string(keep))
		l.n -= len(keep)
	}
	return len(p), nil
}
func RunHelper(args []string) (bool, error) {
	if handled, err := mediaexec.RunHelper(args); handled {
		return true, err
	}
	if len(args) == 0 || (args[0] != linuxHelperFlag && args[0] != "--portico-decoder-inner") {
		return false, nil
	}
	if len(args) != 4 || args[2] != "--fds" {
		return true, ErrInvalidConfiguration
	}
	v, e := decodeLinux(args[1])
	if e != nil {
		return true, e
	}
	n, e := strconv.Atoi(args[3])
	if e != nil {
		return true, e
	}
	fds, e := inheritedFiles(n)
	if e != nil {
		return true, e
	}
	defer func() {
		for _, f := range fds {
			_ = f.Close()
		}
	}()
	if args[0] == "--portico-decoder-inner" {
		return true, runInner(v, fds)
	}
	return true, runOuter(v, fds)
}
func runOuter(v linuxSpec, fds []*os.File) error {
	if v.Probe {
		return ErrInvalidConfiguration
	}
	dir, e := os.MkdirTemp(os.TempDir(), "portico-decoder-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "input.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = os.Chmod(socket, 0600); e != nil {
		return e
	}
	target, _ := url.Parse("http://" + v.Endpoint)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", v.Endpoint)
	}, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 8}
	defer transport.CloseIdleConnections()
	server := &http.Server{Handler: proxyHandler(target, transport, v.OutputPath), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan struct{})
	supervise.Go("decoder.sandbox.serve", func() { defer close(done); _ = server.Serve(listener) })
	defer func() { _ = server.Close(); <-done }()
	binary, e := exec.LookPath("bwrap")
	if e != nil {
		return ErrConfinementUnavailable
	}
	args, e := sandboxArgs(v, socket, len(fds))
	if e != nil {
		return e
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TMPDIR=" + os.TempDir(), "TMP=" + os.TempDir(), "TEMP=" + os.TempDir()}
	cmd.ExtraFiles = fds
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// bwrap's PID namespace and parent-death fence retire every inner descendant.
	// All lifetime descriptors are also inherited, so a parent crash cannot free
	// a physical tuner or let the gateway's host ports be rebound prematurely.
	return cmd.Run()
}
func runInner(v linuxSpec, fds []*os.File) error {
	if v.Probe {
		return nil
	}
	listener, e := net.Listen("tcp4", v.Endpoint)
	if e != nil {
		return e
	}
	defer listener.Close()
	target, _ := url.Parse("http://" + v.Endpoint)
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/portico-input.sock")
	}, ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 8}
	defer transport.CloseIdleConnections()
	server := &http.Server{Handler: proxyHandler(target, transport, v.OutputPath), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan struct{})
	supervise.Go("decoder.helper.serve", func() { defer close(done); _ = server.Serve(listener) })
	defer func() { _ = server.Close(); <-done }()
	cmd := exec.Command(v.Executable, v.Arguments...)
	cmd.Env = os.Environ()
	cmd.ExtraFiles = fds
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return errors.New("confined decoder failed")
	}
	return nil
}
