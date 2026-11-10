package scan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
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
// album.nfo, and the pictures beside its files.
type albumLocal struct {
	nfo    details
	images []db.ImportImage
	cover  *db.ImportImage // the first cover found inside one of its files
}

// albumLocal reads an album's folder once a scan: album.nfo and the cover pictures. files is
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
		case file.role == db.FileArtwork && coverNames[stem(file.entry.Name)]:
			if image, ok := r.picture(file, db.ImageCover); ok {
				local.images = append(local.images, image)
			}
		}
	}
	return local
}

// embeddedCover is an album's cover from inside its files: the front cover in a file's tags,
// or its first picture, copied into the picture store once a scan. It is used after a picture
// beside the files.
func (r *run) embeddedCover(folder string, tags audiotags.Tags) (db.ImportImage, bool) {
	if local := r.albums[folder]; local.cover != nil && folder != "" {
		return *local.cover, true
	}
	if r.s.Pictures == nil || len(tags.Pictures) == 0 {
		return db.ImportImage{}, false
	}
	picture := tags.Pictures[0]
	for _, p := range tags.Pictures {
		if p.Type == 3 { // the front cover, as ID3 and FLAC number it
			picture = p
			break
		}
	}
	sum := sha256.Sum256(picture.Data)
	id := hex.EncodeToString(sum[:])
	if err := r.s.Pictures.Put(id, picture.Data); err != nil {
		r.s.log().Warn("embedded picture not copied", "error", err)
		return db.ImportImage{}, false
	}
	out := db.ImportImage{Role: db.ImageCover, Source: db.ImageFromEmbedded, FileHash: id}
	if config, _, err := image.DecodeConfig(bytes.NewReader(picture.Data)); err == nil {
		out.Width, out.Height = config.Width, config.Height
	}
	if local, ok := r.albums[folder]; ok && folder != "" {
		local.cover = &out
		r.albums[folder] = local
	}
	return out, true
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
