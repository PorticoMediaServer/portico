//go:build linux

package mediaexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func copyCacheELF(t *testing.T) string {
	t.Helper()
	source, err := exec.LookPath("true")
	if err != nil {
		t.Skip("a local ELF fixture executable is unavailable")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLibraryClosureCacheKeepsExactPathsPrivate(t *testing.T) {
	path := copyCacheELF(t)
	first, err := ResolveLibraries(path, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Skip("fixture executable has no dynamic dependencies")
	}
	expected := slices.Clone(first)
	first[0] = filepath.Join(t.TempDir(), "must-not-be-mounted")
	for range 3 {
		next, err := ResolveLibraries(path, path, nil)
		if err != nil || !slices.Equal(next, expected) {
			t.Fatal("caller mutated cached custody", next, err)
		}
	}
}

func TestLibraryClosureCacheRejectsChangedExecutable(t *testing.T) {
	path := copyCacheELF(t)
	if _, err := ResolveLibraries(path, path, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement is not an ELF executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLibraries(path, path, nil); err == nil {
		t.Fatal("used stale closure for changed executable")
	}
}

func TestLibraryClosureCacheRejectsRemovedExtra(t *testing.T) {
	path := copyCacheELF(t)
	extra := filepath.Join(t.TempDir(), "explicit-owner-dependency")
	if err := os.WriteFile(extra, []byte("controlled metadata dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLibraries(path, path, []string{extra}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLibraries(path, path, []string{extra}); err == nil {
		t.Fatal("used stale deleted dependency")
	}
}

func TestLibraryClosureCacheFollowsChangedAlias(t *testing.T) {
	path := copyCacheELF(t)
	dir := t.TempDir()
	alias := filepath.Join(dir, "tool")
	bad := filepath.Join(dir, "replacement")
	if err := os.WriteFile(bad, []byte("not an ELF"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLibraries(alias, alias, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bad, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLibraries(alias, alias, nil); err == nil {
		t.Fatal("used stale executable symlink target")
	}
}

func TestLibraryClosureCacheNoticesEarlierRUNPATHLibrary(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("C compiler unavailable for real RUNPATH fixture")
	}
	dir := t.TempDir()
	earlier, later := filepath.Join(dir, "earlier"), filepath.Join(dir, "later")
	for _, p := range []string{earlier, later} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	libSource, exeSource := filepath.Join(dir, "lib.c"), filepath.Join(dir, "main.c")
	if err := os.WriteFile(libSource, []byte("int portico_cache_fixture(void) { return 0; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exeSource, []byte("extern int portico_cache_fixture(void); int main(void) { return portico_cache_fixture(); }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	name := "libportico-custody-fixture.so"
	build := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture compile: %s %v", out, err)
		}
	}
	build("-shared", "-fPIC", libSource, "-Wl,-soname,"+name, "-o", filepath.Join(later, name))
	exe := filepath.Join(dir, "tool")
	build(exeSource, "-L"+later, "-lportico-custody-fixture", "-Wl,-rpath,"+earlier+":"+later, "-o", exe)
	first, err := ResolveLibraries(exe, exe, nil)
	if err != nil || !slices.Contains(first, filepath.Join(later, name)) {
		t.Fatal(first, err)
	}
	data, err := os.ReadFile(filepath.Join(later, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(earlier, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	// Make directory generation change explicit even on coarse-timestamp filesystems.
	at := time.Now().Add(time.Second)
	if err := os.Chtimes(earlier, at, at); err != nil {
		t.Fatal(err)
	}
	next, err := ResolveLibraries(exe, exe, nil)
	if err != nil || !slices.Contains(next, filepath.Join(earlier, name)) || slices.Contains(next, filepath.Join(later, name)) {
		t.Fatal("cached lower-priority library", next, err)
	}
}

func TestLibraryClosureCacheConcurrentReads(t *testing.T) {
	path := copyCacheELF(t)
	if _, err := ResolveLibraries(path, path, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 3 {
				if _, err := ResolveLibraries(path, path, nil); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}

func TestLibrariesForRevalidatesRemovedExtra(t *testing.T) {
	tool := copyCacheELF(t)
	extra := filepath.Join(t.TempDir(), "selected-extra")
	if err := os.WriteFile(extra, []byte("trusted explicit dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := librariesFor(tool, []string{extra}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if _, err := librariesFor(tool, []string{extra}); err == nil {
		t.Fatal("outer sandbox cache bypassed dependency revalidation")
	}
}

func TestLibraryClosureCacheRejectsRestoredTimestamp(t *testing.T) {
	tool := copyCacheELF(t)
	before, err := os.Stat(tool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveLibraries(tool, tool, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 0 // Same length and restored mtime; no longer a valid ELF.
	if err = os.WriteFile(tool, raw, before.Mode()); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(tool, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveLibraries(tool, tool, nil); err == nil {
		after, statErr := os.Stat(tool)
		key := tool + "\x00" + tool
		libraryClosures.Lock()
		cached := libraryClosures.entries[key]
		libraryClosures.Unlock()
		if cached != nil {
			t.Logf("cached generation: entry=%+v target=%+v watchUnchanged=%v", cached.watch[tool].entry.Sys(), cached.watch[tool].target.Sys(), cached.watch.unchanged())
		}
		if statErr == nil {
			t.Logf("original generation=%+v current generation=%+v", before.Sys(), after.Sys())
		}
		t.Fatal("same-size restored-mtime replacement retained its cached ELF closure")
	}
}

func TestLibraryClosureCacheFollowsAliasToSameInode(t *testing.T) {
	tool := copyCacheELF(t)
	alternate := filepath.Join(t.TempDir(), "other-origin")
	if err := os.Link(tool, alternate); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "tool")
	if err := os.Symlink(tool, alias); err != nil {
		t.Fatal(err)
	}
	first, err := ResolveLibraries(alias, alias, nil)
	if err != nil || !slices.Contains(first, tool) {
		t.Fatal(first, err)
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(alternate, alias); err != nil {
		t.Fatal(err)
	}
	next, err := ResolveLibraries(alias, alias, nil)
	if err != nil || !slices.Contains(next, alternate) || slices.Contains(next, tool) {
		t.Fatal("same inode retained the wrong loader origin", next, err)
	}
}

func TestLibraryWatchNoticesResolvedDanglingCandidate(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new-library")
	alias := filepath.Join(t.TempDir(), "earlier-candidate.so")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	watch := libraryWatch{}
	watch.record(alias)
	if !watch.unchanged() {
		t.Fatal("unchanged missing target was not recorded")
	}
	if err := os.WriteFile(target, []byte("new higher-priority dependency"), 0600); err != nil {
		t.Fatal(err)
	}
	if watch.unchanged() {
		t.Fatal("resolved earlier candidate failed to invalidate the library closure")
	}
}
