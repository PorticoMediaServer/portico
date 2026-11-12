package scan

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// bookUnits turns a book library's folder into books. Each file is a book; two formats of one
// book, with the same name in the same folder, are two versions of it. Details come from the
// book's own file (an EPUB's package document), then from an OPF file beside the book
// (Calibre's metadata.opf, or one with the book's name), which the owner keeps and so wins;
// otherwise the name is the title and a folder above is the author.
func (r *run) bookUnits(f *folder) []unit {
	var units []unit
	for _, file := range f.media {
		if !file.touched() && !r.rereads(file) {
			continue
		}
		d, read := r.embedded(file)
		if r.rereads(file) && !read {
			// A changed file that cannot be read now (still being copied, or cut short) says
			// nothing about the book: it keeps what it has until a scan can read it.
			continue
		}
		if r.lib.Options.Depth != db.DepthFiles {
			for _, sidecar := range []string{path.Join(f.rel, "metadata.opf"), strings.TrimSuffix(file.rel, path.Ext(file.rel)) + ".opf"} {
				if raw, err := readSmall(r.ctx, r.src, sidecar, 256<<10); err == nil {
					if more, err := localmetadata.OPF(raw); err == nil {
						d.overlay(more)
					}
				}
			}
		}
		name := cleanName(file.entry.Name)
		authors := d.names("authors", "author")
		if len(authors) == 0 {
			// Author/Title/file and Author/file: the folder at the top is the author.
			if top, _, nested := strings.Cut(file.rel, "/"); nested {
				authors = []string{top}
			}
			// "Title - Author" is how Calibre names the file.
			if title, author, ok := strings.Cut(name, " - "); ok && len(authors) == 1 && fold(author) == fold(authors[0]) {
				name = title
			}
		}
		bookTitle := firstOf(d["album"], name)
		item := db.ImportItem{Kind: db.KindBook, Title: bookTitle, Year: d.year(), Summary: d["description"], Tags: genreTags(d),
			Credits: credits(db.RoleAuthor, authors),
			Keys: []db.ItemKey{db.FileKey(r.path.ID, file.rel, 0),
				{Kind: db.KeyName, Key: "book|" + fold(bookTitle) + "|" + fold(strings.Join(authors, ","))}},
			Versions: []db.ImportVersion{{Name: strings.TrimPrefix(strings.ToLower(path.Ext(file.rel)), "."), Parts: []db.ImportPart{{File: file.ref()}}}}}
		given(&item, fieldIf(d["album"] != "", db.FieldTitle), fieldIf(d.year() > 0, db.FieldYear), fieldIf(d["description"] != "", db.FieldSummary),
			fieldIf(len(item.Tags) > 0, db.FieldGenres), fieldIf(len(d.names("authors", "author")) > 0, db.FieldCredits))
		if isbn := d["isbn"]; isbn != "" {
			item.ExternalIDs = []db.ExternalID{{Provider: "isbn", ID: isbn}}
		}
		if n, err := strconv.Atoi(d["word_count"]); err == nil {
			item.WordCount = &n
		}
		item.Reread = r.rereads(file)
		item.Publication, item.ReleaseDate = issue(d, name)
		u := unit{files: []*found{file}}
		if series := d["series"]; series != "" {
			seriesKey := db.ItemKey{Kind: db.KeyName, Key: "series|" + fold(series)}
			item.ExtraOf, item.ExtraKind = []db.ItemKey{seriesKey}, db.RelationSeriesMember
			if position := number(d["series_position"]); position > 0 {
				item.IndexNumber = &position
			}
			u.items = append(u.items, db.ImportItem{Kind: db.KindSeries, Title: series, Keys: []db.ItemKey{seriesKey}})
		}
		u.items = append(u.items, item)
		units = append(units, u)
	}
	return units
}

// cleanName is a file's name as a title: no extension, dots and underscores as spaces.
func cleanName(name string) string {
	name = strings.TrimSuffix(name, path.Ext(name))
	return strings.Join(strings.Fields(strings.NewReplacer("_", " ", ".", " ").Replace(name)), " ")
}

// issueNumber finds the issue number at the end of a comic's name: "Saga 012", "Saga #12",
// "Saga 012 (2013)", "Saga v02". A four-digit number in brackets is a year.
var issueNumber = regexp.MustCompile(`(?i)(?:^|[ #_.-])(?:#|v|vol\.? ?|issue ?)?(\d{1,4})(?: ?\([^)]*\))*$`)

