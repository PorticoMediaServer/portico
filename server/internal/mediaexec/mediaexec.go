// Package mediaexec is the one way the server runs FFmpeg and ffprobe.
//
// Every media process decodes bytes the server did not write: a file someone
// dropped in a library folder, a tuner stream, a remote object. A demuxer bug
// in any of them is code execution as the server, which can read the database,
// the TLS seal key, backup keys and the server identity key, and can reach the
// home network. So there is one policy, applied here, whatever subsystem asks
// (ARCH-MEDIA-09, SEC-11, D-MEDIA-6):
//
//   - Sandboxed where the platform can: bubblewrap on Linux (new user, PID,
//     IPC, UTS and network namespaces; only the executable, its exact
//     libraries, the job's declared inputs and its private output folders are
//     visible), sandbox-exec on macOS (no network except a declared loopback
//     input, file reads only from the executable, its libraries, system
//     frameworks and the declared inputs, writes only to the output folders).
//   - The same baseline everywhere, sandboxed or not: the input is an open
//     descriptor or a private loopback bridge rather than a library path, the
//     caller's protocol whitelist, a minimal environment, no core dumps, a
//     bound on the size of any file the process writes (zero when the job has
//     no output folder), low CPU priority for background work, its own process
//     group killed as a whole on cancel, parent-death signal on Linux, and a
//     ledger so a crashed server's successor reaps what it left (BE-MEDIA-06).
//   - Where no sandbox is available (Docker without user namespaces, Linux
//     without bubblewrap, Windows) the features keep working with the baseline
//     and the owner's diagnostics say so, with the reason and the fix. The
//     owner can require the sandbox (PORTICO_DECODER_SANDBOX=required: media
//     jobs then fail rather than run unsandboxed) or turn it off (=off).
//
// Nothing else in the server may call exec.Command on a media tool; the
// spawn-site inventory test enforces it.
package mediaexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

var (
	// ErrInvalidJob is a job this package refuses to build: a relative path, a
	// write folder that is not a folder, too many inherited files.
	ErrInvalidJob = errors.New("invalid media process")
	// ErrUnsupported is a platform capability that does not exist here.
	ErrUnsupported = errors.New("media process capability unsupported on this platform")
	// ErrSandboxRequired is returned when the owner requires the sandbox and it
	// is not available.
	ErrSandboxRequired = errors.New("the decoder sandbox is required by the owner and is not available on this server")
)

// maxLibraryFiles bounds the exact dependency files a sandbox mounts.
const maxLibraryFiles = 512

const maxJobArguments = 4096

// MaxGeneratedArguments bounds the complete sandbox and limits-shim argv.
// Each exact dependency contributes three mount arguments; the remaining
// overhead covers the sandbox, declared input paths and limits shim. Helpers
// receiving a generated argv use this bound in addition to their byte limits.
const MaxGeneratedArguments = maxJobArguments + 3*maxLibraryFiles + 128

// DefaultOutputBytes bounds any one file a job with output folders writes.
// HLS sessions are budgeted at 4 GiB in total, so one file never needs more.
const DefaultOutputBytes int64 = 4 << 30

// NoOutputFiles is Job.MaxFileBytes for a job that must not create or grow any
// regular file (probes, analysis and conversions that stream to a pipe).
const NoOutputFiles int64 = -1

