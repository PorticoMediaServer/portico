package storagepaths

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/atomicfile"
)

type FolderUsage struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}
type Notice struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Usage struct {
	DataDirectory      string        `json:"dataDirectory"`
	TranscodeDirectory string        `json:"transcodeDirectory"`
	TranscodeBytes     int64         `json:"transcodeBytes"`
	Folders            []FolderUsage `json:"folders"`
	Notices            []Notice      `json:"notices"`
}

func (l Layout) Usage(ctx context.Context, mounts []Mount) (Usage, error) {
	out := Usage{DataDirectory: l.Data, TranscodeDirectory: l.Transcode, Folders: []FolderUsage{}, Notices: []Notice{}}
	if err := canonicalParents(l.Data); err != nil {
		return out, err
	}
	root, err := os.OpenRoot(l.Data)
	if err != nil {
		return out, err
	}
	defer root.Close()
	for _, name := range []string{"portico.toml", "Database", "Metadata", "Cache", "Logs", "Keys", "Backups", "Temp"} {
		usage := FolderUsage{Name: name}
		err := fs.WalkDir(root.FS(), name, func(_ string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.Name() == ownershipMarker {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				usage.Bytes += info.Size()
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		out.Folders = append(out.Folders, usage)
	}
	if l.Transcode != "" {
		if err := canonicalParents(l.Transcode); err != nil {
			return out, err
		}
		transcode, err := os.OpenRoot(l.Transcode)
		if err != nil {
			return out, err
		}
		err = fs.WalkDir(transcode.FS(), ".", func(_ string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				out.TranscodeBytes += info.Size()
			}
			return nil
		})
		transcode.Close()
		if err != nil {
			return out, err
		}
	}
	if l.TranscodeFallback {
		out.Notices = append(out.Notices, Notice{"transcode_not_mapped", "Map /transcode to a fast disk. Unmapped /transcode uses the data volume's Transcode folder."})
	}
	if SharedFilesystem(Filesystem(l.Data, mounts)) {
		out.Notices = append(out.Notices, Notice{"data_on_shared_drive", "The data folder is on a shared or network drive, which can make the database slow or damage it. Use a local disk or Unraid's cache pool (for example /mnt/cache/appdata/portico)."})
	}
	return out, nil
}

// Cache is emptied after graceful restart, before cache consumers are opened.
func (l Layout) RequestCacheEmpty() error {
	if err := l.verifyOwned("Cache"); err != nil {
		return err
	}
	if err := refuseMountedChildren(l.Cache); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(l.Database, "empty-cache-on-start"), []byte("empty cache\n"), 0600)
}
func (l Layout) ApplyCacheEmpty() error {
	marker := filepath.Join(l.Database, "empty-cache-on-start")
	info, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("invalid cache empty marker")
	}
	if err = l.EmptyOwned("Cache"); err != nil {
		return err
	}
	return os.Remove(marker)
}
