package localmetadata

import (
	"context"
	"encoding/json"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"strings"
	"unicode/utf8"
)

// prepareAudioEvidence adds the sidecars' evidence to the file's own tags. A
// later sidecar replaces what an earlier one said, so the order is precedence,
// lowest first. For an audiobook:
//
//	embedded tags < desc.txt, reader.txt < metadata.opf < <file>.opf < book.nfo
//	< <file>.nfo < metadata.json (Audiobookshelf) < <file>.portico.json
//
// and the folders (naming.ReadAudiobookFolder) only fill what all of these left
// empty. Owner edits stay above every local source, as for any title.
func (s *Service) prepareAudioEvidence(ctx context.Context, library, kind, root, path string, f assets.Facts) assets.Facts {
	if f.Tags == nil {
		f.Tags = map[string]string{}
	}
	if f.TagSources == nil {
		f.TagSources = map[string]string{}
	}
	// Older in-process producers may provide just Tags. Preserve them as evidence.
	if len(f.AudioEvidence) == 0 {
		for k, v := range f.Tags {
			source := f.TagSources[k]
			if source == "" {
				source = "embedded"
			}
			f.AudioEvidence = append(f.AudioEvidence, assets.AudioTagEvidence{Field: k, Value: v, Source: source})
		}
	}
	dir := filepath.Dir(path)
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	sides := []string{filepath.Join(dir, "album.nfo"), stem + ".nfo", path + ".portico.json"}
	if kind == "audiobook" {
		book := bookFolder(path)
		sides = []string{filepath.Join(book, "desc.txt"), filepath.Join(book, "reader.txt"), filepath.Join(book, "metadata.opf"), stem + ".opf", filepath.Join(book, "book.nfo"), stem + ".nfo", filepath.Join(book, "metadata.json"), path + ".portico.json"}
	}
	var bookChapters []assets.Chapter
	for _, side := range sides {
		if unlisted(ctx, side) {
			continue
		}
		name := filepath.Base(side)
		limit := int64(64 << 10)
		if name == "metadata.json" {
			limit = 256 << 10
		}
		raw, e := s.read(ctx, library, side, limit)
		if e != nil {
			continue
		}
		var values map[string]string
		source := "nfo"
		switch {
		case name == "desc.txt" || name == "reader.txt":
			source = "txt"
			values, e = parseBookText(name, raw)
		case name == "metadata.json":
			source = "abs_json"
			values, bookChapters, e = parseABSMetadata(raw)
		case filepath.Ext(side) == ".opf":
			source = "opf"
			values, e = parseOPF(raw)
		case filepath.Ext(side) == ".json":
			source = "portico_json"
			values, e = parseAudioJSON(raw)
		default:
			values, e = parseMusicNFO(raw, kind)
		}
		if e != nil {
			f.LocalMetadataIssue = "invalid_" + source + "_sidecar"
			continue
		}
		if values["_partial"] == "true" {
			f.LocalMetadataIssue = "invalid_" + source + "_sidecar"
			delete(values, "_partial")
		}
		for k, v := range values {
			f = withEvidence(f, k, v, source)
		}
	}
	if kind == "music" {
		f = s.musicCue(ctx, library, path, f)
		f = musicPackedTrackEvidence(ctx, path, f)
	}
	if kind == "audiobook" {
		f = s.audiobookChapters(ctx, library, path, f, bookChapters)
		f = audiobookFolderEvidence(root, path, f)
	}
	return f
}

// bookFolder holds a book's own sidecars: the file's folder, or the one above
// it when the file is on a disc ("Title/Disc 1/01.mp3").
func bookFolder(path string) string {
	dir := filepath.Dir(path)
	if _, ok := naming.AudiobookDiscFolder(filepath.Base(dir)); ok {
		return filepath.Dir(dir)
	}
	return dir
}

func (s *Service) read(ctx context.Context, library, path string, limit int64) ([]byte, error) {
	if s.readSmall != nil {
		return s.readSmall(ctx, library, path, limit)
	}
	return s.storage.ReadSmall(ctx, library, path, limit)
}

