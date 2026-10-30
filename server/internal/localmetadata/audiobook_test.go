package localmetadata

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

const fixtureCue = `REM GENRE Audiobook
PERFORMER "Fixture Author"
TITLE "Fixture Book"
FILE "Fixture Book.m4b" MP4
  TRACK 01 AUDIO
    TITLE "Opening Credits"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Chapter One"
    INDEX 00 00:41:70
    INDEX 01 00:42:15
  TRACK 03 AUDIO
    TITLE "Chapter Two"
    INDEX 01 125:03:74
`

func TestCueSheetsGiveChaptersInAnyCommonEncoding(t *testing.T) {
	utf16, _ := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(fixtureCue))
	latin1, _ := charmap.ISO8859_1.NewEncoder().Bytes([]byte(strings.Replace(fixtureCue, "Chapter Two", "Chapitre Deux à Noël", 1)))
	for name, raw := range map[string][]byte{"utf8": []byte(fixtureCue), "utf8 bom": append([]byte{0xEF, 0xBB, 0xBF}, fixtureCue...), "utf16": utf16, "latin1": latin1} {
		files, e := parseCue(raw)
		if e != nil || len(files) != 1 || len(files[0].tracks) != 3 {
			t.Fatalf("%s: %v %+v", name, e, files)
		}
		got := cueChapters(files, "/books/Fixture Book.m4b", true)
		if len(got) != 3 || got[0].Title != "Opening Credits" || got[1].Start != 42.2 || math.Abs(got[2].Start-(125*60+3+74.0/75)) > 1e-9 {
			t.Fatalf("%s: %+v", name, got)
		}
		if name == "latin1" && got[2].Title != "Chapitre Deux à Noël" {
			t.Fatalf("latin-1 title: %q", got[2].Title)
		}
	}
}

func TestCueSheetsMapEachFileToItsOwnTracks(t *testing.T) {
	sheet := `FILE "Disc 1/Part 01.mp3" MP3
  TRACK 01 AUDIO
    TITLE "One"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Two"
    INDEX 01 10:00:00
FILE Part 02.mp3 MP3
  TRACK 03 AUDIO
    TITLE "Three"
    INDEX 01 00:00:00
`
	files, e := parseCue([]byte(sheet))
	if e != nil || len(files) != 2 || files[1].name != "Part 02.mp3" {
		t.Fatal(e, files)
	}
	if got := cueChapters(files, "/b/Part 02.mp3", false); len(got) != 1 || got[0].Title != "Three" {
		t.Fatalf("%+v", got)
	}
	if got := cueChapters(files, "/b/Disc 1/Part 01.flac", false); len(got) != 2 || got[1].Start != 600 {
		t.Fatalf("same name, another format: %+v", got)
	}
	if got := cueChapters(files, "/b/Part 03.mp3", false); got != nil {
		t.Fatalf("a file the sheet does not name: %+v", got)
	}
	// A folder's sheet listing one other file is not this file's.
	one, _ := parseCue([]byte("FILE \"Other.mp3\" MP3\n TRACK 01 AUDIO\n  INDEX 01 00:00:00\n"))
	if cueChapters(one, "/b/Book.m4b", false) != nil || len(cueChapters(one, "/b/Book.m4b", true)) != 1 {
		t.Fatal("own sheet rule")
	}
	for name, bad := range map[string]string{
		"no tracks":       "FILE \"a.mp3\" MP3\n",
		"track first":     "TRACK 01 AUDIO\n INDEX 01 00:00:00\n",
		"frames past 74":  "FILE \"a.mp3\" MP3\n TRACK 01 AUDIO\n  INDEX 01 00:00:75\n",
		"seconds past 59": "FILE \"a.mp3\" MP3\n TRACK 01 AUDIO\n  INDEX 01 00:60:00\n",
		"binary":          "FILE \"a.mp3\" MP3\x00\n",
	} {
		if _, e := parseCue([]byte(bad)); e == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

const fixtureABS = `{
  "tags": ["Owner Tag"],
  "chapters": [
    {"id": 0, "start": 0, "end": 300.5, "title": "Opening"},
    {"id": 1, "start": 300.5, "end": 3600, "title": "Chapter 1"}
  ],
  "title": "Fixture Book",
  "subtitle": "A Fixture",
  "authors": ["Fixture Author", "Second Author", "Fixture Author"],
  "narrators": ["Fixture Narrator"],
  "series": ["Fixture Saga #2", "Fixture Universe #10.5", "Unnumbered"],
  "genres": ["Fantasy", "Humour"],
  "publishedYear": "1999",
  "publishedDate": null,
  "publisher": "Fixture Press",
  "description": "A book made for tests.",
  "isbn": "9780000000000",
  "asin": "B000000000",
  "language": "en",
  "explicit": false,
  "abridged": false
}`

func TestAudiobookshelfMetadataJSONIsReadWithItsOwnFieldNames(t *testing.T) {
	values, chapters, e := parseABSMetadata([]byte(fixtureABS))
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]string{"album": "Fixture Book", "author": "Fixture Author; Second Author", "authors": `["Fixture Author","Second Author"]`, "narrator": "Fixture Narrator", "series": "Fixture Saga", "series_position": "2", "genres": `["Fantasy","Humour"]`, "year": "1999", "publisher": "Fixture Press", "description": "A book made for tests.", "isbn": "9780000000000", "asin": "B000000000", "language": "en"}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("%s = %q, want %q", k, values[k], v)
		}
	}
	if values["series_list"] != `[{"name":"Fixture Saga","position":"2"},{"name":"Fixture Universe","position":"10.5"},{"name":"Unnumbered","position":""}]` {
		t.Error(values["series_list"])
	}
	for _, absent := range []string{"edition", "tags", "subtitle", "date"} {
		if _, ok := values[absent]; ok {
			t.Errorf("%s should not be imported", absent)
		}
	}
	if len(chapters) != 2 || chapters[1].Start != 300.5 || chapters[1].End != 3600 || chapters[0].Title != "Opening" {
		t.Fatalf("%+v", chapters)
	}
	// The older form nests its fields under "metadata".
	values, _, e = parseABSMetadata([]byte(`{"metadata":{"title":"Old Form","authors":["Someone"],"series":["Saga #1"]}}`))
	if e != nil || values["album"] != "Old Form" || values["author"] != "Someone" || values["series_position"] != "1" {
		t.Fatal(values, e)
	}
	// One unusable chapter discards the list, as Audiobookshelf does; the rest of the file still counts.
	values, chapters, e = parseABSMetadata([]byte(`{"title":"T","chapters":[{"start":0,"end":5,"title":"A"},{"start":null,"end":9,"title":"B"}]}`))
	if e != nil || chapters != nil || values["album"] != "T" {
		t.Fatal(values, chapters, e)
	}
	if _, _, e = parseABSMetadata([]byte(`{"title":"T"} {"title":"U"}`)); e == nil {
		t.Fatal("trailing document accepted")
	}
}

