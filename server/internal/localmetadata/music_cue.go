package localmetadata

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"strings"
)

func (s *Service) musicCue(ctx context.Context, library, path string, f assets.Facts) assets.Facts {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".flac" && ext != ".wav" {
		return f
	}
	own := strings.TrimSuffix(path, filepath.Ext(path)) + ".cue"
	dir := filepath.Dir(path)
	sheets := []string{own, filepath.Join(dir, filepath.Base(dir)+".cue")}
	chapters := embeddedMusicCue(path, f)
	for _, sheet := range sheets {
		if unlisted(ctx, sheet) {
			continue
		}
		raw, e := s.read(ctx, library, sheet, 256<<10)
		if e != nil {
			continue
		}
		files, e := parseCue(raw)
		if e != nil {
			f.LocalMetadataIssue = "invalid_cue_sidecar"
			continue
		}
		if c := cueChapters(files, path, sheet == own); len(c) > 0 {
			chapters = c
			break
		}
	}
	return finishMusicCue(f, chapters)
}

func embeddedMusicCue(path string, f assets.Facts) []assets.Chapter {
	chapters := append([]assets.Chapter(nil), f.MusicCueTracks...)
	// Embedded textual CUESHEET is kept private and is parsed under the same
	// bounds as a sidecar. A filename in it is matched, never followed.
	if f.EmbeddedAudio != nil {
		chapters = nil
		for _, c := range f.EmbeddedAudio.CueTracks {
			chapters = append(chapters, assets.Chapter{Title: c.Title, Start: c.Start, End: c.End})
		}
		for _, raw := range f.EmbeddedAudio.Values["cuesheet"] {
			if files, e := parseCue([]byte(raw)); e == nil {
				chapters = cueChapters(files, path, true)
				break
			}
		}
	}
	return chapters
}

func finishMusicCue(f assets.Facts, chapters []assets.Chapter) assets.Facts {
	// A retained baseline contains raw observations, not an accepted timeline.
	// Rejected embedded cues must not survive through the input Facts.
	f.MusicCueTracks = nil
	if len(chapters) < 2 || f.Duration <= 0 {
		return f
	}
	cleaned, status := assets.CleanChapters(chapters, f.Duration)
	if status != "known" && status != "complete" {
		if status == "partial" {
			f.LocalMetadataIssue = "invalid_music_cue"
			return f
		}
	}
	if len(cleaned) != len(chapters) {
		f.LocalMetadataIssue = "invalid_music_cue"
		return f
	}
	f.MusicCueTracks = cleaned
	return f
}
