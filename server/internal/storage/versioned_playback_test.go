package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"runtime"
	"strings"
	"testing"
	"time"
)

func versionedLocalFixture(t *testing.T) (*Client, string) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("filesystem version implementation is not qualified on this OS")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "source.bin")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	c.Timeout = 2 * time.Second
	c.Guard = func(p string) error {
		if p != path {
			return ErrPlaybackSource
		}
		return nil
	}
	// This private synthetic fixture owns every mutation and has no concurrent
	// writers. Tests deliberately violate its lease rule to verify detection.
	c.VersionPolicy = func(p string) (LocalVersionPolicy, error) {
		if p != path {
			return LocalVersionPolicy{}, ErrPlaybackSource
		}
		return LocalVersionPolicy{Scope: "fixture-root", StableObjectIDs: true, RevisionTracksWrites: true, NoInPlaceMutation: true}, nil
	}
	t.Cleanup(func() { waitReaderExit(t, c) })
	return c, path
}

func TestVersionedLocalRequiresRootGuarantees(t *testing.T) {
	c, path := versionedLocalFixture(t)
	c.VersionPolicy = nil
	if _, err := c.DiscoverPlaybackVersion(context.Background(), path); !errors.Is(err, mediasource.ErrIdentityRequired) {
		t.Fatalf("unqualified root accepted: %v", err)
	}
	for _, policy := range []LocalVersionPolicy{
		{Scope: "root", StableObjectIDs: true, RevisionTracksWrites: true},
		{Scope: "root", StableObjectIDs: true, NoInPlaceMutation: true},
		{Scope: "root", RevisionTracksWrites: true, NoInPlaceMutation: true},
	} {
		c.VersionPolicy = func(string) (LocalVersionPolicy, error) { return policy, nil }
		if _, err := c.DiscoverPlaybackVersion(context.Background(), path); !errors.Is(err, mediasource.ErrIdentityRequired) {
			t.Fatalf("partial root guarantee accepted: %v", err)
		}
	}
	if c.Supervisor.Active() != 0 {
		t.Fatal("denied root started a helper")
	}
}

func TestVersionedLocalDiscoveryDescriptorAndReader(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	evidence := version.Evidence()
	if evidence.Size != 10 || evidence.Kind != mediasource.LocalRevision || evidence.Scope != "fixture-root" || strings.Contains(evidence.Object, path) || evidence.Object == "" {
		t.Fatal("source evidence missing or exposes locator")
	}
	file, err := c.OpenVersionedPlaybackDescriptor(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("descriptor bytes %q %v", data, err)
	}
	reader, err := c.OpenVersionedPlayback(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Seek(7, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(reader)
	if err != nil || string(data) != "789" {
		t.Fatalf("range bytes %q %v", data, err)
	}
	reader.Close()
	other, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil || other.ID() != version.ID() {
		t.Fatal("stable source version changed", err)
	}
}

func TestVersionedLocalEqualSizeMtimeReplacementBeforeReopen(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if reader, err := c.OpenVersionedPlayback(ctx, path, version); !errors.Is(err, mediasource.ErrSourceChanged) {
		if reader != nil {
			reader.Close()
		}
		t.Fatalf("replacement reader accepted: %v", err)
	}
	if file, err := c.OpenVersionedPlaybackDescriptor(ctx, path, version); !errors.Is(err, mediasource.ErrSourceChanged) {
		if file != nil {
			file.Close()
		}
		t.Fatalf("replacement descriptor accepted: %v", err)
	}
	current, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil || current.ID() == version.ID() {
		t.Fatal("replacement identity did not change", err)
	}
}

func TestVersionedLocalInPlaceMutationRejectsFutureBlocks(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := c.OpenVersionedPlayback(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	file, err := c.OpenVersionedPlaybackDescriptor(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Same inode and size, rewritten in place: the content identity is inode,
	// size and mtime (never ctime, DECISIONS 29 Oct), so the write's new mtime
	// is what refuses the old version. A rewrite that restores the original
	// mtime is the accepted residual risk of that ruling, as with Plex.
	if err := os.WriteFile(path, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidatePlaybackVersion(ctx, file, version); !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatalf("in-place rewrite accepted: %v", err)
	}
	var b [10]byte
	n, err := reader.Read(b[:])
	if n != 0 || !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatalf("changed block returned %d bytes / %v", n, err)
	}
	reader.Close()
}

// R-04: a permission, ownership or ACL write moves only ctime. It changes no
// byte, so a playing source must keep its version and keep reading.
func TestVersionedLocalPermissionChangeKeepsPlaying(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := c.OpenVersionedPlayback(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	file, err := c.OpenVersionedPlaybackDescriptor(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if err := c.ValidatePlaybackVersion(ctx, file, version); err != nil {
		t.Fatalf("a permission change refused the playing version: %v", err)
	}
	again, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID() != version.ID() {
		t.Fatalf("a permission change moved the version: %q -> %q", version.ID(), again.ID())
	}
	var b [10]byte
	if n, err := io.ReadFull(reader, b[:]); n != 10 || err != nil || string(b[:]) != "0123456789" {
		t.Fatalf("playing reader stopped after a permission change: %d %v", n, err)
	}
}

func TestVersionedLocalScopesGuardsAndCancellation(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DiscoverPlaybackVersion(ctx, path+".outside"); !errors.Is(err, ErrPlaybackSource) {
		t.Fatal("root guard bypassed")
	}
	oldPolicy := c.VersionPolicy
	c.VersionPolicy = func(string) (LocalVersionPolicy, error) {
		return LocalVersionPolicy{Scope: "different-root", StableObjectIDs: true, RevisionTracksWrites: true, NoInPlaceMutation: true}, nil
	}
	if _, err := c.OpenVersionedPlayback(ctx, path, version); !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatal("root identity ignored")
	}
	c.VersionPolicy = oldPolicy
	reader, err := c.OpenVersionedPlayback(ctx, path, version)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	waitReaderExit(t, c)
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := c.DiscoverPlaybackVersion(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery started: %v", err)
	}
}

func TestVersionedLocalDescriptorChangedSizeAndMtime(t *testing.T) {
	for _, kind := range []string{"size", "mtime"} {
		t.Run(kind, func(t *testing.T) {
			c, path := versionedLocalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			version, err := c.DiscoverPlaybackVersion(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "size" {
				if err := os.Truncate(path, 5); err != nil {
					t.Fatal(err)
				}
			} else {
				modified := info.ModTime().Add(time.Second)
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			if file, err := c.OpenVersionedPlaybackDescriptor(ctx, path, version); !errors.Is(err, mediasource.ErrSourceChanged) {
				if file != nil {
					file.Close()
				}
				t.Fatalf("%s change became %v", kind, err)
			}
		})
	}
}

type truncateDuringRead struct {
	file *os.File
	path string
}

func (r truncateDuringRead) ReadAt(p []byte, offset int64) (int, error) {
	if err := os.Truncate(r.path, 2); err != nil {
		return 0, err
	}
	return r.file.ReadAt(p, offset)
}

func TestVersionedLocalTruncateBetweenCheckAndRead(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version, err := c.DiscoverPlaybackVersion(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// The injected IO boundary truncates a real fixture only after precheck has
	// passed; the failed read must inspect independent filesystem evidence.
	data, err := checkedPlaybackBlock(truncateDuringRead{file, path}, streamCommand{Offset: 0, Length: 10}, func() error { return matchLocalVersion(file, version.Evidence()) })
	if len(data) != 0 || !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatalf("short mutated read returned %d bytes / %v", len(data), err)
	}
}
