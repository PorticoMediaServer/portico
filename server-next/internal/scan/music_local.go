package scan

import (
	"path"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
)

// coverNames are the names a picture in an album's folder has when it is the album's cover.
var coverNames = map[string]bool{"cover": true, "folder": true, "front": true, "album": true}

// albumNFOKeys are what album.nfo says about the album as a whole. What it says about single
// tracks is left to their own tags.
var albumNFOKeys = []string{"album", "album_artist", "album_artists", "year", "date", "genre", "genres", "description",
	"musicbrainz_albumid", "musicbrainz_releasegroupid"}

// albumLocal is what an album's folder says about the album besides its files' tags: its
// album.nfo. The cover is the scanner's own (cover).
type albumLocal struct {
	nfo details
}

// albumLocal reads an album's album.nfo once a scan. files is
// the folder's listing when the scan is in that folder now; otherwise the folder is listed.
func (r *run) albumLocal(folder string, files []*found) albumLocal {
	if cached, ok := r.albums[folder]; ok {
		return cached
	}
	local := albumLocal{nfo: details{}}
	defer func() { r.albums[folder] = local }()
	if folder == "" || r.lib.Options.Depth == db.DepthFiles {
		return local
	}
	if files == nil {
		entries, _ := r.src.List(r.ctx, folder)
		for _, e := range entries {
			if !e.Dir {
				files = append(files, &found{entry: e, rel: path.Join(folder, e.Name), role: role(r.lib.Kind, e.Name), state: added})
			}
		}
	}
	for _, file := range files {
		name := strings.ToLower(file.entry.Name)
		switch {
		case name == "album.nfo":
			raw, err := readSmall(r.ctx, r.src, file.rel, 64<<10)
			if err != nil {
				continue
			}
			values, err := localmetadata.AlbumNFO(raw)
			if err != nil {
				continue
			}
			for _, key := range albumNFOKeys {
				if value := localmetadata.CleanAudioTag(key, values[key]); value != "" {
					local.nfo[key] = value
				}
			}
		}
	}
	return local
}

// given names the fields of an item whose values came from its files (tags or album.nfo), so
// that no provider replaces them.
func given(item *db.ImportItem, fields ...string) {
	for _, field := range fields {
		if field != "" {
			item.LocalFields = append(item.LocalFields, field)
		}
	}
}

// fieldIf is field when the files gave its value, and "" otherwise.
func fieldIf(fromFiles bool, field string) string {
	if fromFiles {
		return field
	}
	return ""
}

// tagLyrics are the lyrics in a song's tags: timed ones before plain ones.
func tagLyrics(tags audiotags.Tags) *db.Lyrics {
	var out *db.Lyrics
	for _, l := range tags.Lyrics {
		if strings.TrimSpace(l.Text) == "" || out != nil && (out.Timed || l.Format != "lrc") {
			continue
		}
		language := strings.ToLower(l.Language)
		if language == "und" || language == "xxx" {
			language = ""
		}
		out = &db.Lyrics{Source: db.LyricsFromFile, Body: l.Text, Timed: l.Format == "lrc", Language: language}
	}
	return out
}