// comicUnits turns a comic library's folder into comics. The files of a folder are the issues
// of one series, named after the folder and numbered from their names. A file at the top of
// the library is a comic by itself. A folder of pictures is one comic: see imagePages.
func (r *run) comicUnits(f *folder) []unit {
	if pages := imagePages(f); pages != nil {
		return r.imageComic(f, pages)
	}
	var units []unit
	for _, file := range f.media {
		if !file.touched() && !r.rereads(file) {
			continue
		}
		name := cleanName(file.entry.Name)
		version := db.ImportVersion{Parts: []db.ImportPart{{File: file.ref()}}}
		u := unit{files: []*found{file}}
		// A comic's own ComicInfo.xml gives its title, date, summary, writers and genres. A
		// changed comic that cannot be read now keeps what it has.
		d, read := r.embedded(file)
		if r.rereads(file) && !read {
			continue
		}
		title := firstOf(d["album"], name)
		if f.rel == "" {
			comic := db.ImportItem{Kind: db.KindComic, Title: title, Versions: []db.ImportVersion{version},
				Keys: []db.ItemKey{db.FileKey(r.path.ID, file.rel, 0), {Kind: db.KeyName, Key: "comic|" + fold(name)}}}
			comicDetails(&comic, d)
			comic.Reread = r.rereads(file)
			u.items = []db.ImportItem{comic}
			units = append(units, u)
			continue
		}
		series := cleanName(path.Base(f.rel))
		issue := db.ImportItem{Kind: db.KindIssue, Title: title, ParentRow: 1, Versions: []db.ImportVersion{version},
			Keys: []db.ItemKey{db.FileKey(r.path.ID, file.rel, 0)}, ChildKey: "issue|" + fold(name)}
		comicDetails(&issue, d)
		issue.Reread = r.rereads(file)
		if m := issueNumber.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			issue.IndexNumber, issue.ChildKey = &n, fmt.Sprintf("i%d", n)
		} else if n := number(d["series_position"]); n > 0 {
			issue.IndexNumber, issue.ChildKey = &n, fmt.Sprintf("i%d", n)
		}
		u.items = []db.ImportItem{{Kind: db.KindSeries, Title: series,
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: "series|" + fold(series)}, db.FolderKey(r.path.ID, f.rel)}}, issue}
		units = append(units, u)
	}
	return units
}

// artworkName is a picture's name, without its extension, when it is artwork for whatever is
// in its folder and not a page.
var artworkName = regexp.MustCompile(`(?i)^(poster|folder|fanart|backdrop|background|logo|banner|thumb|clearart|landscape)$|-(poster|fanart|logo|banner|thumb)$`)

// imagePages are the pages of a folder that is a comic made of pictures, in reading order, or
// nil for a folder that is not one. Such a folder is told apart from artwork kept beside
// something else in three ways: it is in a comics library; nothing else in it is a comic
// (beside a .cbz or .pdf, pictures are that comic's artwork); and at least two of its pictures
// are not named as artwork is (poster, folder, fanart and the like), so a series folder that
// holds only "folder.jpg" and its issues' folders is not a one-page comic. Pages are in the
// natural order of their names (2 before 10), with a picture named "cover" first.
func imagePages(f *folder) []*found {
	if len(f.media) > 0 || f.rel == "" {
		return nil
	}
	var pages []*found
	real := 0
	for _, file := range f.files {
		if file.role != db.FileArtwork || file.problem != db.ProblemNone {
			continue
		}
		stem := strings.TrimSuffix(file.entry.Name, path.Ext(file.entry.Name))
		if !artworkName.MatchString(stem) {
			real++
		}
		pages = append(pages, file)
	}
	if real < 2 {
		return nil
	}
	order := func(file *found) string {
		stem := strings.TrimSuffix(file.entry.Name, path.Ext(file.entry.Name))
		if strings.EqualFold(stem, "cover") {
			return ""
		}
		return sorttext.Key(stem, stem, "")
	}
	sort.SliceStable(pages, func(i, j int) bool { return order(pages[i]) < order(pages[j]) })
	return pages
}

// imageComic is the one comic a folder of pictures is: an issue of the series its parent
// folder names, or a comic by itself when the folder is at the top of the library. Its one
// version has a part for every page, in reading order, so whoever serves the pages reads
// them from the scanner's own list and never lists the folder.
func (r *run) imageComic(f *folder, pages []*found) []unit {
	touched := false
	for _, page := range pages {
		touched = touched || page.state != same
		page.role = db.FileMedia
	}
	if !touched {
		return nil
	}
	version := db.ImportVersion{Name: "images", Whole: true}
	for _, page := range pages {
		version.Parts = append(version.Parts, db.ImportPart{File: page.ref()})
	}
	name := cleanName(path.Base(f.rel) + ".")
	u := unit{files: pages}
	parent := path.Dir(f.rel)
	if parent == "." {
		u.items = []db.ImportItem{{Kind: db.KindComic, Title: name, Versions: []db.ImportVersion{version},
			Keys: []db.ItemKey{db.FolderKey(r.path.ID, f.rel), {Kind: db.KeyName, Key: "comic|" + fold(name)}}}}
		return []unit{u}
	}
	series := cleanName(path.Base(parent) + ".")
	issue := db.ImportItem{Kind: db.KindIssue, Title: name, ParentRow: 1, Versions: []db.ImportVersion{version},
		Keys: []db.ItemKey{db.FolderKey(r.path.ID, f.rel)}, ChildKey: "issue|" + fold(name)}
	if m := issueNumber.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		issue.IndexNumber, issue.ChildKey = &n, fmt.Sprintf("i%d", n)
	}
	u.items = []db.ImportItem{{Kind: db.KindSeries, Title: series,
		Keys: []db.ItemKey{{Kind: db.KeyName, Key: "series|" + fold(series)}, db.FolderKey(r.path.ID, parent)}}, issue}
	return []unit{u}
}

