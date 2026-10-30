//go:build linux || darwin

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestObservedStorageHonorsAcceptedPlaybackBinding(t *testing.T) {
	for _, mutation := range []string{"unchanged", "replacement", "symlink", "source-less symlink"} {
		t.Run(mutation, func(t *testing.T) {
			c, lease, path, released, _ := observedStorageFixture(t)
			binding := playbackBindingFor(t, path)
			binding.Inventory.FollowSymlinks = false
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if mutation == "replacement" {
				replacement := path + ".new"
				if err := os.WriteFile(replacement, []byte("abcdefghij"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			} else if mutation != "unchanged" {
				if err := os.Rename(path, path+".target"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(path)+".target", path); err != nil {
					t.Fatal(err)
				}
				if mutation == "source-less symlink" {
					binding = nil
				}
			}
			reader, err := c.OpenObservedPlayback(context.Background(), lease, binding)
			if mutation == "unchanged" {
				if err != nil {
					t.Fatal(err)
				}
				data, err := reader.ReadExtent(context.Background(), 0, 10)
				if err != nil || string(data) != "0123456789" {
					t.Fatal(string(data), err)
				}
			} else if err == nil {
				t.Error("observed session acquired substituted object")
			}
			if reader != nil {
				reader.Close()
			}
			waitReaderExit(t, c)
			if released.Load() != 1 {
				t.Fatal("accepted-binding acquisition leaked its lease")
			}
		})
	}
}
