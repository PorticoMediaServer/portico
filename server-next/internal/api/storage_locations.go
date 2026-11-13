package api

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// StorageLocation is one actual destination, including an older one still in use.
// Inside the data folder, its bytes are already included in Storage.Folders.
type StorageLocation struct {
	Folder   string `json:"folder"`
	Bytes    int64  `json:"bytes"`
	External bool   `json:"external"`
	Problem  string `json:"problem,omitempty"`
}

func (s *Server) storageLocations(ctx context.Context) (conversions, recordings []string, err error) {
	conversions, recordings = []string{}, []string{s.dvrOptions().Folder}
	if engine, ok := s.playback.Engine().(conversionStorage); ok {
		conversions = engine.ConversionFolders()
	}
	id, _, err := s.db.Setting(ctx, "dvr.library")
	if err != nil || id == "" {
		return conversions, recordings, err
	}
	libraries, err := s.db.AllLibraries(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, library := range libraries {
		if library.PublicID == id {
			for _, p := range library.Paths {
				if p.RemoteID == 0 && !slices.Contains(recordings, p.Path) {
					recordings = append(recordings, p.Path)
				}
			}
		}
	}
	return conversions, recordings, nil
}

func measureStorageLocations(ctx context.Context, dataDir string, folders []string) ([]StorageLocation, error) {
	out := make([]StorageLocation, 0, len(folders))
	for _, folder := range folders {
		folder, err := filepath.Abs(folder)
		if err != nil {
			return nil, err
		}
		location := StorageLocation{Folder: folder}
		root, err := filepath.EvalSymlinks(folder)
		if err != nil {
			root = folder
		}

		rel, err := filepath.Rel(dataDir, root)
		location.External = err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
		err = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if err != nil {
					return err
				}
				location.Bytes += info.Size()
			}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			location.Problem = err.Error()
		}
		out = append(out, location)
	}
	return out, nil
}
