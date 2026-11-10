// Package localmetadata parses the small files people keep beside their media: Kodi-style NFO
// files, cue sheets, OPF files and Audiobookshelf's metadata.json. It reads bytes it is handed
// and touches no disk, network or database. The parsers were copied from the old server with
// their tests; this file holds the few types and helpers they used from its other packages.
package localmetadata

import (
	"encoding/json"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Chapter is a named span of a file, in seconds.
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"startSeconds"`
	End   float64 `json:"endSeconds"`
}

func AudioTagName(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "musicbrainz track id", "musicbrainz/track id", "ufid:http://musicbrainz.org", "musicbrainz_recordingid":
		return "musicbrainz_trackid"
	case "musicbrainz album id", "musicbrainz/album id", "musicbrainz_releaseid":
		return "musicbrainz_albumid"
	case "musicbrainz release track id", "musicbrainz/release track id":
		return "musicbrainz_releasetrackid"
	case "musicbrainz release group id", "musicbrainz/release group id":
		return "musicbrainz_releasegroupid"
	case "musicbrainz artist id", "musicbrainz/artist id":
		return "musicbrainz_artistid"
	case "musicbrainz album artist id", "musicbrainz/album artist id":
		return "musicbrainz_albumartistid"
	case "albumartist", "album artist":
		return "album_artist"
	case "tracknumber", "track_number":
		return "track"
	case "discnumber", "disc_number", "disk":
		return "disc"
	case "totaltracks", "tracktotal":
		return "total_tracks"
	case "totaldiscs", "disctotal":
		return "total_discs"
	case "fingerprint algorithm", "acoustid_fingerprint_algorithm":
		return "fingerprint_algorithm"
	case "acoustid fingerprint", "chromaprint_fingerprint":
		return "acoustid_fingerprint"
	case "catalog_number", "catalog number":
		return "catalognumber"
	case "releasecountry", "musicbrainz album release country":
		return "release_country"
	case "musicbrainz album status", "releasestatus":
		return "release_status"
	case "musicbrainz album type", "releasetype":
		return "release_type"
	case "series_part", "series-part", "series_index", "seriesindex", "calibre:series_index":
		return "series_position"
	case "series_title", "calibre:series", "show":
		return "series"
	case "comment", "comments", "synopsis":
		return "description"
	case "grouping", "mvnm":
		return "series"
	case "mvin":
		return "series_position"
	case "artistsort":
		return "artist_sort"
	case "albumartistsort":
		return "album_artist_sort"
	case "titlesort":
		return "title_sort"
	case "albumsort":
		return "album_sort"
	case "publisher", "organization":
		return "publisher"
	case "language", "language_ietf":
		return "language"
	case "subtitle", "version":
		return "edition"
	}
	switch key {
	case "replaygain_track_gain", "replaygain_album_gain", "replaygain_track_peak", "replaygain_album_peak", "r128_track_gain", "r128_album_gain", "encoder_delay",
		"encoder_padding", "itunsmpb", "title", "album", "artist", "album_artist", "artists", "album_artists", "track", "disc", "date", "year", "originaldate", "release_date",
		"genre", "composer", "author", "authors", "narrator", "narrators", "compilation", "musicbrainz_trackid", "musicbrainz_albumid", "musicbrainz_releasetrackid",
		"musicbrainz_releasegroupid", "musicbrainz_artistid", "musicbrainz_albumartistid", "isrc", "barcode", "catalognumber", "label", "release_country", "release_status",
		"release_type", "media", "total_tracks", "total_discs", "acoustid_fingerprint", "fingerprint_algorithm", "series", "series_position", "series_list", "publisher",
		"language", "edition", "isbn", "asin", "identifiers", "description", "genres", "composers", "musicbrainz_artistids", "musicbrainz_albumartistids", "artist_sort",
		"album_artist_sort", "title_sort", "album_sort", "disc_subtitle":
		return key
	}
	return ""
}
func ValidAudioTag(field, value string) bool {
	limit := 4096
	if field == "acoustid_fingerprint" {
		limit = 16384
	}
	return AudioTagName(field) != "" && len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value,
		func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
}

// CleanAudioTag makes a tag value safe to show on one line (CD-27): every run
// of whitespace, including the tabs and line breaks some taggers leave inside a
// title or artist, becomes one space. Only a description keeps its lines. A
// title with a tab or newline made whole music pages unreadable to clients,
// which refuse control characters in names.
func CleanAudioTag(field, value string) string {
	if AudioTagName(field) == "description" {
		return strings.TrimSpace(value)
	}
	return strings.Join(strings.Fields(value), " ")
}

// CleanChapters rejects bad entries individually and retains a bounded timeline.
func CleanChapters(input []Chapter, duration float64) ([]Chapter, string) {
	out := make([]Chapter, 0, min(len(input), 4096))
	status := "known"
	if len(input) > 4096 {
		status = "partial"
		input = input[:4096]
	}
	sorted := append([]Chapter(nil), input...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if math.IsNaN(sorted[i].Start) {
			return false
		}
		if math.IsNaN(sorted[j].Start) {
			return true
		}
		return sorted[i].Start < sorted[j].Start
	})
	for i, c := range sorted {
		if math.IsNaN(c.Start) || math.IsNaN(c.End) || math.IsInf(c.Start, 0) || math.IsInf(c.End, 0) || c.Start < 0 || c.Start >= duration {
			status = "partial"
			continue
		}
		if c.End == 0 {
			c.End = duration
			if i+1 < len(sorted) && sorted[i+1].Start > c.Start {
				c.End = sorted[i+1].Start
			}
		}
		if c.End > duration {
			c.End = duration
			status = "partial"
		}
		if len(out) > 0 && c.Start < out[len(out)-1].End {
			if out[len(out)-1].End-c.Start <= .1 {
				c.Start = out[len(out)-1].End
				status = "partial"
			} else {
				status = "partial"
				continue
			}
		}
		if c.End <= c.Start {
			status = "partial"
			continue
		}
		out = append(out, c)
	}
	return out, status
}

// VideoNFO is bounded descriptive evidence, never probe facts or source identity.
// Locator is an opaque sidecar digest, not a client-visible filesystem path.
type NFOActorThumbnail struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type VideoNFO struct {
	Issues                 []string            `json:"issues,omitempty"`
	Tags                   []string            `json:"tags,omitempty"`
	Countries              []string            `json:"countries,omitempty"`
	Status                 string              `json:"status,omitempty"`
	Trailer                string              `json:"trailer,omitempty"`
	Rating                 *ScreenRating       `json:"rating,omitempty"`
	ActorThumbnails        []NFOActorThumbnail `json:"actorThumbnails,omitempty"`
	DisplaySeason          *int                `json:"displaySeason,omitempty"`
	DisplayEpisode         *int                `json:"displayEpisode,omitempty"`
	DateAdded              string              `json:"dateAdded,omitempty"`
	CollectionOverview     string              `json:"collectionOverview,omitempty"`
	LockData               bool                `json:"lockData,omitempty"`
	Kind                   string              `json:"kind"`
	Locator                string              `json:"locator"`
	Digest                 string              `json:"digest"`
	Size                   int64               `json:"size"`
	ModifiedNS             int64               `json:"modifiedNs"`
	Revision               string              `json:"revision,omitempty"`
	Title                  string              `json:"title,omitempty"`
	OriginalTitle          string              `json:"originalTitle,omitempty"`
	SortTitle              string              `json:"sortTitle,omitempty"`
	Year                   int                 `json:"year,omitempty"`
	Overview               string              `json:"overview,omitempty"`
	Tagline                string              `json:"tagline,omitempty"`
	Date                   string              `json:"date,omitempty"`
	Edition                string              `json:"edition,omitempty"`
	Collection             string              `json:"collection,omitempty"`
	Certification          string              `json:"certification,omitempty"`
	Genres                 []string            `json:"genres,omitempty"`
	Studios                []string            `json:"studios,omitempty"`
	IDs                    []ScreenID          `json:"ids,omitempty"`
	Credits                []ScreenCredit      `json:"credits,omitempty"`
	Season                 *int                `json:"season,omitempty"`
	Episode                *int                `json:"episode,omitempty"`
	Absolute               *int                `json:"absolute,omitempty"`
	AdvisoryRuntimeMinutes *int                `json:"advisoryRuntimeMinutes,omitempty"`
}

type ScreenID struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	ID       string `json:"id"`
}
type ScreenCredit struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Department string `json:"department"`
	Ordinal    int    `json:"ordinal"`
}
type ScreenRating struct {
	Value float64 `json:"value"`
	Scale int     `json:"scale"`
	Votes int     `json:"votes"`
}

func ValidScreenID(v ScreenID) bool {
	if v.Provider == "imdb" {
		// IMDb is only ever a crosswalk: a title id as IMDb writes it.
		return v.Type == "title" && validIMDbID(v.ID)
	}
	n, err := strconv.ParseInt(v.ID, 10, 64)
	if err != nil || n <= 0 || n > 9007199254740991 || strconv.FormatInt(n, 10) != v.ID {
		return false
	}
	switch v.Provider {
	case "tmdb", "tvdb":
		return v.Type == "movie" || v.Type == "show" || v.Type == "episode" || v.Type == "season" || v.Type == "person" || v.Type == "collection" || v.Type == "company"
	case "anilist":
		return n <= 2147483647 && (v.Type == "anime" || v.Type == "studio" || v.Type == "character" || v.Type == "person")
	}
	return false
}
func validIMDbID(id string) bool {
	if len(id) < 9 || len(id) > 12 || !strings.HasPrefix(id, "tt") {
		return false
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ParseScreenHint extracts an identifier from known provider syntax. It never
// returns a remote URL to fetch and never accepts a host supplied as a provider.
func ParseScreenHint(input, kind string) (ScreenID, bool) {
	input = strings.TrimSpace(input)
	if len(input) > 2048 {
		return ScreenID{}, false
	}
	if validIMDbID(input) {
		return ScreenID{"imdb", "title", input}, true
	}
	if u, e := url.Parse(input); e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Port() == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		switch host {
		case "imdb.com":
			if len(parts) == 2 && parts[0] == "title" && validIMDbID(parts[1]) {
				return ScreenID{"imdb", "title", parts[1]}, true
			}
		case "themoviedb.org":
			if len(parts) == 2 {
				typ := map[string]string{"movie": "movie", "tv": "show"}[parts[0]]
				id := ScreenID{"tmdb", typ, strings.Split(parts[1], "-")[0]}
				return id, ValidScreenID(id)
			}
		case "anilist.co":
			if len(parts) == 2 && parts[0] == "anime" {
				id := ScreenID{"anilist", "anime", parts[1]}
				return id, ValidScreenID(id)
			}
		case "thetvdb.com":
			if len(parts) == 3 && parts[0] == "dereferrer" {
				typ := map[string]string{"movie": "movie", "series": "show"}[parts[1]]
				id := ScreenID{"tvdb", typ, parts[2]}
				return id, ValidScreenID(id)
			}
		}
	}
	provider, value, found := strings.Cut(input, ":")
	provider = strings.ToLower(provider)
	if !found {
		return ScreenID{}, false
	}
	if provider == "imdb" {
		id := ScreenID{"imdb", "title", value}
		return id, ValidScreenID(id)
	}
	if provider == "anilist" {
		kind = "anime"
	}
	id := ScreenID{provider, kind, value}
	return id, ValidScreenID(id)
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

// CueTracks reads a cue sheet and returns the tracks it gives one audio file: each track's
// title and where it starts in the file, in seconds. ownSheet says the sheet is named after
// the file, so a sheet that lists one file is that file's whatever name it gives.
func CueTracks(raw []byte, audio string, ownSheet bool) ([]Chapter, error) {
	files, err := parseCue(raw)
	if err != nil {
		return nil, err
	}
	return cueChapters(files, audio, ownSheet), nil
}

// Audiobookshelf reads the metadata.json Audiobookshelf keeps in a book's folder: the book's
// details by tag name, and its chapters.
func Audiobookshelf(raw []byte) (map[string]string, []Chapter, error) { return parseABSMetadata(raw) }

// OPF reads a book's OPF file into details by tag name.
func OPF(raw []byte) (map[string]string, error) { return parseOPF(raw) }

// BookText reads Audiobookshelf's desc.txt or reader.txt.
func BookText(name string, raw []byte) (map[string]string, error) { return parseBookText(name, raw) }

// Names reads a list the parsers above store as JSON ("authors", "narrators", "genres").
func Names(list string) []string {
	var out []string
	if json.Unmarshal([]byte(list), &out) != nil {
		return nil
	}
	return out
}
