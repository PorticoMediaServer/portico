package localmetadata

import (
	"math"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
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

func TestAudiobookshelfMetadataJSONIsReadWithItsOwnFieldNames(t *testing.T) {
	values, chapters, e := parseABSMetadata([]byte(fixtureABS))
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]string{
		"album":           "Fixture Book",
		"author":          "Fixture Author; Second Author",
		"authors":         `["Fixture Author","Second Author"]`,
		"narrator":        "Fixture Narrator",
		"series":          "Fixture Saga",
		"series_position": "2",
		"genres":          `["Fantasy","Humour"]`,
		"year":            "1999",
		"publisher":       "Fixture Press",
		"description":     "A book made for tests.",
		"isbn":            "9780000000000",
		"asin":            "B000000000",
		"language":        "en",
	}
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
