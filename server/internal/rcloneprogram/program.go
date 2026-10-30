// Package rcloneprogram locates the install's rclone and checks its version.
// It does not download programs, approve file bytes or manage remote processes.
package rcloneprogram

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

const BundledVersion = "1.75.2"

// MinimumVersion requires the explicit HTTP proxy option (rclone 1.71.0).
// Environment-only proxy discovery bypasses loopback targets; source egress
// policy requires --http-proxy / RCLONE_HTTP_PROXY. Version 1.71.1 also fixes
// the pacer deadlock with our --max-connections cap. See the bundle notice.
const MinimumVersion = "1.71.1"
const CheckTimeout = 5 * time.Second

type Program struct {
	Path     string
	Included bool
}
type Version struct {
	Version string `json:"version"`
}
type Status struct {
	Included       bool   `json:"included"`
	Available      bool   `json:"available"`
	Version        string `json:"version"`
	MinimumVersion string `json:"minimumVersion"`
	ErrorCode      string `json:"errorCode"`
}

type CheckError struct {
	Code    string
	Version string
}

func (e *CheckError) Error() string {
	if e.Code == "rclone_too_old" {
		return fmt.Sprintf("This rclone is too old (%s). Choose %s or newer.", e.Version, MinimumVersion)
	}
	return "That isn't rclone. Choose the rclone program."
}

// BundledPath uses an installation-relative path and never searches PATH.
func BundledPath(serverPath, goos, goarch string) (string, error) {
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (goarch != "amd64" && goarch != "arm64") {
		return "", fmt.Errorf("no bundled rclone for %s-%s", goos, goarch)
	}
	name := "rclone"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(serverPath), "third_party", "rclone", name), nil
}
func Resolve() (Program, error) {
	if override := os.Getenv("PORTICO_RCLONE"); override != "" {
		return Program{Path: override, Included: true}, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return Program{}, err
	}
	// Resolve only the server's own symlink so an installed launcher still finds its bundle.
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return Program{}, err
	}
	path, err := BundledPath(executable, runtime.GOOS, runtime.GOARCH)
	return Program{Path: path, Included: true}, err
}

// Inspect is called once during server startup. Errors are reduced to the
// install-level state; program output and private paths never become API/log text.
func Inspect(ctx context.Context) Status {
	program, err := Resolve()
	if err != nil {
		return Status{Included: true, MinimumVersion: MinimumVersion, ErrorCode: "cant_start_rclone"}
	}
	return program.Inspect(ctx)
}
func (p Program) Inspect(ctx context.Context) Status {
	status := Status{Included: p.Included, MinimumVersion: MinimumVersion}
	version, err := p.Check(ctx)
	if err != nil || (p.Included && version.Version != BundledVersion) {
		status.ErrorCode = "cant_start_rclone"
		return status
	}
	status.Available = true
	status.Version = version.Version
	return status
}

// CheckVersionLine applies the same rclone and minimum-version rules to a
// version line captured by the owner's existing validation custody. It never
// runs a process itself.
func CheckVersionLine(line string) (Version, error) {
	return parseVersion(strings.TrimSuffix(line, "\r"))
}

// CheckCustom runs only `version`, at every source start. Wrapper scripts and
// in-place upgrades work without permissions, file-format, size or hash approval.
func CheckCustom(ctx context.Context, path string) (Version, error) {
	return (Program{Path: path}).Check(ctx)
}
func (p Program) Check(ctx context.Context) (Version, error) {
	check, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	cmd := versionCommand(check, p.Path)
	childprocess.Configure(cmd)
	defer childprocess.Kill(cmd) // A wrapper must not leave version-check children behind.
	cmd.WaitDelay = 100 * time.Millisecond
	first := &firstLine{}
	cmd.Stdout = first
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || check.Err() != nil {
		return Version{}, &CheckError{Code: "not_rclone"}
	}
	return parseVersion(first.String())
}

// Keep bounded memory without refusing a valid program's later version details.
type firstLine struct {
	data []byte
	done bool
}

func (b *firstLine) Write(p []byte) (int, error) {
	n := len(p)
	for _, c := range p {
		if b.done {
			break
		}
		if c == '\n' {
			b.done = true
			break
		}
		if len(b.data) < 129 {
			b.data = append(b.data, c)
		}
	}
	return n, nil
}
func (b *firstLine) String() string { return strings.TrimSuffix(string(b.data), "\r") }

var versionPattern = regexp.MustCompile(`^rclone v([0-9]+)\.([0-9]+)(?:\.([0-9]+))?([-+][0-9A-Za-z][0-9A-Za-z.+-]*)?$`)

func parseVersion(line string) (Version, error) {
	match := versionPattern.FindStringSubmatch(line)
	if len(line) > 128 || match == nil {
		return Version{}, &CheckError{Code: "not_rclone"}
	}
	numbers := [3]int{}
	for i, part := range match[1:4] {
		if part != "" {
			n, err := strconv.Atoi(part)
			if err != nil {
				return Version{}, &CheckError{Code: "not_rclone"}
			}
			numbers[i] = n
		}
	}
	version := fmt.Sprintf("%d.%d.%d", numbers[0], numbers[1], numbers[2])
	if numbers[0] < 1 || (numbers[0] == 1 && (numbers[1] < 71 || (numbers[1] == 71 && numbers[2] < 1))) {
		return Version{}, &CheckError{Code: "rclone_too_old", Version: version}
	}
	return Version{Version: version + match[4]}, nil
}