type fakeBook map[string]string

func (b fakeBook) service(t *testing.T) *Service {
	s, e := New(t.TempDir(), "", nil)
	if e != nil {
		t.Fatal(e)
	}
	s.readSmall = func(_ context.Context, _ string, path string, limit int64) ([]byte, error) {
		if v, ok := b[path]; ok && int64(len(v)) <= limit {
			return []byte(v), nil
		}
		return nil, errors.New("missing")
	}
	return s
}

func facts(tags map[string]string, duration float64) assets.Facts {
	f := assets.Facts{Tags: map[string]string{}, TagSources: map[string]string{}, Duration: duration, AudioCodec: "aac"}
	for k, v := range tags {
		f.Tags[k], f.TagSources[k] = v, "embedded"
	}
	return f
}

func TestAudiobookSidecarsFollowTheirPrecedence(t *testing.T) {
	dir := "/library/Fixture Author/Fixture Saga/Book 2 - Fixture Book"
	book := fakeBook{
		dir + "/desc.txt":              "\xEF\xBB\xBFA description from desc.txt.\r\n",
		dir + "/reader.txt":            "Text Narrator\nSecond Narrator",
		dir + "/metadata.opf":          `<package><metadata><title>OPF Title</title><creator role="aut">OPF Author</creator><publisher>OPF Press</publisher></metadata></package>`,
		dir + "/metadata.json":         fixtureABS,
		dir + "/book.m4b.portico.json": `{"publisher":"Portico Press"}`,
	}
	f := book.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/book.m4b", facts(map[string]string{"album": "Embedded Title", "narrator": "Embedded Narrator", "description": "Embedded description."}, 3600))
	for k, want := range map[string][2]string{
		"album":       {"Fixture Book", "abs_json"}, // metadata.json over OPF over embedded
		"author":      {"Fixture Author; Second Author", "abs_json"},
		"narrator":    {"Fixture Narrator", "abs_json"},       // metadata.json over reader.txt over embedded
		"description": {"A book made for tests.", "abs_json"}, // metadata.json over desc.txt
		"publisher":   {"Portico Press", "portico_json"},      // the file's own Portico sidecar is last
		"series":      {"Fixture Saga", "abs_json"},           // a sidecar's series, not the folder's
	} {
		if f.Tags[k] != want[0] || f.TagSources[k] != want[1] {
			t.Errorf("%s = %q from %q, want %q from %q", k, f.Tags[k], f.TagSources[k], want[0], want[1])
		}
	}
	// Every observation is kept as evidence, whichever won.
	seen := map[string]bool{}
	for _, ev := range f.AudioEvidence {
		seen[ev.Field+"/"+ev.Source] = true
	}
	for _, want := range []string{"narrator/embedded", "narrator/txt", "description/txt", "album/opf", "album/abs_json"} {
		if !seen[want] {
			t.Errorf("evidence %s missing", want)
		}
	}
	// metadata.json's chapters span this one-file book.
	if len(f.Chapters) != 2 || f.Chapters[1].Title != "Chapter 1" {
		t.Fatalf("chapters: %+v", f.Chapters)
	}
}