// Job is one media process.
type Job struct {
	// Executable is the media tool (ffmpeg, ffprobe). A bare name is resolved on
	// PATH; the real, absolute file is what runs.
	Executable string
	Args       []string
	// Files are inherited as descriptors 3, 4, … in order. The input goes here
	// (as /dev/fd/3), so the process never needs authority over a library path.
	Files []*os.File
	// ReadPaths are extra files or folders the process may read, such as a
	// subtitle file the server wrote for burn-in.
	ReadPaths []string
	// InputFD is an inherited directory descriptor that the sandbox
	// read-only binds at InputMount. It exists for disc folders, whose reader
	// needs a subtree without granting any host path: no other part of the
	// host filesystem is mounted and the sandbox root stays empty.
	InputFD int
	// InputMount is the absolute sandbox path InputFD is bound at.
	InputMount string
	// WriteDirs are the job's private output folders.
	WriteDirs []string
	// Loopback is the numeric 127.0.0.1:port bridge the process may connect to,
	// or empty for none. Linux bridge isolation uses decoder's verified helper
	// (PreConfined). Generic Linux loopback jobs retain host network in optional
	// mode and are refused when the owner requires the sandbox.
	Loopback string
	// Hardware jobs may use GPU devices and the system's driver libraries.
	Hardware bool
	// Fonts gives the process Portico's font pack and the host's font folders,
	// read-only, for text subtitles burned in by libass (fonts.go).
	Fonts bool
	// Background work (scans, analysis, prepared versions) runs at low priority.
	Background bool
	// MaxFileBytes bounds each regular file the process writes: 0 means
	// DefaultOutputBytes when there are WriteDirs and NoOutputFiles otherwise.
	MaxFileBytes int64
	// MaxCPUSeconds bounds the process's CPU time; 0 is unbounded.
	MaxCPUSeconds int
	// PreConfined says Executable and Args already form a sandboxed invocation
	// built by the decoder package (its Linux bridge helper, or the fd-bound
	// prepared-media sandbox). Only the process policy is applied.
	PreConfined bool
	// Libraries are extra exact dependency files (PORTICO_*_DECODER_LIBRARIES).
	Libraries []string
}

// Mode is how a job runs.
type Mode string

const (
	ModeSandboxed Mode = "sandboxed"
	ModeBaseline  Mode = "baseline"
)

// Command builds a media process that the caller runs (or hands to a
// supervisor, which owns cancellation). The returned command has its own
// process group, inherited Files, a minimal environment and, on a configured
// server, the resource limits and the orphan ledger.
func Command(job Job) (*exec.Cmd, error) {
	argv, err := Argv(job)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	finish(cmd, job.Files, job.Fonts)
	return cmd, nil
}

// CommandContext is Command whose context cancellation kills the whole process
// group, sandbox and all.
func CommandContext(ctx context.Context, job Job) (*exec.Cmd, error) {
	argv, err := Argv(job)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	finish(cmd, job.Files, job.Fonts)
	return cmd, nil
}

// CommandArgv starts an argv that Argv built in the server process, from a
// helper process (the storage helper runs the lyric probe on a descriptor only
// it holds). files become descriptors 3, 4, … as in Job.Files.
func CommandArgv(ctx context.Context, argv []string, files ...*os.File) (*exec.Cmd, error) {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return nil, ErrInvalidJob
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	finish(cmd, files, false)
	return cmd, nil
}

func finish(cmd *exec.Cmd, files []*os.File, fonts bool) {
	if len(files) > 0 {
		cmd.ExtraFiles = append([]*os.File{}, files...)
	}
	cmd.Env = environment()
	if fonts {
		// The font pack's configuration (fonts.go). Inside the Linux sandbox the
		// environment is rebuilt, and the argv sets it again.
		if pack, err := Fonts(); err == nil {
			cmd.Env = append(cmd.Env, "FONTCONFIG_FILE="+pack.Config)
		}
	}
	childprocess.Configure(cmd)
}

// Argv is the complete argument vector for a job: the limits shim (on a
// configured Unix server), then the sandbox, then the tool and its arguments.
// A helper process that already holds the input descriptor runs it with
// CommandArgv or ExecArgv.
func Argv(job Job) ([]string, error) {
	if err := validate(&job); err != nil {
		return nil, err
	}
	var inner []string
	networkIsolated := job.PreConfined
	networkReason := "The server admitted a decoder with baseline restrictions only. Its process can use the host network; a protocol whitelist is not network isolation."
	if job.PreConfined {
		inner = append([]string{job.Executable}, job.Args...)
	} else {
		exe, err := resolveExecutable(job.Executable)
		if err != nil {
			return nil, err
		}
		job.Executable = exe
		posture := postureFor(exe)
		posture.Setting = setting()
		if posture.Setting == SettingOff {
			// The owner's choice holds even for a posture decided earlier.
			posture = Posture{Setting: SettingOff}
		}
		if runtime.GOOS == "linux" && job.Loopback != "" {
			// Only decoder's verified bridge helper can cross into an isolated
			// namespace. The generic Linux loopback profile retains host network.
			if posture.Setting == SettingRequired {
				return nil, ErrSandboxRequired
			}
			networkReason = "A direct loopback decoder path retains the host network namespace. Filesystem sandboxing and protocol restrictions do not restrict a compromised decoder's network access. Required sandbox mode refuses this path when the isolated decoder bridge is unavailable."
		}
		if !posture.Sandboxed {
			if posture.Setting == SettingRequired {
				return nil, ErrSandboxRequired
			}
			if job.InputFD != 0 {
				// A bare descriptor directory cannot be mounted without the
				// sandbox; the disc folder stays unsupported rather than
				// leaking a host path.
				return nil, ErrSandboxRequired
			}
			inner = append([]string{exe}, job.Args...)
		} else {
			if inner, err = sandboxArgv(job, posture); err != nil {
				return nil, err
			}
			networkIsolated = runtime.GOOS != "linux" || job.Loopback == ""
		}
	}
	argv := shimArgv(job, inner)
	if len(argv) > MaxGeneratedArguments {
		return nil, ErrInvalidJob
	}
	reportNetworkIsolation(networkIsolated, networkReason)
	noteCommand()
	return argv, nil
}