// withEvidence records one observation and makes it the file's value.
func withEvidence(f assets.Facts, k, v, source string) assets.Facts {
	v = assets.CleanAudioTag(k, v)
	if !assets.ValidAudioTag(k, v) || len(f.AudioEvidence) >= 512 {
		return f
	}
	f.AudioEvidence = append(f.AudioEvidence, assets.AudioTagEvidence{Field: k, Value: v, Source: source})
	f.Tags[k] = v
	f.TagSources[k] = source
	return f
}

// parseBookText reads Audiobookshelf's desc.txt (the description) and
// reader.txt (the narrators, one per line or separated by ";" or "&").
func parseBookText(name string, raw []byte) (map[string]string, error) {
	text, e := localText(raw)
	if e != nil {
		return nil, e
	}
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if name == "desc.txt" {
		return map[string]string{"description": boundedText(text, 4096)}, nil
	}
	names := uniqueAudioNames(strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ';' || r == '&' }))
	if len(names) == 0 {
		return map[string]string{}, nil
	}
	return map[string]string{"narrator": strings.Join(names, "; "), "narrators": audioList(names)}, nil
}

// boundedText keeps a long description to what a tag may hold, on a character boundary.
func boundedText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s)
}

// audiobookChapters gives a book file the chapters a sidecar describes: a cue
// sheet named after the file or its book's folder, else Audiobookshelf's chapters when
// they span exactly this file. Either replaces the file's embedded chapters.
func (s *Service) audiobookChapters(ctx context.Context, library, path string, f assets.Facts, bookChapters []assets.Chapter) assets.Facts {
	book := bookFolder(path)
	own := strings.TrimSuffix(path, filepath.Ext(path)) + ".cue"
	sheets := []string{own}
	if folder := filepath.Join(book, filepath.Base(book)+".cue"); folder != own {
		sheets = append(sheets, folder)
	}
	var chapters []assets.Chapter
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
		if chapters = cueChapters(files, path, sheet == own); len(chapters) > 0 {
			break
		}
	}
	if len(chapters) == 0 && fitsFile(bookChapters, f.Duration) {
		chapters = bookChapters
	}
	if len(chapters) == 0 || f.Duration <= 0 {
		return f
	}
	cleaned, status := assets.CleanChapters(chapters, f.Duration)
	if len(cleaned) == 0 {
		f.LocalMetadataIssue = "invalid_chapter_timeline"
		return f
	}
	f.Chapters, f.ChapterStatus = cleaned, status
	if status == "partial" {
		f.LocalMetadataIssue = "invalid_chapter_timeline"
	}
	return f
}

// audiobookFolderEvidence fills, from the folder names, what no tag or sidecar
// said. Series, place in the series and year never decide which book a file
// belongs to, so the folders give them to any file. Author, narrator and ASIN
// do, so the folders give them only to a file with no album tag, whose book is
// already known by its folder; a tagged file's identity never changes here.
func audiobookFolderEvidence(root, path string, f assets.Facts) assets.Facts {
	if root == "" {
		return f
	}
	rel, e := filepath.Rel(root, path)
	if e != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return f
	}
	folder := naming.ReadAudiobookFolder(rel)
	empty := func(keys ...string) bool {
		for _, k := range keys {
			if strings.TrimSpace(f.Tags[k]) != "" {
				return false
			}
		}
		return true
	}
	if folder.Series != "" && empty("series", "series_list") {
		f = withEvidence(f, "series", folder.Series, "folder")
		if folder.Sequence != "" {
			f = withEvidence(f, "series_position", folder.Sequence, "folder")
		}
	}
	if folder.Year != "" && empty("date", "year") {
		f = withEvidence(f, "year", folder.Year, "folder")
	}
	if !empty("album") {
		return f
	}
	if folder.Author != "" && empty("author", "authors", "artist", "artists") {
		f = withEvidence(f, "author", folder.Author, "folder")
	}
	if len(folder.Narrators) > 0 && empty("narrator", "narrators", "composer") {
		names, _ := json.Marshal(folder.Narrators)
		f = withEvidence(f, "narrator", strings.Join(folder.Narrators, "; "), "folder")
		f = withEvidence(f, "narrators", string(names), "folder")
	}
	if folder.ASIN != "" && empty("asin") {
		f = withEvidence(f, "asin", folder.ASIN, "folder")
	}
	return f
}