func TestTextSidecarsFillWhatTagsLeaveAndFoldersComeLast(t *testing.T) {
	dir := "/library/Fixture Author/Fixture Saga/Book 3 - Third Book {Folder Narrator}"
	book := fakeBook{dir + "/reader.txt": "Text Narrator & Second Narrator", dir + "/desc.txt": "Described."}
	f := book.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/01.mp3", facts(nil, 600))
	for k, want := range map[string][2]string{
		"narrator":        {"Text Narrator; Second Narrator", "txt"},
		"description":     {"Described.", "txt"},
		"series":          {"Fixture Saga", "folder"},
		"series_position": {"3", "folder"},
		"author":          {"Fixture Author", "folder"},
	} {
		if f.Tags[k] != want[0] || f.TagSources[k] != want[1] {
			t.Errorf("%s = %q from %q, want %q from %q", k, f.Tags[k], f.TagSources[k], want[0], want[1])
		}
	}
	// A tagged book keeps its identity: no author or narrator from its folders,
	// though its folders may still place it in a series.
	f = fakeBook{}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/01.mp3", facts(map[string]string{"album": "Tagged", "title": "Part 1"}, 600))
	if f.Tags["author"] != "" || f.Tags["narrator"] != "" || f.Tags["series"] != "Fixture Saga" || f.Tags["series_position"] != "3" {
		t.Fatalf("%+v", f.Tags)
	}
	// An embedded series is never replaced by a folder's.
	f = fakeBook{}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/01.mp3", facts(map[string]string{"series": "Tagged Saga"}, 600))
	if f.Tags["series"] != "Tagged Saga" || f.Tags["series_position"] != "" {
		t.Fatalf("%+v", f.Tags)
	}
	// A book on discs keeps its sidecars in the book's folder, above the discs.
	f = fakeBook{dir + "/desc.txt": "Described above the discs."}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/Disc 2/01.mp3", facts(nil, 600))
	if f.Tags["description"] != "Described above the discs." || f.Tags["series_position"] != "3" {
		t.Fatalf("%+v", f.Tags)
	}
	// Without the library root, folders say nothing.
	f = fakeBook{}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "", dir+"/01.mp3", facts(nil, 600))
	if f.Tags["series"] != "" || f.Tags["author"] != "" {
		t.Fatalf("%+v", f.Tags)
	}
}

func TestAudiobookChaptersComeFromCueSheetsBeforeMetadataJSON(t *testing.T) {
	dir := "/library/Fixture Author/Fixture Book"
	embedded := []assets.Chapter{{Title: "Embedded", Start: 0, End: 9000}}
	withEmbedded := func(duration float64) assets.Facts {
		f := facts(nil, duration)
		f.Chapters = embedded
		return f
	}
	// The file's own sheet wins over metadata.json and the embedded chapters.
	f := fakeBook{dir + "/Fixture Book.cue": fixtureCue, dir + "/metadata.json": fixtureABS}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/Fixture Book.m4b", withEmbedded(9000))
	if len(f.Chapters) != 3 || f.Chapters[0].Title != "Opening Credits" || f.Chapters[0].End != 42.2 || f.Chapters[2].End != 9000 {
		t.Fatalf("cue: %+v", f.Chapters)
	}
	// A sheet named after the folder serves the file it names.
	f = fakeBook{dir + "/Fixture Author.cue": "x", dir + "/Fixture Book.cue": ""}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/Fixture Book.m4b", withEmbedded(9000))
	if len(f.Chapters) != 1 || f.Chapters[0].Title != "Embedded" || f.LocalMetadataIssue != "invalid_cue_sidecar" {
		t.Fatalf("an unreadable sheet leaves the embedded chapters: %+v %q", f.Chapters, f.LocalMetadataIssue)
	}
	f = fakeBook{dir + "/Fixture Book.cue": strings.Replace(fixtureCue, "Fixture Book.m4b", "Part 1.mp3", 1)}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/Part 2.mp3", withEmbedded(9000))
	if f.Chapters[0].Title != "Embedded" {
		t.Fatalf("a folder sheet for another file: %+v", f.Chapters)
	}
	// metadata.json's chapters span the whole book: one file of a longer book keeps its own.
	f = fakeBook{dir + "/metadata.json": fixtureABS}.service(t).prepareAudioEvidence(context.Background(), "lib", "audiobook", "/library", dir+"/Part 1.mp3", withEmbedded(1800))
	if len(f.Chapters) != 1 || f.Chapters[0].Title != "Embedded" {
		t.Fatalf("multi-file: %+v", f.Chapters)
	}
	// Music is untouched by audiobook sidecars.
	f = fakeBook{dir + "/Fixture Book.cue": fixtureCue, dir + "/metadata.json": fixtureABS}.service(t).prepareAudioEvidence(context.Background(), "lib", "music", "/library", dir+"/Fixture Book.m4b", withEmbedded(9000))
	if f.Chapters[0].Title != "Embedded" || f.Tags["album"] != "" {
		t.Fatalf("music: %+v %+v", f.Chapters, f.Tags)
	}
}
