package rcloneprogram

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPinnedVersionsMatchPackaging(t *testing.T) {
	raw, err := os.ReadFile("../../../third_party/rclone/bundle.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Version        string
		MinimumVersion string
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != BundledVersion || lock.MinimumVersion != MinimumVersion {
		t.Fatalf("Go/package version mismatch: %+v", lock)
	}
}
func TestPlatformPaths(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			path, err := BundledPath(filepath.Join("install", "portico-server"), platform, arch)
			want := filepath.Join("install", "third_party", "rclone", "rclone")
			if platform == "windows" {
				want += ".exe"
			}
			if err != nil || path != want {
				t.Fatalf("%s/%s: %s %v", platform, arch, path, err)
			}
		}
	}
	if _, err := BundledPath("server", "plan9", "amd64"); err == nil {
		t.Fatal("unsupported target accepted")
	}
	if _, err := BundledPath("server", "linux", "386"); err == nil {
		t.Fatal("unsupported architecture accepted")
	}
}
func TestVersionResults(t *testing.T) {
	for _, tc := range []struct{ line, version, code string }{
		{"rclone v1.71.1", "1.71.1", ""}, {"rclone v1.71.0", "", "rclone_too_old"}, {"rclone v1.71", "", "rclone_too_old"}, {"rclone v1.72.0", "1.72.0", ""}, {"rclone v1.70.0", "", "rclone_too_old"}, {"rclone v1.70.99", "", "rclone_too_old"}, {"rclone v1.75.2", "1.75.2", ""}, {"rclone v2.0.0", "2.0.0", ""}, {"rclone v1.69.9", "", "rclone_too_old"}, {"rclone v1.58", "", "rclone_too_old"}, {"rclone vfixture", "", "not_rclone"}, {"rclone v1.75.2evil", "", "not_rclone"}, {"rclone v1.75.2-beta", "1.75.2-beta", ""}, {"other v9.0.0", "", "not_rclone"},
	} {
		t.Run(tc.line, func(t *testing.T) {
			v, err := parseVersion(tc.line)
			if v.Version != tc.version {
				t.Fatalf("%+v", v)
			}
			var ce *CheckError
			if tc.code != "" {
				if !errors.As(err, &ce) || ce.Code != tc.code {
					t.Fatalf("%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOnlyVersionAndChangedWrappers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper; Windows runtime observation is recorded separately")
	}
	path := filepath.Join(t.TempDir(), "wrapper with spaces")
	write := func(v string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = version ] || exit 7\nprintf 'rclone v"+v+"\\n- go/version: details\\n'\n"), 0777); err != nil {
			t.Fatal(err)
		}
	}
	// Old custom versions must fail before a source can start, even when the
	// wrapper itself starts successfully and accepts the version-only command.
	write("1.71.0")
	if _, err := CheckCustom(context.Background(), path); err == nil {
		t.Fatal("custom program below the supported proxy/cap version accepted")
	} else {
		var checkError *CheckError
		if !errors.As(err, &checkError) || checkError.Code != "rclone_too_old" {
			t.Fatalf("old custom program: %v", err)
		}
	}
	write("1.75.2")
	v, err := CheckCustom(context.Background(), path)
	if err != nil || v.Version != "1.75.2" {
		t.Fatalf("%+v %v", v, err)
	}
	write("1.75.3")
	v, err = CheckCustom(context.Background(), path)
	if err != nil || v.Version != "1.75.3" {
		t.Fatalf("upgrade: %+v %v", v, err)
	}
	if s := (Program{Path: path, Included: true}).Inspect(context.Background()); s.Available || s.ErrorCode != "cant_start_rclone" {
		t.Fatalf("wrong included version: %+v", s)
	}
	if err = os.WriteFile(path, []byte("#!/bin/sh\nprintf 'rclone v1.58\\n'\n"), 0777); err != nil {
		t.Fatal(err)
	}
	_, err = CheckCustom(context.Background(), path)
	if err == nil || err.Error() != "This rclone is too old (1.58.0). Choose 1.71.1 or newer." {
		t.Fatalf("%v", err)
	}
}
func TestBoundedFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper; Windows runtime observation is recorded separately")
	}
	path := filepath.Join(t.TempDir(), "hang")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := CheckCustom(context.Background(), path)
	if err == nil || time.Since(start) > CheckTimeout+time.Second {
		t.Fatalf("timeout %s %v", time.Since(start), err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatal("private path in error")
	}
	missing := Program{Path: filepath.Join(t.TempDir(), "missing"), Included: true}
	if status := missing.Inspect(context.Background()); status.ErrorCode != "cant_start_rclone" || status.Available {
		t.Fatalf("%+v", status)
	}
}
func TestOverride(t *testing.T) {
	t.Setenv("PORTICO_RCLONE", "chosen wrapper")
	p, err := Resolve()
	if err != nil || p.Path != "chosen wrapper" || !p.Included {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestFirstLineBound(t *testing.T) {
	b := &firstLine{}
	_, _ = b.Write([]byte(strings.Repeat("x", 10000)))
	if len(b.String()) != 129 {
		t.Fatal(len(b.String()))
	}
	b = &firstLine{}
	_, _ = b.Write([]byte("rclone v1.75.2\r\n" + strings.Repeat("x", 10000)))
	if b.String() != "rclone v1.75.2" {
		t.Fatal(b.String())
	}
}

func TestVersionWrapperLeavesNoBackgroundChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process-group observation; native OS observations are reported separately")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "wrapper")
	pidfile := filepath.Join(directory, "child.pid")
	// A misbehaving version wrapper exits with a child holding its stdout pipe.
	script := "#!/bin/sh\nsleep 60 &\necho $! > '" + pidfile + "'\necho 'rclone v1.75.2'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, _ = CheckCustom(context.Background(), path)
	raw, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(raw))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stat, err := os.ReadFile("/proc/" + pid + "/stat")
		if os.IsNotExist(err) {
			return
		}
		fields := strings.Fields(string(stat))
		if len(fields) > 2 && fields[2] == "Z" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("version wrapper left running child %s", pid)
}
