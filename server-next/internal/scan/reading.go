package scan

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
)

// bookUnits turns a book library's folder into books. Each file is a book; two formats of one
// book, with the same name in the same folder, are two versions of it. Details come from an
// OPF file beside the book (Calibre's metadata.opf, or one with the book's name); otherwise
// the name is the title and a folder above is the author.
func (r *run) bookUnits(f *folder) []unit {
	var units []unit
	for _, file := range f.media {
		if !file.touched() {
			continue
		}
		d := details{}
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
		if isbn := d["isbn"]; isbn != "" {
			item.ExternalIDs = []db.ExternalID{{Provider: "isbn", ID: isbn}}
		}
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
// the library is a comic by itself.
func (r *run) comicUnits(f *folder) []unit {
	var units []unit
	for _, file := range f.media {
		if !file.touched() {
			continue
		}
		name := cleanName(file.entry.Name)
		version := db.ImportVersion{Parts: []db.ImportPart{{File: file.ref()}}}
		u := unit{files: []*found{file}}
		if f.rel == "" {
			u.items = []db.ImportItem{{Kind: db.KindComic, Title: name, Versions: []db.ImportVersion{version},
				Keys: []db.ItemKey{db.FileKey(r.path.ID, file.rel, 0), {Kind: db.KeyName, Key: "comic|" + fold(name)}}}}
			units = append(units, u)
			continue
		}
		series := cleanName(path.Base(f.rel))
		issue := db.ImportItem{Kind: db.KindIssue, Title: name, ParentRow: 1, Versions: []db.ImportVersion{version},
			Keys: []db.ItemKey{db.FileKey(r.path.ID, file.rel, 0)}, ChildKey: "issue|" + fold(name)}
		if m := issueNumber.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			issue.IndexNumber, issue.ChildKey = &n, fmt.Sprintf("i%d", n)
		}
		u.items = []db.ImportItem{{Kind: db.KindSeries, Title: series,
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: "series|" + fold(series)}, db.FolderKey(r.path.ID, f.rel)}}, issue}
		units = append(units, u)
	}
	return units
}
