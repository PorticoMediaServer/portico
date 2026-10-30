//go:build linux

package dlna

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type dlnaMountedRemote struct {
	storage.RemoteSources
	root string
}

func (r dlnaMountedRemote) Handles(path string) bool {
	relative, err := filepath.Rel(r.root, path)
	return err == nil && filepath.IsLocal(relative)
}

func TestMediaServesMountedRemoteInventoryThroughHelper(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const mount = "/dev/shm"
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
	root, err := os.MkdirTemp(mount, "portico-dlna-")
	if err != nil {
		t.Skipf("physical mount is not writable: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "Heat.mkv")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.server.Storage.Timeout = 2 * time.Second
	f.server.Storage.Remote = dlnaMountedRemote{root: root}
	f.server.Storage.MountedRoot = func(path string) string {
		relative, err := filepath.Rel(mount, path)
		if err == nil && filepath.IsLocal(relative) {
			return mount
		}
		return ""
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for f.server.Storage.Supervisor.Active() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if f.server.Storage.Supervisor.Active() != 0 {
			t.Error("DLNA retained a mounted reader helper")
		}
	})
	token := f.items["heat"].Token
	f.c.Exec(`UPDATE catalog_assets SET path=?,size=?,modified_ns=? WHERE token=?`, path, info.Size(), info.ModTime().UnixNano(), token)
	f.c.Exec(`UPDATE library_sources SET kind='remote',root=?,configured_root=?,root_identity='remote-dir:provider:opaque',identity_confirmed=1 WHERE id='films'`, root, root)
	f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns)
SELECT 'mounted-object',s.id,a.token,s.incarnation,'Heat.mkv','remote-object:opaque','revision','{}',a.size,a.modified_ns FROM library_sources s,catalog_assets a WHERE s.id='films' AND a.token=?`, token)
	r := httptest.NewRequest(http.MethodGet, "/dlna/media/"+token+".mkv", nil)
	r.RemoteAddr = "192.168.1.20:40000"
	r.Header.Set("Range", "bytes=4-7")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "4567" || !f.server.Storage.IsRemote(path) {
		t.Fatalf("mounted remote original: %d %q", w.Code, w.Body.String())
	}
	// An unchanged catalog row cannot bypass either current source health or
	// the mount registry at the actual storage acquisition boundary.
	f.c.Exec(`UPDATE library_sources SET health='offline' WHERE id='films'`)
	if w := f.request(http.MethodGet, "/dlna/media/"+token, "192.168.1.20", ""); w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" || w.Body.String() != http.StatusText(http.StatusServiceUnavailable)+"\n" {
		t.Fatalf("offline mapped source: %d retry=%q body=%q", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	f.c.Exec(`UPDATE library_sources SET health='ready' WHERE id='films'`)
	reconnected := httptest.NewRecorder()
	f.server.ServeHTTP(reconnected, r.Clone(r.Context()))
	if reconnected.Code != http.StatusPartialContent || reconnected.Body.String() != "4567" {
		t.Fatalf("reconnected mapped source: %d %q", reconnected.Code, reconnected.Body.String())
	}
	f.server.Storage.MountedRoot = nil
	if w := f.request(http.MethodGet, "/dlna/media/"+token, "192.168.1.20", ""); w.Code != http.StatusConflict {
		t.Fatalf("remote source without physical registry: %d", w.Code)
	}
}
