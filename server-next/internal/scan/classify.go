package scan

import (
	"path"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func set(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		out[name] = true
	}
	return out
}

var (
	videoExt = set(".wmv", ".asf", ".flv", ".f4v", ".3gp", ".3g2", ".ogv", ".ogm", ".mxf", ".dv", ".rm", ".rmvb", ".m2v", ".m1v",
		".mp4", ".m4v", ".mov", ".mkv", ".webm", ".avi", ".ts", ".m2ts", ".mts", ".vob", ".mpg", ".mpeg", ".iso")
	audioExt = set(".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav", ".aac", ".aiff", ".aif", ".oga", ".mka", ".wma", ".ape",
		".wv", ".tta", ".caf", ".w64", ".dsf", ".dff", ".mpc")
	bookExt     = set(".epub", ".pdf", ".mobi", ".azw3")
	comicExt    = set(".cbz", ".cbr", ".cb7", ".pdf")
	subtitleExt = set(".srt", ".vtt", ".ass", ".ssa", ".sub", ".sup", ".idx", ".smi", ".sami", ".ttml", ".dfxp")
	imageExt    = set(".jpg", ".jpeg", ".png", ".webp", ".tbn")
)

// Names for the kinds of library, as the naming rules know them.
const (
	namesMovie     = "movie"
	namesShow      = "tv"
	namesAnime     = "anime"
	namesMusic     = "music"
	namesAudiobook = "audiobook"
)

// namesKind is the word the naming rules use for a kind of library.
func namesKind(libraryKind string) string {
	switch libraryKind {
	case db.LibraryShows:
		return namesShow
	case db.LibraryAnime:
		return namesAnime
	case db.LibraryMusic:
		return namesMusic
	case db.LibraryAudiobooks:
		return namesAudiobook
	}
	return namesMovie
}

// role says what a file is for in a library of this kind, or 0 for a file a scan passes over.
func role(libraryKind, name string) db.FileRole {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case subtitleExt[ext]:
		return db.FileSubtitle
	case imageExt[ext]:
		return db.FileArtwork
	case ext == ".nfo" || ext == ".opf" || strings.EqualFold(name, "metadata.json") || strings.EqualFold(name, "desc.txt") ||
		strings.EqualFold(name, "reader.txt"):
		return db.FileMetadata
	case ext == ".lrc":
		return db.FileLyrics
	case ext == ".cue":
		return db.FileCueSheet
	}
	var media map[string]bool
	switch libraryKind {
	case db.LibraryMovies, db.LibraryShows, db.LibraryAnime:
		media = videoExt
	case db.LibraryMusic, db.LibraryAudiobooks:
		media = audioExt
	case db.LibraryBooks:
		media = bookExt
	case db.LibraryComics:
		media = comicExt
	}
	if media[ext] {
		return db.FileMedia
	}
	return 0
}

// stem is a file's name without its extension, in lower case, for pairing sidecars with media.
func stem(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
}

// discFolder reports whether a folder name is the inside of a film disc.
func discFolder(name string) bool {
	return strings.EqualFold(name, "BDMV") || strings.EqualFold(name, "VIDEO_TS")
}

// discTitle reports whether a file inside a disc folder is the one that stands for the disc.
func discTitle(folder, name string) bool {
	if strings.EqualFold(folder, "BDMV") {
		return strings.EqualFold(name, "index.bdmv")
	}
	return strings.EqualFold(name, "VIDEO_TS.IFO")
}

var subtitleFlags = map[string]string{"forced": "forced", "sdh": "sdh", "cc": "sdh", "hi": "sdh", "default": "default"}

// subtitleStream reads the language and kind a subtitle's name gives after the video's name:
// "Film (1999).en.forced.srt".
func subtitleStream(videoStem, name string) db.Stream {
	s := db.Stream{Kind: db.StreamSubtitle, Codec: strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")}
	rest := strings.TrimPrefix(stem(name), videoStem)
	for _, token := range strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == '_' || r == ' ' || r == '-' }) {
		switch flag := subtitleFlags[token]; {
		case flag == "forced":
			s.Forced = true
		case flag == "sdh":
			s.HearingImpaired = true
		case flag == "default":
			s.Default = true
		case s.Language == "" && len(token) >= 2 && len(token) <= 3 && strings.Trim(token, "abcdefghijklmnopqrstuvwxyz") == "":
			s.Language = token
		case s.Language != "" && len(token) == 2 && strings.Trim(token, "abcdefghijklmnopqrstuvwxyz") == "":
			s.Language += "-" + token // a region: pt-br
		}
	}
	return s
}
