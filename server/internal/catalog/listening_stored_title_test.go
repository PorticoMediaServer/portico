package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestBookPageEnrichmentPreservesStoredTitleAndEdition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, names := phase34ListeningFixture(t)
	book := names["book-a"]
	r := listeningRequest("books", "book", book.Public)
	for _, tc := range []struct {
		name, title, localEdition, edition string
	}{
		{"unabridged", "Project Hail Mary (Unabridged)", "", "Unabridged"},
		{"abridged", "A Book (Abridged)", "", "Abridged"},
		{"special edition", "The Hunger Games: Special Edition", "", ""},
		{"subtitle", "A Book: Its Complete Subtitle", "", ""},
		{"explicit edition", "The Martian (Unabridged)", "Local edition", "Local edition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.Fields(book.ID, map[string]any{"title": tc.title})
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				return saveLocalBookMetadata(tx, book.ID, map[string]string{"author": "Author", "edition": tc.localEdition}, nil)
			})
			c.Drain()
			var storedTitle string
			if err := c.DB.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, book.ID).Scan(&storedTitle); err != nil {
				t.Fatal(err)
			}
			entries := []ContentEntry{{ID: book.Public, Kind: "book", LibraryID: "books", Title: storedTitle}}
			if err := s.EnrichListeningEntries(r.Viewer, entries); err != nil {
				t.Fatal(err)
			}
			entry := entries[0]
			if storedTitle != tc.title || entry.Title != storedTitle || entry.DisplayTitle != "" || entry.Edition != tc.edition || entry.Author != "Author" {
				t.Fatalf("stored title or metadata changed: stored=%q entry=%+v", storedTitle, entry)
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err = json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			if _, invented := wire["displayTitle"]; invented || wire["title"] != storedTitle {
				t.Fatalf("wire invented a short title: %s", raw)
			}
		})
	}
}
