package storagepaths

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const ownershipMarker = ".portico-owned-v1"
const layoutIdentityMarker = ".portico-layout-v1"

func ownedScopeIdentity(parent *os.Root, name string) (string, error) {
	identity, err := readOwnedFile(parent, layoutIdentityMarker)
	if err != nil {
		return "", err
	}
	if len(identity) != 64 {
		return "", errors.New("invalid data-layout identity")
	}
	if _, err := hex.DecodeString(identity); err != nil {
		return "", errors.New("invalid data-layout identity")
	}
	return "Portico disposable directory v1\n" + identity + "\n" + name + "\n", nil
}

func (l Layout) ownership(name string) (string, error) {
	parent, err := os.OpenRoot(l.Data)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	return ownedScopeIdentity(parent, name)
}

// A whole-folder move carries this random root identity and its scope markers.
// A scope copied by itself cannot establish ownership in a different layout.
func (l Layout) prepareIdentity() error {
	if err := os.MkdirAll(l.Data, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(l.Data)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(layoutIdentityMarker); err == nil {
		_, err := ownedScopeIdentity(root, "Temp")
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, scope := range []string{"Temp", "Cache"} {
		if _, err := root.Lstat(scope); err == nil {
			return errors.New("refusing to mint layout ownership over existing disposable scopes")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	marker, err := root.OpenFile(layoutIdentityMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(marker, hex.EncodeToString(random[:]))
	if writeErr == nil {
		writeErr = marker.Sync()
	}
	return errors.Join(writeErr, marker.Close())
}

func readOwnedFile(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", errors.New("refusing to adopt a preexisting unowned Cache or Temp directory; choose a fresh data folder")
	}
	f, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("disposable directory ownership changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func verifyOwnedRoot(root *os.Root, expected string) error {
	raw, err := readOwnedFile(root, ownershipMarker)
	if err != nil {
		return err
	}
	if raw != expected {
		return errors.New("disposable directory does not belong to this data layout")
	}
	return nil
}

func (l Layout) verifyOwned(name string) error {
	dir := filepath.Join(l.Data, name)
	if err := canonicalParents(dir); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	expected, err := l.ownership(name)
	if err != nil {
		return err
	}
	return verifyOwnedRoot(root, expected)
}

func (l Layout) prepareOwned(name string) error {
	expected, err := l.ownership(name)
	if err != nil {
		return err
	}
	dir := filepath.Join(l.Data, name)
	if err := os.Mkdir(dir, 0700); errors.Is(err, os.ErrExist) {
		return l.verifyOwned(name)
	} else if err != nil {
		return err
	}
	// Mkdir succeeded: this scope was created by us. Check its pinned handle is
	// still empty before recording ownership; do not mint a marker over data.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	folder, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := folder.ReadDir(1)
	folder.Close()
	if len(entries) != 0 || readErr != io.EOF {
		return fmt.Errorf("new disposable directory is not empty: %s", dir)
	}
	marker, err := root.OpenFile(ownershipMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(marker, expected)
	if writeErr == nil {
		writeErr = marker.Sync()
	}
	return errors.Join(writeErr, marker.Close())
}
