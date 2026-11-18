package db

import (
	"path/filepath"
	"testing"
)

// A summary a person wrote and then unlocked stays theirs while the provider's answers say
// nothing for it, and becomes the provider's with the first answer that gives one.
func TestAnUnlockedEditStaysUntilTheProviderGivesTheField(t *testing.T) {
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, err := d.CreateLibrary(t.Context(), "Films", LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Import(t.Context(), ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1,
		Items: []ImportItem{{Kind: KindMovie, Title: "Film", Keys: []ItemKey{{Kind: KeyName, Key: "film"}}}}}); err != nil {
		t.Fatal(err)
	}
	var id int64
	var publicID string
	if err := d.read.QueryRow(`SELECT id, public_id FROM items WHERE kind = ?`, KindMovie).Scan(&id, &publicID); err != nil {
		t.Fatal(err)
	}
	doc := func() MetadataDocument {
		t.Helper()
		out, err := d.MetadataDocument(t.Context(), publicID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	mine := "My own words."
	revision, err := d.EditMetadata(t.Context(), publicID, doc().Revision, MetadataEdit{Summary: &mine})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.EditMetadata(t.Context(), publicID, revision, MetadataEdit{Unlock: []string{FieldSummary}}); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyDetails(t.Context(), User, id, ItemDetails{Title: "Film", Year: 2001}); err != nil {
		t.Fatal(err)
	}
	if got := doc(); got.Fields[FieldSummary] != mine || got.Sources[FieldSummary] != SourceEdit {
		t.Fatalf("after an answer with no summary: %v from %q; want mine, an edit", got.Fields[FieldSummary], got.Sources[FieldSummary])
	}
	if err := d.ApplyDetails(t.Context(), User, id, ItemDetails{Title: "Film", Summary: "The provider's words."}); err != nil {
		t.Fatal(err)
	}
	if got := doc(); got.Fields[FieldSummary] != "The provider's words." || got.Sources[FieldSummary] == SourceEdit {
		t.Fatalf("after an answer with a summary: %v from %q; want the provider's", got.Fields[FieldSummary], got.Sources[FieldSummary])
	}
}
