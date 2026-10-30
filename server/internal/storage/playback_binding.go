package storage

import (
	"os"
	"path/filepath"
	"strings"
)

// PlaybackBinding is scan evidence beneath an admitted source root. Sessions
// persist this tuple at acceptance; session-less reads use current evidence.
// Mounted distinguishes remote inventory IDs from local filesystem identities;
// that weaker path requires managed-mount acquisition and proves no immutable
// accepted-object identity. It is not a fresh playback-time observation.
type PlaybackBinding struct {
	Inventory      InventoryRequest
	ObjectIdentity string
	Mounted        bool
}

func (c *Client) bindPlaybackRequest(req request, bindings []*PlaybackBinding) (request, error) {
	if len(bindings) == 1 && bindings[0] != nil && bindings[0].Mounted {
		if c == nil || c.MountedRoot == nil {
			return request{}, ErrPlaybackSource
		}
		mount := c.MountedRoot(req.Path)
		if mount == "" || c.MountedRoot(bindings[0].Inventory.Root) != mount {
			return request{}, ErrPlaybackSource
		}
		req.MountedRoot = mount
	}
	return bindPlaybackRequest(req, bindings)
}

func bindPlaybackRequest(req request, bindings []*PlaybackBinding) (request, error) {
	if len(bindings) > 1 {
		return request{}, ErrPlaybackSource
	}
	if len(bindings) == 0 || bindings[0] == nil {
		return req, nil
	}
	binding := bindings[0]
	r := binding.Inventory
	if binding.ObjectIdentity == "" || r.RootIdentity == "" || !filepath.IsAbs(r.Root) || !filepath.IsLocal(r.RelativePath) || r.RelativePath == "." || filepath.Join(r.Root, r.RelativePath) != filepath.Clean(req.Path) {
		return request{}, ErrPlaybackSource
	}
	if binding.Mounted {
		relative, err := filepath.Rel(req.MountedRoot, r.Root)
		if req.MountedRoot == "" || !filepath.IsAbs(req.MountedRoot) || err != nil || !filepath.IsLocal(relative) {
			return request{}, ErrPlaybackSource
		}
		// Keep the existing helper mount check, no-follow open and version
		// check. Provider root/object IDs must never reach inode comparison.
		return req, nil
	}
	req.Inventory, req.Identity = &r, binding.ObjectIdentity
	return req, nil
}

func openPlaybackRequestFile(req request) (*os.File, error) {
	var file *os.File
	var err error
	if req.Inventory != nil {
		file, err = openInventoryContent(*req.Inventory)
	} else {
		file, err = openPlaybackNoFollow(req.Path)
	}
	if err != nil {
		return nil, ErrPlaybackSource
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrPlaybackSource
	}
	if req.Inventory != nil {
		if !matchesPlaybackObject(file, *req.Inventory, req.Identity) {
			file.Close()
			return nil, ErrPlaybackSource
		}
	}
	return file, nil
}

func matchesPlaybackObject(file *os.File, inventory InventoryRequest, expected string) bool {
	actual, err := FileObjectIdentity(file)
	if err != nil || expected == "" {
		return false
	}
	if actual == expected {
		return true
	}
	if !strings.HasPrefix(expected, "ino:") {
		return false
	}
	// Inventory stores a stable inode only for files on the admitted root's
	// own filesystem. Normalize against the acquired root's current device,
	// never against a device number left over from a previous boot or mount.
	root, err := openInventoryRoot(inventory)
	if err != nil {
		return false
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return false
	}
	defer directory.Close()
	rootIdentity, err := FileObjectIdentity(directory)
	return err == nil && StableObjectIdentity(actual, rootIdentity) == expected
}

func playbackCurrentInfo(req request) (os.FileInfo, error) {
	if req.Inventory != nil {
		// An explicitly admitted, root-confined symlink may be a legitimate input.
		return os.Stat(req.Path)
	}
	return os.Lstat(req.Path)
}

// OpenLocalPlaybackFile is the in-process equivalent for existing local-only
// callers. Supervised clients use the same acquisition boundary in the helper.
func OpenLocalPlaybackFile(path string, size, modified int64, binding *PlaybackBinding) (*os.File, error) {
	req, err := bindPlaybackRequest(request{Path: path, Size: size, ModifiedNS: modified}, []*PlaybackBinding{binding})
	if err != nil {
		return nil, err
	}
	file, err := openPlaybackRequestFile(req)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != size || info.ModTime().UnixNano() != modified {
		file.Close()
		return nil, ErrPlaybackSource
	}
	return file, nil
}
