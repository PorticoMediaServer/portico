//go:build linux

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInventoryWatcherSeesAddsInPlaceWritesAndRemovals(t *testing.T) {
	root := t.TempDir()
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	w, err := newInventoryWatcher(WatchRequest{Roots: []WatchRoot{{SourceID: "source", Root: root, RootIdentity: RootIdentity(observed), Directories: []string{"."}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	path := filepath.Join(root, "film.mkv")
	for _, change := range []func() error{func() error { return os.WriteFile(path, []byte("first"), 0600) }, func() error { return os.WriteFile(path, []byte("changed in place"), 0600) }, func() error { return os.Remove(path) }} {
		if err = change(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		events, err := w.Next(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 || events[0].SourceID != "source" || events[0].RelativePath != "film.mkv" {
			t.Fatalf("event: %+v", events)
		}
	}
}
func TestInventoryWatcherRunsInSupervisedHelperAndCancels(t *testing.T) {
	root := t.TempDir()
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := New(binary)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{}, 1)
	events := make(chan InventoryChange, 8)
	done := make(chan error, 1)
	go func() {
		done <- client.WatchInventory(ctx, WatchRequest{Roots: []WatchRoot{{SourceID: "source", Root: root, RootIdentity: RootIdentity(observed), Directories: []string{"."}}}}, func(change InventoryChange) {
			if change.Ready {
				select {
				case ready <- struct{}{}:
				default:
				}
			} else {
				select {
				case events <- change:
				default:
				}
			}
		})
	}()
	select {
	case <-ready:
	case err = <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("watcher did not become ready")
	}
	if err = os.WriteFile(filepath.Join(root, "movie.nfo"), []byte("<movie/>"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.SourceID != "source" || event.RelativePath != "movie.nfo" {
			t.Fatal(event)
		}
	case <-ctx.Done():
		t.Fatal("helper missed a change")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch helper did not cancel promptly")
	}
}

func TestInventoryHelperDistinguishesAbsentFoldersFromUnreadableOnes(t *testing.T) {
	root := t.TempDir()
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := New(binary)
	request := InventoryRequest{Root: root, RootIdentity: RootIdentity(observed), RelativePath: "removed", Limit: 32}
	if _, err = client.InventoryPage(context.Background(), "missing", request); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing helper error: %v", err)
	}
	request.RelativePath = "unreadable"
	if err = os.Mkdir(filepath.Join(root, request.RelativePath), 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(root, request.RelativePath), 0700)
	if os.Geteuid() != 0 {
		if _, err = client.InventoryPage(context.Background(), "blocked", request); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("permission helper error: %v", err)
		}
	}
}

func TestInventoryWatcherNotifiesSourcesSharingAnInode(t *testing.T) {
	root := t.TempDir()
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	roots := []WatchRoot{}
	for _, id := range []string{"one", "two"} {
		roots = append(roots, WatchRoot{SourceID: id, Root: root, RootIdentity: RootIdentity(observed), Directories: []string{"."}})
	}
	w, err := newInventoryWatcher(WatchRequest{Roots: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = os.WriteFile(filepath.Join(root, "new.mkv"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := w.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, event := range events {
		got[event.SourceID] = true
	}
	if !got["one"] || !got["two"] {
		t.Fatalf("shared inode events: %+v", events)
	}
}
