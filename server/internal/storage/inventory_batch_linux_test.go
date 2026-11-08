//go:build linux

package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
