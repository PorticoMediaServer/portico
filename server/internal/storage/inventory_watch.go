package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

const MaxInventoryWatches = 4096
const MaxWatchRoots = 64

var ErrWatchUnavailable = errors.New("native folder monitoring is unavailable")

type WatchRoot struct {
	SourceID       string   `json:"sourceId"`
	Root           string   `json:"root"`
	RootIdentity   string   `json:"rootIdentity"`
	Directories    []string `json:"directories"`
	FollowSymlinks bool     `json:"followSymlinks"`
}
type WatchRequest struct {
	Roots []WatchRoot `json:"roots"`
}
type InventoryChange struct {
	SourceID     string `json:"sourceId,omitempty"`
	RelativePath string `json:"relativePath,omitempty"`
	Overflow     bool   `json:"overflow,omitempty"`
	Ready        bool   `json:"ready,omitempty"`
}

type inventoryWatcher interface {
	Next(context.Context) ([]InventoryChange, error)
	Close() error
}

// Watches produce acceleration hints only. All filesystem work stays in a
// killable helper; the server receives bounded directory paths, never contents.
func (c *Client) WatchInventory(ctx context.Context, r WatchRequest, emit func(InventoryChange)) error {
	if err := validateWatchRequest(r); err != nil {
		return err
	}
	data, _ := json.Marshal(request{Operation: "inventory-watch", Watch: &r})
	if len(data) > 64<<10 {
		return ErrWatchUnavailable
	}
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(data)
	// One watcher uses a short-helper slot rather than the CPU/probe pool. It
	// must never occupy the last analysis permit for the lifetime of the server.
	ctx = dbwork.WithClass(ctx, dbwork.ClassInteractive)
	return c.Supervisor.Run(ctx, "folder-watch", cmd, func(reader io.Reader) error {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var change InventoryChange
			if err := json.Unmarshal(scanner.Bytes(), &change); err != nil {
				return err
			}
			emit(change)
		}
		return scanner.Err()
	})
}

func validateWatchRequest(r WatchRequest) error {
	if len(r.Roots) == 0 || len(r.Roots) > MaxWatchRoots {
		return ErrWatchUnavailable
	}
	count := 0
	for _, root := range r.Roots {
		if !filepath.IsAbs(root.Root) || root.RootIdentity == "" || root.SourceID == "" || len(root.SourceID) > 256 || len(root.Root) > 16384 {
			return ErrWatchUnavailable
		}
		count += len(root.Directories)
		for _, relative := range root.Directories {
			if !filepath.IsLocal(relative) || len(relative) > 16384 {
				return ErrWatchUnavailable
			}
		}
	}
	if count > MaxInventoryWatches {
		return ErrWatchUnavailable
	}
	return nil
}
func inventoryWatchHelper(r WatchRequest, out io.Writer) error {
	if err := validateWatchRequest(r); err != nil {
		return err
	}
	admitted := WatchRequest{}
	for _, root := range r.Roots {
		observed, err := LocalInventoryDirectory(InventoryRequest{Root: root.Root, RelativePath: "."})
		if err != nil || !SameRoot(root.RootIdentity, RootIdentity(observed)) {
			continue
		}
		admitted.Roots = append(admitted.Roots, root)
	}
	if len(admitted.Roots) == 0 {
		return ErrWatchUnavailable
	}
	w, err := newInventoryWatcher(admitted)
	if err != nil {
		return err
	}
	defer w.Close()
	enc := json.NewEncoder(out)
	if err = enc.Encode(InventoryChange{Ready: true}); err != nil {
		return err
	}
	// Parent cancellation kills the supervised helper. The independent lifetime
	// also retires forgotten helpers after a server/process failure.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	for ctx.Err() == nil {
		changes, err := w.Next(ctx)
		if err != nil {
			return err
		}
		for _, change := range changes {
			if err = enc.Encode(change); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func watchDirectory(root WatchRoot, relative string) (string, error) {
	return inventoryPath(InventoryRequest{Root: root.Root, RootIdentity: root.RootIdentity, RelativePath: relative, FollowSymlinks: root.FollowSymlinks})
}
