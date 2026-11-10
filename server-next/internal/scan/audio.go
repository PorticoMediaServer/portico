package scan

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/naming"
)

// tagLed reports whether this library's titles come from inside its files (their tags), so a
// file that only moved has nothing new to say.
func (r *run) tagLed() bool {
	switch r.lib.Kind {
	case db.LibraryMusic, db.LibraryAudiobooks:
		return r.lib.Options.Depth != db.DepthFiles
	}
	return false
}

// tags reads a file's own tags: a small read from the start and end of the file, never the
// audio. With the file list only, and for a file that cannot be opened, there are none.
func (r *run) tags(file *found) audiotags.Tags {
	if r.lib.Options.Depth == db.DepthFiles {
		return audiotags.Tags{}
	}
	f, err := r.src.Open(r.ctx, file.rel)
	if err != nil {
		return audiotags.Tags{}
	}
	defer f.Close()
	return audiotags.Read(f, f.Size())
}

// details are a file's tag values by name, one value each, cleaned to show on one line.
type details map[string]string

func tagDetails(tags audiotags.Tags) details {
	out := details{}
	for name, value := range tags.JSONValues() {
		if value = localmetadata.CleanAudioTag(name, value); value != "" {
			out[name] = value
		}
	}
	return out
}

// overlay puts later, more deliberate details over these.
func (d details) overlay(more map[string]string) {
	for name, value := range more {
		if value != "" && !strings.HasPrefix(name, "_") {
			d[name] = value
		}
	}
}