func validate(job *Job) error {
	if job.Executable == "" || len(job.Files) > 32 || len(job.Args) > maxJobArguments {
		return ErrInvalidJob
	}
	if job.PreConfined && !filepath.IsAbs(job.Executable) {
		return ErrInvalidJob
	}
	for _, list := range [][]string{job.ReadPaths, job.WriteDirs, job.Libraries} {
		for _, p := range list {
			if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\x00\n\r") {
				return ErrInvalidJob
			}
		}
	}
	for _, dir := range job.WriteDirs {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return ErrInvalidJob
		}
	}
	if job.Loopback != "" && !loopbackEndpoint(job.Loopback) {
		return ErrInvalidJob
	}
	if job.InputFD != 0 || job.InputMount != "" {
		if job.InputFD != 3 || !filepath.IsAbs(job.InputMount) || filepath.Clean(job.InputMount) != job.InputMount || strings.ContainsAny(job.InputMount, "\x00\n\r") {
			return ErrInvalidJob
		}
		if runtime.GOOS != "linux" || job.PreConfined {
			// Descriptor-bound directory mounts are a Linux sandbox feature and
			// exist only where this package builds the sandbox itself.
			return ErrInvalidJob
		}
	}
	if job.MaxFileBytes == 0 {
		job.MaxFileBytes = NoOutputFiles
		if len(job.WriteDirs) > 0 {
			job.MaxFileBytes = DefaultOutputBytes
		}
	}
	return nil
}

func loopbackEndpoint(endpoint string) bool {
	host, port, ok := strings.Cut(endpoint, ":")
	if !ok || host != "127.0.0.1" || port == "" || len(port) > 5 {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	return port[0] != '0'
}

// resolveExecutable turns "ffmpeg" or a symlink into the real absolute file, so
// the sandbox mounts and the profile name the file that actually runs.
func resolveExecutable(name string) (string, error) {
	path := name
	if !filepath.IsAbs(path) {
		found, err := exec.LookPath(name)
		if err != nil {
			return "", err
		}
		if path, err = filepath.Abs(found); err != nil {
			return "", err
		}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrInvalidJob
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", ErrInvalidJob
	}
	return real, nil
}

// passedEnvironment is everything a media process may inherit from the server.
// Secrets the server was started with (tokens, keys, database locations) never
// reach a decoder.
var passedEnvironment = []string{
	"PATH", "TMPDIR", "TMP", "TEMP", "SystemRoot", "windir",
	"LIBVA_DRIVER_NAME", "LIBVA_DRIVERS_PATH", "CUDA_VISIBLE_DEVICES", "NVIDIA_VISIBLE_DEVICES", "NVIDIA_DRIVER_CAPABILITIES",
	"FONTCONFIG_FILE", "FONTCONFIG_PATH",
}

func environment() []string {
	out := []string{}
	for _, name := range passedEnvironment {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// HardwareEnvironment carries only the existing driver allowlist to a private
// bridge specification; the supervisor need not inherit server environment.
func HardwareEnvironment() [][2]string { return hardwareEnvironment() }

// hardwareEnvironment is what a GPU driver inside the Linux sandbox needs.
func hardwareEnvironment() [][2]string {
	out := [][2]string{}
	for _, name := range []string{"LIBVA_DRIVER_NAME", "LIBVA_DRIVERS_PATH", "CUDA_VISIBLE_DEVICES", "NVIDIA_VISIBLE_DEVICES", "NVIDIA_DRIVER_CAPABILITIES"} {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, [2]string{name, v})
		}
	}
	return out
}
