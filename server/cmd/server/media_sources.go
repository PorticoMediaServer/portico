package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// configureLocalMedia is the production composition for local source creation,
// scans and the owner picker. The guard is carried by the storage client but is
// called only when catalog adds or changes a source path.
func configureLocalMedia(db *sql.DB, state, helperBinary string, admin *administration.Service) (*storage.Client, *mounts.Service, error) {
	mountRoot := os.Getenv("PORTICO_MANAGED_MOUNT_ROOT")
	if mountRoot == "" {
		mountRoot = storagepaths.Path(state, "mounts")
	}
	var err error
	mountRoot, err = filepath.Abs(mountRoot)
	if err != nil {
		return nil, nil, err
	}
	store := storage.New(helperBinary)
	store.SourceGuard = (storage.SourcePolicy{StateDirectory: state, ManagedMountDirectory: mountRoot}).Check
	admin.ManagedMountDirectory = mountRoot
	managed, err := mounts.New(db, state, mountRoot, helperBinary, store)
	if err != nil {
		return nil, nil, err
	}
	return store, managed, nil
}
