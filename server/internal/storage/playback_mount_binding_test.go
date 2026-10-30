package storage

import (
	"path/filepath"
	"testing"
)

func TestMountedPlaybackBindingRequiresTheSameManagedRoot(t *testing.T) {
	mount := t.TempDir()
	root := filepath.Join(mount, "library")
	path := filepath.Join(root, "movie.mp4")
	binding := &PlaybackBinding{Inventory: InventoryRequest{Root: root, RootIdentity: "remote-dir:provider:opaque", RelativePath: "movie.mp4"}, ObjectIdentity: "remote-object:opaque", Mounted: true}
	for name, registry := range map[string]func(string) string{
		"absent": nil,
		"empty":  func(string) string { return "" },
		"file only": func(p string) string {
			if p == path {
				return mount
			}
			return ""
		},
		"different roots": func(p string) string {
			if p == path {
				return mount
			}
			return filepath.Join(mount, "other")
		},
		"outside claimed root": func(string) string { return filepath.Join(mount, "other") },
	} {
		t.Run(name, func(t *testing.T) {
			client := &Client{MountedRoot: registry}
			if _, err := client.bindPlaybackRequest(request{Path: path}, []*PlaybackBinding{binding}); err == nil {
				t.Fatal("remote mapping lost its managed mount requirement")
			}
		})
	}
	client := &Client{MountedRoot: func(string) string { return mount }}
	req, err := client.bindPlaybackRequest(request{Path: path}, []*PlaybackBinding{binding})
	if err != nil || req.MountedRoot != mount || req.Inventory != nil || req.Identity != "" {
		t.Fatalf("remote identity became filesystem custody: %+v %v", req, err)
	}
	if file, err := OpenLocalPlaybackFile(path, 0, 0, binding); err == nil {
		file.Close()
		t.Fatal("in-process local acquisition accepted a remote mounted binding")
	}
}
