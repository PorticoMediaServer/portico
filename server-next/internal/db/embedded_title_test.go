package db_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// titled is a prober whose every file has the same title written inside it.
type titled struct{ title string }

func (p titled) Probe(context.Context, string) (scan.Facts, error) {
	return scan.Facts{Container: "mkv", DurationMS: 60000, Title: p.title, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// The title written inside a film's file names the film only where local details are all
// there is: in a library that uses local metadata only, or once a provider was asked and did
// not match. Before a film is looked up it keeps the name its file name gave, and a matched
// film, or one a person named, is never renamed by it.
func TestTheTitleInsideAFileIsUsedOnlyWhereLocalDetailsAreAllThereIs(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	library := func(name string, films ...string) db.Library {
		t.Helper()
		folder := filepath.Join(root, name)
		for n, film := range films {
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(folder, film), make([]byte, 100+n), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		lib, err := d.CreateLibrary(ctx, name, db.LibraryMovies, "en", []string{folder})
		if err != nil {
			t.Fatal(err)
		}
		return lib
	}
	titles := func(lib db.Library) map[string]string {
		t.Helper()
		page, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, item := range page.Items {
			out[item.Title] = item.PublicID
		}
		return out
	}
	scanner := &scan.Scanner{DB: d, Prober: titled{"Léon: The Professional"}}

	// A library with a provider: not looked up yet, the film keeps its file's name.
	provided := library("provided", "Leon (1994).mkv", "Heat (1995).mkv", "Ronin (1998).mkv")
	if _, err := scanner.Scan(ctx, provided); err != nil {
		t.Fatal(err)
	}
	before := titles(provided)
	if before["Leon"] == "" || before["Léon: The Professional"] != "" {
		t.Fatalf("before it is looked up a film keeps its file's name: %v", before)
	}
	// The provider is asked. Heat is matched and a person named Ronin; nothing confident is
	// found for Leon or for Ronin, which is recorded the way the matcher records it.
	for _, set := range []string{
		`UPDATE items SET match_state = 1, refreshed_at = 1 WHERE title = 'Heat'`,
		`UPDATE items SET locked_fields = '["title"]' WHERE title = 'Ronin'`,
	} {
		if err := d.ExecForTest(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	items, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: provided.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items.Items {
		if item.Title == "Heat" {
			continue
		}
		if err := d.SetUnmatched(ctx, db.Background, item.ID, "No confident match.", ""); err != nil {
			t.Fatal(err)
		}
	}
	after := titles(provided)
	if after["Léon: The Professional"] != before["Leon"] || after["Heat"] == "" || after["Ronin"] == "" || len(after) != 3 {
		t.Fatalf("after the provider was asked: %v", after)
	}

	// A library that uses local metadata only: the title inside the file is used at once.
	local := library("local", "leon.mkv")
	if err := d.ExecForTest(ctx, `UPDATE libraries SET metadata_provider = 'local' WHERE id = ?`, local.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, local); err != nil {
		t.Fatal(err)
	}
	named := titles(local)
	if named["Léon: The Professional"] == "" {
		t.Fatalf("a local-only library: %v", named)
	}
	// Renamed on disk, the file is not read again: the film is the same film and keeps the
	// title written inside it.
	if err := os.Rename(filepath.Join(root, "local", "leon.mkv"), filepath.Join(root, "local", "Leon (1994).mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, local); err != nil {
		t.Fatal(err)
	}
	if got := titles(local); got["Léon: The Professional"] != named["Léon: The Professional"] || len(got) != 1 {
		t.Fatalf("after its file was renamed: %v", got)
	}
}
