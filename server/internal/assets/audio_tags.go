package assets

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AudioTagEvidence retains independent observations. Tags/TagSources are the
// selected local projection, not a replacement for this provenance.
type AudioTagEvidence struct {
	Field  string `json:"field"`
	Value  string `json:"value"`
	Source string `json:"source"`
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
	case "replaygain_track_gain", "replaygain_album_gain", "replaygain_track_peak", "replaygain_album_peak", "r128_track_gain", "r128_album_gain", "encoder_delay", "encoder_padding", "itunsmpb", "title", "album", "artist", "album_artist", "artists", "album_artists", "track", "disc", "date", "year", "originaldate", "release_date", "genre", "composer", "author", "authors", "narrator", "narrators", "compilation", "musicbrainz_trackid", "musicbrainz_albumid", "musicbrainz_releasetrackid", "musicbrainz_releasegroupid", "musicbrainz_artistid", "musicbrainz_albumartistid", "isrc", "barcode", "catalognumber", "label", "release_country", "release_status", "release_type", "media", "total_tracks", "total_discs", "acoustid_fingerprint", "fingerprint_algorithm", "series", "series_position", "series_list", "publisher", "language", "edition", "isbn", "asin", "identifiers", "description", "genres", "composers", "musicbrainz_artistids", "musicbrainz_albumartistids", "artist_sort", "album_artist_sort", "title_sort", "album_sort", "disc_subtitle":
		return key
	}
	return ""
}
func ValidAudioTag(field, value string) bool {
	limit := 4096
	if field == "acoustid_fingerprint" {
		limit = 16384
	}
	return AudioTagName(field) != "" && len(value) > 0 && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
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

func CopyEmbeddedAudioTags(f *Facts, tags map[string]string) {
	if f.Tags == nil {
		f.Tags = map[string]string{}
	}
	if f.TagSources == nil {
		f.TagSources = map[string]string{}
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := AudioTagName(key)
		value := CleanAudioTag(field, tags[key])
		if !ValidAudioTag(field, value) || len(f.AudioEvidence) >= 128 {
			continue
		}
		f.AudioEvidence = append(f.AudioEvidence, AudioTagEvidence{field, value, "embedded"})
		if f.Tags[field] == "" {
			f.Tags[field] = value
			f.TagSources[field] = "embedded"
		} else if strings.HasPrefix(field, "musicbrainz_") && !strings.EqualFold(strings.TrimSpace(f.Tags[field]), value) {
			f.Tags[field] = "conflicting IDs"
		}
	}
}
