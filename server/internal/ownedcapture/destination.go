package ownedcapture

import (
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
)

// Destination is created only after mediaartifact.New establishes the owned
// directory layout. Its paths stay private; producer sees the ORIGINAL opaque
// volume key and checked growth capacity, never a path or a byte-opening API.
type Destination struct {
	root, key   string
	directories [3]os.FileInfo
}

func newDestination(root string) (Destination, error) {
	d := Destination{root: root}
	for i, name := range []string{root, filepath.Join(root, "staging"), filepath.Join(root, "objects")} {
		info, e := os.Lstat(name)
		if e != nil {
			return Destination{}, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return Destination{}, ErrInput
		}
		key, e := mounts.CacheVolumeKey(name)
		if e != nil {
			return Destination{}, e
		}
		if i == 0 {
			d.key = key
		} else if key != d.key {
			return Destination{}, ErrInput
		}
		d.directories[i] = info
	}
	return d, nil
}
func (d Destination) check() error {
	if d.root == "" || d.key == "" {
		return ErrInput
	}
	for i, name := range []string{d.root, filepath.Join(d.root, "staging"), filepath.Join(d.root, "objects")} {
		info, e := os.Lstat(name)
		if e != nil {
			return e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !os.SameFile(info, d.directories[i]) {
			return ErrCurrent
		}
		key, e := mounts.CacheVolumeKey(name)
		if e != nil {
			return e
		}
		if key != d.key {
			return ErrCurrent
		}
	}
	return nil
}
func (d Destination) VolumeKey() (string, error) {
	if e := d.check(); e != nil {
		return "", e
	}
	return d.key, nil
}
func (d Destination) SpaceFor(floor, growth int64) bool {
	return floor >= 0 && growth >= 0 && d.check() == nil && mounts.CacheSpaceFor(filepath.Join(d.root, "staging"), floor, growth)
}
func (d Destination) SpaceAvailable(floor int64) bool { return d.SpaceFor(floor, 0) }
