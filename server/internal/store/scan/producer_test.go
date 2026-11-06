package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type localReader struct {
	stopped int
	calls   int
}

func (r *localReader) InventoryPage(_ context.Context, _ string, q storage.InventoryRequest) (storage.InventoryPage, error) {
	r.calls++
	return storage.LocalInventoryPage(q)
}
func (r *localReader) StopInventory(string) { r.stopped++ }

func TestProducerNativeN1Traversal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native-cookie FIRST")
	}
	root := t.TempDir()
	for _, dir := range []string{"Movies", "empty", "Movies/nested"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"root.mkv", "Movies/Arrival (2016).mkv", "Movies/nested/Film.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader := &localReader{}
	files := map[string]bool{}
	cursors := map[string]string{}
	counts, err := WalkInventory(t.Context(), reader, "test", root, 256<<10, func(_ context.Context, turn InventoryTurn) error {
		q, p := turn.Request, turn.Page
		if q.Limit != 1 || q.FollowSymlinks || q.Cursor != cursors[q.RelativePath] {
			t.Fatalf("changed original request %+v", q)
		}
		// Every delivered observation is the original adapter page, including EOF.
		replay, err := storage.LocalInventoryPage(q)
		if err != nil || !reflect.DeepEqual(p, replay) {
			t.Fatalf("changed native page: %v", err)
		}
		cursors[q.RelativePath] = p.NextCursor
		for _, e := range p.Entries {
			if !e.Directory {
				if files[e.Path] {
					t.Fatal("duplicate file")
				}
				files[e.Path] = true
			}
		}
		return nil
	})
	if err != nil || counts.Files != 3 || counts.Directories != 3 || len(files) != 3 || reader.stopped != 1 || counts.MaxDepth > 3 {
		t.Fatalf("counts=%+v stop=%d err=%v", counts, reader.stopped, err)
	}
}

func TestProducerBackpressureStopsBeforeNextRead(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native-cookie FIRST")
	}
	root := t.TempDir()
	for _, s := range []string{"a.mkv", "b.mkv"} {
		if err := os.WriteFile(filepath.Join(root, s), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader := &localReader{}
	refused := errors.New("writer refused; original immutable command must be resolved/retried by owner")
	counts, err := WalkInventory(t.Context(), reader, "test", root, 4096, func(context.Context, InventoryTurn) error {
		if reader.calls != 1 {
			t.Fatal("read ahead")
		}
		return refused
	})
	if !errors.Is(err, refused) || counts.Pages != 0 || reader.calls != 1 || reader.stopped != 1 {
		t.Fatalf("counts=%+v calls=%d stopped=%d err=%v", counts, reader.calls, reader.stopped, err)
	}
}

type pageReader struct {
	page           storage.InventoryPage
	calls, stopped int
}

func (r *pageReader) InventoryPage(context.Context, string, storage.InventoryRequest) (storage.InventoryPage, error) {
	r.calls++
	return r.page, nil
}
func (r *pageReader) StopInventory(string) { r.stopped++ }
func TestProducerRejectsPartialAndOversizedObservations(t *testing.T) {
	good := storage.InventoryPage{RootIdentity: "root", DirectoryIdentity: "directory", DirectoryRevision: "revision", Complete: true}
	for _, tc := range []struct {
		name   string
		edit   func(*storage.InventoryPage)
		budget int64
	}{
		{"unstable", func(p *storage.InventoryPage) { p.Unstable = true }, 4096},
		{"pending", func(p *storage.InventoryPage) { p.Pending = true }, 4096},
		{"no progress", func(p *storage.InventoryPage) { p.Complete = false }, 4096},
		{"missing identity", func(p *storage.InventoryPage) { p.DirectoryIdentity = "" }, 4096},
		{"bytes", func(p *storage.InventoryPage) {}, 1},
		{"escape", func(p *storage.InventoryPage) {
			p.Entries = []storage.Snapshot{{Name: "../outside", ObjectIdentity: "obj"}}
		}, 4096},
		{"two names", func(p *storage.InventoryPage) { p.Entries = make([]storage.Snapshot, 2) }, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &pageReader{page: good}
			tc.edit(&r.page)
			visited := false
			_, err := WalkInventory(t.Context(), r, "test", t.TempDir(), tc.budget, func(context.Context, InventoryTurn) error { visited = true; return nil })
			if err == nil || visited || r.stopped != 1 {
				t.Fatalf("visited=%v stopped=%d err=%v", visited, r.stopped, err)
			}
		})
	}
}

func TestProducerCancellationAndIssueEOF(t *testing.T) {
	r := &pageReader{page: storage.InventoryPage{RootIdentity: "r", DirectoryIdentity: "d", DirectoryRevision: "v", Complete: true, Issues: []storage.InventoryIssue{{Path: "hidden", Reason: "ignored_name"}}}}
	counts, err := WalkInventory(t.Context(), r, "test", t.TempDir(), 4096, func(context.Context, InventoryTurn) error { return nil })
	if err != nil || counts.Issues != 1 || counts.Files != 0 || counts.Pages != 1 {
		t.Fatalf("%+v %v", counts, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.calls = 0
	_, err = WalkInventory(ctx, r, "test", t.TempDir(), 4096, func(context.Context, InventoryTurn) error { t.Fatal("visited after cancellation"); return nil })
	if !errors.Is(err, context.Canceled) || r.calls != 0 {
		t.Fatal(err, r.calls)
	}
}
