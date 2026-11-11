package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A title written inside a video file shows for a title its provider could not match, while
// the name its files gave is kept for the next search; a provider's title then replaces it.
// A title a person locked is never replaced by it.
func TestAnEmbeddedTitleIsUsedOnlyUntilSomethingElseSpeaks(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, err := d.CreateLibrary(ctx, "Films", LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	libraries, _ := d.AllLibraries(ctx)
	batch := ImportBatch{LibraryID: lib.ID, PathID: libraries[0].Paths[0].ID, Scan: 1, Dirs: []ImportDir{{RelPath: ""}}}
	for _, name := range []string{"Zzyzx Home Video", "Locked Title"} {
		ref := FileRef{Name: name + ".mkv"}
		batch.Files = append(batch.Files, ImportFile{FileRef: ref, Role: FileMedia, Size: 1000, Container: "mkv", DurationMS: 60_000})
		batch.Items = append(batch.Items, ImportItem{Kind: KindMovie, Title: name, Keys: []ItemKey{{Kind: KeyFile, Key: ref.Name}},
			Versions: []ImportVersion{{Parts: []ImportPart{{File: ref}}}}})
	}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, name := range []string{"Zzyzx Home Video", "Locked Title"} {
		var id int64
		if err := d.read.QueryRow(`SELECT id FROM items WHERE title = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
		// What the scanner records when the prober reports the file's title tag.
		if err := d.write(ctx, User, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE items SET extra = json_set(ifnull(extra, '{}'), '$.embeddedTitle', 'A Title From The File') WHERE id = ?`, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(id int64) (title, local, names string) {
		t.Helper()
		var l, n sql.NullString
		if err := d.read.QueryRow(`SELECT title, local_fields, extra ->> '$.namesTitle' FROM items WHERE id = ?`, id).Scan(&title, &l, &n); err != nil {
			t.Fatal(err)
		}
		return title, l.String, n.String
	}
	if err := d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE items SET locked_fields = '["title"]' WHERE id = ?`, ids["Locked Title"])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := d.SetUnmatched(ctx, Background, id, "no_results", "[]"); err != nil {
			t.Fatal(err)
		}
	}
	if title, local, names := read(ids["Zzyzx Home Video"]); title != "A Title From The File" || local != `{"title":"embedded"}` || names != "Zzyzx Home Video" {
		t.Fatalf("an unmatched title: %q %q %q", title, local, names)
	}
	if title, _, _ := read(ids["Locked Title"]); title != "Locked Title" {
		t.Fatalf("a locked title was replaced by the file's: %q", title)
	}
	if err := d.ApplyDetails(ctx, Background, ids["Zzyzx Home Video"], ItemDetails{Match: MatchFound, Title: "The Provider's Title"}); err != nil {
		t.Fatal(err)
	}
	if title, local, names := read(ids["Zzyzx Home Video"]); title != "The Provider's Title" || local != "" || names != "" {
		t.Fatalf("after the provider's answer: %q %q %q", title, local, names)
	}
}
