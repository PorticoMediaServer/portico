//go:build darwin

package mediaexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sandboxExec = "/usr/bin/sandbox-exec"

var libraryCache sync.Map

func librariesFor(executable string, extra []string) ([]string, error) {
	key := executable + "\x00" + strings.Join(extra, "\x00")
	if info, err := os.Stat(executable); err == nil {
		key += fmt.Sprintf("\x00%d\x00%d", info.Size(), info.ModTime().UnixNano())
	}
	if cached, ok := libraryCache.Load(key); ok {
		return cached.([]string), nil
	}
	libraries, err := ResolveLibraries(executable, executable, extra)
	if err != nil {
		return nil, err
	}
	libraryCache.Store(key, libraries)
	return libraries, nil
}

// probeSandbox runs /usr/bin/true under a job's profile, then checks the tool's
// own libraries can be listed in it.
func probeSandbox(executable string) (string, bool, error) {
	if _, err := os.Stat(sandboxExec); err != nil {
		return "", false, errors.New("/usr/bin/sandbox-exec is missing")
	}
	argv, err := sandboxArgv(Job{Executable: "/usr/bin/true"}, Posture{})
	if err != nil {
		return "", false, fmt.Errorf("the sandbox profile can't be prepared: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = environment()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 512 {
			detail = detail[:512]
		}
		if detail == "" {
			detail = err.Error()
		}
		return "", false, fmt.Errorf("sandbox-exec refused to run: %s", detail)
	}
	if _, err = librariesFor(executable, configuredLibraries()); err != nil {
		return "", false, fmt.Errorf("the libraries of %s can't be resolved for the sandbox: %v", executable, err)
	}
	return "sandbox-exec", false, nil
}

// sandboxArgv is the sandbox-exec invocation for a job. Everything not denied
// stays as macOS allows it (Mach services, IOKit, so VideoToolbox works); the
// profile takes away the network, reading anything but the tool, its libraries,
// the system and the declared inputs, and writing anything but the job's own
// folders. An inherited descriptor needs no path permission.
func sandboxArgv(job Job, _ Posture) ([]string, error) {
	libraries, err := librariesFor(job.Executable, configuredLibraries())
	if err != nil {
		return nil, err
	}
	libraries = append(append([]string{}, libraries...), job.Libraries...)
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny network*)\n")
	if job.Loopback != "" {
		_, port, _ := strings.Cut(job.Loopback, ":")
		b.WriteString(`(allow network-outbound (remote tcp "localhost:` + port + `"))` + "\n")
	}
	// "/Library/Apple/usr/libexec/oah" is Rosetta's runtime: an Intel FFmpeg on
	// Apple silicon can't start without reading it.
	b.WriteString(`(deny file-read-data)
(deny file-write*)
(allow file-read-data
 (literal "/")
 (literal ` + strconv.Quote(job.Executable) + `)
 (literal "/dev/null")
 (literal "/dev/urandom")
 (literal "/dev/random")
 (subpath "/dev/fd")
 (subpath "/System/Library")
 (subpath "/System/Cryptexes/OS")
 (subpath "/System/Volumes/Preboot/Cryptexes/OS")
 (subpath "/usr/lib")
 (subpath "/Library/Apple/System/Library")
 (subpath "/Library/Apple/usr/libexec/oah")
 )
`)
	for _, file := range libraries {
		b.WriteString("(allow file-read-data (literal " + strconv.Quote(file) + "))\n")
	}
	for _, path := range withCanonical(job.ReadPaths) {
		b.WriteString("(allow file-read-data (literal " + strconv.Quote(path) + ") (subpath " + strconv.Quote(path) + "))\n")
	}
	if job.Fonts {
		// The font pack (fonts.go, named by FONTCONFIG_FILE) and the host's fonts.
		if pack, err := Fonts(); err == nil {
			for _, path := range withCanonical(append([]string{pack.Dir}, pack.Host...)) {
				b.WriteString("(allow file-read-data (subpath " + strconv.Quote(path) + "))\n")
			}
		}
	}
	for _, dir := range withCanonical(job.WriteDirs) {
		b.WriteString("(allow file-read-data file-write* (subpath " + strconv.Quote(dir) + "))\n")
	}
	return append([]string{sandboxExec, "-p", b.String(), job.Executable}, job.Args...), nil
}

// withCanonical adds each path's real location: the sandbox matches resolved
// paths, and /var, /tmp and /etc are symlinks into /private on macOS.
func withCanonical(paths []string) []string {
	out := []string{}
	for _, path := range paths {
		out = append(out, path)
		if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
			out = append(out, real)
		}
	}
	return out
}
