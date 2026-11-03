package compactcatalog

import "testing"

// What a folder says about a book must be the same whether the folder holds one
// book or several: the inner folder is the series and the one above it the
// author. Adding a second book must not rename the first one's author.
func TestReadingFolderEvidenceDoesNotDependOnSiblingCount(t *testing.T) {
	first := ReadingRuleInput{Item: 1, SourceRoot: "/lib", RelativePath: "Brandon Sanderson/Mistborn/Book 1 - The Final Empire.epub", Kind: Ebook}
	second := ReadingRuleInput{Item: 2, SourceRoot: "/lib", RelativePath: "Brandon Sanderson/Mistborn/Book 2 - The Well of Ascension.epub", Kind: Ebook}
	comic := ReadingRuleInput{Item: 3, SourceRoot: "/lib", RelativePath: "Batman/Batman 001.cbz", Kind: Comic}
	comic2 := ReadingRuleInput{Item: 4, SourceRoot: "/lib", RelativePath: "Batman/Batman 002.cbz", Kind: Comic}

	for _, row := range []struct {
		name string
		one  []ReadingRuleInput
		two  []ReadingRuleInput
		item int64
	}{
		{"book", []ReadingRuleInput{first}, []ReadingRuleInput{first, second}, 1},
		{"comic", []ReadingRuleInput{comic}, []ReadingRuleInput{comic, comic2}, 3},
	} {
		t.Run(row.name, func(t *testing.T) {
			alone, err := ReadingPathValues(row.one)
			if err != nil {
				t.Fatal(err)
			}
			withSibling, err := ReadingPathValues(row.two)
			if err != nil {
				t.Fatal(err)
			}
			a, b := alone[row.item], withSibling[row.item]
			value := func(v *ReadingValue) string {
				if v == nil {
					return ""
				}
				return v.Value
			}
			if value(a.Author) != value(b.Author) {
				t.Fatalf("author alone = %q; with a sibling = %q: the folder says the same thing either way", value(a.Author), value(b.Author))
			}
			if value(a.Series) != value(b.Series) {
				t.Fatalf("series alone = %q; with a sibling = %q: the folder says the same thing either way", value(a.Series), value(b.Series))
			}
			if a.Title != b.Title {
				t.Fatalf("title alone = %q; with a sibling = %q", a.Title, b.Title)
			}
			number := func(n *ReadingNumber) float64 {
				if n == nil {
					return -1
				}
				return n.Value
			}
			if number(a.SeriesNumber) != number(b.SeriesNumber) {
				t.Fatalf("number alone = %v; with a sibling = %v", number(a.SeriesNumber), number(b.SeriesNumber))
			}
		})
	}
}
