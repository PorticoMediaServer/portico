//go:build linux

package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

func TestNativeInventoryBatchDrainsShortPageBeforeEOF(t *testing.T) {
	for _, count := range []int{1, 3, 8, 9} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < count; i++ {
				if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("Film %d.mkv", i)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			q := NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 8}
			seen := 0
			names := map[string]bool{}
			for turns := 0; turns < 4; turns++ {
				var buf bytes.Buffer
				if err := nativeInventoryEmit(&nativeInventoryWire{NativeInventoryRequest: q}, &buf); err != nil {
					t.Fatal(err)
				}
				var page NativeInventoryPage
				if err := json.Unmarshal(buf.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Complete {
					if seen != count || len(page.Entries) != 0 || page.NextCursor != "" {
						t.Fatal("EOF lost names", seen, page)
					}
					for i := 0; i < count; i++ {
						if !names[fmt.Sprintf("Film %d.mkv", i)] {
							t.Fatal("missing original filename", i)
						}
					}
					return
				}
				if page.NextCursor == "" || page.NextCursor == q.Cursor || len(page.Entries) < 1 {
					t.Fatal("missing native cookie", page)
				}
				var retry bytes.Buffer
				if err := nativeInventoryEmit(&nativeInventoryWire{NativeInventoryRequest: q}, &retry); err != nil {
					t.Fatal(err)
				}
				var again NativeInventoryPage
				if err := json.Unmarshal(retry.Bytes(), &again); err != nil || !reflect.DeepEqual(page, again) {
					t.Fatal("uncommitted page changed", page, again, err)
				}
				seen += len(page.Entries)
				for _, entry := range page.Entries {
					name := string(entry.Name)
					if names[name] {
						t.Fatal("duplicate filename", name)
					}
					names[name] = true
				}
				q.RootIdentity, q.DirectoryRevision, q.Cursor = page.RootIdentity, page.DirectoryRevision, page.NextCursor
			}
			t.Fatal("no empty EOF")
		})
	}
}

func TestNativeInventoryBatchCursorRequiresRevision(t *testing.T) {
	r := NativeInventoryRequest{Root: []byte(t.TempDir()), RelativePath: []byte("."), Limit: 8}
	if !nativeinventory.ValidRequest(r) {
		t.Fatal("initial observation refused")
	}
	r.Cursor = "42"
	if nativeinventory.ValidRequest(r) {
		t.Fatal("unbound continuation accepted")
	}
	r.DirectoryRevision = "observed-revision"
	if !nativeinventory.ValidRequest(r) {
		t.Fatal("bound continuation refused")
	}
}

func TestNativeInventoryBatchShortPageRefusesChangedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Film.mkv"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	c, owner := nativeClient(t)
	r := NativeInventoryRequest{Root: []byte(root), RelativePath: []byte("."), Limit: 8}
	page, err := c.NativeInventoryPage(t.Context(), owner, "batch-stale", r)
	if err != nil || page.Complete || len(page.Entries) != 1 || page.NextCursor == "" {
		t.Fatal("short page did not retain a cookie", page, err)
	}
	r.RootIdentity, r.DirectoryRevision, r.Cursor = page.RootIdentity, page.DirectoryRevision, page.NextCursor
	if err = os.WriteFile(filepath.Join(root, "Added.mkv"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Force a distinct native directory timestamp even on coarse clocks.
	changed := time.Now().Add(time.Hour)
	if err = os.Chtimes(root, changed, changed); err != nil {
		t.Fatal(err)
	}
	page, err = c.NativeInventoryPage(t.Context(), owner, "batch-stale", r)
	if !errors.Is(err, ErrInventoryChanged) || !reflect.DeepEqual(page, NativeInventoryPage{}) {
		t.Fatal("changed directory reused its old cookie", page, err)
	}
}
