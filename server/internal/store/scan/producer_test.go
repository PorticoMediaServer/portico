package scan

import (
	"bytes"
	"context"
	"encoding/json"
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
	client  *storage.Client
}

func (r *localReader) NativeInventoryPage(ctx context.Context, owner *storage.OperationScope, key string, q storage.NativeInventoryRequest) (storage.NativeInventoryPage, error) {
	r.calls++
	if r.client == nil {
		binary, err := os.Executable()
		if err != nil {
			return storage.NativeInventoryPage{}, err
		}
		r.client = storage.New(binary)
	}
	return r.client.NativeInventoryPage(ctx, owner, key, q)
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
	counts, err := WalkInventory(t.Context(), reader, producerOwner(t), "test", []byte(root), 256<<10, func(_ context.Context, turn InventoryTurn) error {
		q, p := turn.Request, turn.Page
		if q.Limit != 1 || q.FollowSymlinks || q.Cursor != cursors[string(q.RelativePath)] {
			t.Fatalf("changed original request %+v", q)
		}
		// Every delivered observation is the original adapter page, including EOF.
		replay, err := reader.client.NativeInventoryPage(t.Context(), producerOwner(t), "replay", q)
		if err != nil || !reflect.DeepEqual(p, replay) {
			t.Fatalf("changed native page: %v", err)
		}
		cursors[string(q.RelativePath)] = p.NextCursor
		for _, e := range p.Entries {
			if !e.Directory {
				if files[string(e.Path)] {
					t.Fatal("duplicate file")
				}
				files[string(e.Path)] = true
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
	counts, err := WalkInventory(t.Context(), reader, producerOwner(t), "test", []byte(root), 4096, func(context.Context, InventoryTurn) error {
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
	page           storage.NativeInventoryPage
	calls, stopped int
}

func (r *pageReader) NativeInventoryPage(context.Context, *storage.OperationScope, string, storage.NativeInventoryRequest) (storage.NativeInventoryPage, error) {
	r.calls++
	return r.page, nil
}
func (r *pageReader) StopInventory(string) { r.stopped++ }
func TestProducerRejectsPartialAndOversizedObservations(t *testing.T) {
	good := storage.NativeInventoryPage{Version: 1, RootIdentity: "root", DirectoryIdentity: "directory", DirectoryRevision: "revision", Complete: true}
	for _, tc := range []struct {
		name   string
		edit   func(*storage.NativeInventoryPage)
		budget int64
	}{
		{"unstable", func(p *storage.NativeInventoryPage) { p.Unstable = true }, 4096},
		{"wire version", func(p *storage.NativeInventoryPage) { p.Version = 0 }, 4096},
		{"no progress", func(p *storage.NativeInventoryPage) { p.Complete = false }, 4096},
		{"missing identity", func(p *storage.NativeInventoryPage) { p.DirectoryIdentity = "" }, 4096},
		{"bytes", func(p *storage.NativeInventoryPage) {}, 1},
		{"escape", func(p *storage.NativeInventoryPage) {
			p.Entries = []storage.NativeInventoryEntry{{Name: []byte("../outside"), ObjectIdentity: "obj"}}
		}, 4096},
		{"two names", func(p *storage.NativeInventoryPage) { p.Entries = make([]storage.NativeInventoryEntry, 2) }, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &pageReader{page: good}
			tc.edit(&r.page)
			visited := false
			_, err := WalkInventory(t.Context(), r, producerOwner(t), "test", []byte(t.TempDir()), tc.budget, func(context.Context, InventoryTurn) error { visited = true; return nil })
			if err == nil || visited || r.stopped != 1 {
				t.Fatalf("visited=%v stopped=%d err=%v", visited, r.stopped, err)
			}
		})
	}
}

func TestProducerCancellationAndIssueEOF(t *testing.T) {
	r := &pageReader{page: storage.NativeInventoryPage{Version: 1, RootIdentity: "r", DirectoryIdentity: "d", DirectoryRevision: "v", Complete: true, Issues: []storage.NativeInventoryIssue{{Path: []byte("hidden"), Reason: []byte("ignored_name")}}}}
	counts, err := WalkInventory(t.Context(), r, producerOwner(t), "test", []byte(t.TempDir()), 4096, func(context.Context, InventoryTurn) error { return nil })
	if err != nil || counts.Issues != 1 || counts.Files != 0 || counts.Pages != 1 {
		t.Fatalf("%+v %v", counts, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r.calls = 0
	_, err = WalkInventory(ctx, r, producerOwner(t), "test", []byte(t.TempDir()), 4096, func(context.Context, InventoryTurn) error { t.Fatal("visited after cancellation"); return nil })
	if !errors.Is(err, context.Canceled) || r.calls != 0 {
		t.Fatal(err, r.calls)
	}
}

func producerOwner(t *testing.T) *storage.OperationScope {
	t.Helper()
	owner := storage.NewOperationScope()
	t.Cleanup(func() {
		owner.Close()
		if err := owner.Wait(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return owner
}
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProducerRealHelperNativeBytesRoundtrip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux native d_off")
	}
	root := filepath.Join(t.TempDir(), "root-\xff")
	nested := "directory-\xfe"
	if err := os.MkdirAll(filepath.Join(root, nested), 0700); err != nil {
		t.Fatal(err)
	}
	names := []string{"Film-\xff.mkv", "Film-\xfe.mkv", "Film-\xef\xbf\xbd.mkv", "NUL-adjacent-\x01\xff\x01.mkv"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, nested, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader := &localReader{}
	owner := producerOwner(t)
	seen := map[string]bool{}
	counts, err := WalkInventory(t.Context(), reader, owner, "native", []byte(root), 256<<10, func(_ context.Context, turn InventoryTurn) error {
		// The producer's full original request/page can be persisted or passed to
		// SC's raw-byte DTO without a replacement string at either JSON boundary.
		encoded, err := json.Marshal(turn)
		if err != nil {
			return err
		}
		var restored InventoryTurn
		if err := json.Unmarshal(encoded, &restored); err != nil {
			return err
		}
		if !reflect.DeepEqual(turn, restored) {
			t.Fatal("producer path was not byte-exact")
		}
		if !bytes.Equal(restored.Request.Root, []byte(root)) {
			t.Fatal("native root changed")
		}
		for _, e := range restored.Page.Entries {
			if !e.Directory {
				seen[string(e.Path)] = true
			}
		}
		return nil
	})
	if err != nil || counts.Files != uint64(len(names)) {
		t.Fatal(counts, err)
	}
	for _, name := range names {
		if !seen[filepath.Join(root, nested, name)] {
			t.Fatalf("native filename replaced: %x", name)
		}
	}
	owner.Close()
	if err := owner.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reader.client.Supervisor.Active() != 0 || owner.Active() != 0 || reader.client.Supervisor.Stats().InProcess != 0 {
		t.Fatal("producer did not join real helpers")
	}
}

func TestProducerNULBytesRefuseWithoutConsumption(t *testing.T) {
	root := []byte(t.TempDir())
	for _, name := range [][]byte{{0, 0xff}, {0xff, 0}, {1, 0, 1}} {
		p := storage.NativeInventoryPage{Version: 1, RootIdentity: "r", DirectoryIdentity: "d", DirectoryRevision: "v", NextCursor: "123",
			Entries: []storage.NativeInventoryEntry{{Name: name, Path: append(append(bytes.Clone(root), '/'), name...), ObjectIdentity: "o"}}}
		wire, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var carrier struct{ Entries []struct{ Name []byte } }
		if err := json.Unmarshal(wire, &carrier); err != nil || len(carrier.Entries) != 1 || !bytes.Equal(carrier.Entries[0].Name, name) {
			t.Fatal("NUL bytes replaced", err)
		}
		var decoded storage.NativeInventoryPage
		if err := json.Unmarshal(wire, &decoded); !errors.Is(err, storage.ErrNativeInventory) || len(decoded.Entries) != 0 {
			t.Fatal("strict transport admitted NUL", err)
		}
		// Also reject an invalid in-process adapter observation before consumption.
		r := &pageReader{page: p}
		counts, err := WalkInventory(t.Context(), r, producerOwner(t), "nul", root, 4096, func(context.Context, InventoryTurn) error { t.Fatal("NUL candidate consumed"); return nil })
		if !errors.Is(err, ErrInvalid) || counts.Pages != 0 {
			t.Fatal("invalid path advanced", counts, err)
		}
	}
}
