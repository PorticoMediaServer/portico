package localmetadata

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
)

// A book's details as its own file gives them: an EPUB's package document, a comic's
// ComicInfo.xml. They are read into the same fields as a sidecar's.

// metadataElement finds a package document's <metadata>…</metadata>, with or without a prefix.
var metadataElement = regexp.MustCompile(`(?s)<(?:[A-Za-z_][\w.-]*:)?metadata[\s>].*?</(?:[A-Za-z_][\w.-]*:)?metadata\s*>`)

// EPUBPackage reads an EPUB's own package document. Only its metadata is read: in a long book
// the list of parts after it can be larger than a document read here may be.
func EPUBPackage(raw []byte) (map[string]string, error) {
	if len(raw) > 64<<10 {
		meta := metadataElement.Find(raw)
		if meta == nil {
			return nil, errors.New("no metadata in the package")
		}
		raw = meta
	}
	return parseOPF(raw)
}

// ComicInfo reads a comic's ComicInfo.xml: its title, series and number, writers, summary,
// date, genres and publisher.
func ComicInfo(raw []byte) (map[string]string, error) {
	// Element names are read in lower case.
	root, err := parseAudioXML(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	if err != nil {
		return nil, err
	}
	if root.name != "comicinfo" {
		return nil, errors.New("not ComicInfo")
	}
	out := map[string]string{"album": root.first("title"), "series": root.first("series"), "series_position": root.first("number"),
		"description": root.first("summary"), "publisher": root.first("publisher")}
	if year := root.first("year"); len(year) == 4 {
		out["date"] = year
		if month := root.first("month"); len(month) > 0 && len(month) <= 2 {
			out["date"] = year + "-" + strings.Repeat("0", 2-len(month)) + month
		}
	}
	writers := uniqueAudioNames(strings.Split(root.first("writer"), ","))
	if len(writers) > 0 {
		out["author"] = strings.Join(writers, "; ")
		out["authors"] = audioList(writers)
	}
	if genres := uniqueAudioNames(strings.Split(root.first("genre"), ",")); len(genres) > 0 {
		out["genres"] = audioList(genres)
	}
	return out, nil
}
