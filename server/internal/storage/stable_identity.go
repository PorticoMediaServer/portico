package storage

import "github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"

// SameRoot keeps the existing storage API; the values-only implementation is
// shared with schema-v1 preparation without loading supervisor/database hooks.
func SameRoot(recorded, observed string) bool { return objectidentity.SameRoot(recorded, observed) }

func localChangeToken(inode uint64, size, modifiedNS int64) string {
	return objectidentity.LocalChangeToken(inode, size, modifiedNS)
}

// StableObjectIdentity keeps the existing storage API and exact projection.
func StableObjectIdentity(object, root string) string {
	return objectidentity.StableObjectIdentity(object, root)
}
