package scan

import (
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// The folder author key is dbwork.AuthorIdentityKey exactly: Unicode lowercase
// only, with no trim, normalization or full case fold. Empty and whitespace-only
// values keep distinct exact keys but grant no author membership.
func TestIAAudiobookAuthorKeyExactUnicode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		author string
		key    string
		member bool
	}{
		{"composed_upper_emile", "\u00c9mile", "author:\u00e9mile", true},
		{"lower_emile", "\u00e9mile", "author:\u00e9mile", true},
		{"sharp_s", "Stra\u00dfe", "author:stra\u00dfe", true},
		{"upper_strasse", "STRASSE", "author:strasse", true},
		{"composed_accent", "\u00c9", "author:\u00e9", true},
		{"decomposed_accent", "E\u0301", "author:e\u0301", true},
		{"empty", "", "author:", false},
		{"space", " ", "author: ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			author := AudiobookFolderAuthor(naming.AudiobookFolder{Author: tc.author})
			if author.Author() != tc.author || author.Key() != tc.key || author.Member() != tc.member {
				t.Fatalf("author %q: value=%q key=%q member=%t", tc.author, author.Author(), author.Key(), author.Member())
			}
		})
	}
	key := func(author string) string {
		return AudiobookFolderAuthor(naming.AudiobookFolder{Author: author}).Key()
	}
	if key("\u00c9mile") != key("\u00e9mile") {
		t.Fatal("Unicode lowercase did not share the composed case")
	}
	if key("Stra\u00dfe") == key("STRASSE") {
		t.Fatal("full case fold substituted Stra\u00dfe/STRASSE")
	}
	if key("\u00c9") == key("E\u0301") {
		t.Fatal("Unicode normalization substituted composed/decomposed")
	}
	if key("") == key(" ") {
		t.Fatal("trim collapsed the empty and space authors")
	}
}

// Structured folder evidence stays separate: author, ASIN, narrators, series,
// sequence and title are independent fields, and the key uses the author only.
func TestIAAudiobookFolderFactsStaySeparate(t *testing.T) {
	file := iaEvidenceFile("Author/Series/Book [B0015T963C] {Narrator One;Narrator Two}/part1.m4b")
	evidence, err := PrepareMediaEvidence("audiobook", file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	folder, ok := evidence.AudiobookFolder()
	if !ok {
		t.Fatal("audiobook folder evidence missing")
	}
	if folder.Title != "Book" || folder.Author != "Author" || folder.Series != "Series" || folder.Sequence != "" ||
		folder.ASIN != "B0015T963C" || !slices.Equal(folder.Narrators, []string{"Narrator One", "Narrator Two"}) {
		t.Fatalf("folder facts merged: %+v", folder)
	}
	author := AudiobookFolderAuthor(folder)
	if author.Author() != "Author" || author.Key() != "author:author" || !author.Member() {
		t.Fatalf("author fact: %q %q", author.Author(), author.Key())
	}
	// The key never absorbs the other folder fields.
	other := AudiobookFolderAuthor(naming.AudiobookFolder{Author: "Author", Series: "Other", ASIN: "B000000000", Narrators: []string{"Other Reader"}})
	if other.Key() != author.Key() {
		t.Fatal("key absorbed non-author folder facts", other.Key())
	}
}