// embedded reads a book's details from inside its own file: an EPUB's package document, a
// comic's ComicInfo.xml (10-reading rule 8). What the file says is recorded as the file's own,
// so a provider never replaces it. A library scanned for names only gives none, and the book
// keeps what its name and folders say. read is false when the file could not be read at all
// (it will not open, or is not a whole archive): that says nothing about the book.
func (r *run) embedded(file *found) (d details, read bool) {
	d = details{}
	ext := strings.ToLower(path.Ext(file.rel))
	if r.lib.Options.Depth == db.DepthFiles || ext != ".epub" && ext != ".cbz" {
		return d, true
	}
	f, err := r.src.Open(r.ctx, file.rel)
	if err != nil {
		return d, false
	}
	defer f.Close()
	var more map[string]string
	if ext == ".epub" {
		raw, err := reading.EPUBPackage(f, f.Size())
		if err != nil {
			return d, false
		}
		more, _ = localmetadata.EPUBPackage(raw)
		if more == nil {
			more = map[string]string{}
		}
		if words, err := reading.EPUBWords(f, f.Size()); err == nil {
			more["word_count"] = strconv.Itoa(words)
		}
	} else {
		raw, err := reading.ComicInfo(f, f.Size())
		if err != nil {
			return d, false
		}
		if raw != nil {
			more, _ = localmetadata.ComicInfo(raw)
		}
	}
	d.overlay(more)
	return d, true
}

// rereads reports whether a book or comic file changed where it is (it was edited, or its details
// rewritten): what it says now brings its title up to date, where neither a match nor a person's
// edit has taken those details over.
func (r *run) rereads(file *found) bool {
	ext := strings.ToLower(path.Ext(file.rel))
	return file.state == changed && r.lib.Options.Depth != db.DepthFiles && (ext == ".epub" || ext == ".cbz")
}

// comicDetails puts what a comic's ComicInfo.xml said on its item, as the file's own.
func comicDetails(item *db.ImportItem, d details) {
	writers := d.names("authors", "author")
	item.Year, item.Summary, item.Tags, item.Credits = d.year(), d["description"], genreTags(d), credits(db.RoleAuthor, writers)
	given(item, fieldIf(d["album"] != "", db.FieldTitle), fieldIf(d.year() > 0, db.FieldYear), fieldIf(d["description"] != "", db.FieldSummary),
		fieldIf(len(item.Tags) > 0, db.FieldGenres), fieldIf(len(writers) > 0, db.FieldCredits))
	item.Publication, item.ReleaseDate = issue(d, item.Title)
}

var (
	fullDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	monthDate = regexp.MustCompile(`^\d{4}-\d{2}$`)
	// A date in a periodical's file name: "The Economist 2026-11-07", "Wired 2026-03".
	namedDay   = regexp.MustCompile(`(?:^|[^\d])(\d{4}-\d{2}-\d{2})(?:[^\d]|$)`)
	namedMonth = regexp.MustCompile(`(?:^|[^\d])(\d{4}-\d{2})(?:[^\d-]|$)`)
)

// issue is a book's or comic's publication, as its own file names it, and its date: the file's
// own (a whole date, or a year and month for a magazine), or for a magazine or newspaper a date in
// its name. A newspaper's issue is a day; a magazine's may be a month.
func issue(d details, name string) (int, string) {
	publication, _ := db.ParsePublication(d["publication"])
	date := d["date"]
	switch {
	case fullDate.MatchString(date):
		return publication, date[:10]
	case publication == db.PublicationMagazine && monthDate.MatchString(date):
		return publication, date
	case publication == db.PublicationBook:
		return publication, ""
	}
	if m := namedDay.FindStringSubmatch(name); m != nil {
		return publication, m[1]
	}
	if m := namedMonth.FindStringSubmatch(name); m != nil && publication == db.PublicationMagazine {
		return publication, m[1]
	}
	return publication, ""
}
