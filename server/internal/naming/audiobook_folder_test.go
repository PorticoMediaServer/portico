package naming

import (
	"reflect"
	"testing"
)

func TestAudiobookFoldersReadLikeAudiobookshelf(t *testing.T) {
	for relative, want := range map[string]AudiobookFolder{
		"Terry Pratchett/Guards! Guards!/01.mp3":                           {Title: "Guards! Guards!", Author: "Terry Pratchett"},
		"Terry Pratchett/Discworld/Book 8 - Guards! Guards!/01.mp3":        {Title: "Guards! Guards!", Author: "Terry Pratchett", Series: "Discworld", Sequence: "8"},
		"Terry Pratchett/Discworld/8. Guards! Guards!/01.mp3":              {Title: "Guards! Guards!", Author: "Terry Pratchett", Series: "Discworld", Sequence: "8"},
		"Terry Pratchett/Discworld/08 - Guards! Guards!/01.mp3":            {Title: "Guards! Guards!", Author: "Terry Pratchett", Series: "Discworld", Sequence: "8"},
		"Terry Pratchett/Discworld/Guards! Guards! - Vol. 8/01.mp3":        {Title: "Guards! Guards!", Author: "Terry Pratchett", Series: "Discworld", Sequence: "8"},
		"Terry Pratchett/Discworld/Vol 1.5 - Interlude/01.mp3":             {Title: "Interlude", Author: "Terry Pratchett", Series: "Discworld", Sequence: "1.5"},
		"Terry Pratchett/Discworld/1989 - Book 8 - Guards! Guards!/01.mp3": {Title: "Guards! Guards!", Author: "Terry Pratchett", Series: "Discworld", Sequence: "8", Year: "1989"},
		"Terry Pratchett/Discworld/Book 2/01.mp3":                          {Title: "Book 2", Author: "Terry Pratchett", Series: "Discworld", Sequence: "2"},
		"Author/Series/101 Dalmatians/01.mp3":                              {Title: "101 Dalmatians", Author: "Author", Series: "Series"},
		"Author/(1999) - Title {Jim Dale & Stephen Fry}/Disc 2/03.mp3":     {Title: "Title", Author: "Author", Year: "1999", Narrators: []string{"Jim Dale", "Stephen Fry"}},
		"Author/Title {Dale, Jim} [B0015T963C]/book.m4b":                   {Title: "Title", Author: "Author", ASIN: "B0015T963C", Narrators: []string{"Dale, Jim"}},
		"Author/Title {Jim Dale, Stephen Fry}/book.m4b":                    {Title: "Title", Author: "Author", Narrators: []string{"Jim Dale", "Stephen Fry"}},
		"Author/Guards! Guards! (Discworld #8)/book.m4b":                   {Title: "Guards! Guards!", Author: "Author", Series: "Discworld", Sequence: "8"},
		"Collections/Author/Series/1 - Title/part.mp3":                     {Title: "Title", Author: "Author", Series: "Series", Sequence: "1"},
		"Title Only/book.m4b":                                              {Title: "Title Only"},
		"book.m4b":                                                         {},
	} {
		got := ReadAudiobookFolder(relative)
		if got.Narrators == nil && want.Narrators == nil {
			got.Narrators = nil
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", relative, got, want)
		}
	}
}

func TestAudiobookFolderSequenceNeedsASeries(t *testing.T) {
	// Without a series folder a leading number belongs to the title.
	if got := ReadAudiobookFolder("Author/1 - Title/part.mp3"); got.Title != "1 - Title" || got.Sequence != "" || got.Series != "" {
		t.Fatalf("%+v", got)
	}
}

func TestAudiobookDiscFoldersAreOnlyDiscs(t *testing.T) {
	for name, want := range map[string]int{"Disc 1": 1, "CD2": 2, "disk-03": 3, "Part 3 of 4": 3, "Vol 1": 1, "Vol. 2 - Second Book": 0, "Part 1 - The Beginning": 0, "Disc 1 (Bonus)": 0, "Discworld": 0} {
		got, ok := AudiobookDiscFolder(name)
		if got != want || ok != (want > 0) {
			t.Errorf("AudiobookDiscFolder(%q) = %d %v, want %d", name, got, ok, want)
		}
	}
}
