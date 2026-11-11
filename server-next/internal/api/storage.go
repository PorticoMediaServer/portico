package api

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
)

// Storage: the data folder, what uses space in it folder by folder, and how full its disk is.
// The free-space warnings are the `storage` settings document.

// StorageSettings is the owner's `storage` settings document.
type StorageSettings struct {
	// WarnPercent and MajorPercent are how little free space on the data folder's disk is a
	// warning and a major problem.
	WarnPercent  int `json:"warnPercent"`
	MajorPercent int `json:"majorPercent"`
}

var storageDocument = settings.Define("storage", StorageSettings{WarnPercent: 10, MajorPercent: 5}, func(s *StorageSettings) error {
	if s.WarnPercent < 1 || s.WarnPercent > 50 {
		return &settings.Invalid{Field: "warnPercent", Message: "Choose 1 to 50 percent."}
	}
	if s.MajorPercent < 1 || s.MajorPercent > s.WarnPercent {
		return &settings.Invalid{Field: "majorPercent", Message: "Choose 1 percent up to the warning's."}
	}
	return nil
})

// Storage is the storage page.
type Storage struct {
	DataFolder string `json:"dataFolder"`
	// SharedDrive says the data folder is on a shared or network drive, which can make the
	// database slow or damage it.
	SharedDrive bool  `json:"sharedDrive"`
	DiskBytes   int64 `json:"diskBytes"`
	FreeBytes   int64 `json:"freeBytes"`
	// Level is ok, warning or major, by the owner's free-space warnings.
	Level   string        `json:"level"`
	Folders []FolderUsage `json:"folders"`
}

// FolderUsage is what one part of the data folder uses.
type FolderUsage struct {
	// Name is database, artwork, conversions, logs, backups, reports, title-data, or the name
	// of anything else in the data folder.
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

func (s *Server) storageRoutes(dataDir string) {
	// Reading the whole data folder grows with the number of pictures: an accepted cost on a
	// page the owner opens now and then.
	Handle(s, "GET /v1/admin/storage", Owner, func(r *http.Request, _ None) (Storage, error) {
		return storageUsage(r.Context(), dataDir, storageDocument.Get(s.settings))
	})
}

// partNames gives the data folder's own files and folders the names the page shows.
var partNames = map[string]string{"main.db": "database", "main.db-wal": "database", "main.db-shm": "database",
	"search.db": "database", "guide.db": "database", "transcodes": "conversions"}

func storageUsage(ctx context.Context, dataDir string, limits StorageSettings) (Storage, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return Storage{}, err
	}
	out := Storage{DataFolder: abs, Folders: []FolderUsage{}, Level: "ok"}
	if mounts, err := storagepaths.CurrentMounts(); err == nil {
		out.SharedDrive = storagepaths.SharedFilesystem(storagepaths.Filesystem(abs, mounts))
	}
	out.DiskBytes, _ = diskspace.Total(abs)
	out.FreeBytes, _ = diskspace.Free(abs)
	if out.DiskBytes > 0 {
		switch free := out.FreeBytes * 100 / out.DiskBytes; {
		case free < int64(limits.MajorPercent):
			out.Level = "major"
		case free < int64(limits.WarnPercent):
			out.Level = "warning"
		}
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return out, err
	}
	sizes := map[string]int64{}
	for _, e := range entries {
		name := e.Name()
		part := partNames[name]
		if part == "" {
			part = name
		}
		err := filepath.WalkDir(filepath.Join(abs, name), func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // gone or unreadable: not counted
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					sizes[part] += info.Size()
				}
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	for name, n := range sizes {
		out.Folders = append(out.Folders, FolderUsage{Name: name, Bytes: n})
	}
	sort.Slice(out.Folders, func(i, j int) bool { return out.Folders[i].Bytes > out.Folders[j].Bytes })
	return out, nil
}
