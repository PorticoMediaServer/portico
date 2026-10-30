//go:build linux

package assets

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This adapter identifies the same mounted path as remote inventory. Playback
// must use the physical helper; no native RemoteSources operation is supplied.
type playbackMountedRemote struct {
	storage.RemoteSources
	root string
}

func (r playbackMountedRemote) Handles(path string) bool {
	relative, err := filepath.Rel(r.root, path)
	return err == nil && filepath.IsLocal(relative)
}

func mountedPlaybackFixture(t *testing.T, physical bool) (*sql.DB, *storage.Client, string) {
	t.Helper()
	db, root := playbackBindingDatabase(t)
	mount := root
	if physical {
		// Use an existing real mount without mounting anything or requiring
		// rclone, FUSE, privileges or external network access.
		mount = "/dev/shm"
		current, err := os.Lstat(mount)
		parent, parentErr := os.Stat(filepath.Dir(mount))
		if err != nil || parentErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
			t.Skip("a writable physical /dev/shm mount is required")
		}
		left, leftOK := current.Sys().(*syscall.Stat_t)
		right, rightOK := parent.Sys().(*syscall.Stat_t)
		if !leftOK || !rightOK || left.Dev == right.Dev {
			t.Skip("/dev/shm is not a separate mounted filesystem")
		}
		root, err = os.MkdirTemp(mount, "portico-playback-")
		if err != nil {
			t.Skipf("physical mount is not writable: %v", err)
		}
		t.Cleanup(func() { os.RemoveAll(root) })
	}
	path := filepath.Join(root, "source.mp4")
	if err := os.WriteFile(path, []byte("mounted movie bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE library_sources SET root=?,root_identity='remote-dir:source:opaque',kind='remote'`, []any{root}},
		{`UPDATE catalog_assets SET path=?,size=?,modified_ns=?`, []any{path, info.Size(), info.ModTime().UnixNano()}},
		{`UPDATE playback_source_pins SET size=?,modified_ns=?`, []any{info.Size(), info.ModTime().UnixNano()}},
		{`UPDATE inventory_objects SET object_identity='remote-object:opaque',size=?,modified_ns=?`, []any{info.Size(), info.ModTime().UnixNano()}},
	} {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	pinTestPlaybackBinding(t, db)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(binary)
	client.Timeout = 2 * time.Second
	client.Remote = playbackMountedRemote{root: root}
	client.MountedRoot = func(path string) string {
		relative, err := filepath.Rel(mount, path)
		if err == nil && filepath.IsLocal(relative) {
			return mount
		}
		return ""
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for client.Supervisor.Active() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if client.Supervisor.Active() != 0 {
			t.Error("mounted playback retained a storage helper")
		}
	})
	return db, client, path
}

func TestOpenPlaybackInputPreservesMountedRemoteInventory(t *testing.T) {
	db, client, path := mountedPlaybackFixture(t, true)
	source, err := ResolvePlaybackSource(context.Background(), db, "session", "")
	if err != nil || source.Binding == nil || !source.Binding.Mounted || !client.IsRemote(path) {
		t.Fatalf("lost remote mounted source: %+v %v", source, err)
	}
	input, err := OpenPlaybackInput(context.Background(), db, client, "session")
	if err != nil {
		t.Fatal("ordinary HLS input rejected mounted remote inventory", err)
	}
	defer input.Close()
	data, err := io.ReadAll(input.File)
	if err != nil || string(data) != "mounted movie bytes" || !input.Inherit || input.Argument != "/dev/fd/3" {
		t.Fatalf("mounted converter input: %q %+v %v", data, input, err)
	}
	input.Close()
	// The weaker compatibility path still retains the helper's no-follow
	// boundary; matching bytes, size and timestamp cannot authorize a symlink.
	if err := os.Rename(path, path+".target"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".target", path); err != nil {
		t.Fatal(err)
	}
	if input, err := OpenPlaybackInput(context.Background(), db, client, "session"); err == nil {
		input.Close()
		t.Fatal("mounted source followed a substituted symlink")
	}
}

func TestOpenPlaybackInputRejectsUnmountedRemoteInventory(t *testing.T) {
	db, client, _ := mountedPlaybackFixture(t, false)
	if input, err := OpenPlaybackInput(context.Background(), db, client, "session"); err == nil {
		input.Close()
		t.Fatal("registry entry alone made an ordinary directory a mount")
	}
}

func TestRemotePlaybackResolverRetainsSourceAndCatalogFences(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE library_sources SET generation=4`,
		`UPDATE library_sources SET incarnation='replacement'`,
		`UPDATE library_sources SET health='offline'`,
		`UPDATE library_sources SET enabled=0`,
		`UPDATE library_sources SET identity_confirmed=0`,
		`UPDATE inventory_objects SET retired=1`,
		`UPDATE inventory_objects SET state='missing'`,
		`UPDATE inventory_objects SET root_incarnation='replacement'`,
		`UPDATE inventory_objects SET object_identity=''`,
		`UPDATE catalog_assets SET size=13`,
	} {
		t.Run(mutation, func(t *testing.T) {
			db, _ := playbackBindingDatabase(t)
			if _, err := db.Exec(`UPDATE library_sources SET kind='remote'`); err != nil {
				t.Fatal(err)
			}
			pinTestPlaybackBinding(t, db)
			if source, err := ResolvePlaybackSource(context.Background(), db, "session", ""); err != nil || source.Binding == nil || !source.Binding.Mounted {
				t.Fatalf("initial remote scan evidence was not admitted: %+v %v", source, err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			accepted, err := ResolvePlaybackSource(context.Background(), db, "session", "")
			if strings.HasPrefix(mutation, "UPDATE library_sources") || strings.HasPrefix(mutation, "UPDATE catalog_assets") {
				if err == nil {
					t.Fatal("remote session bypassed source/catalog fence")
				}
			} else if err != nil || accepted.Binding == nil || !accepted.Binding.Mounted || accepted.Binding.ObjectIdentity != "ino:42" {
				t.Fatalf("mounted session followed inventory: %+v %v", accepted, err)
			}
			if mutation != `UPDATE library_sources SET generation=4` {
				if _, err := ResolveCatalogPlaybackSource(context.Background(), db, "asset"); err == nil {
					t.Fatal("catalog bypassed stale remote inventory")
				}
			}
		})
	}
}