// number reads "3" or "3/12".
func number(s string) int {
	s, _, _ = strings.Cut(s, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func (d details) year() int {
	for _, name := range []string{"year", "date", "originaldate", "release_date"} {
		if value := d[name]; len(value) >= 4 {
			if year, err := strconv.Atoi(value[:4]); err == nil && year > 0 {
				return year
			}
		}
	}
	return 0
}

// names are the people a detail lists: the JSON list when there is one, else the joined text.
func (d details) names(list, joined string) []string {
	if names := localmetadata.Names(d[list]); len(names) > 0 {
		return names
	}
	var out []string
	for _, name := range strings.FieldsFunc(d[joined], func(r rune) bool { return r == ';' }) {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func credits(role db.CreditRole, names []string) []db.ImportCredit {
	var out []db.ImportCredit
	for _, name := range names {
		out = append(out, db.ImportCredit{Name: name, Role: role})
	}
	return out
}

func genreTags(d details) []db.TagName {
	var out []db.TagName
	for _, genre := range d.names("genres", "genre") {
		out = append(out, db.TagName{Kind: db.TagGenre, Name: genre})
	}
	return out
}

func ms(seconds float64) *int64 {
	n := int64(seconds*1000 + .5)
	return &n
}

// musicUnits turns a music library's folder into artists, albums and tracks: from each file's
// tags first, and from Artist/Album/01 - Track otherwise. One file with a cue sheet is an
// album whose tracks each play their own part of it.
func (r *run) musicUnits(f *folder) []unit {
	versions := r.versions(f, func(file *found) (string, bool) { return file.rel, true })
	var units []unit
	for _, v := range versions {
		lead := v.parts[0]
		sheet := r.cueSheet(f, lead)
		if lead.state != added && !(lead.state == moved && !r.tagLed()) && !v.sideTouched() && (sheet == nil || sheet.state == same) {
			continue
		}
		tags := r.tags(lead)
		d := tagDetails(tags)
		read := lead.reading
		artist := firstOf(d["album_artist"], d["artist"], first(read.Artists), "Unknown Artist")
		album := firstOf(d["album"], first(read.Albums), "Unknown Album")
		disc := firstNumber(number(d["disc"]), first(read.DiscNumbers), 1)
		artistItem := db.ImportItem{Kind: db.KindArtist, Title: artist, SortTitle: d["album_artist_sort"],
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: "artist|" + fold(artist)}}}
		if id := d["musicbrainz_albumartistid"]; id != "" {
			artistItem.Keys = append([]db.ItemKey{{Kind: db.KeyEmbedded, Key: "musicbrainz-artist:" + id}}, artistItem.Keys...)
			artistItem.ExternalIDs = []db.ExternalID{{Provider: "musicbrainz", ID: id}}
		}
		albumItem := db.ImportItem{Kind: db.KindAlbum, Title: album, SortTitle: d["album_sort"], Year: d.year(), ParentRow: 1,
			ChildKey: "album|" + fold(album), Tags: genreTags(d), Credits: credits(db.RoleArtist, []string{artist})}
		if id := d["musicbrainz_albumid"]; id != "" {
			albumItem.Keys = []db.ItemKey{{Kind: db.KeyEmbedded, Key: "musicbrainz-release:" + id}}
			albumItem.ExternalIDs = []db.ExternalID{{Provider: "musicbrainz", ID: id}}
		}
		if folder := albumFolder(lead.rel); folder != "" {
			albumItem.Keys = append(albumItem.Keys, db.FolderKey(r.path.ID, folder))
		}
		u := unit{files: v.files(), items: []db.ImportItem{artistItem, albumItem}}
		trackArtists := credits(db.RoleArtist, []string{firstOf(d["artist"], artist)})
		var chapters []localmetadata.Chapter
		for _, c := range tags.CueTracks {
			chapters = append(chapters, localmetadata.Chapter{Title: c.Title, Start: c.Start, End: c.End})
		}
		if sheet != nil {
			u.files = append(u.files, sheet)
			if raw, err := readSmall(r.ctx, r.src, sheet.rel, 256<<10); err == nil {
				if fromSheet, err := localmetadata.CueTracks(raw, lead.entry.Name, stem(sheet.entry.Name) == stem(lead.entry.Name)); err == nil {
					chapters = fromSheet
				}
			}
		}
		if len(chapters) >= 2 {
			// A whole album in one file: each track plays from its start to the next one's.
			for n, chapter := range chapters {
				part := db.ImportPart{File: lead.ref(), StartMS: ms(chapter.Start)}
				if n+1 < len(chapters) {
					part.EndMS = ms(chapters[n+1].Start)
				} else if chapter.End > chapter.Start {
					part.EndMS = ms(chapter.End)
				}
				track := n + 1
				u.items = append(u.items, db.ImportItem{Kind: db.KindTrack, Title: firstOf(chapter.Title, fmt.Sprintf("Track %d", track)),
					Year: albumItem.Year, IndexNumber: &track, ParentIndex: &disc, ParentRow: 2, ChildKey: fmt.Sprintf("d%dt%d", disc, track),
					Keys: []db.ItemKey{db.FileKey(r.path.ID, lead.rel, track)}, Credits: trackArtists,
					Versions: []db.ImportVersion{{Parts: []db.ImportPart{part}}}})
			}
			units = append(units, u)
			continue
		}
		stored, keys := r.stored(v, false, 0)
		name, _ := title(lead)
		track := db.ImportItem{Kind: db.KindTrack, Title: firstOf(d["title"], name), SortTitle: d["title_sort"], Year: albumItem.Year,
			ParentIndex: &disc, ParentRow: 2, Keys: keys, Credits: trackArtists, Versions: []db.ImportVersion{stored}}
		if n := firstNumber(number(d["track"]), first(read.TrackNumbers), 0); n > 0 {
			track.IndexNumber, track.ChildKey = &n, fmt.Sprintf("d%dt%d", disc, n)
		} else {
			track.ChildKey = "track|" + fold(track.Title)
		}
		if id := d["musicbrainz_trackid"]; id != "" {
			track.ExternalIDs = []db.ExternalID{{Provider: "musicbrainz", ID: id}}
		}
		u.items = append(u.items, track)
		units = append(units, u)
	}
	return units
}

func (v *version) sideTouched() bool {
	for _, file := range v.side {
		if file.state != same {
			return true
		}
	}
	return false
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNumber(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// albumFolder is the folder an album has to itself: the file's folder, or the one above a
// disc folder. A file at the top of the library has none.
func albumFolder(rel string) string {
	dir := path.Dir(rel)
	if _, disc := naming.DiscFolder(path.Base(dir)); disc {
		dir = path.Dir(dir)
	}
	if dir == "." {
		return ""
	}
	return dir
}

// cueSheet is the cue sheet that describes an audio file: one with the file's name, or, for
// the only audio file of its folder, one with the folder's name.
func (r *run) cueSheet(f *folder, audio *found) *found {
	var named *found
	for _, file := range f.files {
		if file.role != db.FileCueSheet {
			continue
		}
		if stem(file.entry.Name) == stem(audio.entry.Name) {
			return file
		}
		if len(f.media) == 1 && stem(file.entry.Name) == strings.ToLower(path.Base(f.rel)) {
			named = file
		}
	}
	return named
}

// bookFolderOf is the folder an audiobook has to itself: the file's folder, or the one above
// a disc folder.
func bookFolderOf(rel string) string {
	dir := path.Dir(rel)
	if _, disc := naming.AudiobookDiscFolder(path.Base(dir)); disc {
		dir = path.Dir(dir)
	}
	if dir == "." {
		return ""
	}
	return dir
}

// audiobookUnits turns an audiobook library's folder into books. A book is everything in its
// folder, as one title whose files are its parts in order, including the files of its disc
// folders. A file at the top of the library is a book by itself. Details come from the first
// file's tags, then desc.txt and reader.txt, an OPF file and Audiobookshelf's metadata.json,
// each over the one before; the folders (Author/Series/Book) fill in what is still unknown.
func (r *run) audiobookUnits(f *folder) []unit {
	if len(f.media) == 0 {
		return nil
	}
	book := bookFolderOf(f.media[0].rel)
	if book == "" {
		var units []unit
		for _, file := range f.media {
			if file.state == added || file.state == moved && !r.tagLed() {
				units = append(units, r.audiobookUnit(f, "", []*found{file}, nil))
			}
		}
		return units
	}
	touched := false
	for _, file := range f.files {
		touched = touched || file.state == added || file.state == changed && file.role != db.FileMedia || file.state == moved && !r.tagLed()
	}
	if !touched {
		return nil
	}
	// The book's files stored by earlier folders of this scan or by earlier scans come first
	// or after, by path: "Disc 1" before "Disc 2".
	parts := append([]*found(nil), f.media...)
	var earlier []db.FileRef
	if err := r.flush(); err != nil {
		return nil
	}
	for _, dir := range r.bookFolders(book, f.rel) {
		stored, err := r.s.DB.Folder(r.ctx, r.path.ID, dir)
		if err != nil || !stored.Known {
			continue
		}
		for name, file := range stored.Files {
			if file.Role == db.FileMedia && !file.Missing {
				earlier = append(earlier, db.FileRef{Dir: dir, Name: name})
			}
		}
	}
	return []unit{r.audiobookUnit(f, book, parts, earlier)}
}

// bookFolders are the other folders that hold a book's files: the book's folder and its disc
// folders, without the one being scanned.
func (r *run) bookFolders(book, scanning string) []string {
	var out []string
	if book != scanning {
		out = append(out, book)
	}
	entries, err := r.src.List(r.ctx, book)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if _, disc := naming.AudiobookDiscFolder(e.Name); e.Dir && disc && path.Join(book, e.Name) != scanning {
			out = append(out, path.Join(book, e.Name))
		}
	}
	return out
}

func (r *run) audiobookUnit(f *folder, book string, here []*found, elsewhere []db.FileRef) unit {
	lead := here[0]
	tags := r.tags(lead)
	d := tagDetails(tags)
	var chapters []localmetadata.Chapter
	if book != "" && r.lib.Options.Depth != db.DepthFiles {
		for _, name := range []string{"desc.txt", "reader.txt", "metadata.opf", "metadata.json"} {
			raw, err := readSmall(r.ctx, r.src, path.Join(book, name), 256<<10)
			if err != nil {
				continue
			}
			var more map[string]string
			switch name {
			case "metadata.opf":
				more, err = localmetadata.OPF(raw)
			case "metadata.json":
				more, chapters, err = localmetadata.Audiobookshelf(raw)
			default:
				more, err = localmetadata.BookText(name, raw)
			}
			if err == nil {
				d.overlay(more)
			}
		}
	}
	folders := naming.ReadAudiobookFolder(lead.rel)
	name, _ := title(lead)
	bookTitle := firstOf(d["album"], folders.Title, d["title"], name)
	authors := d.names("authors", "author")
	if len(authors) == 0 {
		authors = d.names("album_artists", "album_artist")
	}
	if len(authors) == 0 {
		authors = d.names("artists", "artist")
	}
	if len(authors) == 0 && folders.Author != "" {
		authors = []string{folders.Author}
	}
	narrators := d.names("narrators", "narrator")
	if len(narrators) == 0 {
		narrators = folders.Narrators
	}
	year := d.year()
	if year == 0 {
		year, _ = strconv.Atoi(folders.Year)
	}
	item := db.ImportItem{Kind: db.KindAudiobook, Title: bookTitle, Year: year, Summary: d["description"], Tags: genreTags(d),
		Credits: append(credits(db.RoleAuthor, authors), credits(db.RoleNarrator, narrators)...),
		Keys:    []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|" + fold(bookTitle) + "|" + fold(strings.Join(authors, ","))}}}
	for _, scheme := range []string{"asin", "isbn"} {
		if id := firstOf(d[scheme]); id != "" {
			item.ExternalIDs = append(item.ExternalIDs, db.ExternalID{Provider: scheme, ID: id})
		}
	}
	if folders.ASIN != "" && d["asin"] == "" {
		item.ExternalIDs = append(item.ExternalIDs, db.ExternalID{Provider: "asin", ID: folders.ASIN})
	}
	if book != "" {
		item.Keys = append(item.Keys, db.FolderKey(r.path.ID, book))
	}

	// The parts in order: by path, which puts disc folders and numbered files right.
	refs := append([]db.FileRef(nil), elsewhere...)
	u := unit{}
	for _, file := range here {
		refs = append(refs, file.ref())
		u.files = append(u.files, file)
	}
	sort.Slice(refs, func(a, b int) bool {
		return natural(path.Join(refs[a].Dir, refs[a].Name)) < natural(path.Join(refs[b].Dir, refs[b].Name))
	})
	version := db.ImportVersion{}
	var fileKeys []db.ItemKey
	for _, ref := range refs {
		version.Parts = append(version.Parts, db.ImportPart{File: ref})
		fileKeys = append(fileKeys, db.FileKey(r.path.ID, path.Join(ref.Dir, ref.Name), 0))
	}
	item.Keys = append(fileKeys, item.Keys...)
	// Chapters: a cue sheet named after a single file, else Audiobookshelf's, else the file's own.
	if len(refs) == 1 {
		if sheet := r.cueSheet(f, lead); sheet != nil {
			u.files = append(u.files, sheet)
			if raw, err := readSmall(r.ctx, r.src, sheet.rel, 256<<10); err == nil {
				if fromSheet, err := localmetadata.CueTracks(raw, lead.entry.Name, true); err == nil && len(fromSheet) > 0 {
					chapters = fromSheet
				}
			}
		}
		if len(chapters) == 0 {
			for _, c := range tags.Chapters {
				chapters = append(chapters, localmetadata.Chapter{Title: c.Title, Start: c.Start, End: c.End})
			}
		}
	}
	for n, chapter := range chapters {
		end := chapter.End
		if end <= chapter.Start && n+1 < len(chapters) {
			end = chapters[n+1].Start
		}
		if end < chapter.Start {
			end = chapter.Start
		}
		version.Markers = append(version.Markers, db.Marker{Kind: db.MarkerChapter, Source: db.MarkerFromFile,
			StartMS: *ms(chapter.Start), EndMS: *ms(end), Title: chapter.Title})
	}
	item.Versions = []db.ImportVersion{version}
	if series := firstOf(d["series"], folders.Series); series != "" {
		// A series is a title of its own that its books belong to.
		seriesKey := db.ItemKey{Kind: db.KeyName, Key: "series|" + fold(series)}
		item.ExtraOf, item.ExtraKind = []db.ItemKey{seriesKey}, db.RelationSeriesMember
		if position := number(firstOf(d["series_position"], folders.Sequence)); position > 0 {
			item.IndexNumber = &position
		}
		u.items = append(u.items, db.ImportItem{Kind: db.KindSeries, Title: series, Keys: []db.ItemKey{seriesKey}})
	}
	u.items = append(u.items, item)
	return u
}

// natural pads the numbers in a path so "Part 2" sorts before "Part 10".
func natural(s string) string {
	var out strings.Builder
	digits := 0
	flush := func(end int) {
		if digits > 0 {
			out.WriteString(strings.Repeat("0", max(0, 8-digits)))
			out.WriteString(s[end-digits : end])
			digits = 0
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			digits++
			continue
		}
		flush(i)
		out.WriteByte(s[i])
	}
	flush(len(s))
	return strings.ToLower(out.String())
}
