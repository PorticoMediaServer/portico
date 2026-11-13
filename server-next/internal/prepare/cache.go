package prepare

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// CachedCopies measures or removes reusable copies, including their extracted subtitles.
// Any copy a download request still names, queued/running work and open reads stay put.
func (s *Service) CachedCopies(ctx context.Context, empty bool) (int64, error) {
	var after, bytes int64
	var files map[string][]string
	for {
		copies, err := s.DB.CachedPrepared(ctx, after)
		if err != nil || len(copies) == 0 {
			return bytes, err
		}
		if files == nil {
			files, err = s.copyFiles()
			if err != nil {
				return bytes, err
			}
		}
		for _, p := range copies {
			after = p.ID
			s.mu.Lock()
			if s.reading[p.ID] > 0 {
				s.mu.Unlock()
				continue
			}
			if empty {
				removed, err := s.DB.RemoveCachedPrepared(ctx, p.ID, p.LastUsedAt)
				s.mu.Unlock()
				if err != nil {
					return bytes, err
				}
				if !removed {
					continue
				}
			} else {
				s.mu.Unlock()
			}
			for _, file := range files[p.PublicID] {
				info, err := os.Lstat(file)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return bytes, err
				}
				if !info.Mode().IsRegular() {
					continue
				}
				if empty {
					if err := os.Remove(file); err != nil {
						return bytes, err
					}
				}
				bytes += info.Size()
			}
		}
	}
}

// copyFiles lists the folder once, rather than globbing all its files for each copy.
func (s *Service) copyFiles() (map[string][]string, error) {
	entries, err := os.ReadDir(s.Folder)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		id, _, _ := strings.Cut(entry.Name(), ".")
		id, _, _ = strings.Cut(id, "-")
		out[id] = append(out[id], filepath.Join(s.Folder, entry.Name()))
	}
	return out, nil
}
