package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type storageEngine struct {
	*fakeEngine
	folders []string
	bytes   int64
}

func (e *storageEngine) MemoryConversionsAvailable() bool         { return false }
func (e *storageEngine) ConversionFolderReady(folder string) bool { return folder == e.folders[0] }
func (e *storageEngine) ConversionFolders() []string              { return e.folders }
func (e *storageEngine) CachedConversions(_ context.Context, empty bool) (int64, error) {
	n := e.bytes
	if empty {
		if err := os.Remove(filepath.Join(e.folders[0], "usage.bin")); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		e.bytes = 0
	}
	return n, nil
}

func TestStorageShowsActualDestinationsAndRefreshesAfterFolderChanges(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	ctx := context.Background()
	old, next := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "next")
	write := func(folder, text string) {
		t.Helper()
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "usage.bin"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(old, "old")
	write(next, "newer")
	call(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]any{"folder": old}, nil)
	if _, err := s.recorder.Library(ctx); err != nil {
		t.Fatal(err)
	}
	external, inside := t.TempDir(), filepath.Join(s.dataDir, "transcodes")
	write(external, "outside")
	write(inside, "inside")
	engine := &storageEngine{fakeEngine: &fakeEngine{}, folders: []string{external, inside}, bytes: 7}
	s.playback.SetEngine(engine, engine)
	first := get[Storage](t, s, owner, "/v1/admin/storage")
	if len(first.Conversions) != 2 || first.Conversions[0].Bytes != 7 || !first.Conversions[0].External ||
		first.Conversions[1].Bytes != 6 || first.Conversions[1].External {
		t.Fatalf("conversion destinations: %+v", first.Conversions)
	}
	if first.MemoryAvailable == nil || *first.MemoryAvailable {
		t.Fatal("storage guessed a RAM filesystem", first.MemoryAvailable)
	}
	if first.Conversions[0].Ready == nil || !*first.Conversions[0].Ready || first.Conversions[0].FreeBytes == nil ||
		first.Conversions[1].Ready == nil || *first.Conversions[1].Ready {
		t.Fatal("storage invented readiness instead of asking the engine", first.Conversions)
	}
	if len(first.Recordings) != 1 || first.Recordings[0].Folder != old || first.Recordings[0].Bytes != 3 || !first.Recordings[0].External {
		t.Fatalf("recording destination: %+v", first.Recordings)
	}
	if w := call(t, s, "PUT", "/v1/admin/settings/dvr", owner, map[string]any{"folder": next}, nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	second := get[Storage](t, s, owner, "/v1/admin/storage")
	if len(second.Recordings) != 2 || second.Recordings[0].Folder != next || second.Recordings[0].Bytes != 5 ||
		second.Recordings[1].Folder != old || second.Recordings[1].Bytes != 3 {
		t.Fatalf("the cached read hid a destination change or the old recordings: %+v", second.Recordings)
	}
	if preview := get[StorageCacheReport](t, s, owner, "/v1/admin/storage/cache"); preview.Bytes != 7 {
		t.Fatal(preview)
	}
	var emptied StorageCacheReport
	if w := call(t, s, "POST", "/v1/admin/storage/cache/empty", owner, nil, &emptied); w.Code != 200 || emptied.Bytes != 7 {
		t.Fatal(w.Code, w.Body)
	}
	if preview := get[StorageCacheReport](t, s, owner, "/v1/admin/storage/cache"); preview.Bytes != 0 {
		t.Fatal(preview)
	}
	refreshed := get[Storage](t, s, owner, "/v1/admin/storage")
	if refreshed.Conversions[0].Bytes != 0 {
		t.Fatal("Empty cache left the cached usage stale", refreshed.Conversions)
	}
	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	for _, route := range []struct{ method, path string }{{"GET", "/v1/admin/storage/cache"}, {"POST", "/v1/admin/storage/cache/empty"}} {
		if got := status(t, s, route.method, route.path, admin, nil); got != 403 {
			t.Fatalf("administrator %s cache: %d", route.method, got)
		}
	}
}
