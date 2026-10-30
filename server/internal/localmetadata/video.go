package localmetadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"strings"
	"time"
)

// VideoNFO uses the scanner's admitted root/read guard and never resolves URLs
// inside XML. A failed/unavailable sidecar is not evidence to erase old metadata.
func (s *Service) VideoNFO(ctx context.Context, library, root, path, kind string, f assets.Facts) assets.Facts {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	f.VideoMetadataStatus = "not_observed"
	if s.storage == nil {
		return f
	}
	candidates := []string{strings.TrimSuffix(path, filepath.Ext(path)) + ".nfo"}
	if kind == "movie" {
		candidates = append(candidates, filepath.Join(filepath.Dir(path), "movie.nfo"))
	} else {
		dir := filepath.Dir(path)
		candidates = append(candidates, filepath.Join(dir, "tvshow.nfo"))
		parent := filepath.Dir(dir)
		if rel, e := filepath.Rel(root, parent); e == nil && (rel == "." || filepath.IsLocal(rel)) {
			candidates = append(candidates, filepath.Join(parent, "tvshow.nfo"))
		}
	}
	candidates = append(candidates, filepath.Join(filepath.Dir(path), filepath.Base(filepath.Dir(path))+".nfo"))
	complete := true
	seen := map[string]bool{}
	for _, side := range candidates {
		if seen[side] {
			continue
		}
		seen[side] = true
		rel, e := filepath.Rel(root, side)
		if e != nil || !filepath.IsLocal(rel) {
			continue
		}
		if unlisted(ctx, side) {
			continue
		}
		before, e := s.storage.Stat(ctx, library, side)
		if e != nil {
			if !errors.Is(e, os.ErrNotExist) {
				complete = false
				f.LocalMetadataIssue = "video_nfo_unavailable"
			}
			continue
		}
		if before.Directory || before.Size > 256<<10 {
			complete = false
			f.LocalMetadataIssue = "invalid_video_nfo"
			continue
		}
		raw, e := s.storage.ReadSmall(ctx, library, side, 256<<10)
		if e != nil {
			complete = false
			f.LocalMetadataIssue = "video_nfo_unavailable"
			continue
		}
		after, e := s.storage.Stat(ctx, library, side)
		if e != nil || before.Size != after.Size || before.ModifiedNS != after.ModifiedNS || before.Revision != after.Revision || before.ObjectIdentity != after.ObjectIdentity {
			complete = false
			f.LocalMetadataIssue = "video_nfo_changed"
			continue
		}
		records, e := ParseVideoNFO(raw, kind == "anime")
		if e != nil {
			complete = false
			f.LocalMetadataIssue = "invalid_video_nfo"
			continue
		}
		locator := sha256.Sum256([]byte(rel))
		for i := range records {
			v := &records[i]
			if v.Kind == "work" {
				if kind == "movie" {
					v.Kind = "movie"
				} else if filepath.Base(side) == "tvshow.nfo" || strings.TrimSuffix(filepath.Base(side), filepath.Ext(side)) == filepath.Base(filepath.Dir(side)) {
					v.Kind = "show"
				} else {
					v.Kind = "episode"
				}
			}
			v.Locator = hex.EncodeToString(locator[:])
			v.Size = after.Size
			v.ModifiedNS = after.ModifiedNS
			v.Revision = after.Revision
			if kind == "movie" && v.Kind != "movie" || kind != "movie" && v.Kind == "movie" {
				complete = false
				f.LocalMetadataIssue = "video_nfo_kind_conflict"
				continue
			}
			if len(v.Issues) > 0 && f.LocalMetadataIssue == "" {
				f.LocalMetadataIssue = "video_nfo_fields_skipped"
			}
			f.VideoMetadata = append(f.VideoMetadata, *v)
		}
		f.VideoMetadataStatus = "observed"
	}
	if complete {
		f.VideoMetadataStatus = "observed"
	} else {
		f.VideoMetadataStatus = "partial"
	}
	return f
}
