package naming

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// AudiobookFolder is what an audiobook's folders say about it, read with
// Audiobookshelf's conventions so a library arranged for it reads the same here:
//
//	Author/Title/files
//	Author/Series/Title/files       (a series only with an author above it)
//	Author/Series/Book 1 - Title/files, …/1. Title/, …/Title - Vol. 2/
//	…/1999 - Title/, …/(1999) - Title/, …/Title {Narrator}/, …/Title [B0015T963C]/
//	…/Title/Disc 1/files            (a disc folder is part of its book)
//
// One addition: a title folder "Title (Series #3)" names its series when there
// is no series folder. Every field is the folder's evidence only: tags and
// sidecars win wherever they say something.
type AudiobookFolder struct {
	Title, Author, Series, Sequence, Year, ASIN string
	Narrators                                   []string
}

var (
	folderASIN      = regexp.MustCompile(`(?:^| )\[([A-Z0-9]{10})\](?: |$)`)
	folderNarrator  = regexp.MustCompile(`^(.*) \{([^{}]+)\}$`)
	folderYear      = regexp.MustCompile(`^ *\(?([0-9]{4})\)? *- *(.+)$`)
	folderSequence  = regexp.MustCompile(`(?i)^(vol\.? |volume |book )?(\d{1,3}(?:\.\d{1,2})?)(\.?)(?: (.*))?$`)
	folderSeriesTag = regexp.MustCompile(`^(.+?) \(([^()#]+?) #(\d{1,3}(?:\.\d{1,2})?)\)$`)
	audiobookDisc   = regexp.MustCompile(`(?i)^(?:disc|disk|cd|vol|volume|part)[ ._-]*([0-9]{1,3})(?:[ ._-]*of[ ._-]*[0-9]{1,3})?$`)
)

// AudiobookDiscFolder is DiscFolder for audiobooks: the folder is only a disc
// ("Disc 2", "CD1", "Part 3 of 4"). A title folder that starts with a volume
// ("Vol. 2 - Title", Audiobookshelf's way to number a book in its series) is
// the book itself.
func AudiobookDiscFolder(s string) (int, bool) {
	m := audiobookDisc.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, n > 0
}

// ReadAudiobookFolder reads a library-relative audiobook file path. The file's
// own folder (above any disc folder) is the title folder; a file at the
// library's top has no folder evidence.
func ReadAudiobookFolder(relative string) AudiobookFolder {
	var out AudiobookFolder
	if len(relative) > MaxPathBytes || strings.IndexByte(relative, 0) >= 0 {
		return out
	}
	dirs := strings.Split(path.Clean(slash(relative)), "/")
	dirs = dirs[:len(dirs)-1]
	if len(dirs) > 0 {
		if _, ok := AudiobookDiscFolder(dirs[len(dirs)-1]); ok {
			dirs = dirs[:len(dirs)-1]
		}
	}
	if len(dirs) == 0 || len(dirs) > MaxDepth {
		return out
	}
	folder := strings.TrimSpace(dirs[len(dirs)-1])
	dirs = dirs[:len(dirs)-1]
	// As Audiobookshelf: with two folders or more above the title, the nearest
	// is the series and the next the author; with one, it is the author.
	if len(dirs) >= 2 {
		out.Series = tidy(dirs[len(dirs)-1])
		out.Author = tidy(dirs[len(dirs)-2])
	} else if len(dirs) == 1 {
		out.Author = tidy(dirs[0])
	}
	if m := folderASIN.FindStringSubmatchIndex(folder); m != nil {
		out.ASIN = folder[m[2]:m[3]]
		folder = strings.TrimSpace(folder[:m[0]] + " " + folder[m[1]:])
	}
	if m := folderNarrator.FindStringSubmatch(folder); m != nil {
		out.Narrators = folderNames(m[2])
		folder = strings.TrimSpace(m[1])
	}
	if out.Series != "" {
		folder, out.Sequence = sequenceFrom(folder)
	}
	if m := folderYear.FindStringSubmatch(folder); m != nil {
		out.Year = m[1]
		folder = strings.TrimSpace(m[2])
	}
	if out.Series == "" {
		if m := folderSeriesTag.FindStringSubmatch(folder); m != nil {
			folder, out.Series, out.Sequence = m[1], tidy(m[2]), canonicalSequence(m[3])
		}
	}
	out.Title = tidy(folder)
	if out.Title == "" {
		return AudiobookFolder{}
	}
	return out
}

// sequenceFrom finds a book's place in its series in one " - " part of the
// title folder: "Book 2", "Vol. 3", "volume 9", "1. Title", "01". A number that
// simply starts a title ("101 Dalmatians") is not a place in a series.
func sequenceFrom(folder string) (string, string) {
	parts := strings.Split(folder, " - ")
	for i, part := range parts {
		m := folderSequence.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil || m[4] != "" && m[1] == "" && m[3] == "" {
			continue
		}
		rest := append(append([]string{}, parts[:i]...), parts[i+1:]...)
		if m[4] != "" {
			rest = append(append(append([]string{}, parts[:i]...), m[4]), parts[i+1:]...)
		}
		if len(rest) == 0 {
			// "Series/Book 2/": the place is known, and the folder stays the title.
			return folder, canonicalSequence(m[2])
		}
		return strings.Join(rest, " - "), canonicalSequence(m[2])
	}
	return folder, ""
}

func canonicalSequence(s string) string {
	if n, e := strconv.ParseFloat(s, 64); e == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return s
}

// folderNames splits "{Jim Dale & Stephen Fry}" into narrators. A comma splits
// only between full names, so "Dale, Jim" stays one person.
func folderNames(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == '&' })
	out := []string{}
	for _, field := range fields {
		pieces := strings.Split(field, ",")
		whole := len(pieces) > 1
		for _, piece := range pieces {
			whole = whole && strings.Contains(strings.TrimSpace(piece), " ")
		}
		if !whole {
			pieces = []string{field}
		}
		for _, piece := range pieces {
			if name := tidy(piece); name != "" && len(out) < 16 {
				out = append(out, name)
			}
		}
	}
	return out
}

// tidy keeps a folder name's own punctuation (a title's dots and dashes are its
// own); it only folds whitespace.
func tidy(s string) string {
	return norm.NFC.String(strings.Join(strings.Fields(s), " "))
}
