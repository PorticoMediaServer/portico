//go:build !linux

package storage

import "os"

// Other platforms retain their inode/revision checks.
type inventoryChanges struct{}

func watchInventoryChanges(*os.File) (*inventoryChanges, error) { return nil, nil }
func (*inventoryChanges) Changed() bool                         { return false }
func (*inventoryChanges) Close()                                {}
