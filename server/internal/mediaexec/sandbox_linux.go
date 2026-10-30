//go:build linux

package mediaexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Paths a hardware job may read: the system's driver libraries (VA-API and
// Quick Sync drivers, libcuda and the NVENC runtime are loaded at run time from
// here) and the device description sysfs gives them. None of them is private.
var hardwareReadable = []string{"/usr/lib", "/usr/lib64", "/usr/lib32", "/lib", "/lib64", "/usr/local/lib", "/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d", "/usr/share/vulkan", "/etc/vulkan", "/usr/lib/wsl", "/usr/share/glvnd", "/usr/share/libdrm", "/etc/OpenCL", "/sys"}

func bwrapPath() (string, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", errors.New("bubblewrap (bwrap) is not installed on this server")
	}
	return filepath.Abs(path)
}

func librariesFor(executable string, extra []string) ([]string, error) {
	// ResolveLibraries already memoizes ELF parsing while revalidating every
	// alias, library and searched directory. An executable-only outer cache
	// must not bypass those checks after a dependency or RUNPATH change.
	return ResolveLibraries(executable, executable, extra)
}

// probeSandbox starts a trivial program (true) inside the sandbox a job gets,
// then checks that the tool's own libraries can be mounted. It is what decides
// the posture: bubblewrap installed but refused user namespaces (Docker's
// default profile, Ubuntu's AppArmor restriction, a hardened kernel) is found
// here, with its message.
func probeSandbox(executable string) (string, bool, error) {
	bwrap, err := bwrapPath()
	if err != nil {
		return "", false, err
	}
	disableUserns := bwrapSupports(bwrap, "--disable-userns")
	probe := executable
	args := []string{"-hide_banner", "-version"}
	if trivial, lookErr := resolveExecutable("true"); lookErr == nil {
		probe, args = trivial, nil
	}
	argv, err := linuxArgv(bwrap, Job{Executable: probe, Args: args}, disableUserns)
	if err != nil {
		return "", false, fmt.Errorf("the sandbox can't be prepared: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = environment()
	var stderr bytes.Buffer
	cmd.Stderr = &limited{buffer: &stderr, limit: 512}
	if err = cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", false, fmt.Errorf("bubblewrap can't create a sandbox here (user namespaces may be disabled or restricted): %s", detail)
	}
	if _, err = librariesFor(executable, configuredLibraries()); err != nil {
		return "", false, fmt.Errorf("the libraries of %s can't be resolved for the sandbox: %v", executable, err)
	}
	return "bubblewrap", disableUserns, nil
}

func bwrapSupports(bwrap, flag string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bwrap, "--help").CombinedOutput()
	return bytes.Contains(out, []byte(flag))
}

func sandboxArgv(job Job, posture Posture) ([]string, error) {
	bwrap, err := bwrapPath()
	if err != nil {
		return nil, err
	}
	return linuxArgv(bwrap, job, posture.supportsDisableUserns)
}

// LinuxHardwareSandboxArgs is the existing hardware-job grant set, shared with
// the decoder's isolated loopback bridge. It does not grant network or writes.
func LinuxHardwareSandboxArgs(environment [][2]string) []string {
	args := []string{}
	for _, kv := range environment {
		args = append(args, "--setenv", kv[0], kv[1])
	}
	for _, dir := range hardwareReadable {
		args = append(args, "--ro-bind-try", dir, dir)
	}
	args = append(args, "--dev-bind-try", "/dev/dri", "/dev/dri")
	for _, device := range []string{"/dev/dxg", "/dev/mpp_service", "/dev/rga", "/dev/dma_heap"} {
		args = append(args, "--dev-bind-try", device, device)
	}
	devices, _ := filepath.Glob("/dev/nvidia*")
	sort.Strings(devices)
	for _, device := range devices {
		args = append(args, "--dev-bind-try", device, device)
	}
	return args
}

// linuxArgv is the bubblewrap invocation for a job. The root is an empty,
// read-only tmpfs; the process sees the tool, its exact ELF dependencies, the
// job's declared paths, a private /tmp, a minimal /dev and its own /proc (so
// /dev/fd/3 reaches the inherited input). Descriptors pass through bubblewrap
// unchanged.
func linuxArgv(bwrap string, job Job, disableUserns bool) ([]string, error) {
	if job.Loopback != "" && setting() == SettingRequired {
		return nil, ErrSandboxRequired
	}
	// The tool's own dependencies, resolved from its ELF headers, plus the
	// caller's already-resolved exact files.
	libraries, err := librariesFor(job.Executable, configuredLibraries())
	if err != nil {
		return nil, err
	}
	libraries = append(append([]string{}, libraries...), job.Libraries...)
	args := []string{bwrap, "--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-cgroup-try"}
	// A job with a loopback input and no decoder helper keeps the host network
	// namespace only in optional mode. A protocol whitelist does not confine a
	// compromised process; required mode refuses this fallback above. Network
	// posture is reported separately from the filesystem sandbox.
	if job.Loopback == "" {
		args = append(args, "--unshare-net")
	}
	if disableUserns {
		args = append(args, "--disable-userns")
	}
	args = append(args, "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv", "--setenv", "PATH", "/usr/bin:/bin", "--setenv", "TMPDIR", os.TempDir(), "--setenv", "TMP", os.TempDir(), "--setenv", "TEMP", os.TempDir(), "--setenv", "HOME", os.TempDir())
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", os.TempDir(), "--chdir", os.TempDir())
	if job.Hardware {
		args = append(args, LinuxHardwareSandboxArgs(hardwareEnvironment())...)
	}
	if job.Fonts {
		// After the /tmp tmpfs, so a pack under /tmp is visible inside it.
		if pack, err := Fonts(); err == nil {
			for _, dir := range append([]string{pack.Dir}, pack.Host...) {
				args = append(args, "--ro-bind-try", dir, dir)
			}
			args = append(args, "--setenv", "FONTCONFIG_FILE", pack.Config)
		}
	}
	dirs := []string{}
	seen := map[string]bool{}
	for _, file := range append(append([]string{}, libraries...), job.Executable) {
		if seen[file] {
			continue
		}
		seen[file] = true
		args = append(args, "--ro-bind", file, file)
		if dir := filepath.Dir(file); !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) > 0 {
		args = append(args, "--setenv", "LD_LIBRARY_PATH", strings.Join(dirs, ":"))
	}
	for _, path := range job.ReadPaths {
		args = append(args, "--ro-bind", path, path)
	}
	if job.InputFD == 3 && job.InputMount != "" {
		// The disc reader's directory, mounted from the helper's admitted
		// descriptor. Nothing else of the host root is mounted, so the
		// sandbox cannot walk out of the subtree with "..".
		args = append(args, "--ro-bind-fd", "3", job.InputMount)
	}
	for _, dir := range job.WriteDirs {
		args = append(args, "--bind", dir, dir)
	}
	args = append(args, "--remount-ro", "/", "--", job.Executable)
	return append(args, job.Args...), nil
}

type limited struct {
	buffer *bytes.Buffer
	limit  int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.limit - l.buffer.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		l.buffer.Write(p[:room])
	}
	return len(p), nil
}
