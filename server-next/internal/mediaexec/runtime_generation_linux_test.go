//go:build linux

package mediaexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedRuntimeIdentityReadsNoExecutableOrMetadataBytes(t *testing.T) {
	tool := copyCacheELF(t)
	identity, complete, err := RuntimeIdentity(t.Context(), tool)
	if err != nil || !complete {
		t.Fatal(identity, complete, err)
	}
	priorCode, priorMetadata := openRuntimeIdentityFile, openLibraryMetadataInput
	t.Cleanup(func() { openRuntimeIdentityFile, openLibraryMetadataInput = priorCode, priorMetadata })
	reads := 0
	openRuntimeIdentityFile = func(string) (*os.File, error) {
		reads++
		return nil, errors.New("request must not read executable bytes")
	}
	openLibraryMetadataInput = func(string, int, os.FileMode) (*os.File, error) {
		reads++
		return nil, errors.New("request must not read ELF metadata bytes")
	}
	for range 20 {
		if got, ready := CachedRuntimeIdentity(t.Context(), tool); !ready || got != identity {
			t.Fatal("completed generation was not reused", got, ready)
		}
	}
	if got, ready := CachedRuntimeIdentity(t.Context(), tool+"-cold"); ready || got != "" {
		t.Fatal("cold runtime received another tool's identity", got, ready)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ready := CachedRuntimeIdentity(ctx, tool); ready {
		t.Fatal("retired request received a generation")
	}
	if reads != 0 {
		t.Fatalf("cached selection attempted %d code/metadata reads", reads)
	}
}

// recheckEveryCall makes every cached lookup confirm its files, so a change is
// refused on the very next call.
func recheckEveryCall(t *testing.T) {
	previous := runtimeGenerationRecheck
	runtimeGenerationRecheck = 0
	t.Cleanup(func() { runtimeGenerationRecheck = previous })
}

func TestCachedRuntimeIdentityRefusesChangedCodeGeneration(t *testing.T) {
	recheckEveryCall(t)
	tool := copyCacheELF(t)
	before, complete, err := RuntimeIdentity(t.Context(), tool)
	if err != nil || !complete {
		t.Fatal(before, complete, err)
	}
	info, err := os.Stat(tool)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	raw[8] ^= 1 // ELF padding: same file length and loader closure.
	if err := os.WriteFile(tool, raw, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tool, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got, ready := CachedRuntimeIdentity(t.Context(), tool); ready || got != "" {
		t.Fatal("same-size/restored-mtime edit retained a positive", got, ready)
	}
	after, complete, err := RuntimeIdentity(t.Context(), tool)
	if err != nil || !complete || after == before {
		t.Fatal("fresh observation did not refresh the generation", after, complete, err)
	}
	if got, ready := CachedRuntimeIdentity(t.Context(), tool); !ready || got != after {
		t.Fatal("refreshed generation unavailable", got, ready)
	}
}

func TestCachedRuntimeIdentityRefusesLibraryAndAliasChanges(t *testing.T) {
	recheckEveryCall(t)
	for _, change := range []string{"library", "alias"} {
		t.Run(change, func(t *testing.T) {
			tool, library := copyCacheELF(t), copyCacheELF(t)
			alias := filepath.Join(t.TempDir(), "libgeneration.so")
			if err := os.Symlink(library, alias); err != nil {
				t.Fatal(err)
			}
			configured.Lock()
			previous := configured.libraries
			configured.libraries = []string{alias}
			configured.Unlock()
			t.Cleanup(func() {
				configured.Lock()
				configured.libraries = previous
				configured.Unlock()
			})
			if _, complete, err := RuntimeIdentity(t.Context(), tool); err != nil || !complete {
				t.Fatal(complete, err)
			}
			if _, ready := CachedRuntimeIdentity(t.Context(), tool); !ready {
				t.Fatal("completed configured-library generation missing")
			}
			if change == "alias" {
				replacement := copyCacheELF(t)
				if err := os.Symlink(replacement, alias+".new"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(alias+".new", alias); err != nil {
					t.Fatal(err)
				}
			} else {
				info, err := os.Stat(library)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(library)
				if err != nil {
					t.Fatal(err)
				}
				raw[8] ^= 1
				if err := os.WriteFile(library, raw, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(library, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if _, ready := CachedRuntimeIdentity(t.Context(), tool); ready {
				t.Fatal("changed library ownership reused a generation", change)
			}
		})
	}
}

func BenchmarkRuntimeIdentityGeneration(b *testing.B) {
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		b.Skip("FFmpeg integration benchmark")
	}
	if _, complete, err := RuntimeIdentity(b.Context(), tool); err != nil || !complete {
		b.Fatal(complete, err)
	}
	b.Run("full", func(b *testing.B) {
		for b.Loop() {
			if _, complete, err := RuntimeIdentity(b.Context(), tool); err != nil || !complete {
				b.Fatal(complete, err)
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		for b.Loop() {
			if _, ready := CachedRuntimeIdentity(b.Context(), tool); !ready {
				b.Fatal("completed runtime generation missing")
			}
		}
	})
}

// Playback-options asked for the identity on every call, and confirming it stats
// the whole closure. A confirmed generation is trusted for the recheck window,
// then confirmed again: a replaced binary is refused once the window passes.
func TestCachedRuntimeIdentityRechecksItsFilesOncePerWindow(t *testing.T) {
	previous := runtimeGenerationRecheck
	runtimeGenerationRecheck = time.Hour
	t.Cleanup(func() { runtimeGenerationRecheck = previous })
	tool := copyCacheELF(t)
	identity, complete, err := RuntimeIdentity(t.Context(), tool)
	if err != nil || !complete {
		t.Fatal(identity, complete, err)
	}
	if err := os.Chtimes(tool, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, ready := CachedRuntimeIdentity(t.Context(), tool); !ready || got != identity {
		t.Fatal("a confirmed generation was stat'ed again inside its window", got, ready)
	}
	runtimeGenerationRecheck = 0
	if got, ready := CachedRuntimeIdentity(t.Context(), tool); ready || got != "" {
		t.Fatal("a changed executable was trusted after the window", got, ready)
	}
}
