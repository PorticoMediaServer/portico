//go:build linux

package mediaexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUsrMergedHardwareFileMountsResolveExistingAliases(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "usr/lib/x86_64-linux-gnu")
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(realDir, "ld-linux-x86-64.so.2")
	if err := os.WriteFile(real, []byte("fixture loader"), 0600); err != nil {
		t.Fatal(err)
	}
	lib64 := filepath.Join(root, "usr/lib64")
	if err := os.MkdirAll(lib64, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib64", filepath.Join(root, "lib64")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/x86_64-linux-gnu/ld-linux-x86-64.so.2", filepath.Join(lib64, filepath.Base(real))); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "lib64", filepath.Base(real))
	args, err := linuxReadOnlyFileArgs(alias, []string{filepath.Join(root, "lib64")})
	if err != nil || !slices.Equal(args, []string{"--ro-bind", real, real}) {
		t.Fatalf("hardware loader mount = %v, %v", args, err)
	}
	args, err = LinuxReadOnlyFileArgs(alias, false)
	if err != nil || !slices.Equal(args, []string{"--ro-bind", alias, alias}) {
		t.Fatalf("software exact alias mount = %v, %v", args, err)
	}
	// A lexical prefix is not a granted subtree, and input paths outside the
	// existing trees must retain their declared spelling.
	outside := filepath.Join(root, "lib64-private", "input")
	args, err = linuxReadOnlyFileArgs(outside, []string{filepath.Join(root, "lib64")})
	if err != nil || !slices.Equal(args, []string{"--ro-bind", outside, outside}) {
		t.Fatalf("outside mount changed = %v, %v", args, err)
	}
	if _, err := linuxReadOnlyFileArgs(alias+"-missing", []string{filepath.Join(root, "lib64")}); err == nil {
		t.Fatal("unresolvable hardware file was accepted")
	}
}

// Explicitly enabled by the Debian-container workflow. Ordinary unit tests do
// not need a namespace-capable host or distribution FFmpeg installation.
func TestUsrMergedSandboxExecution(t *testing.T) {
	if os.Getenv("PORTICO_TEST_USRMERGE") != "1" {
		t.Skip("hosted Debian container integration")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"true", "ffmpeg", "ffprobe"} {
		t.Run(tool, func(t *testing.T) {
			executable, err := exec.LookPath(tool)
			if err != nil {
				t.Fatal(err)
			}
			for _, hardware := range []bool{false, true} {
				job := Job{Executable: executable, Hardware: hardware}
				if tool != "true" {
					job.Args = []string{"-version"}
				}
				argv, err := linuxArgv(bwrap, job, bwrapSupports(bwrap, "--disable-userns"))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("hardware=%v: %v: %s", hardware, err, out)
				}
				t.Logf("%s hardware=%v executed", tool, hardware)
				if hardware {
					// Reconstruct the former exact-file mount grammar to pin the
					// distribution failure rather than just testing a passing fix.
					old := append([]string{}, argv...)
					libraries, err := librariesFor(executable, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, library := range libraries {
						// The /lib parent alias becomes a directory mount inside
						// the sandbox. The loader below /lib64 is itself a symlink
						// in Debian's mounted tree, which is the refused target.
						if real, e := filepath.EvalSymlinks(library); e == nil && real != library && filepath.Dir(library) == "/lib64" {
							for i := 0; i+2 < len(old); i++ {
								if old[i] == "--ro-bind" && old[i+1] == real && old[i+2] == real {
									old[i+1], old[i+2] = library, library
									break
								}
							}
							break
						}
					}
					ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
					out, err := exec.CommandContext(ctx, old[0], old[1:]...).CombinedOutput()
					cancel()
					if err == nil || !strings.Contains(string(out), "symlink destination") {
						t.Fatalf("former mount grammar did not reproduce: %v: %s", err, out)
					}
					t.Logf("former hardware mount refused: %s", strings.TrimSpace(string(out)))
				}
			}
		})
	}
}
