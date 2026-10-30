package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func playbackBindingFor(t *testing.T, path string) *PlaybackBinding {
	t.Helper()
	root := filepath.Dir(path)
	directory, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	rootIdentity, err := FileObjectIdentity(directory)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	object, err := FileObjectIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	return &PlaybackBinding{Inventory: InventoryRequest{Root: root, RootIdentity: rootIdentity, RelativePath: filepath.Base(path), FollowSymlinks: true}, ObjectIdentity: StableObjectIdentity(object, rootIdentity)}
}

func openBoundPlayback(t *testing.T, c *Client, descriptor bool, path string, info os.FileInfo, binding *PlaybackBinding) (io.ReadSeekCloser, error) {
	t.Helper()
	if descriptor {
		return c.OpenPlaybackDescriptor(context.Background(), path, info.Size(), info.ModTime().UnixNano(), binding)
	}
	return c.OpenPlayback(context.Background(), path, info.Size(), info.ModTime().UnixNano(), binding)
}

func TestPlaybackAcquisitionRejectsSymlinkAndRecordedObjectReplacement(t *testing.T) {
	for _, descriptor := range []bool{false, true} {
		if descriptor && !DescriptorPassing {
			continue
		}
		for _, attack := range []string{"unbound symlink", "bound outside symlink", "bound replacement"} {
			t.Run(attack+map[bool]string{true: " descriptor", false: " stream"}[descriptor], func(t *testing.T) {
				c, path, info := playbackFixture(t)
				binding := playbackBindingFor(t, path)
				good, err := openBoundPlayback(t, c, descriptor, path, info, binding)
				if err != nil {
					t.Fatalf("recorded ordinary file rejected: %v", err)
				}
				var data [10]byte
				if _, err := io.ReadFull(good, data[:]); err != nil || string(data[:]) != "0123456789" {
					t.Fatalf("ordinary bytes: %q %v", data, err)
				}
				good.Close()
				waitReaderExit(t, c)
				replacement := filepath.Join(t.TempDir(), "replacement.mp4")
				if err := os.WriteFile(replacement, make([]byte, info.Size()), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if attack == "bound replacement" {
					if err := os.Rename(replacement, path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(replacement, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				if attack == "unbound symlink" {
					binding = nil
				}
				if opened, err := openBoundPlayback(t, c, descriptor, path, info, binding); err == nil {
					opened.Close()
					t.Fatal("replacement with matching size and mtime was accepted")
				}
				waitReaderExit(t, c)
			})
		}
	}
}

func TestPlaybackAcquisitionPreservesAdmittedInRootSymlink(t *testing.T) {
	c, path, info := playbackFixture(t)
	binding := playbackBindingFor(t, path)
	alias := filepath.Join(filepath.Dir(path), "alias.mp4")
	if err := os.Symlink(filepath.Base(path), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	binding.Inventory.RelativePath = filepath.Base(alias)
	for _, descriptor := range []bool{false, true} {
		if descriptor && !DescriptorPassing {
			continue
		}
		file, err := openBoundPlayback(t, c, descriptor, alias, info, binding)
		if err != nil {
			t.Fatalf("admitted in-root symlink rejected: %v", err)
		}
		var data [10]byte
		if _, err := io.ReadFull(file, data[:]); err != nil || string(data[:]) != "0123456789" {
			t.Fatalf("admitted symlink bytes: %q %v", data, err)
		}
		file.Close()
		waitReaderExit(t, c)
	}
	binding.Inventory.FollowSymlinks = false
	if opened, err := c.OpenPlayback(context.Background(), alias, info.Size(), info.ModTime().UnixNano(), binding); err == nil {
		opened.Close()
		t.Fatal("disabled symlink was admitted")
	}
	waitReaderExit(t, c)
}

func TestPlaybackBindingRejectsReplacementRootEvenForSameFile(t *testing.T) {
	_, path, info := playbackFixture(t)
	binding := playbackBindingFor(t, path)
	root := filepath.Dir(path)
	retained := root + "-retained"
	if err := os.Rename(root, retained); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(retained); err != nil {
			t.Error(err)
		}
	})
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	// The file itself is still the recorded inode. Its new root is not.
	if err := os.Link(filepath.Join(retained, filepath.Base(path)), path); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenLocalPlaybackFile(path, info.Size(), info.ModTime().UnixNano(), binding); err == nil {
		opened.Close()
		t.Fatal("replacement root was accepted solely because the file matched")
	}
}
