package naming

import "testing"

func TestEmbeddedFileNamesWinFoldersButExplicitFileCoordinatesStay(t *testing.T) {
	r := ReadWithEmbedded("Wrong Folder (2000)/movie.mkv", "movie", Embedded{Title: "Heat", Year: 1995, Edition: "Extended"})
	title, year := r.PreferredTitle()
	if title != "Heat" || year != 1995 || r.Titles[0].Source != EmbeddedMetadata || r.Editions[0].Value != "Extended" {
		t.Fatal(r)
	}
	season, number := 1, 5
	r = ReadWithEmbedded("Wrong/Season 1/Show - S02E01.mkv", "tv", Embedded{Title: "Episode display title", ShowTitle: "Show", Season: &season, Number: &number})
	planSeason, planNumber := r.Seasons[0].Value, r.Episodes[0].Value[0]
	if planSeason != 2 || planNumber != 1 {
		t.Fatal(r)
	}
	r = ReadWithEmbedded("Wrong/Season 1/video.mkv", "tv", Embedded{ShowTitle: "Show", Season: &season, Number: &number})
	if r.Seasons[0].Source != EmbeddedMetadata || r.Episodes[0].Value[0] != 5 {
		t.Fatal("folder won over embedded coordinates", r)
	}
	for _, folder := range []string{"Disc 1 - Studio", "CD2", "Volume 3", "Part 4", "Digital Media 5", "Act 6"} {
		if _, ok := DiscFolder(folder); !ok {
			t.Fatal(folder)
		}
	}
}
